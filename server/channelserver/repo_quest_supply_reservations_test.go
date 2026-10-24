package channelserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/questsupplies"
	"github.com/jmoiron/sqlx"
)

//go:embed testdata/quest_supply_reservations.schema.sql
var questSupplyReservationFixture string

func supplyTestCatalog(t *testing.T, version string, start, end time.Time) *questsupplies.Catalog {
	t.Helper()
	d := questsupplies.ApprovedSeasonalDocument()
	d.Version = version
	d.Windows = []questsupplies.Window{
		{ID: "test_gw_2026", SupplyKey: questsupplies.GWKey, StartsAt: start, EndsAt: end},
		{ID: "test_new_year_2026", SupplyKey: questsupplies.HunterDiceBagKey, StartsAt: start, EndsAt: end},
	}
	c, err := questsupplies.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setupQuestSupplyReservations(t *testing.T) (*QuestSupplyReservationRepository, *sqlx.DB, *questsupplies.Catalog, time.Time) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("seasonal fixture requires isolated localhost:5433/erupe_test")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=3", c.Host, c.Port, c.User, c.Password, c.DBName)
	admin, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot initialize isolated seasonal fixture")
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skip("isolated localhost:5433/erupe_test is unavailable")
	}
	schema := fmt.Sprintf("quest_supply_reservation_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("seasonal fixture cleanup: %v", err)
		}
		_ = admin.Close()
	})
	db, err := sqlx.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal("cannot open isolated seasonal schema")
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(questSupplyReservationFixture)
	db.MustExec(`INSERT INTO users(id,gacha_premium) VALUES(1,10),(2,10);
		INSERT INTO characters(id,user_id) VALUES(11,1),(12,1),(21,2)`)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	catalog := supplyTestCatalog(t, "local-test-v1", now.Add(-time.Hour), now.Add(time.Hour))
	repo := NewQuestSupplyReservationRepository(db)
	repo.now = func() time.Time { return now }
	if err := repo.Bind(context.Background(), catalog, []QuestSupplyOccurrence{{"test_gw_2026", "gw_2026"}, {"test_new_year_2026", "new_year_2026"}}); err != nil {
		t.Fatal(err)
	}
	return repo, db, catalog, now
}

