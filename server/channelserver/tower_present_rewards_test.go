package channelserver

import (
	"encoding/binary"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestTowerPendingRewardsAndClaimFiltering(t *testing.T) {
	state := TowerRewardState{Floors: 1, Claimed: map[uint64]bool{}}
	got := towerPendingRewards(state, []uint32{towerPresentFloor})
	if len(got) != 1 || got[0].itemID != 0x2B96 || got[0].quantity != 1 ||
		got[0].claimIndex != towerClaimIndex(towerRewardFloor, 1) {
		t.Fatalf("first original floor reward not offered: %+v", got)
	}
	if len(towerPresentFrames(got)[0].Data()) != 44 {
		t.Fatal("present-box reward frame must be 44 bytes")
	}
	state.Claimed[towerRewardKey(towerRewardFloor, 1)] = true
	if len(towerPendingRewards(state, []uint32{towerPresentFloor})) != 0 {
		t.Fatal("claimed floor reward offered again")
	}
	state.AdvanceEligible = true
	if len(towerPendingRewards(state, []uint32{towerPresentAdvance})) != 1 {
		t.Fatal("eligible guardian reward not offered")
	}
}

func TestTowerDailyRewardsRemainClaimableAfterNoon(t *testing.T) {
	oldDay := towerDailyStart(time.Date(2026, 9, 24, 18, 0, 0, 0, time.FixedZone("JST", 9*3600)))
	missions := towerDailyMissions(oldDay)
	state := TowerRewardState{Daily: []TowerDailyDay{{
		Start: oldDay,
		Counters: TowerDailyCounters{
			Floors: 4, Antiques: 3, Chests: 3, TRP: 1500, Slays: 3,
		},
	}}}
	got := towerPendingRewards(state, []uint32{towerPresentDaily})
	if len(got) != 6 {
		t.Fatalf("six completed daily missions = %d pending rewards", len(got))
	}
	if got[0].itemID != missions[0].Reward1ID {
		t.Fatal("daily reward no longer matches the day's advertised table")
	}
}

func TestTowerDailyMissionsAllUseActiveTimetable(t *testing.T) {
	day := towerDailyStart(time.Date(2026, 9, 25, 18, 0, 0, 0, time.FixedZone("JST", 9*3600)))
	for slot, mission := range towerDailyMissions(day) {
		if mission.Unk0 != 1 {
			t.Fatalf("mission slot %d uses timetable %d, want 1", slot+1, mission.Unk0)
		}
	}
}

// parseAckSimpleValue reads a simple (non-buffer) ACK: opcode, handle,
// isBuffer=0, error code, u16 0, then the 4-byte value.
func parseAckSimpleValue(t *testing.T, raw []byte) (errorCode uint8, value uint32) {
	t.Helper()
	if len(raw) < 14 {
		t.Fatalf("raw packet too short: %d bytes", len(raw))
	}
	if raw[6] != 0 {
		t.Fatal("expected simple ack, got buffer response")
	}
	return raw[7], binary.BigEndian.Uint32(raw[10:14])
}

func towerPresentListIndexes(t *testing.T, raw []byte) []uint32 {
	t.Helper()
	_, code, payload := parseAckBufData(t, raw)
	if code != 0 || len(payload) < 16 {
		t.Fatalf("list ack code=%d payload=%x", code, payload)
	}
	n := binary.BigEndian.Uint32(payload[12:16])
	if len(payload) != 16+int(n)*44 {
		t.Fatalf("list payload %d bytes for %d rows", len(payload), n)
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(payload[16+i*44:])
	}
	return out
}

func TestTowerPresentBoxReceiveRecordsReceiptsOnly(t *testing.T) {
	repo := &mockTowerRepo{rewardState: TowerRewardState{Floors: 3}}
	server := createMockServer()
	server.towerRepo = repo
	server.erupeConfig.EarthID = 1
	s := createMockSession(56, server)
	types := []uint32{towerPresentFloor, towerPresentAdvance}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 1, Unk1: 1, Unk2: 2, Unk7: types})
	ids := towerPresentListIndexes(t, (<-s.sendPackets).data)
	if len(ids) != 3 || ids[0] != towerClaimIndex(towerRewardFloor, 1) {
		t.Fatalf("floor rewards listed = %v", ids)
	}
	// The hunter had room for floors 1 and 3 only.
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{
		AckHandle: 2, Unk1: 2, Unk2: 2, Unk7: []uint32{ids[0], ids[2]},
	})
	if code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data); code != 0 {
		t.Fatalf("claim ack code %d", code)
	}
	if len(repo.claimedRewards) != 2 ||
		repo.claimedRewards[0] != towerRewardKey(towerRewardFloor, 1) ||
		repo.claimedRewards[1] != towerRewardKey(towerRewardFloor, 3) {
		t.Fatalf("receipts = %v", repo.claimedRewards)
	}
	// The next page is past the end: the receive loop must terminate.
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 3, Unk1: 1, Unk2: 2, Unk3: 0x100, Unk7: types})
	if ids := towerPresentListIndexes(t, (<-s.sendPackets).data); len(ids) != 0 {
		t.Fatalf("page 2 rows = %v", ids)
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 4, Unk1: 1, Unk2: 2, Unk7: types})
	if ids := towerPresentListIndexes(t, (<-s.sendPackets).data); len(ids) != 1 ||
		ids[0] != towerClaimIndex(towerRewardFloor, 2) {
		t.Fatalf("unclaimed reward not listed again: %v", ids)
	}
}

