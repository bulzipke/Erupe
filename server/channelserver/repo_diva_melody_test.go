package channelserver

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var _ DivaMelodyRepository = (*DivaRepository)(nil)

func melodyOfferIDs(t *testing.T, r *DivaRepository, charID uint32, event DivaEvent, now time.Time, want int) []uint32 {
	t.Helper()
	rows, err := r.offerDivaRewardsAt(charID, event.ID, 6, nil, now, 2)
	if err != nil || len(rows) != want {
		t.Fatalf("offers=%+v err=%v", rows, err)
	}
	ids := make([]uint32, len(rows))
	for i, row := range rows {
		if row.ItemType != 29 || row.Quantity != uint16(i+1) {
			t.Fatal(row)
		}
		ids[i] = row.ID
	}
	return ids
}

func TestRepoDivaMelodyClaimsSharedMaximumAndConcurrentSpend(t *testing.T) {
	r, db, a, guild, event, start, quest := setupDivaMapRepoTest(t)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	b := CreateTestCharacter(t, db, user, "MelodyAlt")
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO guild_characters(character_id,guild_id,joined_at) VALUES($1,$2,$3)`, b, guild, start); err != nil {
		t.Fatal(err)
	}
	depart := start.Add(time.Minute)
	divaMapTestReport(t, r, a, guild, event, quest, divaRunKeyOne, 47000, depart, depart.Add(time.Minute))
	divaMapTestReport(t, r, b, guild, event, quest, divaRunKeyTwo, 134000, depart, depart.Add(time.Minute))
	now := start.Add(2 * time.Hour)
	idsA := melodyOfferIDs(t, r, a, event, now, 6)
	idsB := melodyOfferIDs(t, r, b, event, now, 10)
	if got, err := r.getDivaMelodyAt(user, a, 2, now); err != nil || got != 0 {
		t.Fatal("availability query credited wallet", got, err)
	}
	if _, err := r.prepareDivaRewardClaimsAt(b, 6, idsA, now, 2); err == nil {
		t.Fatal("foreign receipt accepted")
	}
	for range 2 {
		rows, err := r.prepareDivaRewardClaimsAt(a, 6, idsA, now, 2)
		if err != nil || len(rows) != 0 {
			t.Fatal("server-only caps reached savedata staging", rows, err)
		}
	}
	if got, err := r.getDivaMelodyAt(user, b, 2, now); err != nil || got != 6 {
		t.Fatal(got, err)
	}
	if _, err := r.useDivaMelodyAt(user, a, 1, 2, now); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("spent before welcome", err)
	}
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude)*time.Second + time.Minute)
	if got, err := r.useDivaMelodyAt(user, a, 2, 3, welcome); err != nil || got != 4 {
		t.Fatal(got, err)
	}
	if _, err := r.prepareDivaRewardClaimsAt(b, 6, idsB, welcome, 3); err != nil {
		t.Fatal(err)
	}
	// max(6,10)-2=8, not 6+10-2 and not a reset of the two spent points.
	if got, err := r.getDivaMelodyAt(user, a, 3, welcome); err != nil || got != 8 {
		t.Fatal(got, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			char := a
			if i%2 == 0 {
				char = b
			}
			_, err := r.useDivaMelodyAt(user, char, 1, 3, welcome)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, errDivaMelodyUnavailable) {
			t.Fatal(err)
		}
	}
	if success != 8 {
		t.Fatal("concurrent overspend", success)
	}
	if got, err := r.getDivaMelodyAt(user, b, 3, welcome); err != nil || got != 0 {
		t.Fatal(got, err)
	}
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, idsA, welcome, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaMelodyAt(user, a, 3, welcome); err != nil || got != 0 {
		t.Fatal("retry replenished spent balance", got, err)
	}
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	if got, err := r.getDivaMelodyAt(user, a, 1, end); err != nil || got != 0 {
		t.Fatal(got, err)
	}
	if _, err := r.useDivaMelodyAt(user, a, 1, 1, end); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("expired purchase", err)
	}
	var earned, spent int
	if err := db.QueryRow(`SELECT earned,spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2`, user, event.ID).Scan(&earned, &spent); err != nil || earned != 10 || spent != 10 {
		t.Fatal(earned, spent, err)
	}
}

func TestRepoDivaMelodyGRExpiryAndOrphanWindows(t *testing.T) {
	r, db, a, guild, event, start, quest := setupDivaMapRepoTest(t)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	depart := start.Add(time.Minute)
	divaMapTestReport(t, r, a, guild, event, quest, divaRunKeyOne, 134000, depart, depart.Add(time.Minute))
	now := start.Add(time.Hour)
	ids := melodyOfferIDs(t, r, a, event, now, 10)
	material := DivaRewardCatalogEntry{Key: "melody-expiry-material", RewardType: 6, ItemType: 7, ItemID: 1, Quantity: 1}
	all, err := r.offerDivaRewardsAt(a, event.ID, 6, []DivaRewardCatalogEntry{material}, now, 2)
	if err != nil || len(all) != 11 {
		t.Fatal(all, err)
	}
	materialID := all[len(all)-1].ID
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, ids, end, 1); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal("expired cap claimed", err)
	}
	rows, err := r.offerDivaRewardsAt(a, event.ID, 6, nil, end, 1)
	if err != nil || len(rows) != 1 || rows[0].ID != materialID || rows[0].ItemType != 7 {
		t.Fatal("expired caps offered", rows, err)
	}
	staged, err := r.prepareDivaRewardClaimsAt(a, 6, []uint32{materialID}, end, 1)
	if err != nil || len(staged) != 1 || staged[0].ItemType != 7 {
		t.Fatal("melody expiry blocked surviving material", staged, err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_melody_wallets`); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, ids, now, 2); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaMelodyAt(user+12345, a, 2, now); err != nil || got != 0 {
		t.Fatal("foreign owner read", got, err)
	}
	if _, err := db.Exec(`DELETE FROM diva_interception_periods WHERE event_id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaMelodyAt(user, a, 1, now); err != nil || got != 0 {
		t.Fatal("orphan round balance revived", got, err)
	}
	if _, err := r.useDivaMelodyAt(user, a, 1, 1, now); !errors.Is(err, errDivaMelodyUnavailable) {
		t.Fatal(err)
	}
}

func TestRepoDivaMelodyHRCustomThresholdBoundaries(t *testing.T) {
	r, db, a, guild, event, start, quest := setupDivaMapRepoTest(t)
	depart := start.Add(time.Minute)
	divaMapTestReport(t, r, a, guild, event, quest, divaRunKeyOne, 1, depart, depart.Add(time.Minute))
	now := start.Add(time.Hour)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	for i, threshold := range [...]uint32{7000, 17000, 27000, 37500, 42000, 47000, 52000, 57000, 62000, 67000} {
		for _, points := range []uint32{threshold - 1, threshold} {
			// Only this fixture's personal total changes; wallet credit must
			// still wait for an explicit immutable receipt claim.
			if _, err := db.Exec(`UPDATE diva_interception_runs SET points=$3 WHERE char_id=$1 AND event_id=$2`, a, event.ID, points); err != nil {
				t.Fatal(err)
			}
			want := i
			if points == threshold {
				want++
			}
			rows, err := r.offerDivaRewardsAt(a, event.ID, 6, nil, now, 2)
			if err != nil || len(rows) != want {
				t.Fatalf("points %d: rows=%+v err=%v", points, rows, err)
			}
			for j, row := range rows {
				if row.CatalogKey != fmt.Sprintf("custom-hr-melody-cap-%d", j+1) || row.Quantity != uint16(j+1) {
					t.Fatal("HR was offered a GR or incorrect cap", row)
				}
			}
			if got, err := r.getDivaMelodyAt(user, a, 2, now); err != nil || got != 0 {
				t.Fatal("listing credited the wallet", got, err)
			}
		}
	}
}

func TestRepoDivaMelodyHRPromotionPreservesSpentAndPendingCaps(t *testing.T) {
	r, db, a, guild, event, start, quest := setupDivaMapRepoTest(t)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	depart := start.Add(time.Minute)
	divaMapTestReport(t, r, a, guild, event, quest, divaRunKeyOne, 67000, depart, depart.Add(time.Minute))
	now := start.Add(time.Hour)
	hrIDs := melodyOfferIDs(t, r, a, event, now, 10)
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, []uint32{hrIDs[5]}, now, 2); err != nil {
		t.Fatal(err)
	}
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude)*time.Second + time.Minute)
	if got, err := r.useDivaMelodyAt(user, a, 2, 3, welcome); err != nil || got != 4 {
		t.Fatal(got, err)
	}
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	rows, err := r.offerDivaRewardsAt(a, event.ID, 6, nil, welcome, 3)
	if err != nil || len(rows) != 12 { // Nine pending HR receipts plus three GR caps.
		t.Fatal(rows, err)
	}
	var grIDs []uint32
	for _, row := range rows {
		if row.CatalogKey == "melody-cap-1" || row.CatalogKey == "melody-cap-2" || row.CatalogKey == "melody-cap-3" {
			grIDs = append(grIDs, row.ID)
		}
	}
	if len(grIDs) != 3 {
		t.Fatal("promotion did not use preserved GR keys", rows)
	}
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, grIDs, welcome, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaMelodyAt(user, a, 3, welcome); err != nil || got != 4 {
		t.Fatal("promotion added caps or reset spending", got, err)
	}
	// The already offered HR cap remains claimable after promotion. It raises
	// max(6,3) to 10 while the original two spent points remain spent.
	if _, err := r.prepareDivaRewardClaimsAt(a, 6, []uint32{hrIDs[9]}, welcome, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaMelodyAt(user, a, 3, welcome); err != nil || got != 8 {
		t.Fatal("pending HR receipt changed semantics", got, err)
	}
	for range 2 {
		if _, err := r.prepareDivaRewardClaimsAt(a, 6, hrIDs, welcome, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := r.prepareDivaRewardClaimsAt(a, 6, grIDs, welcome, 3); err != nil {
			t.Fatal(err)
		}
	}
	var earned, spent int
	if err := db.QueryRow(`SELECT earned,spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2`, user, event.ID).Scan(&earned, &spent); err != nil || earned != 10 || spent != 2 {
		t.Fatal("repeated rank-specific claims changed shared wallet", earned, spent, err)
	}
	var oldGRCount int
	if err := db.Get(&oldGRCount, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND catalog_key IN ('melody-cap-1','melody-cap-2','melody-cap-3')`, a, event.ID); err != nil || oldGRCount != 3 {
		t.Fatal("GR receipt identity changed", oldGRCount, err)
	}
}
