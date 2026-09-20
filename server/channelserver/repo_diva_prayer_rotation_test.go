package channelserver

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setupDivaPrayerRotationRepoTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32) {
	t.Helper()
	repo, db, charID, eventID := setupDivaRewardRepoTest(t)
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	CreateTestUserBinary(t, db, charID)
	return repo, db, charID, eventID
}

func divaPrayerRotationTestCursor(t *testing.T, db *sqlx.DB, charID, eventID uint32) int64 {
	t.Helper()
	var next int64
	err := db.Get(&next, `SELECT next_index FROM diva_prayer_rotation_progress WHERE char_id=$1 AND event_id=$2 AND track='gr'`, charID, eventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return next
}

func divaPrayerRotationTestOffers(offers []DivaRewardOffer) []DivaRewardOffer {
	var result []DivaRewardOffer
	for _, offer := range offers {
		if strings.HasPrefix(offer.CatalogKey, "rotation-") {
			result = append(result, offer)
		}
	}
	return result
}

func assertDivaPrayerRotationTestSequence(t *testing.T, offers []DivaRewardOffer, first int) {
	t.Helper()
	for i, offer := range offers {
		index := first + i
		if offer.CatalogKey != fmt.Sprintf("rotation-gr-crystals-v1-%d", index) || offer.ItemType != 7 ||
			offer.RewardType != 1 || offer.ItemID != uint16(0x2338+index%2) || offer.Quantity != 5 {
			t.Fatalf("rotation index %d = %+v", index, offer)
		}
	}
}

func divaPrayerRotationTestReceiptCounts(t *testing.T, db *sqlx.DB, charID, eventID uint32) (total, claimed int) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(claimed_at) FROM diva_reward_receipts
		WHERE char_id=$1 AND event_id=$2 AND reward_type=1`, charID, eventID).Scan(&total, &claimed); err != nil {
		t.Fatal(err)
	}
	return
}

func saveDivaPrayerRotationTestItems(t *testing.T, repo *DivaRepository, db *sqlx.DB, charID uint32, offers []DivaRewardOffer) {
	t.Helper()
	ids := make([]uint32, len(offers))
	for i, offer := range offers {
		if offer.ItemType != 7 {
			t.Fatalf("item-save helper received non-item: %+v", offer)
		}
		ids[i] = offer.ID
	}
	if _, err := repo.PrepareDivaRewardClaims(charID, 1, ids); err != nil {
		t.Fatal(err)
	}
	if err := NewCharacterRepository(db).SaveCharacterDataAtomic(SaveAtomicParams{
		CharID: charID, Name: "RotationTester", GR: 1, CompSave: []byte{1, 2, 3},
		HouseData: []byte{4}, DivaRewardIDs: ids,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDivaPrayerRotationThresholds(t *testing.T) {
	for _, tc := range []struct {
		points int64
		count  int
	}{
		{-1, 0}, {102999, 0}, {103000, 1}, {103999, 1}, {104000, 2}, {105000, 3},
	} {
		t.Run(fmt.Sprint(tc.points), func(t *testing.T) {
			repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
			progress := DivaRewardProgress{GR: 1, Points: tc.points}
			offers, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
			if err != nil {
				t.Fatal(err)
			}
			rotation := divaPrayerRotationTestOffers(offers)
			if len(rotation) != tc.count || len(offers) != len(eligibleDivaSongRewards(1, progress))+tc.count {
				t.Fatalf("score %d: %d offers, %d rotation rows; want %d repeats", tc.points, len(offers), len(rotation), tc.count)
			}
			assertDivaPrayerRotationTestSequence(t, rotation, 0)
			if next := divaPrayerRotationTestCursor(t, db, charID, eventID); next != int64(tc.count) {
				t.Fatalf("cursor = %d, want %d", next, tc.count)
			}
		})
	}
}

func TestRepoDivaPrayerRotationBoundedOffersAndPartialSave(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	progress := DivaRewardProgress{GR: 1, Points: 1000000}
	first, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || len(first) != 32 {
		t.Fatalf("first batch: %d, %v", len(first), err)
	}
	rotation := divaPrayerRotationTestOffers(first)
	if len(rotation) != 21 {
		t.Fatalf("finite 11 + repeat 21 expected, got %d repeats", len(rotation))
	}
	assertDivaPrayerRotationTestSequence(t, rotation, 0)
	for attempt := 0; attempt < 3; attempt++ {
		// A fresh repository represents reconnecting without an in-memory cursor.
		again, err := NewDivaRepository(db).OfferDivaPrayerRewards(charID, eventID, progress)
		if err != nil || !reflect.DeepEqual(first, again) {
			t.Fatalf("unclaimed reconnect changed batch: %+v, %v", again, err)
		}
	}
	if next := divaPrayerRotationTestCursor(t, db, charID, eventID); next != 21 {
		t.Fatalf("unclaimed queries advanced cursor: %d", next)
	}
	saveDivaPrayerRotationTestItems(t, repo, db, charID, rotation[:5])
	next, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || len(next) != 32 {
		t.Fatalf("partial refill: %d, %v", len(next), err)
	}
	if got := divaPrayerRotationTestCursor(t, db, charID, eventID); got != 26 {
		t.Fatalf("refill cursor = %d, want 26", got)
	}
	nextRotation := divaPrayerRotationTestOffers(next)
	if len(nextRotation) != 21 {
		t.Fatalf("refill repeat count = %d", len(nextRotation))
	}
	assertDivaPrayerRotationTestSequence(t, nextRotation, 5)
	for i, old := range rotation[5:] {
		if nextRotation[i] != old {
			t.Fatal("partial save replaced an unclaimed snapshot")
		}
	}
	if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 37 || claimed != 5 {
		t.Fatalf("receipt counts = %d/%d, want 37/5", total, claimed)
	}
}

func TestRepoDivaPrayerRotationFullBatchSaveAndExtremePoints(t *testing.T) {
	for _, points := range []int64{1000000, math.MaxInt64} {
		t.Run(fmt.Sprint(points), func(t *testing.T) {
			repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
			progress := DivaRewardProgress{GR: 1, Points: points}
			first, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
			if err != nil || len(first) != 32 {
				t.Fatalf("first batch: %d, %v", len(first), err)
			}
			rotation := divaPrayerRotationTestOffers(first)
			if len(rotation) != 21 || divaPrayerRotationTestCursor(t, db, charID, eventID) != 21 {
				t.Fatalf("extreme score allocated unbounded rotation: %d", len(rotation))
			}
			assertDivaPrayerRotationTestSequence(t, rotation, 0)
			var items []DivaRewardOffer
			var gpIDs []uint32
			var gpAmount uint32
			for _, offer := range first {
				if offer.ItemType == 7 {
					items = append(items, offer)
				} else if offer.ItemType == 26 {
					gpIDs = append(gpIDs, offer.ID)
					gpAmount += uint32(offer.Quantity)
				} else {
					t.Fatalf("unexpected item type: %+v", offer)
				}
			}
			if len(items) != 31 || len(gpIDs) != 1 || gpAmount != 5100 {
				t.Fatalf("first batch lost mixed save types: %d items, %d GP entries, %d GP", len(items), len(gpIDs), gpAmount)
			}
			saveDivaPrayerRotationTestItems(t, repo, db, charID, items)
			if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 32 || claimed != 31 {
				t.Fatalf("item save consumed GP or missed items: %d/%d", total, claimed)
			}
			if _, err := repo.PrepareDivaRewardClaims(charID, 1, gpIDs); err != nil {
				t.Fatal(err)
			}
			if err := NewCharacterRepository(db).UpdateGCPAndPactWithDivaRewards(charID, gpAmount, 0, gpIDs); err != nil {
				t.Fatal(err)
			}
			if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 32 || claimed != 32 {
				t.Fatalf("whole batch not consumed by matching saves: %d/%d", total, claimed)
			}
			next, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
			if err != nil || len(next) != 32 || len(divaPrayerRotationTestOffers(next)) != 32 {
				t.Fatalf("second batch should contain 32 repeats only: %d, %v", len(next), err)
			}
			assertDivaPrayerRotationTestSequence(t, next, 21)
			for _, offer := range next {
				if offer.ID <= first[len(first)-1].ID {
					t.Fatal("second batch reoffered an old receipt")
				}
			}
			if nextIndex := divaPrayerRotationTestCursor(t, db, charID, eventID); nextIndex != 53 {
				t.Fatalf("second-batch cursor = %d, want 53", nextIndex)
			}
			if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 64 || claimed != 32 {
				t.Fatalf("score created more than two bounded batches: %d/%d", total, claimed)
			}
		})
	}
}

func TestRepoDivaPrayerRotationConcurrentOffers(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	type result struct {
		offers []DivaRewardOffer
		err    error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			offers, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{GR: 1, Points: 1000000})
			results <- result{offers, err}
		}()
	}
	wg.Wait()
	close(results)
	var first []DivaRewardOffer
	for result := range results {
		if result.err != nil || len(result.offers) != 32 {
			t.Fatalf("concurrent offer: %d, %v", len(result.offers), result.err)
		}
		if first == nil {
			first = result.offers
		} else if !reflect.DeepEqual(first, result.offers) {
			t.Fatal("concurrent queries allocated different batches")
		}
	}
	if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 32 || claimed != 0 {
		t.Fatalf("concurrent receipts = %d/%d", total, claimed)
	}
	if next := divaPrayerRotationTestCursor(t, db, charID, eventID); next != 21 {
		t.Fatalf("concurrent cursor = %d", next)
	}
}

func TestRepoDivaPrayerRotationCursorFailureRollsBackWholeOffer(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	const drop = `DROP TRIGGER IF EXISTS test_diva_rotation_cursor_failure ON diva_prayer_rotation_progress; DROP FUNCTION IF EXISTS test_diva_rotation_cursor_failure()`
	if _, err := db.Exec(`CREATE FUNCTION test_diva_rotation_cursor_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.next_index > 0 THEN RAISE EXCEPTION 'injected cursor failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER test_diva_rotation_cursor_failure BEFORE INSERT OR UPDATE ON diva_prayer_rotation_progress
		FOR EACH ROW EXECUTE FUNCTION test_diva_rotation_cursor_failure()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(drop); err != nil {
			t.Error(err)
		}
	})
	progress := DivaRewardProgress{GR: 1, Points: 105000}
	if _, err := repo.OfferDivaPrayerRewards(charID, eventID, progress); err == nil {
		t.Fatal("cursor storage failure ignored")
	}
	if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 0 || claimed != 0 {
		t.Fatalf("cursor failure committed receipts: %d/%d", total, claimed)
	}
	for _, table := range []string{"diva_prayer_rotation_progress", "diva_reward_groups"} {
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM `+table+` WHERE char_id=$1 AND event_id=$2`, charID, eventID); err != nil || count != 0 {
			t.Fatalf("failed offer retained %s rows: %d, %v", table, count, err)
		}
	}
	if _, err := db.Exec(drop); err != nil {
		t.Fatal(err)
	}
	offers, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || len(offers) != 14 || divaPrayerRotationTestCursor(t, db, charID, eventID) != 3 {
		t.Fatalf("retry after rollback: %d, %v", len(offers), err)
	}
	assertDivaPrayerRotationTestSequence(t, divaPrayerRotationTestOffers(offers), 0)
}

func TestRepoDivaPrayerRotationSaveFailureAndIdempotentRetry(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	progress := DivaRewardProgress{GR: 1, Points: 103000}
	first, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil {
		t.Fatal(err)
	}
	rotation := divaPrayerRotationTestOffers(first)
	if len(rotation) != 1 {
		t.Fatalf("expected first repeat: %+v", rotation)
	}
	old := []byte{9, 8, 7}
	if _, err := db.Exec(`UPDATE characters SET savedata=$1 WHERE id=$2`, old, charID); err != nil {
		t.Fatal(err)
	}
	const drop = `DROP TRIGGER IF EXISTS test_diva_rotation_save_failure ON diva_reward_receipts; DROP FUNCTION IF EXISTS test_diva_rotation_save_failure()`
	if _, err := db.Exec(`CREATE FUNCTION test_diva_rotation_save_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected rotation receipt save failure'; END $$;
		CREATE TRIGGER test_diva_rotation_save_failure BEFORE UPDATE ON diva_reward_receipts FOR EACH ROW EXECUTE FUNCTION test_diva_rotation_save_failure()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(drop); err != nil {
			t.Error(err)
		}
	})
	params := SaveAtomicParams{CharID: charID, Name: "RotationTester", GR: 1, CompSave: []byte{1}, HouseData: []byte{2}, DivaRewardIDs: []uint32{rotation[0].ID}}
	characterRepo := NewCharacterRepository(db)
	if err := characterRepo.SaveCharacterDataAtomic(params); err == nil {
		t.Fatal("receipt save failure ignored")
	}
	var saved []byte
	if err := db.Get(&saved, `SELECT savedata FROM characters WHERE id=$1`, charID); err != nil || !bytes.Equal(saved, old) {
		t.Fatalf("failed claim changed savedata: %v, %v", saved, err)
	}
	if divaSaveTestClaimTime(t, db, rotation[0].ID).Valid || divaPrayerRotationTestCursor(t, db, charID, eventID) != 1 {
		t.Fatal("failed save consumed reward or reset issued cursor")
	}
	again, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("failed-save reconnect lost pending offers: %+v, %v", again, err)
	}
	if _, err := db.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if err := characterRepo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	claimed := divaSaveTestClaimTime(t, db, rotation[0].ID)
	if !claimed.Valid {
		t.Fatal("successful save did not commit receipt")
	}
	if pending, err := repo.PrepareDivaRewardClaims(charID, 1, params.DivaRewardIDs); err != nil || len(pending) != 0 {
		t.Fatalf("saved retry became another entitlement: %+v, %v", pending, err)
	}
	if err := characterRepo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	if now := divaSaveTestClaimTime(t, db, rotation[0].ID); !now.Time.Equal(claimed.Time) {
		t.Fatal("retry replaced claim time")
	}
	next, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || len(divaPrayerRotationTestOffers(next)) != 0 || divaPrayerRotationTestCursor(t, db, charID, eventID) != 1 {
		t.Fatalf("same score reissued claimed repeat: %+v, %v", next, err)
	}
}

