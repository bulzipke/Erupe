package channelserver

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"sync"
	"testing"
)

func cacheTestFrame(id uint16, text string) []byte {
	b := make([]byte, 22+352+len(text)+1+2)
	binary.BigEndian.PutUint32(b, uint32(id)+90000) // DB row IDs are independent of file IDs.
	binary.BigEndian.PutUint16(b[20:], uint16(352+len(text)+1))
	binary.LittleEndian.PutUint16(b[68:], id)
	binary.LittleEndian.PutUint32(b[62:], 320)
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(b[22+320+i*4:], 352)
	}
	copy(b[22+352:], text)
	b[len(b)-2] = 1
	return b
}

func TestQuestClientCacheContentAndLiveState(t *testing.T) {
	frame := cacheTestFrame(63421, "localized title")
	trailer := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}
	makeTransfer := func(frame []byte) *questClientCacheTransfer {
		v, e := newQuestClientCacheTransfer(&questDeliveryListSnapshot{entries: [][]byte{frame}}, trailer)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	a := makeTransfer(frame)
	frame[11] = 1
	frame[25] = 0x17
	frame[175] = 0x20
	frame[9] = 4
	b := makeTransfer(frame)
	if a.root != b.root || !bytes.Equal(a.manifest, b.manifest) || !bytes.Equal(a.bundle, b.bundle) || bytes.Equal(a.state, b.state) {
		t.Fatal("live state leaked into definition hash or failed to refresh")
	}
	frame[22+352] ^= 1
	c := makeTransfer(frame)
	if b.root == c.root {
		t.Fatal("translation changed without invalidating cache")
	}
	if sha256.Sum256(c.manifest) != c.root {
		t.Fatal("root mismatch")
	}
	frame[68] = 0
	frame[69] = 0
	if _, err := newQuestClientCacheTransfer(&questDeliveryListSnapshot{entries: [][]byte{frame}}, trailer); err == nil {
		t.Fatal("zero file ID accepted")
	}
}

func TestQuestClientCacheLargeChunkIntegrity(t *testing.T) {
	s := &questDeliveryListSnapshot{}
	for i := 1; i <= 16384; i++ {
		s.entries = append(s.entries, cacheTestFrame(uint16(i), "quest definition"))
	}
	transfer, err := newQuestClientCacheTransfer(s, []byte{0x40, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind byte
		want []byte
	}{{questCacheState, transfer.state}, {questCacheManifest, transfer.manifest}, {questCacheBundle, transfer.bundle}} {
		var actual []byte
		for i := 0; ; i++ {
			packet, e := transfer.response(test.kind, uint16(i))
			if e != nil {
				t.Fatal(e)
			}
			if len(packet) > questCacheChunk+56 || string(packet[:4]) != "QCH2" || packet[5] != questCacheWireVersion || int(binary.BigEndian.Uint32(packet[8:])) != len(packet)-12 {
				t.Fatal("invalid ACK framing")
			}
			actual = append(actual, packet[56:]...)
			if i+1 == int(binary.BigEndian.Uint16(packet[50:52])) {
				break
			}
		}
		first, _ := transfer.response(test.kind, 0)
		actual = decodeQuestCacheTestPayload(t, first, actual)
		if test.kind == questCacheManifest {
			if int(binary.BigEndian.Uint16(actual)) != questCacheChunks(len(transfer.bundleWire.data)) {
				t.Fatal("wrong bundle request hint")
			}
			actual = actual[2:]
		}
		if !bytes.Equal(actual, test.want) {
			t.Fatal("chunk reassembly changed bytes")
		}
	}
	if _, e := transfer.response(questCacheOne, 16384); e == nil {
		t.Fatal("out-of-range definition accepted")
	}
}

// The old 256KiB setting passed ACK/crypto round trips but overwrote ASock's
// fixed receive slots before either decoder ran (mhf.exe.586524.dmp).
func TestQuestClientCacheNativeReceiveCapacity(t *testing.T) {
	const receiveSlot = 0x31000
	const frameOverhead = 56 + 14 + 14 // cache header, extended ACK+opcode, crypto
	if 256*1024+frameOverhead <= receiveSlot {
		t.Fatal("old overflowing boundary was not exercised")
	}
	// Incompressible bytes force full chunks at the 16MiB transfer boundary.
	data := make([]byte, questCacheMaxBytes)
	for i := range data {
		data[i] = byte(i*31 + i/257)
	}
	transfer := &questClientCacheTransfer{}
	transfer.bundleWire.once.Do(func() {})
	transfer.bundleWire.data = data
	transfer.bundleWire.rawBytes = len(data)
	var joined []byte
	for i := 0; i < questCacheChunks(len(data)); i++ {
		packet, err := transfer.response(questCacheBundle, uint16(i))
		if err != nil {
			t.Fatal(err)
		}
		if len(packet)+14+14 > receiveSlot {
			t.Fatalf("packet %d overwrites native receive slot", i)
		}
		joined = append(joined, packet[56:]...)
	}
	if !bytes.Equal(joined, data) {
		t.Fatal("safe chunking lost data at final partial chunk")
	}
}

func decodeQuestCacheTestPayload(t *testing.T, first, data []byte) []byte {
	t.Helper()
	if first[7] == 1 {
		reader := flate.NewReader(bytes.NewReader(data))
		defer reader.Close()
		decoded, err := io.ReadAll(io.LimitReader(reader, questCacheMaxBytes+1))
		if err != nil {
			t.Fatal(err)
		}
		data = decoded
	}
	if len(data) != int(binary.BigEndian.Uint32(first[52:56])) {
		t.Fatal("raw length mismatch")
	}
	return data
}

func TestQuestClientCacheCompressedBatchAndConcurrentResponses(t *testing.T) {
	s := &questDeliveryListSnapshot{}
	for i := 1; i <= 200; i++ {
		s.entries = append(s.entries, cacheTestFrame(uint16(i), "quest definition repeated text"))
	}
	transfer, err := newQuestClientCacheTransfer(s, []byte{0, 200, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfer.response(questCacheProbe, 0); err != nil {
		t.Fatal(err)
	}
	if transfer.bundleWire.data != nil || transfer.manifestWire.data != nil {
		t.Fatal("warm probe prepared unused definition payloads")
	}
	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for _, kind := range []byte{questCacheProbe, questCacheManifest, questCacheBundle} {
				if _, err := transfer.response(kind, 0); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	group.Wait()
	for _, start := range []uint16{0, 150, 199} {
		packet, err := transfer.response(questCacheBatch, start)
		if err != nil {
			t.Fatal(err)
		}
		body := decodeQuestCacheTestPayload(t, packet, packet[56:])
		want := questCacheBatchSize
		if want > 200-int(start) {
			want = 200 - int(start)
		}
		if int(binary.BigEndian.Uint16(body)) != want {
			t.Fatal("batch count")
		}
		at := 2
		for i := int(start); i < int(start)+want; i++ {
			index, n := int(binary.BigEndian.Uint16(body[at:])), int(binary.BigEndian.Uint16(body[at+2:]))
			at += 4
			if index != i || !bytes.Equal(body[at:at+n], transfer.bodies[i]) {
				t.Fatal("batch identity/content")
			}
			at += n
		}
		if at != len(body) {
			t.Fatal("batch trailing data")
		}
	}
	if _, err := transfer.response(questCacheBatch, 200); err == nil {
		t.Fatal("out of range batch accepted")
	}
}
