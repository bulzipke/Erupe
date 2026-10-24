package channelserver

import (
	"bytes"
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestQuestLicenseStarterSaveReceiptValidation(t *testing.T) {
	id := uint32(campaignCoinRewardBase + 1)
	for _, invalid := range [][]uint32{{0}, {1}, {0xffffffff}, {id, id}, make([]uint32, 513)} {
		if err := validateQuestLicenseStarterSaveIDs(invalid); err == nil {
			t.Fatal("invalid save receipt batch accepted")
		}
	}
	if err := commitQuestLicenseStarterReceipts(context.Background(), nil, 0, 0, nil); err != nil {
		t.Fatal("ordinary save's empty snapshot touched DB", err)
	}
	if err := commitQuestLicenseStarterReceipts(context.Background(), nil, 1, 2, []uint32{id}); err == nil {
		t.Fatal("claim accepted without save transaction")
	}
	var unavailable *QuestLicenseStarterRepository
	if _, err := unavailable.PrepareSave(context.Background(), 1, 2, []uint32{id}); err == nil {
		t.Fatal("nil preparation repository accepted")
	}
}

func commitStarterTestClientBlob(ctx context.Context, db *sqlx.DB, user, char uint32, ids []uint32, blob []byte) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := commitQuestLicenseStarterReceipts(ctx, tx, user, char, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE characters SET savedata=$1 WHERE id=$2 AND user_id=$3`, blob, char, user); err != nil {
		return err
	}
	return tx.Commit()
}

func TestQuestLicenseStarterPrepareThenSaveAtomic(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || queued.Status != StarterPending {
		t.Fatal(queued, err)
	}
	offers, err := repo.PrepareSave(ctx, user, char, []uint32{queued.RewardID})
	if err != nil || len(offers) != 1 || offers[0].Quantity != 1 || offers[0].ItemID != 1859 {
		t.Fatal("native claim preparation failed", offers, err)
	}
	assertStarterState(t, db, user, 1, 0, 0)
	var staged questLicenseStarterPending
	if err := staged.Stage(user, char, offers); err != nil {
		t.Fatal(err)
	}
	ids := staged.Snapshot(user, char)
	blob := []byte("client-inventory-snapshot")
	if err := commitStarterTestClientBlob(ctx, db, user, char, ids, blob); err != nil {
		t.Fatal(err)
	}
	staged.Complete(user, char, ids)
	assertStarterState(t, db, user, 0, 1, 0)
	var saved []byte
	if err := db.Get(&saved, `SELECT savedata FROM characters WHERE id=$1`, char); err != nil || !bytes.Equal(saved, blob) {
		t.Fatal("receipt and client blob did not commit together", saved, err)
	}
	if staged.HasPending() {
		t.Fatal("successfully committed snapshot remained staged")
	}
	if retry, err := repo.PrepareSave(ctx, user, char, ids); err != nil || len(retry) != 0 {
		t.Fatal("committed claim retry offered item again", retry, err)
	}
	if err := commitStarterTestClientBlob(ctx, db, user, char, ids, blob); err != nil {
		t.Fatal("saved packet retry failed", err)
	}
}

func TestQuestLicenseStarterSaveRollbackPreservesPending(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := repo.Ensure(ctx, user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte("previous-client-save")
	db.MustExec(`UPDATE characters SET savedata=$1 WHERE id=$2`, old, char)
	offers, err := repo.PrepareSave(ctx, user, char, []uint32{queued.RewardID})
	if err != nil {
		t.Fatal(err)
	}
	var pending questLicenseStarterPending
	if err := pending.Stage(user, char, offers); err != nil {
		t.Fatal(err)
	}
	db.MustExec(`ALTER TABLE characters ADD CONSTRAINT starter_save_test_failure CHECK(savedata<>decode('000102','hex'))`)
	t.Cleanup(func() { db.MustExec(`ALTER TABLE characters DROP CONSTRAINT IF EXISTS starter_save_test_failure`) })
	if err := commitStarterTestClientBlob(ctx, db, user, char, pending.Snapshot(user, char), []byte{0, 1, 2}); err == nil {
		t.Fatal("forced client save failure accepted")
	}
	assertStarterState(t, db, user, 1, 0, 0)
	if !pending.HasPending() {
		t.Fatal("failed savedata cleared staged receipts")
	}
	var saved []byte
	if err := db.Get(&saved, `SELECT savedata FROM characters WHERE id=$1`, char); err != nil || !bytes.Equal(saved, old) {
		t.Fatal("failed savedata overwrote previous blob", err)
	}
	// A disconnect discards only the session queue. Durable pending receipt
	// is prepared again on the next authenticated native collection.
	pending.Reset()
	if retry, err := repo.PrepareSave(ctx, user, char, []uint32{queued.RewardID}); err != nil || len(retry) != 1 {
		t.Fatal("disconnect consumed a receipt without savedata", retry, err)
	}
}

func TestQuestLicenseStarterSaveRejectsForeignAndLegacyRace(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	first, err := repo.Ensure(ctx, user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	secondChar := CreateTestCharacter(t, db, user, "saveother")
	second, err := repo.Ensure(ctx, user, secondChar, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]uint32{{first.RewardID, second.RewardID}, {first.RewardID, first.RewardID}, {1}} {
		if _, err := repo.PrepareSave(ctx, user, char, ids); err == nil {
			t.Fatal("invalid prepared save batch accepted")
		}
		if err := commitStarterTestClientBlob(ctx, db, user, char, ids, []byte("invalid-save")); err == nil {
			t.Fatal("invalid saved receipt batch accepted")
		}
		assertStarterState(t, db, user, 2, 0, 0)
	}
	// Legacy authentication can appear after Ensure. Both native claim
	// preparation and atomic save recheck the same starter source before
	// consuming a new receipt. This is not a concurrency proof for legacy
	// writers that have not yet adopted this account lock.
	starterLegacyCatalog(t, db, 492821212, 1859, 0)
	db.MustExec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES(492821212,$1,'test-legacy-race')`, char)
	if _, err := repo.PrepareSave(ctx, user, char, []uint32{first.RewardID}); err == nil {
		t.Fatal("new receipt bypassed legacy pending item")
	}
	if err := commitStarterTestClientBlob(ctx, db, user, char, []uint32{first.RewardID}, []byte("duplicate-starter")); err == nil {
		t.Fatal("atomic save bypassed legacy pending item")
	}
	db.MustExec(`INSERT INTO campaign_rewards_claimed(character_id,reward_id)
		SELECT $1,min(id) FROM campaign_rewards WHERE campaign_id=492821212`, char)
	if err := commitStarterTestClientBlob(ctx, db, user, char, []uint32{first.RewardID}, []byte("duplicate-starter")); err == nil {
		t.Fatal("atomic save bypassed legacy claimed item")
	}
	assertStarterState(t, db, user, 2, 0, 0)
}

