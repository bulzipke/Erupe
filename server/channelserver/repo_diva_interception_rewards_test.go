package channelserver

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var _ DivaInterceptionRewardWindowRepository = (*DivaRepository)(nil)

func insertDivaRewardWindowTestEvent(t *testing.T, db *sqlx.DB, interceptionStart time.Time, mode int) DivaEvent {
	t.Helper()
	start := interceptionStart.Add(-time.Duration(divaPhaseDuration+divaInterlude) * time.Second).Truncate(time.Second)
	event := DivaEvent{StartTime: uint32(start.Unix())}
	if err := db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", start.UTC()).Scan(&event.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err = recordDivaInterceptionPeriod(tx, event, mode); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestRepoDivaInterceptionRewardWindowNextActualStart(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-40*24*time.Hour), -1)
	nextStart := now.Add(5 * 24 * time.Hour)
	next := insertDivaRewardWindowTestEvent(t, db, nextStart, -1)
	// A prayer or welcome override has synthetic historical interception dates,
	// but must not shorten a real round's collection window.
	insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), 1)
	insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Minute), 3)
	for _, tt := range []struct {
		name string
		at   time.Time
		want uint32
	}{
		{"before any interception", now.Add(-41 * 24 * time.Hour), 0},
		{"after old full-event lifespan", now, old.ID},
		{"during next prayer phase", nextStart.Add(-24 * time.Hour), old.ID},
		{"last valid instant", nextStart.Add(-time.Nanosecond), old.ID},
		{"next interception exact start", nextStart, next.ID},
		{"next interception started", nextStart.Add(time.Second), next.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event, err := r.GetDivaInterceptionRewardEvent(tt.at)
			if err != nil || event.ID != tt.want {
				t.Fatalf("event=%+v want=%d err=%v", event, tt.want, err)
			}
		})
	}
	// Even an ineligible legacy round closes the previous window; skipping it
	// would incorrectly resurrect older prizes.
	if _, err := db.Exec("INSERT INTO diva_interception_legacy_events(event_id) VALUES($1)", next.ID); err != nil {
		t.Fatal(err)
	}
	if event, err := r.GetDivaInterceptionRewardEvent(nextStart); err != nil || event.ID != 0 {
		t.Fatalf("legacy reopened older rewards: %+v %v", event, err)
	}
}

func TestRepoDivaInterceptionRewardsCarryAndExpire(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "reward_window")
	charID := CreateTestCharacter(t, db, user, "Window")
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-40*24*time.Hour), -1)
	deadline := now.Add(2 * 24 * time.Hour)
	next := insertDivaRewardWindowTestEvent(t, db, deadline, -1)
	entries := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("personal-one", 6), divaRewardRepoTestEntry("personal-two", 6)}
	first, err := r.offerDivaRewardsAt(charID, old.ID, 6, entries, now)
	if err != nil || len(first) != 2 {
		t.Fatalf("old round offer=%+v %v", first, err)
	}
	// Missing/edited catalog rows do not change an already offered snapshot.
	changed := entries[0]
	changed.Quantity = 99
	again, err := r.offerDivaRewardsAt(charID, old.ID, 6, []DivaRewardCatalogEntry{changed}, deadline.Add(-time.Nanosecond))
	if err != nil || len(again) != 2 || again[0] != first[0] || again[1] != first[1] {
		t.Fatalf("snapshot/carry changed: %+v %v", again, err)
	}
	prepared, err := r.prepareDivaRewardClaimsAt(charID, 6, []uint32{first[0].ID}, deadline.Add(-time.Nanosecond))
	if err != nil || len(prepared) != 1 {
		t.Fatalf("pre-boundary claim failed: %+v %v", prepared, err)
	}
	if _, err = r.offerDivaRewardsAt(charID, old.ID, 6, entries, deadline); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("old offer at new start accepted: %v", err)
	}
	if _, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{first[1].ID}, deadline); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("stale receipt claim accepted: %v", err)
	}
	// A claim staged before the deadline may finish its corresponding save after
	// the deadline. The save transaction, not merely listing, consumes it.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err = commitDivaRewardReceipts(tx, charID, []uint32{first[0].ID}, 7); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if prepared, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{first[0].ID}, deadline); err != nil || len(prepared) != 0 {
		t.Fatalf("committed retry must be idempotent: %+v %v", prepared, err)
	}
	newOffers, err := r.offerDivaRewardsAt(charID, next.ID, 6, entries, deadline)
	if err != nil || len(newOffers) != 2 || newOffers[0].ID == first[0].ID {
		t.Fatalf("new round reused receipts: %+v %v", newOffers, err)
	}
	if _, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{first[1].ID, newOffers[0].ID}, deadline); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("mixed stale/current batch accepted: %v", err)
	}
	var claimed int
	if err = db.QueryRow("SELECT COUNT(*) FROM diva_reward_receipts WHERE claimed_at IS NOT NULL").Scan(&claimed); err != nil || claimed != 1 {
		t.Fatalf("prepare consumed anything: count=%d err=%v", claimed, err)
	}
}

