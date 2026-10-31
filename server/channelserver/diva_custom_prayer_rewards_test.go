package channelserver

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestDivaCustomPrayerMilestoneEligibility(t *testing.T) {
	want := map[uint16]uint16{14410: 1, 14411: 1, 14412: 1, 15163: 1}
	for _, tt := range []struct {
		gr     uint16
		points int64
		want   bool
	}{
		{0, 10000, false}, {1, 9999, false}, {1, 10000, true},
		{1, 10001, true}, {999, 10000, true}, {999, 1 << 40, true},
	} {
		got := make(map[uint16]uint16)
		for _, r := range eligibleDivaSongRewards(1, DivaRewardProgress{HR: 999, GR: tt.gr, Points: tt.points}) {
			if _, custom := want[r.ItemID]; !custom {
				continue
			}
			if r.RewardType != 1 || r.Threshold != 10000 || !r.GR || r.ItemType != 7 || r.NormaRepeat {
				t.Fatalf("wrong custom milestone: %+v", r)
			}
			got[r.ItemID] += r.Quantity
		}
		if (tt.want && !reflect.DeepEqual(got, want)) || (!tt.want && len(got) != 0) {
			t.Fatalf("GR=%d points=%d: got %v", tt.gr, tt.points, got)
		}
	}
	wire := divaPrayerNormaRewardPayload()
	got := make(map[uint16]uint16)
	for at := 2; at < len(wire); at += 19 {
		row := wire[at : at+19]
		item := binary.BigEndian.Uint16(row[1:3])
		if _, custom := want[item]; !custom {
			continue
		}
		if row[0] != 7 || row[5] != 1 || binary.BigEndian.Uint32(row[6:10]) != 999 ||
			binary.BigEndian.Uint32(row[10:14]) != 1 || binary.BigEndian.Uint32(row[14:18]) != 10000 || row[18] != 0 {
			t.Fatalf("wrong GR1+ 10,000-point display row: %x", row)
		}
		got[item] += binary.BigEndian.Uint16(row[3:5])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("displayed custom rewards = %v, want %v", got, want)
	}
}

func TestRepoDivaCustomPrayerRewardsPreserveExistingReceipts(t *testing.T) {
	characters, db, charID, eventID := setupDivaSaveTest(t)
	repo := NewDivaRepository(db)
	old, err := repo.OfferDivaRewards(charID, eventID, 1, divaHistoricalPrayerRewards)
	if err != nil || len(old) != 6 {
		t.Fatalf("historical receipts: %+v %v", old, err)
	}
	rewards := eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: 20000})
	offers, err := repo.OfferDivaRewards(charID, eventID, 1, rewards)
	if err != nil || len(offers) != 10 || !reflect.DeepEqual(offers[:6], old) {
		t.Fatalf("custom additions changed existing receipts: %+v %v", offers, err)
	}
	var ids []uint32
	for _, offer := range offers[6:] {
		if offer.ItemType != 7 || offer.Quantity != 1 {
			t.Fatalf("wrong custom item: %+v", offer)
		}
		ids = append(ids, offer.ID)
	}
	if _, err := repo.PrepareDivaRewardClaims(charID, 1, ids); err != nil {
		t.Fatal(err)
	}
	if err := characters.SaveCharacterDataAtomic(SaveAtomicParams{
		CharID: charID, Name: "RewardHunter", GR: 1, CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: ids,
	}); err != nil {
		t.Fatal(err)
	}
	if pending, err := repo.OfferDivaRewards(charID, eventID, 1, rewards); err != nil || !reflect.DeepEqual(pending, old) {
		t.Fatalf("custom rewards reissued or historical rewards lost: %+v %v", pending, err)
	}
}
