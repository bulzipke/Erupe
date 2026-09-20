package channelserver

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestDivaPrayerRotationProcessedRangeSkip(t *testing.T) {
	hr, gr := divaPrayerRotationForRank(2, 0), divaPrayerRotation()
	for _, tc := range []struct {
		name                        string
		rotation, other             DivaPrayerRotation
		index, due, otherNext, want uint32
	}{
		{"HR early rights", hr, gr, 0, 1000, 21, 0},
		{"HR just before GR", hr, gr, 81, 1000, 21, 81},
		{"HR overlap starts", hr, gr, 82, 1000, 21, 103},
		{"HR overlap last", hr, gr, 102, 1000, 21, 103},
		{"HR after overlap", hr, gr, 103, 1000, 21, 103},
		{"GR skips HR range", gr, hr, 0, 1000, 115, 33},
		{"skip stops at earned count", gr, hr, 0, 10, 115, 10},
		{"nothing previously issued", gr, hr, 0, 1000, 0, 0},
		{"large prefix is one jump", gr, hr, 0, 1000000, 900000, 899918},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := divaPrayerRotationSkipProcessed(tc.index, tc.due, tc.rotation, tc.other, tc.otherNext); got != tc.want {
				t.Fatalf("jump=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestDivaPrayerRotationFingerprintBackwardCompatibility(t *testing.T) {
	legacy := sha256.Sum256([]byte("103000:1000:1:7:9016:5:true:1:7:9017:5:true"))
	if got := divaPrayerRotationFingerprint(divaPrayerRotation()); got != fmt.Sprintf("%x", legacy) {
		t.Fatalf("legacy GR fingerprint changed: %s", got)
	}
	low, high := divaPrayerRotationForRank(2, 0), divaPrayerRotationForRank(100, 0)
	want := divaHRPrayerRotationFingerprint(low, high)
	if divaPrayerRotationFingerprint(low) != want || divaPrayerRotationFingerprint(high) != want {
		t.Fatal("HR rank variants do not share a fingerprint")
	}
	high.Items[0].Quantity++
	if divaHRPrayerRotationFingerprint(low, high) == want {
		t.Fatal("upper HR quantity change invisible")
	}
	high = divaPrayerRotationForRank(100, 0)
	low.Items[1].MaxHR--
	if divaHRPrayerRotationFingerprint(low, high) == want {
		t.Fatal("lower HR rank bounds change invisible")
	}
}

func divaHRRotationTestRows(offers []DivaRewardOffer) []DivaRewardOffer {
	var result []DivaRewardOffer
	for _, offer := range offers {
		if strings.HasPrefix(offer.CatalogKey, "rotation-hr-prayer-v1-") {
			result = append(result, offer)
		}
	}
	return result
}

func divaHRRotationTestRank(t *testing.T, db *sqlx.DB, charID uint32, hr, gr uint16) {
	t.Helper()
	if _, err := db.Exec(`UPDATE characters SET hr=$1,gr=$2 WHERE id=$3`, hr, gr, charID); err != nil {
		t.Fatal(err)
	}
}

func divaHRRotationTestCursor(t *testing.T, db *sqlx.DB, charID, eventID uint32) uint32 {
	t.Helper()
	var next uint32
	if err := db.Get(&next, `SELECT next_index FROM diva_prayer_rotation_progress WHERE char_id=$1 AND event_id=$2 AND track='hr'`, charID, eventID); err != nil {
		t.Fatal(err)
	}
	return next
}

func saveDivaHRRotationTestBatch(t *testing.T, repo *DivaRepository, db *sqlx.DB, charID uint32, offers []DivaRewardOffer) {
	t.Helper()
	var hr, gr uint16
	var gp uint32
	if err := db.QueryRow(`SELECT COALESCE(hr,0),COALESCE(gr,0),COALESCE(gcp,0) FROM characters WHERE id=$1`, charID).Scan(&hr, &gr, &gp); err != nil {
		t.Fatal(err)
	}
	var items, gps []uint32
	for _, offer := range offers {
		if offer.ItemType == 7 {
			items = append(items, offer.ID)
		} else if offer.ItemType == 26 {
			gps = append(gps, offer.ID)
			gp += uint32(offer.Quantity)
		} else {
			t.Fatalf("unexpected item type: %+v", offer)
		}
	}
	character := NewCharacterRepository(db)
	if len(items) > 0 {
		if _, err := repo.PrepareDivaRewardClaims(charID, 1, items); err != nil {
			t.Fatal(err)
		}
		if err := character.SaveCharacterDataAtomic(SaveAtomicParams{CharID: charID, Name: "HRRotation", HR: hr, GR: gr, CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: items}); err != nil {
			t.Fatal(err)
		}
	}
	if len(gps) > 0 {
		if _, err := repo.PrepareDivaRewardClaims(charID, 1, gps); err != nil {
			t.Fatal(err)
		}
		if err := character.UpdateGCPAndPactWithDivaRewards(charID, gp, 0, gps); err != nil {
			t.Fatal(err)
		}
	}
}

func drainDivaHRRotationTestRewards(t *testing.T, repo *DivaRepository, db *sqlx.DB, charID, eventID uint32, progress DivaRewardProgress) []DivaRewardOffer {
	t.Helper()
	var result []DivaRewardOffer
	for i := 0; i < 10; i++ {
		offers, err := repo.OfferDivaPrayerRewards(charID, eventID, progress)
		if err != nil {
			t.Fatal(err)
		}
		if len(offers) == 0 {
			return result
		}
		if len(offers) > 32 {
			t.Fatalf("oversize batch: %d", len(offers))
		}
		result = append(result, offers...)
		saveDivaHRRotationTestBatch(t, repo, db, charID, offers)
	}
	t.Fatal("bounded test score never drained")
	return nil
}

func TestRepoDivaHRPrayerRotationRankAndThresholds(t *testing.T) {
	for _, tc := range []struct {
		hr     uint16
		points int64
		count  int
	}{
		{2, 20999, 0}, {2, 21000, 1}, {2, 22000, 2}, {99, 23000, 3}, {100, 21000, 1}, {999, 23000, 3}, {1, 1000000, 0}, {1000, 1000000, 0},
	} {
		t.Run(fmt.Sprintf("hr%d-points%d", tc.hr, tc.points), func(t *testing.T) {
			repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
			divaHRRotationTestRank(t, db, charID, tc.hr, 0)
			// The intentionally stale GR snapshot must not override database HR.
			offers, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{GR: 1, Points: tc.points})
			if err != nil {
				t.Fatal(err)
			}
			rows := divaHRRotationTestRows(offers)
			if len(rows) != tc.count {
				t.Fatalf("HR repeats=%d want=%d", len(rows), tc.count)
			}
			for i, row := range rows {
				quantity := uint16(1)
				if tc.hr >= 100 {
					quantity = 2
				}
				itemType, itemID := uint8(7), uint16(0x3694)
				if i%2 == 1 {
					itemType, itemID, quantity = 26, 0, quantity*100
				}
				if row.CatalogKey != fmt.Sprintf("rotation-hr-prayer-v1-%d", i) || row.ItemType != itemType || row.ItemID != itemID || row.Quantity != quantity {
					t.Fatalf("HR row %d = %+v", i, row)
				}
			}
			if tc.hr < 2 || tc.hr > 999 {
				if len(offers) != 0 {
					t.Fatalf("invalid HR gained finite/repeat rows: %+v", offers)
				}
			}
		})
	}
}

func TestRepoDivaHRPrayerRotationTierChangesPreserveOneSequence(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	divaHRRotationTestRank(t, db, charID, 99, 0)
	first, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{HR: 99, Points: 21000})
	if err != nil || len(first) != 7 {
		t.Fatalf("first HR batch: %d, %v", len(first), err)
	}
	divaHRRotationTestRank(t, db, charID, 100, 0)
	next, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{HR: 99, Points: 23000})
	if err != nil || len(next) != 9 {
		t.Fatalf("promotion duplicated prior milestones: %d, %v", len(next), err)
	}
	if !reflect.DeepEqual(first, next[:len(first)]) {
		t.Fatal("promotion replaced lower-tier pending snapshots")
	}
	rows := divaHRRotationTestRows(next)
	if len(rows) != 3 || rows[0].Quantity != 1 || rows[1].ItemType != 26 || rows[1].Quantity != 200 || rows[2].Quantity != 2 {
		t.Fatalf("wrong tier selected per sequence: %+v", rows)
	}
	if divaHRRotationTestCursor(t, db, charID, eventID) != 3 {
		t.Fatal("HR tier promotion reset cursor")
	}
	divaHRRotationTestRank(t, db, charID, 2, 0)
	again, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{HR: 100, Points: 23000})
	if err != nil || !reflect.DeepEqual(next, again) {
		t.Fatalf("HR downgrade reissued sequence: %+v, %v", again, err)
	}
}

