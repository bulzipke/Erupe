package mhfpacket

import (
	"erupe-ce/common/byteframe"
	"erupe-ce/network/clientctx"
	"testing"
)

func TestSetKijuOneByteWire(t *testing.T) {
	for color := byte(1); color <= 4; color++ {
		bf := byteframe.NewByteFrameFromBytes([]byte{0, 0, 0, 7, color})
		p := &MsgMhfSetKiju{}
		if err := p.Parse(bf, &clientctx.ClientContext{}); err != nil {
			t.Fatal(err)
		}
		if p.AckHandle != 7 || p.Unk1 != color || bf.Index() != 5 {
			t.Fatalf("packet=%+v bytes=%d", p, bf.Index())
		}
	}
	bf := byteframe.NewByteFrameFromBytes([]byte{0, 0, 0, 7})
	if err := (&MsgMhfSetKiju{}).Parse(bf, &clientctx.ClientContext{}); err == nil {
		t.Fatal("truncated color accepted")
	}
}
