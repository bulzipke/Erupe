package channelserver

import (
	"errors"
	"testing"
)

// Cost types the client pays from the character save pass without a server
// deduction; a cost type nobody pays is refused instead of rolling for free.
func TestGachaTransactCostTypes(t *testing.T) {
	for _, itemType := range []uint8{7, gachaItemTypeZenny, 12, 13, 14, 16} {
		gr := &mockGachaRepo{txItemType: itemType, txItemNumber: 5, txRolls: 1}
		svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
		if rolls, err := svc.transact(1, 1, 1, 0); err != nil || rolls != 1 {
			t.Errorf("client-paid cost type %d: rolls %d err %v", itemType, rolls, err)
		}
	}
	for _, itemType := range []uint8{1, 3, 8, 11, 15, 18, 22} {
		gr := &mockGachaRepo{txItemType: itemType, txItemNumber: 5, txRolls: 1}
		svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
		if rolls, err := svc.transact(1, 1, 1, 0); !errors.Is(err, errGachaUnsupportedCost) || rolls != 0 {
			t.Errorf("unpaid cost type %d: rolls %d err %v", itemType, rolls, err)
		}
	}
}

// Zenny rewards are credited by the client and never reach the pending box.
func TestGachaSaveItemsSkipsZenny(t *testing.T) {
	cr := newMockCharacterRepo()
	svc := newTestGachaService(nil, nil, cr)
	svc.saveGachaItems(7, []GachaItem{
		{ItemType: gachaItemTypeZenny, ItemID: 3},
		{ItemType: 7, ItemID: 8, Quantity: 1},
		{ItemType: gachaItemTypeZenny, ItemID: 1},
	})
	saved := cr.columns["gacha_items"]
	if len(saved) != 1+gachaItemRecordBytes || saved[0] != 1 || saved[1] != 7 {
		t.Fatalf("saved blob % x, want one item record of type 7", saved)
	}

	onlyZenny := newMockCharacterRepo()
	svc = newTestGachaService(nil, nil, onlyZenny)
	svc.saveGachaItems(7, []GachaItem{{ItemType: gachaItemTypeZenny, ItemID: 2}})
	if data, ok := onlyZenny.columns["gacha_items"]; ok {
		t.Fatalf("zenny-only roll wrote % x", data)
	}
}

// A corrupt pending box refuses the roll before its cost is paid.
func TestGachaCorruptPendingItemsChargeNothing(t *testing.T) {
	cr := newMockCharacterRepo()
	cr.ints["netcafe_points"] = 100
	cr.columns["gacha_items"] = []byte{3, 7, 0, 8, 0} // declares 3 records, holds less than one
	gr := &mockGachaRepo{
		txItemType: 17, txItemNumber: 10, txRolls: 1,
		rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
		entryItems: map[uint32][]GachaItem{10: {{ItemType: 7, ItemID: 8, Quantity: 1}}},
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)
	if _, err := svc.PlayNormalGacha(1, 1, 1, 0); !errors.Is(err, errGachaItemsBlocked) {
		t.Fatalf("err %v, want errGachaItemsBlocked", err)
	}
	if cr.ints["netcafe_points"] != 100 {
		t.Fatalf("net cafe points %d, want 100 (not charged)", cr.ints["netcafe_points"])
	}
}

func TestGachaBoxBallCount(t *testing.T) {
	for _, tc := range []struct {
		weight float64
		want   int
	}{{0, 1}, {-3, 1}, {1, 1}, {1.9, 1}, {5, 5}, {255, 255}, {300, 255}} {
		if got := gachaBoxBallCount(tc.weight); got != tc.want {
			t.Errorf("gachaBoxBallCount(%v) = %d, want %d", tc.weight, got, tc.want)
		}
	}
}