func supplyTestPrepare(t *testing.T, catalog *questsupplies.Catalog, key string, user, char uint32, nonce byte, now time.Time) questsupplies.Prepared {
	t.Helper()
	window := "test_gw_2026"
	if key == questsupplies.HunterDiceBagKey {
		window = "test_new_year_2026"
	}
	var n questsupplies.Nonce
	n[0] = nonce
	p, err := catalog.Prepare(key, window, questsupplies.Identity{AccountID: user, CharacterID: char}, n, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertSupplyState(t *testing.T, db *sqlx.DB, user uint32, balance, reservations, holds int) {
	t.Helper()
	var gotBalance, gotReservations, gotHolds int
	if err := db.QueryRow(`SELECT COALESCE(gacha_premium,0),(SELECT count(*) FROM quest_supply_reservations),(SELECT count(*) FROM quest_supply_pending_holds) FROM users WHERE id=$1`, user).Scan(&gotBalance, &gotReservations, &gotHolds); err != nil {
		t.Fatal(err)
	}
	if gotBalance != balance || gotReservations != reservations || gotHolds != holds {
		t.Fatalf("balance/reservations/holds=%d/%d/%d want %d/%d/%d", gotBalance, gotReservations, gotHolds, balance, reservations, holds)
	}
}

func TestQuestSupplyReservationFreeConcurrentQuota(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 11, 0, now)
	var wg sync.WaitGroup
	results := make(chan QuestSupplyReservationResult, 12)
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := repo.Reserve(context.Background(), p)
			results <- r
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	newCount := 0
	for r := range results {
		if r.Status != QuestSupplyReservationOK || r.Reservation.Quantity != 4 || r.Reservation.ItemID != 6831 || r.Reservation.BalanceAfter.Valid || r.Reservation.PriceGoldG != 0 {
			t.Fatal(r)
		}
		if id != 0 && id != r.Reservation.ID {
			t.Fatal("same character/window minted another reservation")
		}
		id = r.Reservation.ID
		if !r.Replay {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatal("fresh reservation count", newCount)
	}
	assertSupplyState(t, db, 1, 10, 1, 1)
	other := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 12, 0, now)
	if r, err := repo.Reserve(context.Background(), other); err != nil || r.Status != QuestSupplyReservationOK || r.Reservation.ID == id {
		t.Fatal("other character lost its independent quota", r, err)
	}
	assertSupplyState(t, db, 1, 10, 2, 2)
}

func TestQuestSupplyReservationPaidConcurrentReplayAndConflict(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	var wg sync.WaitGroup
	results := make(chan QuestSupplyReservationResult, 12)
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := repo.Reserve(context.Background(), p)
			results <- r
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	fresh := 0
	for r := range results {
		if r.Status != QuestSupplyReservationOK || r.Reservation.ItemID != 3638 || r.Reservation.Quantity != 1 || r.Reservation.PriceGoldG != 1 || !r.Reservation.BalanceAfter.Valid || r.Reservation.BalanceAfter.Int64 != 9 {
			t.Fatal(r)
		}
		if id != 0 && id != r.Reservation.ID {
			t.Fatal("exact retry minted another paid reservation")
		}
		id = r.Reservation.ID
		if !r.Replay {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal("fresh paid reservation count", fresh)
	}
	for _, row := range []struct {
		char  uint32
		nonce byte
		want  QuestSupplyReservationStatus
	}{{12, 1, QuestSupplyReservationConflict}, {11, 2, QuestSupplyReservationPendingExists}, {12, 2, QuestSupplyReservationOK}} {
		r, err := repo.Reserve(context.Background(), supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, row.char, row.nonce, now))
		if err != nil || r.Status != row.want {
			t.Fatal(row, r, err)
		}
	}
	assertSupplyState(t, db, 1, 8, 2, 2)
}

func TestQuestSupplyReservationSharedBalanceCannotOverdraw(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	db.MustExec(`UPDATE users SET gacha_premium=1 WHERE id=1`)
	requests := []questsupplies.Prepared{
		supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now),
		supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 12, 2, now),
	}
	var wg sync.WaitGroup
	statuses := make(chan QuestSupplyReservationStatus, 2)
	for _, p := range requests {
		wg.Add(1)
		go func(p questsupplies.Prepared) {
			defer wg.Done()
			r, err := repo.Reserve(context.Background(), p)
			if err != nil {
				t.Error(err)
			}
			statuses <- r.Status
		}(p)
	}
	wg.Wait()
	close(statuses)
	counts := map[QuestSupplyReservationStatus]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[QuestSupplyReservationOK] != 1 || counts[QuestSupplyReservationInsufficient] != 1 {
		t.Fatal(counts)
	}
	assertSupplyState(t, db, 1, 0, 1, 1)
}

func TestQuestSupplyReservationFailureRollsBackDebitAndReservation(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	db.MustExec(`CREATE FUNCTION fail_supply_hold() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected hold failure'; END $$;
		CREATE TRIGGER fail_supply_hold BEFORE INSERT ON quest_supply_pending_holds FOR EACH ROW EXECUTE FUNCTION fail_supply_hold();`)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), p); err == nil || r.Status != QuestSupplyReservationUnavailable {
		t.Fatal("injected failure was acknowledged as a committed obligation", r, err)
	}
	assertSupplyState(t, db, 1, 10, 0, 0)
	db.MustExec(`DROP TRIGGER fail_supply_hold ON quest_supply_pending_holds`)
	r, err := repo.Reserve(context.Background(), p)
	if err != nil || r.Status != QuestSupplyReservationOK || r.Replay {
		t.Fatal("retry after rollback did not make one fresh reservation", r, err)
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}

func TestQuestSupplyReservationExpiryRestartRecovery(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	paid := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	free := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 11, 0, now)
	for _, p := range []questsupplies.Prepared{paid, free} {
		if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK {
			t.Fatal(r, err)
		}
	}
	restarted := NewQuestSupplyReservationRepository(db)
	restarted.now = func() time.Time { return now.Add(2 * time.Hour) }
	contract, _ := paid.PaidReplayContract()
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	var restored questsupplies.PaidReplayContract
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if r, found, err := restarted.Replay(context.Background(), 1, 11, restored); err != nil || !found || !r.Replay || r.Status != QuestSupplyReservationOK {
		t.Fatal("persisted replay contract could not recover after process/catalog loss", r, found, err)
	}
	if r, found, err := restarted.Replay(context.Background(), 1, 12, restored); err != nil || !found || r.Status != QuestSupplyReservationConflict {
		t.Fatal("persisted nonce replay crossed character identity", r, found, err)
	}
	for _, p := range []questsupplies.Prepared{paid, free} {
		if r, err := restarted.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK || !r.Replay {
			t.Fatal("closed window lost committed exact replay", r, err)
		}
	}
	freshPaid := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 2, now)
	if r, err := restarted.Reserve(context.Background(), freshPaid); err != nil || r.Status != QuestSupplyReservationClosed {
		t.Fatal("closed season accepted a fresh request", r, err)
	}
	pending, err := restarted.Pending(context.Background(), 1, 11)
	if err != nil || len(pending) != 2 || pending[0].ItemID != 3638 || pending[1].ItemID != 6831 {
		t.Fatal("closed season lost pending recovery", pending, err)
	}
	if foreign, err := restarted.Pending(context.Background(), 2, 11); err != nil || len(foreign) != 0 {
		t.Fatal("foreign account recovered another character's obligations", foreign, err)
	}
	assertSupplyState(t, db, 1, 9, 2, 2)
}

