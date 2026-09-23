package channelserver

import (
	"encoding/binary"
	"testing"

	"erupe-ce/network/mhfpacket"
)

func TestTowerFloorRewardCatalog(t *testing.T) {
	if len(towerFloorRewards) != 115 {
		t.Fatalf("floor reward entries = %d, want 115", len(towerFloorRewards))
	}
	previous := int32(0)
	for _, reward := range towerFloorRewards {
		if reward.Unk0 <= previous || reward.Unk0 > 500 || reward.Unk3 != 7201 || reward.Unk4 == 0 || reward.Unk5 <= 0 {
			t.Fatalf("invalid floor milestone: %+v", reward)
		}
		previous = reward.Unk0
	}
	if towerFloorRewards[0].Unk4 != 0x2B96 || towerFloorRewards[len(towerFloorRewards)-1].Unk0 != 500 {
		t.Fatal("screenshot first and last floor milestones changed")
	}
}

func TestTowerRewardPacketRouting(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(1, srv)
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{
		AckHandle: 1, Unk1: 5, Unk2: 260003,
	})
	_, code, data := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || len(data) != 16+len(towerFloorRewards)*24 {
		t.Fatalf("tower reward payload code=%d length=%d", code, len(data))
	}
	if count := binary.BigEndian.Uint32(data[12:16]); count != uint32(len(towerFloorRewards)) {
		t.Fatalf("tower reward count=%d", count)
	}
	if floor := binary.BigEndian.Uint32(data[16:20]); floor != 1 {
		t.Fatalf("first reward floor=%d", floor)
	}
	if item := binary.BigEndian.Uint32(data[32:36]); item != 0x2B96 {
		t.Fatalf("first reward item=%x", item)
	}

	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{
		AckHandle: 2, Unk1: 5, Unk2: 260001,
	})
	_, _, data = parseAckBufData(t, (<-s.sendPackets).data)
	if count := binary.BigEndian.Uint32(data[12:16]); count != 1 {
		t.Fatalf("unverified Dure catalog should retain default response, got %d", count)
	}
}

func TestTowerGuildSubstituteRewardPacket(t *testing.T) {
	srv := createMockServer()
	s := createMockSession(1, srv)
	handleMsgMhfGetTenrouirai(s, &mhfpacket.MsgMhfGetTenrouirai{AckHandle: 1, DataType: 2})
	_, code, data := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || len(data) != 16+len(towerGuildRewards)*16 {
		t.Fatalf("guild reward payload code=%d length=%d", code, len(data))
	}
	if count := binary.BigEndian.Uint32(data[12:16]); count != uint32(len(towerGuildRewards)) {
		t.Fatalf("guild reward count=%d", count)
	}
	if data[16] != 1 || binary.BigEndian.Uint16(data[17:19]) != 0x2B97 {
		t.Fatal("first guild investigation reward was not encoded")
	}
}

func TestTowerMissionStatsBoundsAndMapping(t *testing.T) {
	stats := TowerMissionStats{Floors: 4, Antiques: 3, Chests: 2, Cats: 1, TRP: 4000, Slays: 5}
	if !stats.Valid() {
		t.Fatal("ordinary tower run rejected")
	}
	for kind, want := range []int{4, 3, 2, 1, 4000, 5} {
		if got := stats.missionValue(uint8(kind + 1)); got != want {
			t.Fatalf("mission %d=%d, want %d", kind+1, got, want)
		}
	}
	if (TowerMissionStats{Floors: 5}).Valid() || (TowerMissionStats{TRP: 50001}).Valid() {
		t.Fatal("impossible run counters accepted")
	}
}