func TestQuestLicenseStarterExistingPendingRechecksLegacyDelivery(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	original, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || original.Status != StarterPending {
		t.Fatal(original, err)
	}
	starterLegacyCatalog(t, db, 492821212, 1859, 0)
	db.MustExec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES(492821212,$1,'test-later-auth')`, char)
	hidden, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || hidden.Status != StarterLegacyPending || hidden.RewardID != 0 {
		t.Fatal("existing new receipt exposed despite later legacy pending item", hidden, err)
	}
	assertStarterState(t, db, user, 1, 0, 0)
	db.MustExec(`INSERT INTO campaign_rewards_claimed(character_id,reward_id)
		SELECT $1,min(id) FROM campaign_rewards WHERE campaign_id=492821212`, char)
	suppressed, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || suppressed.Status != StarterLegacyClaimed || suppressed.RewardID != 0 {
		t.Fatal("later legacy claim did not suppress existing receipt", suppressed, err)
	}
	assertStarterState(t, db, user, 0, 0, 1)
	if pending, err := repo.Pending(ctx, user, char, policy.Key); err != nil || len(pending) != 0 {
		t.Fatal("suppressed receipt remained collectible", pending, err)
	}
	if _, err := repo.PrepareSave(ctx, user, char, []uint32{original.RewardID}); err == nil {
		t.Fatal("stale new claim survived durable legacy suppression")
	}
	db.MustExec(`DELETE FROM campaigns WHERE id=492821212`)
	again, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || again.Status != StarterLegacyClaimed {
		t.Fatal("legacy history cleanup re-exposed suppressed new receipt", again, err)
	}
}
