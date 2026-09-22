package channelserver

import (
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaMapRewardWindowRepository = (*DivaRepository)(nil)

func setupDivaMapRewardWindowTest(t *testing.T) (*DivaRepository, *sqlx.DB) {
	t.Helper()
	config := DefaultTestDBConfig()
	if (config.Host != "127.0.0.1" && config.Host != "localhost") || config.Port != "5433" || config.DBName != "erupe_test" {
		t.Fatal("map reward window tests require isolated localhost:5433/erupe_test before schema reset")
	}
	return setupDivaRepo(t)
}

func markDivaMapRewardTestPersonalLegacy(t *testing.T, db *sqlx.DB, eventID uint32) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO diva_interception_legacy_events(event_id) VALUES($1) ON CONFLICT DO NOTHING`, eventID); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDivaMapRewardWindowActivationAndNoFallback(t *testing.T) {
	r, db := setupDivaMapRewardWindowTest(t)
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-40*24*time.Hour), -1)
	currentStart := now.Add(-time.Hour)
	current := insertDivaRewardWindowTestEvent(t, db, currentStart, -1)
	markDivaMapRewardTestPersonalLegacy(t, db, current.ID)
	activation := now.Add(time.Minute)
	if _, err := db.Exec(`INSERT INTO diva_map_activations(event_id,activated_at) VALUES($1,$2)`, current.ID, activation); err != nil {
		t.Fatal(err)
	}
	nextStart := now.Add(24 * time.Hour)
	nextLegacy := insertDivaRewardWindowTestEvent(t, db, nextStart, -1)
	markDivaMapRewardTestPersonalLegacy(t, db, nextLegacy.ID)
	newStart := nextStart.Add(24 * time.Hour)
	newNormal := insertDivaRewardWindowTestEvent(t, db, newStart, -1)
	for _, tt := range []struct {
		name string
		at   time.Time
		want uint32
	}{
		{"no actual interception", now.Add(-41 * 24 * time.Hour), 0},
		{"earlier eligible round", currentStart.Add(-time.Nanosecond), old.ID},
		{"legacy latest closes older", currentStart, 0},
		{"activation not yet effective", activation.Add(-time.Nanosecond), 0},
		{"activation exact boundary", activation, current.ID},
		{"activated legacy persists", nextStart.Add(-time.Nanosecond), current.ID},
		{"new inactive legacy never falls back", nextStart, 0},
		{"subsequent normal round", newStart, newNormal.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event, err := r.GetDivaMapRewardEvent(tt.at)
			if err != nil || event.ID != tt.want {
				t.Fatalf("event=%+v expected=%d error=%v", event, tt.want, err)
			}
		})
	}
	personal, err := r.GetDivaInterceptionRewardEvent(activation)
	if err != nil || personal.ID != 0 {
		t.Fatalf("map activation bypassed personal reward cutover: %+v %v", personal, err)
	}
	var stillLegacy bool
	if err = db.Get(&stillLegacy, `SELECT EXISTS(SELECT 1 FROM diva_interception_legacy_events WHERE event_id=$1)`, current.ID); err != nil || !stillLegacy {
		t.Fatal("personal legacy marker was removed", err)
	}
}

func TestRepoDivaMapRewardWindowLatestTieAndFutureActivation(t *testing.T) {
	r, db := setupDivaMapRewardWindowTest(t)
	now := divaTestTime(21, 12, 0)
	lower := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), -1)
	higher := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), -1)
	markDivaMapRewardTestPersonalLegacy(t, db, higher.ID)
	if _, err := db.Exec(`INSERT INTO diva_map_activations(event_id,activated_at) VALUES($1,$2)`, lower.ID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if event, err := r.GetDivaMapRewardEvent(now); err != nil || event.ID != 0 {
		t.Fatalf("activated lower tie replaced latest event: %+v %v", event, err)
	}
	// A recorded future period is not an actual interception yet, even if
	// its activation timestamp is already in the past.
	future := insertDivaRewardWindowTestEvent(t, db, now.Add(time.Hour), -1)
	if _, err := db.Exec(`DELETE FROM diva_map_activations`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_map_activations(event_id,activated_at) VALUES($1,$2)`, future.ID, now); err != nil {
		t.Fatal(err)
	}
	if event, err := r.GetDivaMapRewardEvent(now); err != nil || event.ID != 0 {
		t.Fatalf("future period became current: %+v %v", event, err)
	}
	if event, err := r.GetDivaMapRewardEvent(now.Add(time.Hour)); err != nil || event.ID != future.ID {
		t.Fatalf("future period did not activate at actual start: %+v %v", event, err)
	}
}

