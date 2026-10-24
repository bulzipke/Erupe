package channelserver

import (
	"context"
	"testing"
	"time"
)

// A snapshot issued only after all rights reads must not be reusable for an
// earlier admission, even when the legacy right itself was already valid then.
// Blocking the account branch gives a deterministic point inside that read:
// transaction_timestamp() has already been fixed, but issuance has not occurred.
func TestQuestAccessObservationDoesNotBackdateWhileAccountReadWaits(t *testing.T) {
	repo, db, user, char, now := setupQuestAccessRepo(t)
	const questID = 40079
	insertQuestAccessCampaign(t, db, 100, questID, 9, 1,
		now.Add(-time.Hour), now.Add(time.Hour), char, 1)
	request := QuestAccessLoadRequest{
		UserID: user, CharacterID: char, QuestIDs: []int{questID},
		AccountLicensesEnabled: true,
		// An exact, unowned mapping forces the account SELECT without providing
		// an account grant; successful access still comes from the valid code.
		Catalog: questAccessMapCatalog{questID: {"unowned_observation_test"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	blocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.ExecContext(ctx, `LOCK TABLE quest_licenses IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type loadResult struct {
		snapshot *QuestAccessSnapshot
		err      error
	}
	loaded := make(chan loadResult, 1)
	go func() {
		snapshot, err := repo.Load(ctx, request)
		loaded <- loadResult{snapshot: snapshot, err: err}
	}()

	waitCtx, stopWait := context.WithTimeout(ctx, 4*time.Second)
	defer stopWait()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.GetContext(waitCtx, &waiting, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid()
			AND datname=current_database() AND state='active'
			AND wait_event_type='Lock' AND query LIKE $1
		)`, "%quest_access_account%"); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("access reader did not reach the controlled account relation lock")
		case <-ticker.C:
		}
	}
	var pastAdmission time.Time
	if err := db.GetContext(waitCtx, &pastAdmission, `SELECT clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	// Ensure the database clock has advanced before releasing the read. This is
	// state-based synchronization rather than an assumption about sleep duration
	// or a particular timestamp resolution on the test host.
	for {
		var advanced bool
		if err := db.GetContext(waitCtx, &advanced, `SELECT clock_timestamp()>$1`, pastAdmission); err != nil {
			t.Fatal(err)
		}
		if advanced {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("database clock did not advance before releasing the read")
		case <-ticker.C:
		}
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	var result loadResult
	select {
	case result = <-loaded:
	case <-ctx.Done():
		t.Fatal("access reader did not finish after account lock release")
	}
	if result.err != nil || result.snapshot == nil {
		t.Fatalf("load after controlled wait: %#v %v", result.snapshot, result.err)
	}
	snapshot := result.snapshot
	if !snapshot.ObservedAt().After(pastAdmission) || snapshot.Allows(questID, pastAdmission) {
		t.Fatalf("later read backdated authority: observed=%s past_admission=%s",
			snapshot.ObservedAt(), pastAdmission)
	}
	if snapshot.AccountReadError() != nil || !snapshot.Allows(questID, snapshot.ObservedAt()) {
		t.Fatal("conservative observation lost the independently valid legacy right", snapshot.AccountReadError())
	}
}
