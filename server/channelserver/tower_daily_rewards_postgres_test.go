package channelserver

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"github.com/jmoiron/sqlx"
)

func towerDailyRewardDB(t *testing.T) (*TowerRepository, *sqlx.DB) {
	t.Helper()
	db := towerD463DB(t) // Dedicated schema, disposable loopback port 55463 only.
	ddl, err := os.ReadFile(filepath.Join("..", "migrations", "sql", "0095_tower_daily_rewards.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return NewTowerRepository(db), db
}

func TestTowerDailyPostgresAtomicEnqueueAndConcurrentRetries(t *testing.T) {
	repo, db := towerDailyRewardDB(t)
	day := towerDailyStart(time.Date(2026, 10, 25, 17, 0, 0, 0, divaLocation))
	progress := towerDailyTestProgress(day, 3)
	if _, err := db.Exec(`ALTER TABLE tower_daily_bins ADD CONSTRAINT injected_daily_failure CHECK(false)`); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitTowerDailyRewards(100, 36, day, nil, progress); err == nil {
		t.Fatal("late progress failure accepted")
	}
	var rows, bins int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM tower_daily_rewards),(SELECT COUNT(*) FROM tower_daily_bins)`).Scan(&rows, &bins); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || bins != 0 {
		t.Fatalf("partial enqueue: %d items, %d progress blobs", rows, bins)
	}
	if _, err := db.Exec(`ALTER TABLE tower_daily_bins DROP CONSTRAINT injected_daily_failure`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.CommitTowerDailyRewards(100, 36, day, nil, progress)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A new repository/session still sees the queued items after a restart/noon.
	got, err := NewTowerRepository(db).ListTowerDailyRewards(100, 36, 255)
	if err != nil || len(got) != 2 {
		t.Fatalf("duplicate or missing items: %+v %v", got, err)
	}
	if data, err := repo.GetTowerDailyBin(100); err != nil || !bytes.Equal(data, progress) {
		t.Fatalf("completion not committed: %x %v", data, err)
	}
	// A later day's items coexist with the previous day's unreceived items.
	nextDay := day.Add(24 * time.Hour)
	if err = repo.CommitTowerDailyRewards(100, 36, nextDay, progress, towerDailyTestProgress(nextDay, 1)); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.ListTowerDailyRewards(100, 36, 255); err != nil || len(got) != 3 {
		t.Fatalf("noon lost unreceived items: %+v %v", got, err)
	}
	if got, err = repo.ListTowerDailyRewards(101, 36, 255); err != nil || len(got) != 0 {
		t.Fatal("another hunter can list the items")
	}
	if got, err = repo.ListTowerDailyRewards(100, 37, 255); err != nil || len(got) != 0 {
		t.Fatal("another round can list the items")
	}
}

func TestTowerDailyPostgresNoHistoricalBackfillOrStaleBaseline(t *testing.T) {
	repo, db := towerDailyRewardDB(t)
	day := towerDailyStart(time.Date(2026, 10, 25, 17, 0, 0, 0, divaLocation))
	before := towerDailyTestProgress(day, 1)
	if err := repo.SaveTowerDailyBin(100, before); err != nil {
		t.Fatal(err)
	}
	after := towerDailyTestProgress(day, 3)
	if err := repo.CommitTowerDailyRewards(100, 36, day, before, after); err != nil {
		t.Fatal(err)
	}
	var slot int
	if err := db.Get(&slot, `SELECT mission_slot FROM tower_daily_rewards`); err != nil || slot != 1 {
		t.Fatalf("old completed mission was backfilled: slot=%d err=%v", slot, err)
	}
	if err := repo.CommitTowerDailyRewards(100, 36, day, before, towerDailyTestProgress(day, 5)); err == nil {
		t.Fatal("stale baseline overwrote a different result")
	}
}

func TestTowerDailyPostgresReceiptAndSavedataCommitTogether(t *testing.T) {
	repo, db := towerDailyRewardDB(t)
	day := towerDailyStart(time.Date(2026, 10, 25, 17, 0, 0, 0, divaLocation))
	progress := towerDailyTestProgress(day, 3)
	if err := repo.CommitTowerDailyRewards(100, 36, day, nil, progress); err != nil {
		t.Fatal(err)
	}
	offer, err := repo.ListTowerDailyRewards(100, 36, 255)
	if err != nil {
		t.Fatal(err)
	}
	// Minimal columns used by the real atomic savedata writer, in this schema.
	_, err = db.Exec(`ALTER TABLE characters ADD COLUMN savedata bytea,
		ADD COLUMN savedata_hash bytea, ADD COLUMN is_new_character boolean,
		ADD COLUMN hr integer, ADD COLUMN gr integer, ADD COLUMN is_female boolean,
		ADD COLUMN weapon_type integer, ADD COLUMN weapon_id integer, ADD COLUMN playtime_seconds integer;
		CREATE TABLE user_binary(id integer PRIMARY KEY,house_tier bytea,house_data bytea,
		bookshelf bytea,gallery bytea,tore bytea,garden bytea);
		INSERT INTO user_binary(id) VALUES(100);
		UPDATE characters SET savedata=decode('01','hex') WHERE id=100;`)
	if err != nil {
		t.Fatal(err)
	}
	charRepo := NewCharacterRepository(db)
	params := SaveAtomicParams{CharID: 100, Name: "Hunter", CompSave: []byte{2},
		TowerDailyRewardIDs: []int64{offer[0].ID, 1 << 50}}
	if err := charRepo.SaveCharacterDataAtomic(params); err == nil {
		t.Fatal("unknown receipt accepted")
	}
	var claimed sql.NullTime
	var saved []byte
	if err := db.Get(&saved, `SELECT savedata FROM characters WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&claimed, `SELECT claimed_at FROM tower_daily_rewards WHERE id=$1`, offer[0].ID); err != nil {
		t.Fatal(err)
	}
	if claimed.Valid || !bytes.Equal(saved, []byte{1}) {
		t.Fatal("failed save partially consumed items or changed inventory")
	}
	// Keep the item that did not fit, and a row enqueued after this offer.
	nextDay := day.Add(24 * time.Hour)
	if err := repo.CommitTowerDailyRewards(100, 36, nextDay, progress, towerDailyTestProgress(nextDay, 1)); err != nil {
		t.Fatal(err)
	}
	params.TowerDailyRewardIDs = []int64{offer[0].ID}
	if err := charRepo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&claimed, `SELECT claimed_at FROM tower_daily_rewards WHERE id=$1`, offer[0].ID); err != nil || !claimed.Valid {
		t.Fatalf("receipt not saved: %v %v", claimed, err)
	}
	if err := charRepo.SaveCharacterDataAtomic(params); err != nil {
		t.Fatalf("lost save ACK retry: %v", err)
	}
	var retried sql.NullTime
	if err := db.Get(&retried, `SELECT claimed_at FROM tower_daily_rewards WHERE id=$1`, offer[0].ID); err != nil || !retried.Time.Equal(claimed.Time) {
		t.Fatal("retry changed receipt")
	}
	if got, err := repo.ListTowerDailyRewards(100, 36, 255); err != nil || len(got) != 2 || got[0].ID != offer[1].ID {
		t.Fatalf("receipt erased unreceived/new rows: %+v %v", got, err)
	}
	params.CharID = 101
	params.TowerDailyRewardIDs = []int64{offer[1].ID}
	if err := charRepo.SaveCharacterDataAtomic(params); err == nil {
		t.Fatal("cross-character receipt accepted")
	}
}

func TestTowerDailyPostgresRejectsChangedRound(t *testing.T) {
	repo, _ := towerDailyRewardDB(t)
	day := towerDailyStart(time.Date(2026, 10, 25, 17, 0, 0, 0, divaLocation))
	options := cfg.TowerRotationOptions{Enabled: true, StartAt: day.Format(time.RFC3339), ActiveDays: 7, CycleDays: 21}
	event, err := repo.EnsureTowerEvent(day, options, 36)
	if err != nil {
		t.Fatal(err)
	}
	old := repo.ForTowerRound(event.ID).(TowerDailyRewardRepository)
	if _, err = repo.EnsureTowerEvent(event.ClaimUntil, options, 36); err != nil {
		t.Fatal(err)
	}
	if err = old.CommitTowerDailyRewards(100, event.ID, day, nil, towerDailyTestProgress(day, 1)); err == nil {
		t.Fatal("closed round enqueued rewards into the new round")
	}
}