func TestQuestSupplyReservationStableOccurrenceAndDateRevision(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 11, 0, now)
	first, err := repo.Reserve(context.Background(), p)
	if err != nil || first.Status != QuestSupplyReservationOK {
		t.Fatal(first, err)
	}
	doc := catalog.Document()
	doc.Version = "cosmetic-catalog-version-v2"
	versioned, _ := questsupplies.New(doc)
	bindings := []QuestSupplyOccurrence{{"test_gw_2026", "gw_2026"}, {"test_new_year_2026", "new_year_2026"}}
	if err := repo.Bind(context.Background(), versioned, bindings); err != nil {
		t.Fatal("catalog-only version change rewrote scope", err)
	}
	if err := repo.ReviseWindow(context.Background(), "test_gw_2026", 1, now.Add(-2*time.Hour), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReviseWindow(context.Background(), "test_gw_2026", 1, now.Add(-3*time.Hour), now.Add(3*time.Hour)); err == nil {
		t.Fatal("stale date revision accepted")
	}
	db.MustExec(`CREATE FUNCTION skip_supply_revision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
		CREATE TRIGGER skip_supply_revision BEFORE UPDATE ON quest_supply_active_windows FOR EACH ROW EXECUTE FUNCTION skip_supply_revision();`)
	if err := repo.ReviseWindow(context.Background(), "test_gw_2026", 2, now.Add(-3*time.Hour), now.Add(3*time.Hour)); err == nil {
		t.Fatal("zero-row active date update was acknowledged as a revision")
	}
	db.MustExec(`DROP TRIGGER skip_supply_revision ON quest_supply_active_windows`)
	var revisions int
	if err := db.Get(&revisions, `SELECT count(*) FROM quest_supply_window_revisions WHERE window_key='test_gw_2026'`); err != nil || revisions != 2 {
		t.Fatal("failed active revision left a partial date history", revisions, err)
	}
	if err := repo.Bind(context.Background(), catalog, bindings); err == nil {
		t.Fatal("stale initial dates reset an active revision")
	}
	doc.Windows[0].StartsAt, doc.Windows[0].EndsAt = now.Add(-2*time.Hour), now.Add(2*time.Hour)
	updated, err := questsupplies.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Reserve(context.Background(), supplyTestPrepare(t, updated, questsupplies.GWKey, 1, 11, 0, now))
	if err != nil || r.Status != QuestSupplyReservationOK || !r.Replay || r.Reservation.ID != first.Reservation.ID {
		t.Fatal("date revision minted another free quota", r, err)
	}
	doc.Windows[0].ID = "test_gw_alias"
	alias, _ := questsupplies.New(doc)
	if err := repo.Bind(context.Background(), alias, []QuestSupplyOccurrence{{"test_gw_alias", "gw_2026"}, {"test_new_year_2026", "new_year_2026"}}); err == nil {
		t.Fatal("same occurrence accepted a new window alias")
	}
	if err := repo.Bind(context.Background(), updated, []QuestSupplyOccurrence{{"test_gw_2026", "different_occurrence"}, {"test_new_year_2026", "new_year_2026"}}); err == nil {
		t.Fatal("existing window accepted a different occurrence identity")
	}
	if _, err := db.Exec(`UPDATE quest_supply_active_windows SET revision=1 WHERE window_key='test_gw_2026'`); err == nil {
		t.Fatal("active date revision rolled back")
	}
	assertSupplyState(t, db, 1, 10, 1, 1)
}