func TestRepoDivaMapRewardWindowLockedModesPreservePersonalLegacy(t *testing.T) {
	r, db := setupDivaMapRewardWindowTest(t)
	now := divaTestTime(21, 12, 0)
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	markDivaMapRewardTestPersonalLegacy(t, db, event.ID)
	if _, err = db.Exec(`INSERT INTO diva_map_activations(event_id,activated_at) VALUES($1,$2)`, event.ID, now); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []int{1, 2, 3} {
		tx, err := db.Beginx()
		if err != nil {
			t.Fatal(err)
		}
		got, err := lockDivaMapRewardWindow(tx, now, mode)
		if err != nil || got.ID != event.ID {
			_ = tx.Rollback()
			t.Fatalf("mode %d selected %+v: %v", mode, got, err)
		}
		personal, err := lockDivaInterceptionRewardWindow(tx, now, mode)
		if err != nil || personal.ID != 0 {
			_ = tx.Rollback()
			t.Fatalf("mode %d altered personal eligibility: %+v %v", mode, personal, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []int{-2, 0, 4} {
		tx, err := db.Beginx()
		if err != nil {
			t.Fatal(err)
		}
		_, err = lockDivaMapRewardWindow(tx, now, mode)
		_ = tx.Rollback()
		if !errors.Is(err, ErrDivaInterceptionRewardExpired) {
			t.Fatalf("unsupported mode %d accepted: %v", mode, err)
		}
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_interception_periods`); err != nil || count != 1 {
		t.Fatalf("forced prayer/welcome created interception periods: %d %v", count, err)
	}
}

func TestRepoDivaMapRewardWindowActivatedLegacyGuildTreasureAndHall(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 26)
	start, end := divaInterceptionWindow(event)
	markDivaMapRewardTestPersonalLegacy(t, db, event.ID)
	if _, err := db.Exec(`INSERT INTO diva_map_legacy_events(event_id) VALUES($1)`, event.ID); err != nil {
		t.Fatal(err)
	}
	// This synthetic fixture activates at the stored map's original start;
	// creation at a mid-round activation instant is covered by map-core tests.
	if _, err := db.Exec(`INSERT INTO diva_map_activations(event_id,activated_at) VALUES($1,$2)`, event.ID, start); err != nil {
		t.Fatal(err)
	}
	insertDivaGuildTreasureTestAward(t, db, char, guild, event.ID, 407, divaRunKeyTwo, now, true)
	guildOffers, err := r.offerDivaGuildRewardsAt(char, now)
	if err != nil || len(guildOffers) != 12 {
		t.Fatalf("activated legacy guild rewards=%+v error=%v", guildOffers, err)
	}
	if claims, err := r.prepareDivaGuildRewardClaimsAt(char, divaGuildRewardTestIDs(guildOffers, 0), now); err != nil || len(claims) != 12 {
		t.Fatalf("activated legacy guild claims=%+v error=%v", claims, err)
	}
	treasures, err := r.offerDivaMapRewardsAt(char, 5, now)
	if err != nil || len(treasures) != 2 {
		t.Fatalf("activated legacy treasures=%+v error=%v", treasures, err)
	}
	if claims, err := r.prepareDivaMapRewardClaimsAt(char, 5, divaGuildRewardTestIDs(treasures, 0), now); err != nil || len(claims) != 2 {
		t.Fatalf("activated legacy treasure claims=%+v error=%v", claims, err)
	}
	if hall, err := r.GetDivaSpecialHall(char, guild, end.Add(time.Duration(divaInterlude)*time.Second)); err != nil || !hall {
		t.Fatalf("activated legacy hall flag=%v error=%v", hall, err)
	}
	entries := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("legacy-personal-stays-disabled", 6)}
	if _, err := r.offerDivaRewardsAt(char, event.ID, 6, entries, now); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("map activation exposed legacy personal prizes: %v", err)
	}
}
