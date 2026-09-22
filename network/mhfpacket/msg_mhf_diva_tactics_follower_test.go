package mhfpacket

import (
	"bytes"
	"testing"

	"erupe-ce/common/byteframe"
)

func TestDivaTacticsFollowerNativeRequests(t *testing.T) {
	get := &MsgMhfGetUdTacticsFollower{AckHandle: 0x12345678}
	set := &MsgMhfSetUdTacticsFollower{AckHandle: 0x12345678, NameIndex: 39, Voice: 3, Weapon: 1, Strength: 2}
	for _, tc := range []struct {
		name   string
		packet MHFPacket
		wire   []byte
		fresh  func() MHFPacket
	}{
		{"get", get, []byte{0x12, 0x34, 0x56, 0x78}, func() MHFPacket { return &MsgMhfGetUdTacticsFollower{} }},
		{"set", set, []byte{0x12, 0x34, 0x56, 0x78, 0, 39, 0, 3, 0, 1, 0, 2}, func() MHFPacket { return &MsgMhfSetUdTacticsFollower{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bf := byteframe.NewByteFrame()
			if err := tc.packet.Build(bf, nil); err != nil || !bytes.Equal(bf.Data(), tc.wire) {
				t.Fatalf("build %x: %v", bf.Data(), err)
			}
			packet := tc.fresh()
			if err := packet.Parse(byteframe.NewByteFrameFromBytes(tc.wire), nil); err != nil {
				t.Fatal(err)
			}
			roundtrip := byteframe.NewByteFrame()
			if err := packet.Build(roundtrip, nil); err != nil || !bytes.Equal(roundtrip.Data(), tc.wire) {
				t.Fatalf("roundtrip %x: %v", roundtrip.Data(), err)
			}
			for n := 0; n < len(tc.wire); n++ {
				if err := tc.fresh().Parse(byteframe.NewByteFrameFromBytes(tc.wire[:n]), nil); err == nil {
					t.Fatalf("truncated length %d accepted", n)
				}
			}
		})
	}
}
