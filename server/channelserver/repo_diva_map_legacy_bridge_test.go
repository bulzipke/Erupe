package channelserver

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setupDivaMapLegacyBridgeTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32, DivaEvent, time.Time, uint16) {
	t.Helper()
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	activation := start.Add(12*time.Hour + 17*time.Minute)
	for _, query := range []string{
		`INSERT INTO diva_interception_legacy_events VALUES($1)`,
		`INSERT INTO diva_map_legacy_events VALUES($1)`,
	} {
		if _, err := db.Exec(query, event.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE diva_map_cutover SET installed_at=$1`, activation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_map_activations VALUES($1,$2)`, event.ID, activation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET interception_points=$1::jsonb WHERE character_id=$2`,
		fmt.Sprintf(`{"%d":123456,"58044":7654}`, quest), char); err != nil {
		t.Fatal(err)
	}
	return r, db, char, guild, event, activation, quest
}

func TestRepoDivaMapLegacyBridgePreservesTotalsAndCountsOnlyFreshRuns(t *testing.T) {
	r, db, char, guild, event, activation, quest := setupDivaMapLegacyBridgeTest(t)
	view, err := r.GetDivaMap(char, guild, event.ID, activation)
	if err != nil || !view.Enabled || view.Map.AcquiredAreas != 0 {
		t.Fatalf("initial map=%+v %v", view, err)
	}
	depart := activation.Add(time.Minute)
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, depart, depart); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyOne, depart, depart.Add(time.Minute))
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	points, err := r.GetCharacterInterceptionPoints(char)
	if err != nil || points[strconv.Itoa(int(quest))] != 125956 || points["58044"] != 7654 {
		t.Fatalf("legacy totals=%v %v", points, err)
	}
	boundary := activation.Truncate(time.Hour).Add(time.Hour)
	view, err = r.GetDivaMap(char, guild, event.ID, boundary)
	if err != nil || view.Map.AcquiredAreas != 1 {
		t.Fatalf("fresh area count=%+v %v", view, err)
	}
	var runs, contributions, markers int
	if err = db.Get(&runs, `SELECT COUNT(*) FROM diva_interception_runs WHERE event_id=$1`, event.ID); err != nil || runs != 1 {
		t.Fatalf("runs=%d %v", runs, err)
	}
	if err = db.Get(&contributions, `SELECT COUNT(*) FROM diva_map_contributions WHERE event_id=$1`, event.ID); err != nil || contributions != 1 {
		t.Fatalf("contributions=%d %v", contributions, err)
	}
	if err = db.Get(&markers, `SELECT COUNT(*) FROM diva_interception_legacy_events WHERE event_id=$1`, event.ID); err != nil || markers != 1 {
		t.Fatalf("legacy marker=%d %v", markers, err)
	}
	progress, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || progress.Enabled || progress.Points != 0 {
		t.Fatalf("personal cutover changed=%+v %v", progress, err)
	}
	personal, err := r.GetDivaInterceptionRewardEvent(boundary)
	if err != nil || personal.ID != 0 {
		t.Fatalf("personal prizes unlocked=%+v %v", personal, err)
	}
	mapEvent, err := r.GetDivaMapRewardEvent(boundary)
	if err != nil || mapEvent.ID != event.ID {
		t.Fatalf("map window missing=%+v %v", mapEvent, err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, quest, 2501, guild, divaRunKeyOne, depart, boundary); !errors.Is(err, ErrDivaInterceptionInvalid) {
		t.Fatalf("altered replay=%v", err)
	}
}

func TestRepoDivaMapLegacyBridgeRejectsUnboundOrMismatchedReports(t *testing.T) {
	r, db, char, guild, event, activation, quest := setupDivaMapLegacyBridgeTest(t)
	depart := activation.Add(time.Minute)
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyOne, depart, depart); !errors.Is(err, ErrDivaInterceptionLegacy) {
		t.Fatalf("unbound accepted=%v", err)
	}
	if _, err := r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, depart, depart); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		quest uint16
		guild uint32
		start time.Time
	}{
		{quest + 1, guild, depart}, {quest, guild + 1, depart}, {quest, guild, depart.Add(time.Microsecond)}, {quest, guild, activation.Add(-time.Second)},
	} {
		if err := r.AddDivaInterceptionPoints(char, event.ID, tt.quest, 2500, tt.guild, divaRunKeyOne, tt.start, depart.Add(time.Minute)); !errors.Is(err, ErrDivaInterceptionLegacy) {
			t.Fatalf("mismatched report accepted=%+v %v", tt, err)
		}
	}
	// Removing the test-only activation must not let the personal legacy marker
	// be bypassed merely because a bound departure already exists.
	if _, err := db.Exec(`DELETE FROM diva_map_activations WHERE event_id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyOne, depart, depart); !errors.Is(err, ErrDivaInterceptionLegacy) {
		t.Fatalf("unactivated accepted=%v", err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_interception_runs WHERE event_id=$1`, event.ID); err != nil || count != 0 {
		t.Fatalf("rejected reports saved=%d %v", count, err)
	}
	points, err := r.GetCharacterInterceptionPoints(char)
	if err != nil || points[strconv.Itoa(int(quest))] != 123456 {
		t.Fatalf("rejected reports changed old totals=%v %v", points, err)
	}
}

func TestRepoDivaMapLegacyBridgePersonalFailureRollsBackMap(t *testing.T) {
	r, db, char, guild, event, activation, quest := setupDivaMapLegacyBridgeTest(t)
	if _, err := db.Exec(`UPDATE guild_characters SET interception_points=$1::jsonb WHERE character_id=$2`, fmt.Sprintf(`{"%d":9223372036854775807}`, quest), char); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, activation, activation); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyOne, activation, activation.Add(time.Minute)); err == nil {
		t.Fatal("overflowing personal total should reject entire transaction")
	}
	for _, table := range []string{"diva_interception_runs", "diva_map_contributions"} {
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM `+table+` WHERE event_id=$1`, event.ID); err != nil || count != 0 {
			t.Fatalf("partial %s write=%d %v", table, count, err)
		}
	}
	view, err := r.GetDivaMap(char, guild, event.ID, activation.Add(time.Hour))
	if err != nil || view.Map.AcquiredAreas != 0 {
		t.Fatalf("partial map award=%+v %v", view, err)
	}
}

func TestRepoDivaMapLegacyBridgeLockedBranchKeepsPersonalOnly(t *testing.T) {
	r, db, char, guild, event, activation, _ := setupDivaMapLegacyBridgeTest(t)
	const branchQuest = 58079
	bound, err := r.BindDivaMapDeparture(char, guild, event.ID, branchQuest, divaRunKeyOne, activation, activation)
	if err != nil || bound.Enabled {
		t.Fatalf("branch should be locked=%+v %v", bound, err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, branchQuest, 2500, guild, divaRunKeyOne, activation, activation.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	points, err := r.GetCharacterInterceptionPoints(char)
	if err != nil || points["58079"] != 2500 {
		t.Fatalf("personal result lost=%v %v", points, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_contributions WHERE event_id=$1`, event.ID); err != nil || count != 0 {
		t.Fatalf("locked route credited=%d %v", count, err)
	}
}
