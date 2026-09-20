package channelserver

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
)

func TestDivaHistoricalSingleThresholdCatalog(t *testing.T) {
	want := []struct {
		points uint32
		item   uint16
		amount uint16
	}{{24500, 0x070a, 10}, {29500, 0x070b, 10}, {99000, 0x2337, 10}, {100500, 0x2338, 5}, {102000, 0x2339, 5}}
	if len(divaHistoricalSingleThresholdPrayerRewards) != len(want) {
		t.Fatal("single-threshold historical catalog changed")
	}
	for i, r := range divaHistoricalSingleThresholdPrayerRewards {
		w := want[i]
		if r.Key != fmt.Sprintf("historical-2016-norma-gr-%d-%04x", w.points, w.item) ||
			r.Threshold != w.points || r.ItemID != w.item || r.Quantity != w.amount ||
			r.RewardType != 1 || !r.GR || r.ItemType != 7 ||
			r.Basis != "historical-2016-single-threshold-gr-inferred" {
			t.Fatalf("historical row %d changed: %+v", i, r)
		}
		group, variant := divaRewardGroup(r)
		if group != fmt.Sprintf("milestone-%d", w.points) || variant != "gr" {
			t.Fatalf("historical row bypassed milestone receipt group: %s/%s", group, variant)
		}
	}
	catalog := divaPrayerRewardCatalog()
	if len(catalog) != 23 {
		t.Fatalf("combined prayer catalog = %d, want 23", len(catalog))
	}
	if err := validateDivaRewardCatalog(1, catalog); err != nil {
		t.Fatalf("combined catalog contains invalid or duplicate receipt keys: %v", err)
	}
	if !reflect.DeepEqual(catalog[:6], divaHistoricalPrayerRewards) {
		t.Fatal("new rows changed the original six 20,000-point receipts")
	}
	wire := divaNormaRewardPayload(divaHistoricalSingleThresholdPrayerRewards)
	if len(wire) != 2+5*19 || binary.BigEndian.Uint16(wire) != 5 {
		t.Fatal("wrong single-threshold wire count")
	}
	for i, w := range want {
		row := wire[2+i*19 : 2+(i+1)*19]
		if row[0] != 7 || binary.BigEndian.Uint16(row[1:3]) != w.item ||
			binary.BigEndian.Uint16(row[3:5]) != w.amount || row[5] != 1 ||
			binary.BigEndian.Uint32(row[14:18]) != w.points || row[18] != 0 {
			t.Fatalf("wrong historical wire row: %x", row)
		}
	}
}

func TestDivaHistoricalSingleThresholdEligibility(t *testing.T) {
	for _, r := range divaHistoricalSingleThresholdPrayerRewards {
		for _, gr := range []uint16{0, 1, 999} {
			for _, points := range []int64{0, int64(r.Threshold) - 1, int64(r.Threshold), int64(r.Threshold) + 1, 1 << 40} {
				rows := eligibleDivaSongRewards(1, DivaRewardProgress{HR: 999, GR: gr, Points: points})
				found := 0
				for _, row := range rows {
					if row.Key == r.Key {
						found++
					}
				}
				want := 0
				if gr > 0 && points >= int64(r.Threshold) {
					want = 1
				}
				if found != want {
					t.Fatalf("%s GR=%d points=%d appears %d times, want %d", r.Key, gr, points, found, want)
				}
			}
		}
	}
	for _, points := range []int64{102000, 102001, 1 << 40} {
		if rows := eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: points}); len(rows) != 11 {
			t.Fatalf("high score expanded finite milestones into repeats: %+v", rows)
		}
	}
}

func TestDivaHistoricalSingleThresholdReceiptPreservation(t *testing.T) {
	characters, db, charID, eventID := setupDivaSaveTest(t)
	repo := NewDivaRepository(db)
	old, err := repo.OfferDivaRewards(charID, eventID, 1, eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: 20000}))
	if err != nil || len(old) != 6 {
		t.Fatalf("original 20k receipts: %+v %v", old, err)
	}
	all := eligibleDivaSongRewards(1, DivaRewardProgress{GR: 1, Points: 102000})
	offers, err := repo.OfferDivaRewards(charID, eventID, 1, all)
	if err != nil || len(offers) != 11 || !reflect.DeepEqual(offers[:6], old) {
		t.Fatalf("historical additions replaced original receipts: %+v %v", offers, err)
	}
	changed := append([]DivaRewardCatalogEntry(nil), all...)
	for i := 6; i < len(changed); i++ {
		changed[i].Quantity++
	}
	again, err := repo.OfferDivaRewards(charID, eventID, 1, changed)
	if err != nil || !reflect.DeepEqual(again, offers) {
		t.Fatalf("requery or catalog edit replaced issued snapshots: %+v %v", again, err)
	}
	var addedIDs []uint32
	for _, offer := range offers[6:] {
		addedIDs = append(addedIDs, offer.ID)
	}
	if prepared, err := repo.PrepareDivaRewardClaims(charID, 1, addedIDs); err != nil || !reflect.DeepEqual(prepared, offers[6:]) {
		t.Fatalf("new historical claims: %+v %v", prepared, err)
	}
	// Claim preparation must not consume a receipt before the matching save.
	if pending, err := repo.OfferDivaRewards(charID, eventID, 1, all); err != nil || !reflect.DeepEqual(pending, offers) {
		t.Fatalf("prepare consumed historical receipts: %+v %v", pending, err)
	}
	params := SaveAtomicParams{CharID: charID, Name: "RewardHunter", CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: addedIDs}
	if err := characters.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		remaining, err := repo.OfferDivaRewards(charID, eventID, 1, eligibleDivaSongRewards(1, DivaRewardProgress{GR: 999, Points: 1 << 40}))
		if err != nil || !reflect.DeepEqual(remaining, old) {
			t.Fatalf("claimed additions reissued or old pending rows lost: %+v %v", remaining, err)
		}
	}
	if retry, err := repo.PrepareDivaRewardClaims(charID, 1, addedIDs); err != nil || len(retry) != 0 {
		t.Fatalf("claimed receipt retry grants historical items again: %+v %v", retry, err)
	}
	var receiptCount, claimedCount, groupCount int
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(claimed_at) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=1`, charID, eventID).Scan(&receiptCount, &claimedCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&groupCount, `SELECT COUNT(*) FROM diva_reward_groups WHERE char_id=$1 AND event_id=$2 AND reward_type=1`, charID, eventID); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 11 || claimedCount != 5 || groupCount != 6 {
		t.Fatalf("historical receipt/group counts = %d/%d/%d", receiptCount, claimedCount, groupCount)
	}
}
