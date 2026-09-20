package mhfpacket

import (
	"bytes"
	"testing"

	"erupe-ce/common/byteframe"
)

func TestUseUdShopCoinWire(t *testing.T) {
	for _, cost := range []byte{0, 1, 2, 10, 255} {
		want := MsgMhfUseUdShopCoin{AckHandle: 0x12345678, Cost: cost}
		out := byteframe.NewByteFrame()
		if err := want.Build(out, nil); err != nil {
			t.Fatal(err)
		}
		wire := []byte{0x12, 0x34, 0x56, 0x78, cost}
		if !bytes.Equal(out.Data(), wire) {
			t.Fatalf("unexpected cost-only request: %x", out.Data())
		}
		bf := byteframe.NewByteFrameFromBytes(append(wire, 0xaa, 0xbb))
		var got MsgMhfUseUdShopCoin
		if err := got.Parse(bf, nil); err != nil || got != want {
			t.Fatalf("parse = %+v, %v; want %+v", got, err, want)
		}
		if !bytes.Equal(bf.DataFromCurrent(), []byte{0xaa, 0xbb}) {
			t.Fatal("request consumed the following packet")
		}
	}
}

func TestUseUdShopCoinTruncated(t *testing.T) {
	wire := []byte{0x12, 0x34, 0x56, 0x78, 1}
	for size := 0; size < len(wire); size++ {
		var got MsgMhfUseUdShopCoin
		if err := got.Parse(byteframe.NewByteFrameFromBytes(wire[:size]), nil); err == nil {
			t.Fatalf("accepted %d-byte truncated request", size)
		}
	}
}
