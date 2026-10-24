package channelserver

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"

	"erupe-ce/network/mhfpacket"
)

// These World values cannot be produced by the native power-of-two world mask.
// Counter is a chunk/entry index. Every transfer is pinned to one fresh,
// authority-filtered enumeration; no client cache can grant publication rights.
const (
	questCacheProbe    byte = 0xd0
	questCacheState    byte = 0xd1
	questCacheManifest byte = 0xd2
	questCacheBundle   byte = 0xd3
	questCacheOne      byte = 0xd4
	questCacheBatch    byte = 0xd5
	// ASock's two native receive slots are each 0x31000 bytes in both ZZ
	// clients. An extended ACK length does not enlarge those earlier slots.
	questCacheChunk       = 192 << 10
	questCacheWireVersion = 3
	questCacheBatchSize   = 64
	questCacheMaxBytes    = 16 << 20
)

type questClientCacheTransfer struct {
	root                                [32]byte
	state, manifest, bundle             []byte
	bodies                              [][]byte
	stateWire, manifestWire, bundleWire questCacheWirePayload
}

// Compression is prepared once per immutable transfer and only when requested.
// A warm probe never compresses a definition bundle it will not send.
type questCacheWirePayload struct {
	once     sync.Once
	data     []byte
	rawBytes int
	flags    byte
	err      error
}

func (w *questCacheWirePayload) prepare(source func() ([]byte, error)) error {
	w.once.Do(func() {
		var raw []byte
		raw, w.err = source()
		if w.err != nil {
			return
		}
		w.rawBytes = len(raw)
		w.data = raw
		if len(raw) < 1024 {
			return
		}
		var compressed bytes.Buffer
		writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
		if err != nil {
			w.err = err
			return
		}
		_, err = writer.Write(raw)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			w.err = err
			return
		}
		if compressed.Len() < len(raw) {
			w.data = compressed.Bytes()
			w.flags = 1
		}
	})
	return w.err
}

func questCacheChunks(n int) int {
	chunks := (n + questCacheChunk - 1) / questCacheChunk
	if chunks == 0 {
		return 1
	}
	return chunks
}

func cacheU16(b []byte, v int) []byte { return binary.BigEndian.AppendUint16(b, uint16(v)) }

func newQuestClientCacheTransfer(snapshot *questDeliveryListSnapshot, trailer []byte) (*questClientCacheTransfer, error) {
	if snapshot == nil || len(snapshot.entries) > 16384 || len(trailer) < 4 {
		return nil, fmt.Errorf("invalid cache snapshot")
	}
	t := &questClientCacheTransfer{}
	// One backing allocation owns all normalized definitions. Session snapshots
	// already hold full native frames; do not retain a second bundle-sized copy.
	definitionBytes := 0
	for _, frame := range snapshot.entries {
		definitionBytes += len(frame) - 22
	}
	if definitionBytes < 0 || definitionBytes > questCacheMaxBytes {
		return nil, fmt.Errorf("cache definitions too large")
	}
	t.bundle = make([]byte, 0, definitionBytes)
	t.manifest = cacheU16(nil, len(snapshot.entries))
	t.state = cacheU16(nil, len(snapshot.entries))
	for _, frame := range snapshot.entries {
		if len(frame) < 24 {
			return nil, fmt.Errorf("short quest frame")
		}
		n := int(binary.BigEndian.Uint16(frame[20:22]))
		if n < 352 || n > 896 || len(frame) != 22+n+2 || frame[22+n] != 1 || frame[23+n] != 0 {
			return nil, fmt.Errorf("invalid cached quest frame")
		}
		// Header[0:4] is the DB row ID, not the quest file ID. It stays live.
		id := uint32(binary.LittleEndian.Uint16(frame[68:70]))
		if id == 0 {
			return nil, fmt.Errorf("quest identity mismatch")
		}
		t.bundle = cacheU16(t.bundle, n)
		start := len(t.bundle)
		t.bundle = append(t.bundle, frame[22:22+n]...)
		body := t.bundle[start:]
		t.state = append(t.state, frame[:22]...)
		t.state = append(t.state, body[questFrameTimeFlagOffset-22], body[questFrameVariant3Offset-22])
		body[questFrameTimeFlagOffset-22], body[questFrameVariant3Offset-22] = 0, 0
		hash := sha256.Sum256(body)
		t.manifest = cacheU16(t.manifest, int(id))
		t.manifest = cacheU16(t.manifest, n)
		t.manifest = append(t.manifest, hash[:]...)
		t.bodies = append(t.bodies, body)
	}
	t.root = sha256.Sum256(t.manifest)
	t.state = binary.BigEndian.AppendUint32(t.state, uint32(len(trailer)))
	t.state = append(t.state, trailer...)
	if len(t.bundle) > questCacheMaxBytes || len(t.state) > questCacheMaxBytes {
		return nil, fmt.Errorf("cache snapshot too large")
	}
	return t, nil
}