func TestRepoDivaHRPrayerRotationPromotionSkipsOver32OverlappingScores(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	divaHRRotationTestRank(t, db, charID, 99, 0)
	hrOffers := drainDivaHRRotationTestRewards(t, repo, db, charID, eventID, DivaRewardProgress{HR: 99, Points: 135000})
	if len(divaHRRotationTestRows(hrOffers)) != 115 || divaHRRotationTestCursor(t, db, charID, eventID) != 115 {
		t.Fatal("HR prefix not fully issued")
	}
	divaHRRotationTestRank(t, db, charID, 999, 1)
	// 103k..135k already belongs to HR. Skip all 33 in this same response.
	offers, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{HR: 99, Points: 136000})
	if err != nil || len(offers) != 4 {
		t.Fatalf("promotion batch=%d, err=%v", len(offers), err)
	}
	rotation := divaPrayerRotationTestOffers(offers)
	if len(rotation) != 1 {
		t.Fatalf("promotion leaked old HR/GR repeats: %+v", rotation)
	}
	assertDivaPrayerRotationTestSequence(t, rotation, 33)
	if divaPrayerRotationTestCursor(t, db, charID, eventID) != 34 || divaHRRotationTestCursor(t, db, charID, eventID) != 115 {
		t.Fatal("promotion moved wrong track cursor")
	}
	var duplicates int
	if err := db.Get(&duplicates, `SELECT COUNT(*) FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2
		AND catalog_key LIKE 'rotation-gr-crystals-v1-%' AND catalog_key<>'rotation-gr-crystals-v1-33'`, charID, eventID); err != nil || duplicates != 0 {
		t.Fatalf("duplicate GR thresholds=%d, %v", duplicates, err)
	}
}

