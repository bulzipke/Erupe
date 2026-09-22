package channelserver

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaHRGuildApprovedCatalog(t *testing.T) {
	rows := divaHRGuildRewards()
	want := [][4]uint16{
		{2, 7, 1026, 5}, {3, 7, 1026, 10}, {5, 26, 0, 100},
		{6, 7, 1026, 10}, {8, 26, 0, 200}, {10, 7, 1026, 10},
		{20, 26, 0, 300}, {22, 7, 1026, 20}, {24, 26, 0, 400},
		{26, 26, 0, 500},
	}
	if len(rows) != len(want) {
		t.Fatalf("HR guild rows=%d expected=%d", len(rows), len(want))
	}
	if err := validateDivaRewardCatalog(7, rows); err != nil {
		t.Fatal(err)
	}
	areas := make(map[uint32]bool)
	var tickets, gp uint32
	for i, row := range rows {
		v := want[i]
		if row.Key != fmt.Sprintf("custom-tactics-guild-hr-%d", v[0]) || row.RewardType != 7 ||
			row.Threshold != uint32(v[0]) || row.ItemType != uint8(v[1]) || row.ItemID != v[2] || row.Quantity != v[3] ||
			row.GR || row.MinHR != 2 || row.MaxHR != 999 || row.NormaRepeat ||
			row.Basis != "operator-approved-2026-09-23-hr-guild" {
			t.Fatalf("unapproved HR guild row: %+v", row)
		}
		if areas[row.Threshold] {
			t.Fatal("duplicate milestone", row.Threshold)
		}
		areas[row.Threshold] = true
		if row.ItemType == 7 {
			tickets += uint32(row.Quantity)
		} else {
			gp += uint32(row.Quantity)
		}
	}
	if tickets != 55 || gp != 1500 {
		t.Fatalf("HR total changed: tickets=%d GP=%d", tickets, gp)
	}
	grAreas := make(map[uint32]bool)
	for _, row := range divaApprovedGuildRewards() {
		grAreas[row.Threshold] = true
	}
	if !reflect.DeepEqual(areas, grAreas) {
		t.Fatalf("HR milestones diverged from GR: HR=%v GR=%v", areas, grAreas)
	}
	rows[0].Quantity = 999
	if divaHRGuildRewards()[0].Quantity != 5 {
		t.Fatal("caller mutated shared catalog")
	}
}

func TestDivaHRGuildDisplayRankBoundsAndPreview(t *testing.T) {
	for _, tt := range []struct {
		hr, gr uint16
		want   int
	}{
		{0, 0, 0}, {1, 0, 0}, {2, 0, 10}, {99, 0, 10},
		{100, 0, 10}, {999, 0, 10}, {1000, 0, 0}, {65535, 0, 0},
		{0, 1, 10}, {999, 1, 10}, {1000, 999, 10},
	} {
		rows := divaHRGuildDisplay(tt.hr, tt.gr)
		if len(rows) != tt.want {
			t.Fatalf("hr=%d gr=%d display rows=%d expected=%d", tt.hr, tt.gr, len(rows), tt.want)
		}
		for _, row := range rows {
			if row.Type != "guild" || row.GR || row.Repeatable || row.ID != 0 {
				t.Fatalf("display row misclassified: %+v", row)
			}
		}
		for _, row := range divaHRGuildRewards() {
			wantEligible := tt.gr == 0 && tt.hr >= 2 && tt.hr <= 999
			if divaRewardRankMatches(row, tt.hr, tt.gr) != wantEligible {
				t.Fatalf("preview granted acquisition eligibility: HR=%d GR=%d %+v", tt.hr, tt.gr, row)
			}
		}
	}
}

type divaHRGuildDisplayTestRepo struct {
	mockDivaRepo
	hr, gr      uint16
	rankCalls   int
	guildPrizes []DivaPrize
}

func (r *divaHRGuildDisplayTestRepo) GetDivaRewardRanks(uint32) (uint16, uint16, error) {
	r.rankCalls++
	return r.hr, r.gr, nil
}

func (r *divaHRGuildDisplayTestRepo) GetGuildPrizes() ([]DivaPrize, error) {
	return r.guildPrizes, nil
}

func TestDivaHRGuildDisplayHandlerWireAndRankReuse(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mode   cfg.Mode
		hr, gr uint16
		wantHR int
	}{
		{"lower HR", cfg.ZZ, 2, 0, 10},
		{"upper HR", cfg.ZZ, 999, 0, 10},
		{"GR preview", cfg.ZZ, 999, 1, 10},
		{"invalid HR", cfg.ZZ, 1, 0, 0},
		{"old client", cfg.ZZ - 1, 2, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := createMockSession(1, createMockServer())
			s.server.erupeConfig.RealClientMode = tt.mode
			original := divaGuildTestPrizes()
			r := &divaHRGuildDisplayTestRepo{hr: tt.hr, gr: tt.gr, guildPrizes: append([]DivaPrize(nil), original...)}
			s.server.divaRepo = r
			handleMsgMhfGetUdTacticsRewardList(s, &mhfpacket.MsgMhfGetUdTacticsRewardList{AckHandle: 80})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || len(ack.Payload) < 7 || ack.Payload[0] != 0 {
				t.Fatalf("bad reward-list ACK: %+v", ack)
			}
			personalCount := int(binary.BigEndian.Uint16(ack.Payload[1:3]))
			guildAt := 3 + 11*personalCount
			guildCount := int(binary.BigEndian.Uint16(ack.Payload[guildAt : guildAt+2]))
			if guildCount != 12+tt.wantHR || len(ack.Payload) != 7+(personalCount+guildCount)*11 {
				t.Fatalf("bad guild count/packet length: guild=%d length=%d", guildCount, len(ack.Payload))
			}
			var lastArea uint32
			var grRows, hrRows int
			for at := guildAt + 2; at < guildAt+2+guildCount*11; at += 11 {
				row := ack.Payload[at : at+11]
				area := binary.BigEndian.Uint32(row[:4])
				if area < lastArea || row[10] != 0 {
					t.Fatalf("unsorted or repeating guild row: %x", row)
				}
				lastArea = area
				if row[9] == 1 {
					grRows++
				} else if row[9] == 0 {
					hrRows++
				} else {
					t.Fatalf("invalid guild rank flag: %x", row)
				}
			}
			if grRows != 12 || hrRows != tt.wantHR || !reflect.DeepEqual(original, r.guildPrizes) {
				t.Fatal("original GR catalog mutated or HR catalog missing")
			}
			wantRankCalls := 0
			if tt.mode == cfg.ZZ {
				wantRankCalls = 1
			}
			if r.rankCalls != wantRankCalls || binary.BigEndian.Uint16(ack.Payload[len(ack.Payload)-2:]) != 0 {
				t.Fatal("duplicate rank lookup or trailing ranking layout changed")
			}
		})
	}
}
