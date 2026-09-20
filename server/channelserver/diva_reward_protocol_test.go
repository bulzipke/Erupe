package channelserver

import (
	"bytes"
	"testing"

	"erupe-ce/network/mhfpacket"
)

func TestDivaBonusEmptyCountWidth(t *testing.T) {
	s := createMockSession(1, createMockServer())
	handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0, 0}) {
		t.Fatalf("expected u16 zero count: %+v", ack)
	}
}

func TestDivaAcquireEmptyAndUnsupported(t *testing.T) {
	for _, tt := range []struct {
		name string
		pkt  mhfpacket.MsgMhfAcquireUdItem
		fail bool
	}{
		{"preview", mhfpacket.MsgMhfAcquireUdItem{Unk0: 1}, false},
		{"empty claim", mhfpacket.MsgMhfAcquireUdItem{}, false},
		{"unverified claim", mhfpacket.MsgMhfAcquireUdItem{ItemIDCount: 1, RewardIDs: []uint32{123}}, true},
		{"invalid category", mhfpacket.MsgMhfAcquireUdItem{RewardType: 255}, true},
		{"invalid flag", mhfpacket.MsgMhfAcquireUdItem{Unk0: 2}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := createMockSession(1, createMockServer())
			tt.pkt.AckHandle = 7
			handleMsgMhfAcquireUdItem(s, &tt.pkt)
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.fail {
				t.Fatalf("wrong status: %+v", ack)
			}
			if !tt.fail && !bytes.Equal(ack.Payload, []byte{0, 0}) {
				t.Fatalf("expected status/count pair: %+v", ack)
			}
		})
	}
}