func TestRepoDivaInterceptionRewardLegacyAndNoSchedule(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "reward_no_backfill")
	charID := CreateTestCharacter(t, db, user, "NoBackfill")
	now := divaTestTime(21, 12, 0)
	event := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), 1)
	entry := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("no-retroactive", 6)}
	if _, err := r.offerDivaRewardsAt(charID, event.ID, 6, entry, now); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("fake prayer date enabled: %v", err)
	}
	legacy := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Minute), 2)
	if _, err := db.Exec("INSERT INTO diva_interception_legacy_events(event_id) VALUES($1)", legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.offerDivaRewardsAt(charID, legacy.ID, 6, entry, now); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("legacy enabled: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM diva_reward_receipts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("ineligible receipts inserted: %d %v", count, err)
	}
}

func TestRepoDivaInterceptionRewardPagination(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "interception_pages")
	charID := CreateTestCharacter(t, db, user, "Pages")
	now := divaTestTime(21, 12, 0)
	event := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), 2)
	var entries []DivaRewardCatalogEntry
	for i := 0; i < 40; i++ {
		entries = append(entries, divaRewardRepoTestEntry(fmt.Sprintf("row-%02d", i), 6))
	}
	first, err := r.offerDivaRewardsAt(charID, event.ID, 6, entries, now)
	if err != nil || len(first) != 32 {
		t.Fatalf("first page=%d %v", len(first), err)
	}
	var ids []uint32
	for _, row := range first {
		ids = append(ids, row.ID)
	}
	if _, err = db.Exec("UPDATE diva_reward_receipts SET claimed_at=NOW() WHERE id=ANY($1::bigint[])", pq.Array(ids)); err != nil {
		t.Fatal(err)
	}
	remaining, err := r.offerDivaRewardsAt(charID, event.ID, 6, nil, now)
	if err != nil || len(remaining) != 8 || remaining[0].ID <= first[31].ID {
		t.Fatalf("pending snapshots did not paginate: %+v %v", remaining, err)
	}
}

func TestRepoDivaInterceptionRewardScheduleRace(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "interception_race")
	charID := CreateTestCharacter(t, db, user, "Race")
	now := TimeAdjusted().Truncate(time.Second)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-24*time.Hour), 2)
	offers, err := r.OfferDivaInterceptionRewards(charID, old.ID, []DivaRewardCatalogEntry{divaRewardRepoTestEntry("pending", 6)}, -1)
	if err != nil || len(offers) != 1 {
		t.Fatalf("initial offer=%+v %v", offers, err)
	}
	// A different channel publishes a new interception under the shared lock.
	next := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Second), 1)
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(1146507841)"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, claimErr := NewDivaRepository(db).PrepareDivaInterceptionRewardClaims(charID, []uint32{offers[0].ID}, -1)
		done <- claimErr
	}()
	if err = recordDivaInterceptionPeriod(tx, next, 2); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrDivaInterceptionRewardExpired) {
			t.Fatalf("stale multiserver claim accepted: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("claim deadlocked with schedule creation")
	}
}

