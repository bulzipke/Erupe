package mhfpacket

import (
	"bytes"
	"reflect"
	"testing"

	"erupe-ce/common/byteframe"
)

func TestDivaMapRequestWire(t *testing.T) {
	for _, tt := range []struct {
		name      string
		packet    MHFPacket
		newPacket func() MHFPacket
		wire      []byte
	}{
		{"get", &MsgMhfGetUdGuildMapInfo{AckHandle: 0x12345678},
			func() MHFPacket { return &MsgMhfGetUdGuildMapInfo{} }, []byte{0x12, 0x34, 0x56, 0x78}},
		{"generate", &MsgMhfGenerateUdGuildMap{AckHandle: 0x12345678},
			func() MHFPacket { return &MsgMhfGenerateUdGuildMap{} }, []byte{0x12, 0x34, 0x56, 0x78}},
		{"ranking", &MsgMhfGetUdTacticsRanking{AckHandle: 0x12345678, GuildID: 0x10203040},
			func() MHFPacket { return &MsgMhfGetUdTacticsRanking{} }, []byte{0x12, 0x34, 0x56, 0x78, 0x10, 0x20, 0x30, 0x40}},
		{"remaining_areas", &MsgMhfGetUdTacticsRemainingPoint{AckHandle: 0x12345678, Unk0: 0x10203040, Unk1: 28},
			func() MHFPacket { return &MsgMhfGetUdTacticsRemainingPoint{} }, []byte{0x12, 0x34, 0x56, 0x78, 0x10, 0x20, 0x30, 0x40, 0, 0, 0, 28, 0, 0, 0, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := byteframe.NewByteFrame()
			if err := tt.packet.Build(out, nil); err != nil || !bytes.Equal(out.Data(), tt.wire) {
				t.Fatalf("wire %x: %v", out.Data(), err)
			}
			in := byteframe.NewByteFrameFromBytes(append(append([]byte(nil), tt.wire...), 0xaa, 0xbb))
			got := tt.newPacket()
			if err := got.Parse(in, nil); err != nil || !reflect.DeepEqual(got, tt.packet) {
				t.Fatalf("parsed %+v: %v", got, err)
			}
			if !bytes.Equal(in.DataFromCurrent(), []byte{0xaa, 0xbb}) {
				t.Fatal("consumed following packet")
			}
			for size := 0; size < len(tt.wire); size++ {
				if err := tt.newPacket().Parse(byteframe.NewByteFrameFromBytes(tt.wire[:size]), nil); err == nil {
					t.Fatalf("accepted truncated body length %d", size)
				}
			}
		})
	}
}
