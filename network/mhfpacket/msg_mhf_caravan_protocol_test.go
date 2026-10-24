package mhfpacket

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/clientctx"
)

func TestCaravanRequestWire(t *testing.T) {
	// Byte order and field widths come from the ZZ serializers at
	// 1153ea30 (score) and 1153ecc0 (rankings), rather than Build/Parse
	// round-trips alone. Include signed values and a nontrivial ACK.
	tests := []struct {
		name      string
		packet    MHFPacket
		newPacket func() MHFPacket
		wire      string
	}{
		{"score read", &MsgMhfCaravanMyScore{AckHandle: 0x12345678, Unk0: 1},
			func() MHFPacket { return &MsgMhfCaravanMyScore{} },
			"1234567800000001000000000000000000000000000000000000000000000000"},
		{"score post", &MsgMhfCaravanMyScore{AckHandle: 0x12345678, Unk0: 2, Unk1: 3,
			Unk2: -1, Unk3: 1000, Unk4: 0x89abcdef, Unk5: -2, Unk6: 42},
			func() MHFPacket { return &MsgMhfCaravanMyScore{} },
			"123456780000000200000003ffffffff000003e889abcdeffffffffe0000002a"},
		// Synthetic values in the five argument positions confirmed at
		// submit call 10b0349c: route, base, guild, premium, item bonus.
		{"native score post arguments", &MsgMhfCaravanMyScore{AckHandle: 0x12345678, Unk0: 2, Unk1: 3,
			Unk2: 8, Unk3: 1000, Unk4: 900, Unk5: 500, Unk6: 2500},
			func() MHFPacket { return &MsgMhfCaravanMyScore{} },
			"12345678000000020000000300000008000003e800000384000001f4000009c4"},
		{"ranking", &MsgMhfCaravanRanking{AckHandle: 0x12345678, Unk1: 1, Unk2: -2},
			func() MHFPacket { return &MsgMhfCaravanRanking{} },
			"123456780000000000000001fffffffe"},
		{"my rank", &MsgMhfCaravanMyRank{AckHandle: 0x12345678, Unk1: 2, Unk2: 42},
			func() MHFPacket { return &MsgMhfCaravanMyRank{} },
			"1234567800000000000000020000002a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := hex.DecodeString(tt.wire)
			if err != nil {
				t.Fatal(err)
			}
			bf := byteframe.NewByteFrame()
			if err := tt.packet.Build(bf, &clientctx.ClientContext{}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bf.Data(), want) {
				t.Fatalf("wire = %x, want %x", bf.Data(), want)
			}
			parsed := tt.newPacket()
			if err := parsed.Parse(byteframe.NewByteFrameFromBytes(want), &clientctx.ClientContext{}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parsed, tt.packet) {
				t.Fatalf("parsed = %+v, want %+v", parsed, tt.packet)
			}
			for n := 0; n < len(want); n++ {
				if err := tt.newPacket().Parse(byteframe.NewByteFrameFromBytes(want[:n]), &clientctx.ClientContext{}); err == nil {
					t.Fatalf("accepted truncated request of %d bytes", n)
				}
			}
		})
	}
}
