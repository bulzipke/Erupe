package network

import (
	"bytes"
	"testing"

	cfg "erupe-ce/config"
)

// Cache ACKs now use the existing extended-u32 ACK length and the crypto size
// high bits. Exercise sizes above 64KiB, with no listener or game connection.
func TestQuestCacheLargeEncryptedFrameRoundTrip(t *testing.T) {
	const nativeReceiveSlot = 0x31000 // ZZ HD/non-HD ASock, before ACK parsing.
	for _, size := range []int{65534, 65535, 65536, 192*1024 + 56 + 14} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*31 + i/257)
		}
		sink := newMockConn(nil)
		writer := NewCryptConn(sink, cfg.ZZ, nil)
		if err := writer.SendPacket(payload); err != nil {
			t.Fatal(err)
		}
		if sink.writeData.Len() > nativeReceiveSlot {
			t.Fatalf("encrypted packet %d exceeds native receive slot %d", sink.writeData.Len(), nativeReceiveSlot)
		}
		reader := NewCryptConn(newMockConn(sink.writeData.Bytes()), cfg.ZZ, nil)
		got, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("encrypted frame truncated at size %d", size)
		}
	}
}
