package channelserver

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func insertDivaSaveTestReceipt(t *testing.T, db *sqlx.DB, charID, eventID uint32, itemType uint8, key string) uint32 {
	t.Helper()
	itemID := 0
	if itemType == 7 {
		itemID = 0x3694
	}
	var id uint32
	err := db.QueryRow(`INSERT INTO diva_reward_receipts
		(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
		VALUES ($1,$2,2,$3,$4,$5,5) RETURNING id`, charID, eventID, key, itemType, itemID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func divaSaveTestClaimTime(t *testing.T, db *sqlx.DB, id uint32) sql.NullTime {
	t.Helper()
	var claimed sql.NullTime
	if err := db.Get(&claimed, `SELECT claimed_at FROM diva_reward_receipts WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	return claimed
}

func setupDivaSaveTest(t *testing.T) (*CharacterRepository, *sqlx.DB, uint32, uint32) {
	t.Helper()
	repo, db, charID := setupCharRepo(t)
	CreateTestUserBinary(t, db, charID)
	var eventID uint32
	if err := db.QueryRow(`INSERT INTO events (event_type,start_time) VALUES ('diva',NOW()) RETURNING id`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return repo, db, charID, eventID
}

func TestDivaRewardSaveAtomicItemsAndIdempotency(t *testing.T) {
	repo, db, charID, eventID := setupDivaSaveTest(t)
	itemID := insertDivaSaveTestReceipt(t, db, charID, eventID, 7, "item")
	gpID := insertDivaSaveTestReceipt(t, db, charID, eventID, 26, "gp")
	params := SaveAtomicParams{CharID: charID, Name: "RewardHunter", CompSave: []byte{1, 2}, HouseData: []byte{3}, DivaRewardIDs: []uint32{itemID}}
	if err := repo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	claimed := divaSaveTestClaimTime(t, db, itemID)
	if !claimed.Valid || divaSaveTestClaimTime(t, db, gpID).Valid {
		t.Fatal("incorrect receipt types committed")
	}
	var got []byte
	if err := db.Get(&got, `SELECT savedata FROM characters WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, params.CompSave) {
		t.Fatal("savedata was not committed with receipt")
	}
	if err := repo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatalf("idempotent receipt: %v", err)
	}
	if again := divaSaveTestClaimTime(t, db, itemID); !again.Valid || !again.Time.Equal(claimed.Time) {
		t.Fatal("idempotent save changed claim timestamp")
	}
	if err := repo.UpdateGCPAndPactWithDivaRewards(charID, 12000, 0, []uint32{gpID}); err != nil {
		t.Fatal(err)
	}
	if !divaSaveTestClaimTime(t, db, gpID).Valid {
		t.Fatal("GP receipt not committed")
	}
}

func TestDivaRewardSaveAtomicRejectsAndRollsBack(t *testing.T) {
	repo, db, charID, eventID := setupDivaSaveTest(t)
	itemID := insertDivaSaveTestReceipt(t, db, charID, eventID, 7, "item")
	gpID := insertDivaSaveTestReceipt(t, db, charID, eventID, 26, "gp")
	userID := CreateTestUser(t, db, "diva_other_user")
	otherChar := CreateTestCharacter(t, db, userID, "OtherHunter")
	foreignID := insertDivaSaveTestReceipt(t, db, otherChar, eventID, 7, "foreign")
	oldBlob := []byte{9, 8, 7}
	if _, err := db.Exec(`UPDATE characters SET savedata=$1 WHERE id=$2`, oldBlob, charID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ids  []uint32
	}{
		{"wrong type", []uint32{itemID, gpID}},
		{"foreign", []uint32{itemID, foreignID}},
		{"missing", []uint32{itemID, 4294967295}},
		{"zero", []uint32{0}},
		{"duplicate", []uint32{itemID, itemID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := SaveAtomicParams{CharID: charID, Name: "Uncommitted", CompSave: []byte{1}, HouseData: []byte{2}, BackupSlot: 1, BackupData: []byte{3}, DivaRewardIDs: tc.ids}
			if err := repo.SaveCharacterDataAtomic(params); err == nil {
				t.Fatal("invalid receipt save succeeded")
			}
			var blob []byte
			if err := db.Get(&blob, `SELECT savedata FROM characters WHERE id=$1`, charID); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(blob, oldBlob) {
				t.Fatal("failed receipt validation persisted savedata")
			}
			if divaSaveTestClaimTime(t, db, itemID).Valid {
				t.Fatal("failed batch consumed valid receipt")
			}
			var backups int
			if err := db.Get(&backups, `SELECT count(*) FROM savedata_backups WHERE char_id=$1`, charID); err != nil {
				t.Fatal(err)
			}
			if backups != 0 {
				t.Fatal("failed receipt validation committed backup")
			}
		})
	}
}

func TestDivaRewardSaveAtomicGPValidationAndRollback(t *testing.T) {
	repo, db, charID, eventID := setupDivaSaveTest(t)
	gpID := insertDivaSaveTestReceipt(t, db, charID, eventID, 26, "gp")
	itemID := insertDivaSaveTestReceipt(t, db, charID, eventID, 7, "item")
	if _, err := db.Exec(`UPDATE characters SET gcp=10,pact_id=11 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateGCPAndPactWithDivaRewards(charID, 12010, 99, []uint32{gpID, itemID}); err == nil {
		t.Fatal("mixed receipt save succeeded")
	}
	var gcp, pact uint32
	if err := db.QueryRow(`SELECT gcp,pact_id FROM characters WHERE id=$1`, charID).Scan(&gcp, &pact); err != nil {
		t.Fatal(err)
	}
	if gcp != 10 || pact != 11 || divaSaveTestClaimTime(t, db, gpID).Valid {
		t.Fatal("failed GP save was not rolled back")
	}
	if err := repo.UpdateGCPAndPactWithDivaRewards(charID, 12010, 99, []uint32{gpID}); err != nil {
		t.Fatal(err)
	}
	first := divaSaveTestClaimTime(t, db, gpID)
	if !first.Valid {
		t.Fatal("GP receipt not consumed")
	}
	if err := repo.UpdateGCPAndPactWithDivaRewards(charID, 12010, 99, []uint32{gpID}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT gcp,pact_id FROM characters WHERE id=$1`, charID).Scan(&gcp, &pact); err != nil {
		t.Fatal(err)
	}
	if gcp != 12010 || pact != 99 || !divaSaveTestClaimTime(t, db, gpID).Time.Equal(first.Time) {
		t.Fatal("GP retry incremented balance or replaced claim timestamp")
	}
	if divaSaveTestClaimTime(t, db, itemID).Valid {
		t.Fatal("GP save consumed item receipt")
	}
}

func TestDivaRewardSaveAtomicClaimUpdateFailureRollsBack(t *testing.T) {
	repo, db, charID, eventID := setupDivaSaveTest(t)
	gpID := insertDivaSaveTestReceipt(t, db, charID, eventID, 26, "gp")
	if _, err := db.Exec(`UPDATE characters SET gcp=42 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	// An isolated-test trigger simulates a storage failure after the GP
	// write. It is removed even when an assertion fails.
	if _, err := db.Exec(`CREATE FUNCTION test_diva_claim_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected claim failure'; END $$;
	CREATE TRIGGER test_diva_claim_failure BEFORE UPDATE ON diva_reward_receipts FOR EACH ROW EXECUTE FUNCTION test_diva_claim_failure()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS test_diva_claim_failure ON diva_reward_receipts; DROP FUNCTION IF EXISTS test_diva_claim_failure()`); err != nil {
			t.Error(err)
		}
	})
	if err := repo.UpdateGCPAndPactWithDivaRewards(charID, 100, 0, []uint32{gpID}); err == nil {
		t.Fatal("injected failure was ignored")
	}
	var gcp uint32
	if err := db.Get(&gcp, `SELECT gcp FROM characters WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	if gcp != 42 || divaSaveTestClaimTime(t, db, gpID).Valid {
		t.Fatal("claim update failure did not roll back GP")
	}
}

func TestDivaRewardSaveAtomicConcurrentRetry(t *testing.T) {
	repo, db, charID, eventID := setupDivaSaveTest(t)
	gpID := insertDivaSaveTestReceipt(t, db, charID, eventID, 26, "gp")
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- repo.UpdateGCPAndPactWithDivaRewards(charID, 12000, 0, []uint32{gpID}) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent save deadlocked")
		}
	}
	var gcp uint32
	if err := db.Get(&gcp, `SELECT gcp FROM characters WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	if gcp != 12000 || !divaSaveTestClaimTime(t, db, gpID).Valid {
		t.Fatalf("concurrent absolute GP save = %d", gcp)
	}
}