func TestQuestSupplyReservationRejectsUnapprovedOrInvalidInputs(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	if r, err := repo.Reserve(context.Background(), questsupplies.Prepared{}); err != nil || r.Status != QuestSupplyReservationUnavailable {
		t.Fatal("zero preparation touched the ledger", r, err)
	}
	doc := catalog.Document()
	for i := range doc.Definitions {
		if doc.Definitions[i].Key == questsupplies.HunterDiceBagKey {
			doc.Definitions[i].PriceGoldG = 2
		}
	}
	changed, err := questsupplies.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Bind(context.Background(), changed, []QuestSupplyOccurrence{{"test_gw_2026", "gw_2026"}, {"test_new_year_2026", "new_year_2026"}}); err == nil {
		t.Fatal("unapproved price rebound approved policy")
	}
	bad := supplyTestPrepare(t, changed, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), bad); err != nil || r.Status != QuestSupplyReservationUnavailable {
		t.Fatal("unapproved prepared metadata reached debit", r, err)
	}
	for _, identity := range []questsupplies.Identity{{AccountID: 2, CharacterID: 11}, {AccountID: 1, CharacterID: 21}, {AccountID: math.MaxUint32, CharacterID: 11}} {
		p, err := catalog.Prepare(questsupplies.GWKey, "test_gw_2026", identity, questsupplies.Nonce{}, now)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationUnavailable {
			t.Fatal("unauthenticated identity reached ledger", r, err)
		}
	}
	assertSupplyState(t, db, 1, 10, 0, 0)
}