func TestRepoDivaPrayerRotationCharacterAndEventIsolation(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	userID := CreateTestUser(t, db, "rotation_other_user")
	otherChar := CreateTestCharacter(t, db, userID, "OtherRotation")
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, otherChar); err != nil {
		t.Fatal(err)
	}
	var otherEvent uint32
	if err := db.QueryRow(`INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id`, time.Now()).Scan(&otherEvent); err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint32]bool)
	for _, pair := range [][2]uint32{{charID, eventID}, {otherChar, eventID}, {charID, otherEvent}} {
		offers, err := repo.OfferDivaPrayerRewards(pair[0], pair[1], DivaRewardProgress{GR: 1, Points: 104000})
		if err != nil || len(offers) != 13 {
			t.Fatalf("isolated offer %v: %d, %v", pair, len(offers), err)
		}
		assertDivaPrayerRotationTestSequence(t, divaPrayerRotationTestOffers(offers), 0)
		if next := divaPrayerRotationTestCursor(t, db, pair[0], pair[1]); next != 2 {
			t.Fatalf("isolated cursor %v = %d", pair, next)
		}
		for _, offer := range offers {
			if offer.EventID != pair[1] || seen[offer.ID] {
				t.Fatalf("receipt crossed character/event boundary: %+v", offer)
			}
			seen[offer.ID] = true
		}
	}
}

