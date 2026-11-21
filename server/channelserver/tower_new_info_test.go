package channelserver

import (
	"encoding/binary"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestTowerGuardianCountsAreNotHardcoded(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{guardianBlock1: 7, guardianBlock2: 3}
	s := createMockSession(1, srv)
	handleMsgMhfGetEarthValue(s, &mhfpacket.MsgMhfGetEarthValue{AckHandle: 1, ReqType: 1})
	p := <-s.sendPackets
	response := extractAckPayload(t, p.data)
	if len(response) != 16+2*24 {
		t.Fatalf("guardian response size = %d", len(response))
	}
	if got := binary.BigEndian.Uint32(response[20:24]); got != 7 {
		t.Errorf("block 1 kills = %d, want 7", got)
	}
	if got := binary.BigEndian.Uint32(response[44:48]); got != 3 {
		t.Errorf("block 2 kills = %d, want 3", got)
	}
}

func TestTowerScoutScoresAreNotHardcoded(t *testing.T) {
	srv := createMockServer()
	srv.towerRepo = &mockTowerRepo{scoutBlock1: 6000, scoutBlock2: 17500}
	s := createMockSession(1, srv)
	handleMsgMhfGetEarthValue(s, &mhfpacket.MsgMhfGetEarthValue{AckHandle: 1, ReqType: 2})
	response := extractAckPayload(t, (<-s.sendPackets).data)
	if got := binary.BigEndian.Uint32(response[20:24]); got != 6000 {
		t.Errorf("block 1 scout score = %d", got)
	}
	if got := binary.BigEndian.Uint32(response[44:48]); got != 17500 {
		t.Errorf("block 2 scout score = %d", got)
	}
}

func TestTowerGuardianRunIDsAreUnique(t *testing.T) {
	first := NewStage("Qs-test-a")
	second := NewStage("Qs-test-b")
	if first.towerGuardianRunID == "" || second.towerGuardianRunID == "" ||
		first.towerGuardianRunID == second.towerGuardianRunID {
		t.Fatalf("invalid guardian run IDs: %q / %q", first.towerGuardianRunID, second.towerGuardianRunID)
	}
}

func TestTowerAdvanceRewardsUseSeparateEvent(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(1, srv)
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{
		AckHandle: 1, Unk1: 5, Unk2: 260001,
	})
	response := extractAckPayload(t, (<-s.sendPackets).data)
	want := [][6]uint32{
		{1, 0, 0, 7201, 0x2CC7, 1},
		{1, 0, 0, 0x544E4355, 0x101, 1},
		{1, 0, 0, 0x544E4355, 0x102, 1},
		{1, 0, 0, 0x544E4355, 0x103, 1},
		{1, 0, 0, 0x544E4355, 0x201, 6},
		{1, 0, 0, 0x544E4355, 0x202, 6},
		{1, 0, 0, 0x544E4355, 0x203, 6},
	}
	if len(response) != 16+len(want)*24 || binary.BigEndian.Uint32(response[12:16]) != uint32(len(want)) {
		t.Fatalf("unexpected advance reward response: %x", response)
	}
	for i, row := range want {
		for j, field := range row {
			if got := binary.BigEndian.Uint32(response[16+i*24+j*4:]); got != field {
				t.Errorf("advance reward row %d field %d = %#x, want %#x", i, j, got, field)
			}
		}
	}
}

func TestTowerDailyTimetableEndsAtNoonJST(t *testing.T) {
	s := paperTestSession()
	handleMsgMhfGetPaperData(s, &mhfpacket.MsgMhfGetPaperData{AckHandle: 1, DataType: 0})
	response := extractAckPayload(t, (<-s.sendPackets).data)
	start := time.Unix(int64(binary.BigEndian.Uint32(response[4:8])), 0).In(time.FixedZone("JST", 9*3600))
	end := time.Unix(int64(binary.BigEndian.Uint32(response[8:12])), 0).In(time.FixedZone("JST", 9*3600))
	if start.Hour() != 12 || end.Hour() != 12 || end.Sub(start) != 24*time.Hour {
		t.Errorf("daily interval = %v to %v", start, end)
	}
}