func TestQuestSupplyReservationBoundariesDeletedAndNullBalance(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 11, 0, now)
	repo.now = func() time.Time { return now.Add(time.Hour) }
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationClosed {
		t.Fatal("exclusive end boundary admitted a new reservation", r, err)
	}
	repo.now = func() time.Time { return now.Add(-time.Hour) }
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK {
		t.Fatal("inclusive start boundary rejected", r, err)
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=11; UPDATE users SET gacha_premium=NULL WHERE id=1`)
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationUnavailable {
		t.Fatal("deleted character recovered an obligation", r, err)
	}
	paid := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 12, 1, now)
	if r, err := repo.Reserve(context.Background(), paid); err != nil || r.Status != QuestSupplyReservationInsufficient {
		t.Fatal("NULL balance permitted paid reservation", r, err)
	}
	assertSupplyState(t, db, 1, 0, 1, 1)
}

func TestQuestSupplyReservationHistoryCannotBeReset(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK {
		t.Fatal(r, err)
	}
	for _, statement := range []string{
		`UPDATE quest_supply_definitions SET quantity=2 WHERE supply_key='hunter_dice_bag'`,
		`DELETE FROM quest_supply_definitions`, `DELETE FROM quest_supply_windows`,
		`UPDATE quest_supply_window_revisions SET ends_at=ends_at+interval '1 hour'`,
		`DELETE FROM quest_supply_reservations`, `UPDATE quest_supply_reservations SET quantity=2`,
		`DELETE FROM quest_supply_pending_holds`, `DELETE FROM characters WHERE id=11`, `DELETE FROM users WHERE id=1`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatal("history mutation accepted", statement)
		}
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}

func TestQuestSupplyReservationNextApprovedOccurrenceKeepsOldPending(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	old := supplyTestPrepare(t, catalog, questsupplies.GWKey, 1, 11, 0, now)
	first, err := repo.Reserve(context.Background(), old)
	if err != nil || first.Status != QuestSupplyReservationOK {
		t.Fatal(first, err)
	}
	doc := catalog.Document()
	doc.Windows[0].ID = "test_gw_2027"
	doc.Windows[0].StartsAt = now.AddDate(1, 0, 0).Add(-time.Hour)
	doc.Windows[0].EndsAt = now.AddDate(1, 0, 0).Add(time.Hour)
	next, err := questsupplies.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []QuestSupplyOccurrence{{"test_gw_2027", "gw_2027"}, {"test_new_year_2026", "new_year_2026"}}
	db.MustExec(`CREATE FUNCTION skip_supply_activation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
		CREATE TRIGGER skip_supply_activation BEFORE INSERT ON quest_supply_active_windows FOR EACH ROW EXECUTE FUNCTION skip_supply_activation();`)
	if err := repo.Bind(context.Background(), next, bindings); err == nil {
		t.Fatal("zero-row initial activation was acknowledged as a binding")
	}
	var partial int
	if err := db.Get(&partial, `SELECT count(*) FROM quest_supply_windows WHERE window_key='test_gw_2027'`); err != nil || partial != 0 {
		t.Fatal("failed initial activation left a partial occurrence", partial, err)
	}
	db.MustExec(`DROP TRIGGER skip_supply_activation ON quest_supply_active_windows`)
	if err := repo.Bind(context.Background(), next, bindings); err != nil {
		t.Fatal(err)
	}
	repo.now = func() time.Time { return now.AddDate(1, 0, 0) }
	p, err := next.Prepare(questsupplies.GWKey, "test_gw_2027", questsupplies.Identity{AccountID: 1, CharacterID: 11}, questsupplies.Nonce{}, now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Reserve(context.Background(), p)
	if err != nil || second.Status != QuestSupplyReservationOK || second.Replay || second.Reservation.ID == first.Reservation.ID {
		t.Fatal("new explicit annual occurrence lost its separate quota", second, err)
	}
	rows, err := repo.Pending(context.Background(), 1, 11)
	if err != nil || len(rows) != 2 {
		t.Fatal("new annual window erased the old pending obligation", rows, err)
	}
	assertSupplyState(t, db, 1, 10, 2, 2)
}

func TestQuestSupplyReservationCommitFailureCannotReportSuccess(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	db.MustExec(`CREATE FUNCTION fail_supply_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$;
		CREATE CONSTRAINT TRIGGER fail_supply_commit AFTER INSERT ON quest_supply_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_supply_commit();`)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), p); err == nil || r.Status != QuestSupplyReservationUnavailable || r.Reservation.ID != 0 {
		t.Fatal("failed commit reported a committed obligation", r, err)
	}
	assertSupplyState(t, db, 1, 10, 0, 0)
	db.MustExec(`DROP TRIGGER fail_supply_commit ON quest_supply_reservations`)
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK || r.Replay {
		t.Fatal("commit failure retry did not book exactly once", r, err)
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}

func TestQuestSupplyReservationSkippedDebitCannotBookObligation(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	db.MustExec(`CREATE FUNCTION skip_supply_debit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
		CREATE TRIGGER skip_supply_debit BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION skip_supply_debit();`)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), p); err == nil || r.Status != QuestSupplyReservationUnavailable || r.Reservation.ID != 0 {
		t.Fatal("zero-row debit booked an unpaid obligation", r, err)
	}
	assertSupplyState(t, db, 1, 10, 0, 0)
	db.MustExec(`DROP TRIGGER skip_supply_debit ON users`)
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK || r.Replay {
		t.Fatal("retry after skipped debit did not book exactly once", r, err)
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}

func TestQuestSupplyReservationAlteredDebitCannotBookObligation(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	for _, expression := range []string{"OLD.gacha_premium", "OLD.gacha_premium-2"} {
		t.Run(expression, func(t *testing.T) {
			db.MustExec(fmt.Sprintf(`CREATE OR REPLACE FUNCTION alter_supply_debit() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN NEW.gacha_premium:=%s; RETURN NEW; END $$;`, expression))
			db.MustExec(`CREATE TRIGGER alter_supply_debit BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION alter_supply_debit();`)
			if r, err := repo.Reserve(context.Background(), p); err == nil || r.Status != QuestSupplyReservationUnavailable || r.Reservation.ID != 0 {
				t.Fatal("changed debit booked an incorrectly priced obligation", r, err)
			}
			assertSupplyState(t, db, 1, 10, 0, 0)
			db.MustExec(`DROP TRIGGER alter_supply_debit ON users`)
		})
	}
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK || r.Replay || r.Reservation.BalanceAfter.Int64 != 9 {
		t.Fatal("clean retry after altered debit did not book one correct obligation", r, err)
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}

func TestQuestSupplyReservationSkippedHoldCannotBookObligation(t *testing.T) {
	repo, db, catalog, now := setupQuestSupplyReservations(t)
	db.MustExec(`CREATE FUNCTION skip_supply_hold() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
		CREATE TRIGGER skip_supply_hold BEFORE INSERT ON quest_supply_pending_holds FOR EACH ROW EXECUTE FUNCTION skip_supply_hold();`)
	p := supplyTestPrepare(t, catalog, questsupplies.HunterDiceBagKey, 1, 11, 1, now)
	if r, err := repo.Reserve(context.Background(), p); err == nil || r.Status != QuestSupplyReservationUnavailable || r.Reservation.ID != 0 {
		t.Fatal("zero-row pending hold left an unrecoverable paid obligation", r, err)
	}
	assertSupplyState(t, db, 1, 10, 0, 0)
	db.MustExec(`DROP TRIGGER skip_supply_hold ON quest_supply_pending_holds`)
	if r, err := repo.Reserve(context.Background(), p); err != nil || r.Status != QuestSupplyReservationOK || r.Replay {
		t.Fatal("retry after missing hold did not book exactly once", r, err)
	}
	rows, err := repo.Pending(context.Background(), 1, 11)
	if err != nil || len(rows) != 1 || rows[0].ItemID != 3638 {
		t.Fatal("corrected obligation was not recoverable", rows, err)
	}
	assertSupplyState(t, db, 1, 9, 1, 1)
}
