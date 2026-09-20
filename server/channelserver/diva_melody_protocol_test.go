package channelserver

import (
	"bytes"
	"testing"

	"erupe-ce/network/mhfpacket"
)

// Mock-only protocol checks: no database or earned currency is involved.
func TestDivaMelodyDisabledWire(t *testing.T) {
	s := createMockSession(1, createMockServer())
	handleMsgMhfGetUdShopCoin(s, &mhfpacket.MsgMhfGetUdShopCoin{AckHandle: 0x12345678})
	get := readAck(t, s)
	if get.AckHandle != 0x12345678 || get.IsBufferResponse || get.ErrorCode != 0 ||
		get.PayloadSize != 0 || !bytes.Equal(get.Payload, []byte{0, 0, 0, 0}) {
		t.Fatalf("GET must return a zero balance in the fixed simple ACK: %+v", get)
	}
	for _, cost := range []uint8{0, 1, 2, 10, 255} {
		handleMsgMhfUseUdShopCoin(s, &mhfpacket.MsgMhfUseUdShopCoin{AckHandle: uint32(cost), Cost: cost})
		use := readAck(t, s)
		if use.AckHandle != uint32(cost) || use.IsBufferResponse || use.ErrorCode == 0 ||
			use.PayloadSize != 0 || !bytes.Equal(use.Payload, []byte{0, 0, 0, 0}) {
			t.Fatalf("USE must never authorize a client-local item grant while disabled: %+v", use)
		}
	}
}
