package channelserver

import (
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
)

func TestRepoDivaMapSpecialRoundPolicyAndHistory(t *testing.T) {
	r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureRandomOne, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureRandomOne, start.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var revisions int
	if err := db.Get(&revisions, `SELECT COUNT(*) FROM diva_map_special_policy_history`); err != nil || revisions != 1 {
		t.Fatalf("restart added policy revision: %d %v", revisions, err)
	}
	// No guild has queried the map yet, but the round already started under
	// random-one. A later all setting must not retroactively change this round.
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureAll, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	first, err := r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour))
	if err != nil || !first.Enabled || first.SpecialTreasureError != nil || len(first.SpecialTreasures) != 1 {
		t.Fatalf("view=%+v err=%v", first, err)
	}
	var stored string
	if err := db.Get(&stored, `SELECT map_data::text FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.Get(&mode, `SELECT red_treasure_mode FROM diva_map_events WHERE event_id=$1`, event.ID); err != nil || mode != cfg.DivaMapRedTreasureRandomOne {
		t.Fatalf("pinned=%s err=%v", mode, err)
	}
	if _, err := db.Exec(`UPDATE diva_map_events SET red_treasure_mode='all' WHERE event_id=$1`, event.ID); err == nil {
		t.Fatal("existing round policy was mutable")
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureOff, start.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	restarted := NewDivaRepository(db)
	if err := restarted.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureOff, start.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := restarted.GetDivaMap(char, guild, event.ID, start.Add(4*time.Hour))
	if err != nil || !reflect.DeepEqual(first.SpecialTreasures, second.SpecialTreasures) || !reflect.DeepEqual(first.Map, second.Map) {
		t.Fatal("restart/settings change altered an existing map")
	}
	var after string
	if err := db.Get(&after, `SELECT map_data::text FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil || stored != after {
		t.Fatal("presentation mutated persisted map JSON")
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{start.Add(-time.Hour), cfg.DivaMapRedTreasureOff}, // equality is not a new round
		{start, cfg.DivaMapRedTreasureRandomOne},
		{start.Add(2 * time.Hour), cfg.DivaMapRedTreasureAll},
		{start.Add(4 * time.Hour), cfg.DivaMapRedTreasureOff},
	} {
		mode, err := divaMapSpecialModeForNewEventTx(tx, divaRandomMapRules, tc.at)
		if err != nil || mode != tc.want {
			t.Fatalf("at=%v mode=%s want=%s err=%v", tc.at, mode, tc.want, err)
		}
	}
	if mode, err := divaMapSpecialModeForNewEventTx(tx, divaCustomMapRules, start); err != nil || mode != cfg.DivaMapRedTreasureOff {
		t.Fatal("v1 enabled a new display policy")
	}
}

func TestRepoDivaMapSpecialPolicyReadFailureKeepsMapUsable(t *testing.T) {
	r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureAll, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE diva_map_special_policy_history RENAME TO unavailable_diva_policy_history`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(`ALTER TABLE unavailable_diva_policy_history RENAME TO diva_map_special_policy_history`); err != nil {
			t.Error(err)
		}
	}()
	// A missing optional policy relation aborts a PostgreSQL statement. The
	// savepoint must restore the transaction before normal map creation proceeds.
	view, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Minute))
	if err != nil || !view.Enabled || len(view.SpecialTreasures) != 0 {
		t.Fatalf("optional fault blocked map: %+v %v", view, err)
	}
	var mode string
	if err := db.Get(&mode, `SELECT red_treasure_mode FROM diva_map_events WHERE event_id=$1`, event.ID); err != nil || mode != cfg.DivaMapRedTreasureOff {
		t.Fatalf("failed read did not pin off: %s %v", mode, err)
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureRandomOne, start.Add(2*time.Minute)); err == nil {
		t.Fatal("configuration unexpectedly succeeded without history table")
	}
	if r.mapSpecialPolicyReady.Load() {
		t.Fatal("failed configuration left presentation enabled")
	}
	view, err = r.GetDivaMap(char, guild, event.ID, start.Add(3*time.Minute))
	if err != nil || !view.Enabled || len(view.SpecialTreasures) != 0 {
		t.Fatal("failed startup policy disabled the existing map")
	}
}

func TestRepoDivaMapSpecialViewReadFailurePreservesTransaction(t *testing.T) {
	r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.ConfigureDivaMapSpecialTreasures(cfg.DivaMapRedTreasureAll, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if view, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Minute)); err != nil || !view.Enabled || len(view.SpecialTreasures) != 2 {
		t.Fatalf("initial view=%+v err=%v", view, err)
	}
	if _, err := db.Exec(`ALTER TABLE diva_map_events RENAME COLUMN red_treasure_mode TO unavailable_red_mode`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(`ALTER TABLE diva_map_events RENAME COLUMN unavailable_red_mode TO red_treasure_mode`); err != nil {
			t.Error(err)
		}
	}()
	view, err := r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Minute))
	if err != nil || !view.Enabled || view.SpecialTreasureError == nil || len(view.SpecialTreasures) != 0 {
		t.Fatalf("display read failure damaged core transaction: %+v %v", view, err)
	}
}