// A box draws only the balls the character has not drawn yet.
func TestGachaBoxDrawsOnlyRemainingBalls(t *testing.T) {
	gr := &mockGachaRepo{
		txRolls: 1,
		rewardPool: []GachaEntry{
			{ID: 1, Weight: 2},
			{ID: 2, Weight: 1},
		},
		entryItems: map[uint32][]GachaItem{
			1: {{ItemType: 7, ItemID: 100, Quantity: 1}},
			2: {{ItemType: 7, ItemID: 200, Quantity: 1}},
		},
		boxEntryIDs: []uint32{1, 1}, // both balls of entry 1 are gone
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
	result, err := svc.PlayBoxGacha(1, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gr.insertedBoxIDs) != 1 || gr.insertedBoxIDs[0] != 2 {
		t.Fatalf("drew %v, want [2]", gr.insertedBoxIDs)
	}
	if len(result.Rewards) != 1 || result.Rewards[0].ItemID != 200 {
		t.Fatalf("rewards %+v, want item 200", result.Rewards)
	}
}

// A roll that needs more balls than are left is refused and charges nothing.
func TestGachaBoxShortRollChargesNothing(t *testing.T) {
	cr := newMockCharacterRepo()
	cr.ints["netcafe_points"] = 100
	gr := &mockGachaRepo{
		txItemType: 17, txItemNumber: 10, txRolls: 2,
		rewardPool:  []GachaEntry{{ID: 1, Weight: 2}},
		entryItems:  map[uint32][]GachaItem{1: {{ItemType: 7, ItemID: 100, Quantity: 1}}},
		boxEntryIDs: []uint32{1},
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)
	if _, err := svc.PlayBoxGacha(1, 1, 1, 0); !errors.Is(err, errGachaBoxShort) {
		t.Fatalf("err %v, want errGachaBoxShort", err)
	}
	if cr.ints["netcafe_points"] != 100 || len(gr.insertedBoxIDs) != 0 {
		t.Fatalf("points %d drawn %v, want 100 and none", cr.ints["netcafe_points"], gr.insertedBoxIDs)
	}
}

func TestGachaBoxRemainingBalls(t *testing.T) {
	entries := []GachaEntry{{ID: 1, Weight: 3}, {ID: 2, Weight: 0}, {ID: 3, Weight: 1}}
	drawn := []BoxDrawCount{{EntryID: 1, Count: 1}, {EntryID: 3, Count: 4}}
	balls := gachaBoxRemainingBalls(entries, drawn)
	got := map[uint32]int{}
	for _, b := range balls {
		got[b.ID]++
	}
	if len(balls) != 3 || got[1] != 2 || got[2] != 1 || got[3] != 0 {
		t.Fatalf("remaining %v, want entry 1 x2 and entry 2 x1", got)
	}
	if picks := gachaBoxDraw(balls, 5); len(picks) != 3 {
		t.Fatalf("drew %d balls from 3", len(picks))
	}
}

func TestGachaResetRefusesOneTimeBox(t *testing.T) {
	gr := &mockGachaRepo{oneTimeBox: true}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
	if err := svc.ResetBox(1, 1); !errors.Is(err, errGachaBoxOneTime) {
		t.Fatalf("err %v, want errGachaBoxOneTime", err)
	}
	if gr.deletedBox {
		t.Fatal("one-time box was reset")
	}
}

// The one-time bit is only sent for box gachas.
func TestGachaListFlags(t *testing.T) {
	for _, tc := range []struct {
		g    Gacha
		want uint8
	}{
		{Gacha{GachaType: 4, OneTime: true}, gachaListFlagOneTimeBox},
		{Gacha{GachaType: 5, OneTime: true, Hidden: true}, gachaListFlagOneTimeBox | gachaListFlagHidden},
		{Gacha{GachaType: 0, OneTime: true}, 0},
		{Gacha{GachaType: 1, Hidden: true}, gachaListFlagHidden},
		{Gacha{GachaType: 4}, 0},
	} {
		if got := gachaListFlags(tc.g); got != tc.want {
			t.Errorf("gachaListFlags(%+v) = %#x, want %#x", tc.g, got, tc.want)
		}
	}
}

// A step-up roll must be the character's current step.
func TestGachaStepupRejectsOutOfOrderStep(t *testing.T) {
	cr := newMockCharacterRepo()
	cr.ints["netcafe_points"] = 100
	gr := &mockGachaRepo{
		txItemType: 17, txItemNumber: 10, txRolls: 1,
		rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
		entryItems: map[uint32][]GachaItem{10: {{ItemType: 7, ItemID: 8, Quantity: 1}}},
		// no stored step: the character is on step 0
	}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, cr)
	if _, err := svc.PlayStepupGacha(1, 1, 1, 3); !errors.Is(err, errGachaStepOrder) {
		t.Fatalf("err %v, want errGachaStepOrder", err)
	}
	if cr.ints["netcafe_points"] != 100 || gr.insertedStep != 0 {
		t.Fatalf("points %d step %d, want 100 and no advance", cr.ints["netcafe_points"], gr.insertedStep)
	}
}