func TestRepoDivaInterceptionRewardLifecycleActivatesAtBoundary(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-40*24*time.Hour), -1)
	next, err := r.EnsureDivaEvent(now, -1)
	if err != nil || next.ID == old.ID {
		t.Fatalf("next schedule=%+v %v", next, err)
	}
	boundary, _ := divaInterceptionWindow(next)
	if _, err = r.EnsureDivaEvent(boundary.Add(-time.Second), -1); err != nil {
		t.Fatal(err)
	}
	if event, err := r.GetDivaInterceptionRewardEvent(boundary.Add(-time.Second)); err != nil || event.ID != old.ID {
		t.Fatalf("prayer expired old rewards: %+v %v", event, err)
	}
	// Entering forced prayer/welcome cancels the assumption that a scheduled
	// future normal interception has actually begun.
	for _, mode := range []int{1, 3} {
		at := boundary.Add(time.Hour)
		if _, err = r.EnsureDivaEvent(at, mode); err != nil {
			t.Fatal(err)
		}
		if event, err := r.GetDivaInterceptionRewardEvent(at); err != nil || event.ID != old.ID {
			t.Fatalf("forced mode %d expired old rewards: %+v %v", mode, event, err)
		}
	}
	if actual, err := r.EnsureDivaEvent(boundary.Add(2*time.Hour), -1); err != nil || actual.ID != next.ID {
		t.Fatalf("normal activation=%+v %v", actual, err)
	}
	if event, err := r.GetDivaInterceptionRewardEvent(boundary.Add(2 * time.Hour)); err != nil || event.ID != next.ID {
		t.Fatalf("actual next interception did not expire: %+v %v", event, err)
	}
}

func TestRepoDivaInterceptionRewardClaimRegistersMissingBoundaryAtomically(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "missing_boundary")
	charID := CreateTestCharacter(t, db, user, "Boundary")
	boundary := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, boundary.Add(-40*24*time.Hour), -1)
	// Handler ran before the boundary, so the next period is not registered.
	next := insertDivaRewardWindowTestEvent(t, db, boundary, 1)
	if _, err := db.Exec("INSERT INTO diva_event_lifecycle(mode,event_id) VALUES(-1,$1)", next.ID); err != nil {
		t.Fatal(err)
	}
	rows := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("boundary", 6)}
	offers, err := r.offerDivaRewardsAt(charID, old.ID, 6, rows, boundary.Add(-time.Second), -1)
	if err != nil || len(offers) != 1 {
		t.Fatalf("before boundary: %+v %v", offers, err)
	}
	if _, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[0].ID}, boundary, -1); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("stale claim did not activate actual schedule: %v", err)
	}
	if _, err = r.offerDivaRewardsAt(charID, old.ID, 6, rows, boundary, -1); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("stale offer did not activate actual schedule: %v", err)
	}
	// A virtual forced-prayer date does not create a new interception.
	if got, err := r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[0].ID}, boundary, 1); err != nil || len(got) != 1 {
		t.Fatalf("forced prayer invented interception: %+v %v", got, err)
	}
}

func TestRepoDivaInterceptionRewardForcedRolloverInsideClaim(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "forced_boundary")
	charID := CreateTestCharacter(t, db, user, "ForcedBoundary")
	now := divaTestTime(21, 12, 0)
	old, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	offers, err := r.offerDivaRewardsAt(charID, old.ID, 6, []DivaRewardCatalogEntry{divaRewardRepoTestEntry("forced", 6)}, now, 2)
	if err != nil || len(offers) != 1 {
		t.Fatalf("initial forced offer: %+v %v", offers, err)
	}
	if _, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[0].ID}, divaLifecycleExpiry(old, 2), 2); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("forced rollover raced claim: %v", err)
	}
}