func TestTowerPresentBoxOp3CountsWithoutClaiming(t *testing.T) {
	repo := &mockTowerRepo{rewardState: TowerRewardState{Floors: 3, AdvanceEligible: true}}
	server := createMockServer()
	server.towerRepo = repo
	s := createMockSession(56, server)
	pkt := &mhfpacket.MsgMhfPresentBox{
		AckHandle: 1, Unk1: 3, Unk2: 2,
		Unk7: []uint32{towerPresentFloor, towerPresentAdvance},
	}
	for i := 0; i < 2; i++ {
		handleMsgMhfPresentBox(s, pkt)
		code, value := parseAckSimpleValue(t, (<-s.sendPackets).data)
		if code != 0 || value != 4 {
			t.Fatalf("Op=3 count code=%d value=%d, want 4", code, value)
		}
	}
	if len(repo.claimedRewards) != 0 {
		t.Fatalf("Op=3 claimed rewards: %v", repo.claimedRewards)
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{AckHandle: 2, Unk1: 3})
	if code, value := parseAckSimpleValue(t, (<-s.sendPackets).data); code != 0 || value != 0 {
		t.Fatalf("Op=3 without types code=%d value=%d", code, value)
	}
}

func TestTowerPresentBoxClaimSkipsUnissuedAndRepeated(t *testing.T) {
	repo := &mockTowerRepo{rewardState: TowerRewardState{Floors: 1}}
	server := createMockServer()
	server.towerRepo = repo
	s := createMockSession(56, server)
	id := towerClaimIndex(towerRewardFloor, 1)
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{
		AckHandle: 1, Unk1: 2, Unk2: 3, Unk7: []uint32{12345, id, id},
	})
	if code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data); code != 0 {
		t.Fatalf("claim ack code %d", code)
	}
	handleMsgMhfPresentBox(s, &mhfpacket.MsgMhfPresentBox{
		AckHandle: 2, Unk1: 2, Unk2: 1, Unk7: []uint32{id},
	})
	if code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data); code != 0 {
		t.Fatalf("repeated claim ack code %d", code)
	}
	if len(repo.claimedRewards) != 1 || repo.claimedRewards[0] != towerRewardKey(towerRewardFloor, 1) {
		t.Fatalf("receipts = %v", repo.claimedRewards)
	}
}

func TestTowerPresentPageBounds(t *testing.T) {
	rows := make([]towerPresentReward, 3)
	if got := towerPresentPage(rows, 0); len(got) != 3 {
		t.Fatalf("page 0 = %d rows", len(got))
	}
	if got := towerPresentPage(rows, 2); len(got) != 1 {
		t.Fatalf("offset 2 = %d rows", len(got))
	}
	if got := towerPresentPage(rows, 0x100); got != nil {
		t.Fatalf("offset past end = %d rows", len(got))
	}
	if got := towerPresentPage(nil, 0); got != nil {
		t.Fatal("empty list returned rows")
	}
}
