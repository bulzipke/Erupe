package channelserver

import (
	"encoding/json"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDivaMapCatalogRulesIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   divaMapRules
		version string
		seed    uint64
		valid   bool
	}{
		{"legacy", divaMapRules{divaCustomMapRules, 0}, "", 0, true},
		{"legacy_explicit_metadata_rejected", divaMapRules{divaCustomMapRules, 0}, divaCustomMapRules, 0, false},
		{"legacy_seed_rejected", divaMapRules{divaCustomMapRules, 0}, "", 1, false},
		{"random", divaMapRules{divaRandomMapRules, 17}, divaRandomMapRules, 17, true},
		{"maximum_seed", divaMapRules{divaRandomMapRules, math.MaxInt64}, divaRandomMapRules, math.MaxInt64, true},
		{"wrong_seed", divaMapRules{divaRandomMapRules, 17}, divaRandomMapRules, 18, false},
		{"wrong_version", divaMapRules{divaRandomMapRules, 17}, "", 17, false},
		{"zero_random_seed", divaMapRules{divaRandomMapRules, 0}, divaRandomMapRules, 0, false},
		{"unsigned_overflow", divaMapRules{divaRandomMapRules, uint64(math.MaxInt64) + 1}, divaRandomMapRules, uint64(math.MaxInt64) + 1, false},
		{"unknown_version", divaMapRules{"future", 17}, "future", 17, false},
		{"invalid_legacy_event", divaMapRules{divaCustomMapRules, 1}, "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDivaMapCatalogRules(tc.rules, DivaInterceptionMap{RulesVersion: tc.version, GenerationSeed: tc.seed})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestRepoDivaRandomMapActualPeriodCutover(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cutoverOffset time.Duration
		activation    bool
		wantRandom    bool
	}{
		{"starts_before_cutover_even_if_first_query_later", time.Microsecond, false, false},
		{"exact_cutover_stays_v1", 0, false, false},
		{"starts_after_cutover", -time.Microsecond, false, true},
		{"late_activation_does_not_make_old_round_new", time.Hour, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
			if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(tc.cutoverOffset)); err != nil {
				t.Fatal(err)
			}
			now := start.Add(2 * time.Hour)
			if tc.activation {
				if _, err := db.Exec(`UPDATE diva_map_cutover SET installed_at=$1`, now); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO diva_map_legacy_events VALUES($1);`, event.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO diva_map_activations VALUES($1,$2)`, event.ID, now); err != nil {
					t.Fatal(err)
				}
			}
			view, err := r.GetDivaMap(char, guild, event.ID, now)
			if err != nil || !view.Enabled {
				t.Fatalf("view=%+v err=%v", view, err)
			}
			var version string
			var seed int64
			if err := db.QueryRow(`SELECT rules_version,generation_seed FROM diva_map_events WHERE event_id=$1`, event.ID).Scan(&version, &seed); err != nil {
				t.Fatal(err)
			}
			if tc.wantRandom {
				if version != divaRandomMapRules || seed <= 0 || view.Map.GenerationSeed != uint64(seed) || view.Map.RulesVersion != version {
					t.Fatalf("random identity=%s/%d map=%s/%d", version, seed, view.Map.RulesVersion, view.Map.GenerationSeed)
				}
			} else if version != divaCustomMapRules || seed != 0 || view.Map.RulesVersion != "" || view.Map.GenerationSeed != 0 {
				t.Fatalf("legacy round converted: %s/%d", version, seed)
			}
		})
	}
}