func TestRepoDivaPrayerRotationDowngradePreservesPendingAndQueuesHR(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	progress := DivaRewardProgress{GR: 1, Points: 1000000}
	first, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil {
		t.Fatal(err)
	}
	rotation := divaPrayerRotationTestOffers(first)
	if len(rotation) != 21 {
		t.Fatalf("first repeat count = %d", len(rotation))
	}
	saveDivaPrayerRotationTestItems(t, repo, db, charID, rotation[:3])
	if _, err := db.Exec(`UPDATE characters SET hr=99,gr=0 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	// Deliberately stale progress: the locked database rank must win for minting.
	after, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
	if err != nil || len(after) != 32 || len(divaPrayerRotationTestOffers(after)) != 18 {
		t.Fatalf("downgrade changed preserved batch: %d, %v", len(after), err)
	}
	assertDivaPrayerRotationTestSequence(t, divaPrayerRotationTestOffers(after), 3)
	if next := divaPrayerRotationTestCursor(t, db, charID, eventID); next != 21 {
		t.Fatalf("stale GR minted after downgrade: %d", next)
	}
	if _, err := db.Exec(`UPDATE characters SET gr=1 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	after, err = repo.OfferDivaPrayerRewards(charID, eventID, progress)
	// HR finite snapshots also occupy the pending queue after downgrade.
	if err != nil || len(after) != 32 || divaPrayerRotationTestCursor(t, db, charID, eventID) != 21 {
		t.Fatalf("promotion reset cursor or exceeded pending capacity: %d, %v", len(after), err)
	}
	assertDivaPrayerRotationTestSequence(t, divaPrayerRotationTestOffers(after), 3)
}

func TestRepoDivaPrayerRotationRejectsScheduleMismatch(t *testing.T) {
	for _, column := range []string{"schedule_key", "schedule_fingerprint"} {
		t.Run(column, func(t *testing.T) {
			repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
			if _, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{GR: 1, Points: 103000}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE diva_prayer_rotation_progress SET `+column+`='different-policy' WHERE char_id=$1 AND event_id=$2`, charID, eventID); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{GR: 1, Points: 1000000}); err == nil {
				t.Fatal("changed schedule restarted same round")
			}
			if next := divaPrayerRotationTestCursor(t, db, charID, eventID); next != 1 {
				t.Fatalf("policy mismatch changed cursor: %d", next)
			}
			if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 12 || claimed != 0 {
				t.Fatalf("policy mismatch changed receipts: %d/%d", total, claimed)
			}
		})
	}
}
