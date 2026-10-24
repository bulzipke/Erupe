package channelserver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func saveStarterEpochTestBlob(ctx context.Context, db *sqlx.DB, user, char uint32, expected int64, ids []uint32, blob []byte) (int64, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	next, err := commitQuestLicenseStarterReceiptsWithEpoch(ctx, tx, user, char, expected, ids)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE characters SET savedata=$1 WHERE id=$2`, blob, char); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

func TestQuestLicenseStarterEpochUnavailableBoundaries(t *testing.T) {
	var unavailable *CharacterRepository
	ctx := context.Background()
	for _, pair := range [][2]uint32{{1, 2}, {0, 2}, {1, 0}, {1 << 31, 2}} {
		if _, err := unavailable.LoadSaveDataWithStarterEpoch(ctx, pair[0], pair[1]); err == nil {
			t.Fatal("unavailable savedata reader accepted", pair)
		}
		if _, err := unavailable.LoadBackupsWithStarterEpoch(ctx, pair[0], pair[1], 0); err == nil {
			t.Fatal("unavailable backup reader accepted", pair)
		}
	}
	if _, err := commitQuestLicenseStarterReceiptsWithEpoch(ctx, nil, 1, 2, 0, nil); err == nil {
		t.Fatal("empty client save bypassed unavailable epoch transaction")
	}
	if _, err := commitQuestLicenseStarterReceiptsWithEpoch(ctx, &sqlx.Tx{}, 1, 2, -1, nil); err == nil {
		t.Fatal("negative expected epoch reached DB")
	}
	if _, err := commitQuestLicenseStarterReceiptsWithEpoch(ctx, &sqlx.Tx{}, 1, 2, 0, []uint32{0}); err == nil {
		t.Fatal("invalid receipt reached DB")
	}
}

func TestQuestLicenseStarterEpochLoadsOwnedSnapshotAndFiltersBackups(t *testing.T) {
	_, db, user, char, _ := setupStarterRepo(t)
	ctx := context.Background()
	characters := NewCharacterRepository(db)
	blob, hash := []byte("current-inventory"), []byte("current-hash")
	db.MustExec(`UPDATE characters SET savedata=$1,savedata_hash=$2,starter_inventory_epoch=1 WHERE id=$3`, blob, hash, char)
	snapshot, err := characters.LoadSaveDataWithStarterEpoch(ctx, user, char)
	if err != nil || snapshot.ID != char || snapshot.Epoch != 1 || !bytes.Equal(snapshot.Savedata, blob) || !bytes.Equal(snapshot.Hash, hash) {
		t.Fatal("savedata and epoch did not load together", snapshot, err)
	}
	// Unknown historical metadata and an explicitly older inventory must both
	// be excluded even if they are newer in wall-clock backup order.
	db.MustExec(`INSERT INTO savedata_backups(char_id,slot,savedata,saved_at,starter_inventory_epoch) VALUES
		($1,0,$2,now()+interval '2 hours',NULL),
		($1,1,$3,now()+interval '1 hour',0),
		($1,2,$4,now(),1)`, char, []byte("unknown"), []byte("before-starter"), blob)
	backups, err := characters.LoadBackupsWithStarterEpoch(ctx, user, char, 1)
	if err != nil || len(backups) != 1 || backups[0].Slot != 2 || !bytes.Equal(backups[0].Data, blob) {
		t.Fatal("backup reader borrowed current epoch for older inventory", backups, err)
	}
	if _, err := characters.LoadBackupsWithStarterEpoch(ctx, user, char, 0); !errors.Is(err, ErrQuestLicenseStarterStaleSave) {
		t.Fatal("backup reader accepted an old generation", err)
	}
	other := CreateTestUser(t, db, "epoch_other")
	for _, load := range []func() error{
		func() error { _, err := characters.LoadSaveDataWithStarterEpoch(ctx, other, char); return err },
		func() error { _, err := characters.LoadBackupsWithStarterEpoch(ctx, other, char, 1); return err },
	} {
		if err := load(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("foreign character authenticated for inventory snapshot", err)
		}
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, char)
	if _, err := characters.LoadSaveDataWithStarterEpoch(ctx, user, char); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted character authenticated", err)
	}
}

func TestQuestLicenseStarterEpochFirstClaimInvalidatesOldClientAndDBBlob(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := starters.Ensure(ctx, user, char, policy)
	if err != nil || queued.Status != StarterPending {
		t.Fatal(queued, err)
	}
	characters := NewCharacterRepository(db)
	before, err := characters.LoadSaveDataWithStarterEpoch(ctx, user, char)
	if err != nil || before.Epoch != 0 {
		t.Fatal(before, err)
	}
	// A second load/connection does not revoke a valid old channel just by
	// connecting. Only the inventory-changing receipt advances the generation.
	if loaded, err := characters.LoadSaveDataWithStarterEpoch(ctx, user, char); err != nil || loaded.Epoch != before.Epoch {
		t.Fatal("load changed generation", loaded, err)
	}
	committed := []byte("inventory-with-one-starter")
	next, err := saveStarterEpochTestBlob(ctx, db, user, char, before.Epoch, []uint32{queued.RewardID}, committed)
	if err != nil || next != 1 {
		t.Fatal("first claim did not commit one generation", next, err)
	}
	assertStarterState(t, db, user, 0, 1, 0)
	for _, staleIDs := range [][]uint32{nil, {queued.RewardID}} {
		if _, err := saveStarterEpochTestBlob(ctx, db, user, char, before.Epoch, staleIDs, []byte("old-client-or-db-blob")); !errors.Is(err, ErrQuestLicenseStarterStaleSave) {
			t.Fatal("stale inventory overwrote committed starter", staleIDs, err)
		}
	}
	current, err := characters.LoadSaveDataWithStarterEpoch(ctx, user, char)
	if err != nil || current.Epoch != next || !bytes.Equal(current.Savedata, committed) {
		t.Fatal("stale attempt changed inventory", current, err)
	}
	// Ordinary current saves and already claimed receipt replay do not create
	// another generation. A caller must update its baseline after COMMIT only.
	for _, ids := range [][]uint32{nil, {queued.RewardID}} {
		if got, err := saveStarterEpochTestBlob(ctx, db, user, char, next, ids, committed); err != nil || got != next {
			t.Fatal("current save or committed replay advanced generation", got, err)
		}
	}
	assertStarterState(t, db, user, 0, 1, 0)
}

func TestQuestLicenseStarterEpochRollsBackWithLaterSaveFailure(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := starters.Ensure(ctx, user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte("before-starter")
	db.MustExec(`UPDATE characters SET savedata=$1 WHERE id=$2`, old, char)
	db.MustExec(`ALTER TABLE characters ADD CONSTRAINT starter_epoch_blob_failure CHECK(savedata<>decode('000102','hex'))`)
	t.Cleanup(func() { db.MustExec(`ALTER TABLE characters DROP CONSTRAINT IF EXISTS starter_epoch_blob_failure`) })
	if _, err := saveStarterEpochTestBlob(ctx, db, user, char, 0, []uint32{queued.RewardID}, []byte{0, 1, 2}); err == nil {
		t.Fatal("injected later savedata failure accepted")
	}
	assertStarterState(t, db, user, 1, 0, 0)
	snapshot, err := NewCharacterRepository(db).LoadSaveDataWithStarterEpoch(ctx, user, char)
	if err != nil || snapshot.Epoch != 0 || !bytes.Equal(snapshot.Savedata, old) {
		t.Fatal("failed save persisted generation or blob", snapshot, err)
	}
	if next, err := saveStarterEpochTestBlob(ctx, db, user, char, 0, []uint32{queued.RewardID}, []byte("retry-with-starter")); err != nil || next != 1 {
		t.Fatal("failed save lost the retryable receipt or baseline", next, err)
	}
}

func TestQuestLicenseStarterEpochConcurrentOldBaselineWaitsThenFails(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	queued, err := starters.Ensure(ctx, user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if next, err := commitQuestLicenseStarterReceiptsWithEpoch(ctx, tx, user, char, 0, []uint32{queued.RewardID}); err != nil || next != 1 {
		t.Fatal(next, err)
	}
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := saveStarterEpochTestBlob(ctx, db, user, char, 0, nil, []byte("late-old-save"))
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		t.Fatal("old save bypassed account lock", err)
	case <-time.After(100 * time.Millisecond):
	}
	committed := []byte("winning-starter-save")
	if _, err := tx.ExecContext(ctx, `UPDATE characters SET savedata=$1 WHERE id=$2`, committed, char); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrQuestLicenseStarterStaleSave) {
			t.Fatal("waiting old save borrowed the new generation", err)
		}
	case <-ctx.Done():
		t.Fatal("waiting old save did not finish", ctx.Err())
	}
	snapshot, err := NewCharacterRepository(db).LoadSaveDataWithStarterEpoch(ctx, user, char)
	if err != nil || snapshot.Epoch != 1 || !bytes.Equal(snapshot.Savedata, committed) {
		t.Fatal("late save overwrote winner", snapshot, err)
	}
	assertStarterState(t, db, user, 0, 1, 0)
}

func TestQuestLicenseStarterEpochRejectsOverflowBeforeClaim(t *testing.T) {
	starters, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := starters.Ensure(ctx, user, char, policy)
	if err != nil {
		t.Fatal(err)
	}
	db.MustExec(`UPDATE characters SET starter_inventory_epoch=$1 WHERE id=$2`, int64(math.MaxInt64), char)
	if _, err := saveStarterEpochTestBlob(ctx, db, user, char, math.MaxInt64, []uint32{queued.RewardID}, []byte("overflow")); err == nil {
		t.Fatal("generation overflow consumed receipt")
	}
	assertStarterState(t, db, user, 1, 0, 0)
	if next, err := saveStarterEpochTestBlob(ctx, db, user, char, math.MaxInt64, nil, []byte("ordinary-save")); err != nil || next != math.MaxInt64 {
		t.Fatal("non-advancing save rejected at maximum generation", next, err)
	}
}