func TestRepoDivaRandomMapKeepsExistingV1Progress(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	depart := start.Add(time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 3000, depart, depart.Add(time.Minute))
	before, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
	if err != nil || before.Map.AcquiredAreas != 1 {
		t.Fatalf("v1 progress=%+v %v", before, err)
	}
	var oldJSON string
	if err := db.Get(&oldJSON, `SELECT map_data::text FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil {
		t.Fatal(err)
	}
	// Simulate a changed deployment policy without rewriting the pinned event.
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := NewDivaRepository(db).GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("pinned v1 changed: %+v %v", after, err)
	}
	var same bool
	if err := db.Get(&same, `SELECT map_data::text=$3 FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild, oldJSON); err != nil || !same {
		t.Fatalf("v1 JSON rewritten=%v %v", !same, err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_map_contributions WHERE event_id=$1 AND credited_points=3000`, event.ID); err != nil || count != 1 {
		t.Fatalf("contribution changed=%d %v", count, err)
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_map_area_awards WHERE event_id=$1`, event.ID); err != nil || count != 1 {
		t.Fatalf("award changed=%d %v", count, err)
	}
}

func TestRepoDivaRandomMapSharedSeedConcurrentGuildsAndRestart(t *testing.T) {
	r, db, char1, guild1, event, start, quest := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "diva_random_second")
	char2 := CreateTestCharacter(t, db, user, "RandomSecond")
	guild2 := CreateTestGuild(t, db, char2, "무작위 둘째")
	chars := []uint32{char1, char2}
	guilds := []uint32{guild1, guild2}
	views := make([]DivaMapView, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			views[i], errs[i] = r.GetDivaMap(chars[i], guilds[i], event.ID, start)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !views[0].Enabled || views[0].Map.GenerationSeed == 0 || !reflect.DeepEqual(views[0], views[1]) {
		t.Fatal("same event produced different initial maps across guilds")
	}
	seed := views[0].Map.GenerationSeed
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_map_events WHERE event_id=$1 AND rules_version=$2 AND generation_seed=$3`, event.ID, divaRandomMapRules, int64(seed)); err != nil || count != 1 {
		t.Fatalf("event seed count=%d %v", count, err)
	}
	// Both guilds finish the same main map independently. Points and awarded
	// areas remain per guild, but their next map geometry is identical.
	depart := start.Add(time.Minute)
	for i := range 2 {
		divaMapTestReport(t, r, chars[i], guilds[i], event, quest, divaRunKeyOne, 100000, depart, depart.Add(time.Minute))
	}
	for i := range 2 {
		view, err := r.GetDivaMap(chars[i], guilds[i], event.ID, start.Add(time.Hour))
		if err != nil || view.Map.States[0].MapNumber != 2 || view.Map.GenerationSeed != seed || view.Map.AcquiredAreas != 20 {
			t.Fatalf("next map=%+v %v", view, err)
		}
		views[i] = view
	}
	if !reflect.DeepEqual(views[0], views[1]) {
		t.Fatal("same map number produced different geometry across guilds")
	}
	restarted, err := NewDivaRepository(db).GetDivaMap(char1, guild1, event.ID, start.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(views[0], restarted) {
		t.Fatalf("restart changed pinned seed or geometry: %v", err)
	}
}

func TestRepoDivaRandomMapRejectsForeignSeedCurrentAndDepartureSnapshots(t *testing.T) {
	for _, history := range []bool{false, true} {
		name := "current"
		if history {
			name = "departure_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
			if _, err := db.Exec(`UPDATE diva_random_map_cutover SET installed_at=$1`, start.Add(-time.Microsecond)); err != nil {
				t.Fatal(err)
			}
			view, err := r.GetDivaMap(char, guild, event.ID, start)
			if err != nil {
				t.Fatal(err)
			}
			seed := uint64(1)
			if view.Map.GenerationSeed == seed {
				seed = 2
			}
			foreign, err := divaRandomMapInitial(seed)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(foreign)
			if err != nil {
				t.Fatal(err)
			}
			query := `UPDATE diva_map_guilds SET map_data=$3 WHERE event_id=$1 AND guild_id=$2`
			if history {
				query = `UPDATE diva_map_snapshots SET map_data=$3 WHERE event_id=$1 AND guild_id=$2`
			}
			if _, err = db.Exec(query, event.ID, guild, data); err != nil {
				t.Fatal(err)
			}
			if history {
				_, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, start.Add(time.Minute), start.Add(time.Minute))
			} else {
				_, err = r.GetDivaMap(char, guild, event.ID, start)
			}
			if err == nil {
				t.Fatal("valid geometry from a different event seed was accepted")
			}
			var departures int
			if err := db.Get(&departures, `SELECT COUNT(*) FROM diva_map_departures WHERE event_id=$1`, event.ID); err != nil || departures != 0 {
				t.Fatalf("rejected snapshot created departure=%d %v", departures, err)
			}
		})
	}
}