func TestRepoDivaHRPrayerRotationAfterGRKeepsEarlierHRRights(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	drainDivaHRRotationTestRewards(t, repo, db, charID, eventID, DivaRewardProgress{GR: 1, Points: 105000})
	divaHRRotationTestRank(t, db, charID, 99, 0)
	offers := drainDivaHRRotationTestRewards(t, repo, db, charID, eventID, DivaRewardProgress{GR: 1, Points: 105000})
	hrRows := divaHRRotationTestRows(offers)
	// GR already owns 99k/102k finite rewards and repeats at 103k..105k.
	if len(hrRows) != 80 || divaHRRotationTestCursor(t, db, charID, eventID) != 85 {
		t.Fatalf("HR earlier rights lost or duplicate: %d rows", len(hrRows))
	}
	if hrRows[0].CatalogKey != "rotation-hr-prayer-v1-0" {
		t.Fatal("GR progress skipped the earlier HR interval")
	}
	for _, row := range hrRows {
		for _, index := range []int{78, 81, 82, 83, 84} {
			if row.CatalogKey == fmt.Sprintf("rotation-hr-prayer-v1-%d", index) {
				t.Fatalf("already owned score reissued: %+v", row)
			}
		}
	}
	if divaPrayerRotationTestCursor(t, db, charID, eventID) != 3 {
		t.Fatal("HR issuance mutated GR cursor")
	}
}

func TestRepoDivaHRPrayerRotationScheduleMismatchRollsBackFinite(t *testing.T) {
	repo, db, charID, eventID := setupDivaPrayerRotationRepoTest(t)
	divaHRRotationTestRank(t, db, charID, 99, 0)
	if _, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{HR: 99, Points: 21000}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE diva_prayer_rotation_progress SET schedule_fingerprint='different-HR-policy' WHERE char_id=$1 AND event_id=$2 AND track='hr'`, charID, eventID); err != nil {
		t.Fatal(err)
	}
	divaHRRotationTestRank(t, db, charID, 999, 1)
	if _, err := repo.OfferDivaPrayerRewards(charID, eventID, DivaRewardProgress{GR: 1, Points: 104000}); err == nil {
		t.Fatal("other-track policy mismatch ignored")
	}
	if total, claimed := divaPrayerRotationTestReceiptCounts(t, db, charID, eventID); total != 7 || claimed != 0 {
		t.Fatalf("mismatch committed GR finite/repeats: %d/%d", total, claimed)
	}
	var grCursors int
	if err := db.Get(&grCursors, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE char_id=$1 AND event_id=$2 AND track='gr'`, charID, eventID); err != nil || grCursors != 0 {
		t.Fatalf("mismatch committed GR cursor=%d, %v", grCursors, err)
	}
}