// QCH2/version3/kind/flags/payloadLength/root/encodedLength/index/chunks/rawLength/data.
// The ACK's extended length, opcode and crypto header must all fit the native
// 0x31000-byte receive slot. Disk definitions stay QCF1.
func (t *questClientCacheTransfer) response(kind byte, index uint16) ([]byte, error) {
	var payload *questCacheWirePayload
	switch kind {
	case questCacheProbe, questCacheState:
		payload = &t.stateWire
		if err := payload.prepare(func() ([]byte, error) { return t.state, nil }); err != nil {
			return nil, err
		}
	case questCacheManifest:
		payload = &t.manifestWire
		if err := payload.prepare(func() ([]byte, error) {
			if err := t.prepareBundle(); err != nil {
				return nil, err
			}
			// A transport hint, outside the hashed QCF1 manifest, lets the client
			// choose fewer requests between sparse 64-entry batches and a bundle.
			b := cacheU16(nil, questCacheChunks(len(t.bundleWire.data)))
			return append(b, t.manifest...), nil
		}); err != nil {
			return nil, err
		}
	case questCacheBundle:
		payload = &t.bundleWire
		if err := t.prepareBundle(); err != nil {
			return nil, err
		}
	case questCacheOne:
		if int(index) >= len(t.bodies) {
			return nil, fmt.Errorf("cache entry out of range")
		}
		payload = &questCacheWirePayload{data: t.bodies[index], rawBytes: len(t.bodies[index])}
	case questCacheBatch:
		if int(index) >= len(t.bodies) {
			return nil, fmt.Errorf("cache batch out of range")
		}
		payload = &questCacheWirePayload{}
		if err := payload.prepare(func() ([]byte, error) {
			end := int(index) + questCacheBatchSize
			if end > len(t.bodies) {
				end = len(t.bodies)
			}
			b := cacheU16(nil, end-int(index))
			for i := int(index); i < end; i++ {
				b = cacheU16(b, i)
				b = cacheU16(b, len(t.bodies[i]))
				b = append(b, t.bodies[i]...)
			}
			return b, nil
		}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown cache command")
	}
	source := payload.data
	chunk := int(index)
	if kind == questCacheOne || kind == questCacheBatch {
		chunk = 0
	}
	chunks := questCacheChunks(len(source))
	if chunk >= chunks {
		return nil, fmt.Errorf("cache chunk out of range")
	}
	start, end := chunk*questCacheChunk, (chunk+1)*questCacheChunk
	if end > len(source) {
		end = len(source)
	}
	b := []byte{'Q', 'C', 'H', '2', 0, questCacheWireVersion, kind, payload.flags}
	b = binary.BigEndian.AppendUint32(b, uint32(44+end-start))
	b = append(b, t.root[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(source)))
	b = cacheU16(b, int(index))
	b = cacheU16(b, chunks)
	b = binary.BigEndian.AppendUint32(b, uint32(payload.rawBytes))
	return append(b, source[start:end]...), nil
}

func (t *questClientCacheTransfer) prepareBundle() error {
	return t.bundleWire.prepare(func() ([]byte, error) { return t.bundle, nil })
}

func (s *Session) serveQuestClientCache(p *mhfpacket.MsgMhfEnumerateQuest) bool {
	if p.World < questCacheState || p.World > questCacheBatch {
		return false
	}
	s.questDeliveryListMu.Lock()
	t := s.questClientCache
	s.questDeliveryListMu.Unlock()
	if t == nil {
		doAckBufFail(s, p.AckHandle, nil)
		return true
	}
	data, err := t.response(p.World, p.Counter)
	if err != nil {
		doAckBufFail(s, p.AckHandle, nil)
	} else {
		doAckBufSucceed(s, p.AckHandle, data)
	}
	return true
}
