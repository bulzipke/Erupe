package channelserver

import (
	"errors"
	"testing"
	"time"
)

var _ DivaMapActivationRepository = (*DivaRepository)(nil)

func TestRepoDivaMapActivationKeepsLegacyAndExcludesEarlierDepartures(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	activation := start.Add(2*time.Hour + 17*time.Minute + 123456*time.Microsecond)
	if _, err := db.Exec(`UPDATE diva_map_cutover SET installed_at=$1`, activation); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO diva_map_legacy_events VALUES($1)`,
		`INSERT INTO diva_interception_legacy_events VALUES($1)`,
	} {
		if _, err := db.Exec(query, event.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO diva_map_activations VALUES($1,$2)`, event.ID, activation); err != nil {
		t.Fatal(err)
	}
	got, enabled, err := r.GetDivaMapActivation(event.ID)
	if err != nil || !enabled || !got.Equal(activation) {
		t.Fatalf("activation=%v %v %v", got, enabled, err)
	}
	if _, enabled, err = r.GetDivaMapActivation(event.ID + 999); err != nil || enabled {
		t.Fatalf("unknown activation=%v %v", enabled, err)
	}
	before, err := r.GetDivaMap(char, guild, event.ID, activation.Add(-time.Microsecond))
	if err != nil || before.Enabled {
		t.Fatalf("pre-activation map=%+v %v", before, err)
	}
	view, err := r.GetDivaMap(char, guild, event.ID, activation)
	if err != nil || !view.Enabled || view.Map.AcquiredAreas != 0 {
		t.Fatalf("activation map=%+v %v", view, err)
	}
	_, end := divaInterceptionWindow(event)
	var actualStart, actualEnd time.Time
	if err = db.QueryRow(`SELECT starts_at,ends_at FROM diva_map_events WHERE event_id=$1`, event.ID).Scan(&actualStart, &actualEnd); err != nil || !actualStart.Equal(activation) || !actualEnd.Equal(end) {
		t.Fatalf("effective window=%v..%v %v", actualStart, actualEnd, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_publications WHERE event_id=$1 AND published_at<$2`, event.ID, activation); err != nil || count != 0 {
		t.Fatalf("backdated publications=%d %v", count, err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, activation.Add(-time.Microsecond), activation); !errors.Is(err, ErrDivaMapDeparture) {
		t.Fatalf("old departure admitted: %v", err)
	}
	bound, err := r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, activation, activation)
	if err != nil || !bound.Enabled || bound.MapNumber != 1 {
		t.Fatalf("new departure=%+v %v", bound, err)
	}
	// This tests only the map ledger; the parent-owned legacy bridge updates
	// legacy JSON in its same transaction and retains personal reward policy.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err = recordDivaMapContributionTx(tx, char, event.ID, divaRunKeyOne, quest, guild, 2500, activation, activation.Add(time.Second)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	boundary := activation.Truncate(time.Hour).Add(time.Hour)
	view, err = r.GetDivaMap(char, guild, event.ID, boundary)
	if err != nil || view.Map.AcquiredAreas != 1 {
		t.Fatalf("post-activation settlement=%+v %v", view, err)
	}
	progress, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || progress.Enabled || progress.Points != 0 {
		t.Fatalf("personal legacy policy changed: %+v %v", progress, err)
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_interception_runs WHERE event_id=$1`, event.ID); err != nil || count != 0 {
		t.Fatalf("map retro-converted personal records: %d %v", count, err)
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_legacy_events WHERE event_id=$1`, event.ID); err != nil || count != 1 {
		t.Fatalf("legacy exclusion removed: %d %v", count, err)
	}
	// Restart/re-query must preserve the activation anchor and awarded areas.
	restarted := NewDivaRepository(db)
	view, err = restarted.GetDivaMap(char, guild, event.ID, boundary.Add(time.Hour))
	if err != nil || view.Map.AcquiredAreas != 1 {
		t.Fatalf("restart lost activation progress: %+v %v", view, err)
	}
}
