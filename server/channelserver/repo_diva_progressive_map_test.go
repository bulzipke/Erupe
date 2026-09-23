package channelserver

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func enableDivaProgressiveFixture(t *testing.T, db *sqlx.DB, start time.Time) {
	t.Helper()
	for _, query := range []string{
		`UPDATE diva_random_map_cutover SET installed_at=$1`,
		`UPDATE diva_progressive_map_cutover SET installed_at=$1`,
	} {
		if _, err := db.Exec(query, start.Add(-time.Microsecond)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepoDivaProgressiveCutoverAndPinnedRounds(t *testing.T) {
	for _, offset := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
		t.Run(offset.String(), func(t *testing.T) {
			r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
			enableDivaProgressiveFixture(t, db, start)
			if _, err := db.Exec(`UPDATE diva_progressive_map_cutover SET installed_at=$1`, start.Add(offset)); err != nil {
				t.Fatal(err)
			}
			view, err := r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour))
			if err != nil || !view.Enabled {
				t.Fatalf("map: %+v %v", view, err)
			}
			want := divaRandomMapRules
			if offset < 0 {
				want = divaProgressiveMapRules
			}
			if view.Map.RulesVersion != want {
				t.Fatalf("rules=%s want=%s", view.Map.RulesVersion, want)
			}
			var before string
			if err := db.Get(&before, `SELECT map_data::text FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil {
				t.Fatal(err)
			}
			// Deployment date/config changes never upgrade a pinned old round.
			if _, err := db.Exec(`UPDATE diva_progressive_map_cutover SET installed_at=$1`, start.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			after, err := NewDivaRepository(db).GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour))
			if err != nil || !reflect.DeepEqual(view, after) {
				t.Fatalf("pinned round changed: %v", err)
			}
			var unchanged bool
			if err := db.Get(&unchanged, `SELECT map_data::text=$3 FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild, before); err != nil || !unchanged {
				t.Fatal("stored JSON changed", err)
			}
		})
	}
}

func TestRepoDivaProgressiveClockJournalAndRestart(t *testing.T) {
	r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
	enableDivaProgressiveFixture(t, db, start)
	view, err := r.GetDivaMap(char, guild, event.ID, start)
	if err != nil || !view.Enabled || view.Map.States[0].InvasionTick != 0 {
		t.Fatalf("initial: %+v %v", view, err)
	}
	var mode string
	if err := db.Get(&mode, `SELECT red_treasure_mode FROM diva_map_events WHERE event_id=$1`, event.ID); err != nil || mode != "progressive" {
		t.Fatal("automatic policy missing", mode, err)
	}
	// Work out every expected deterministic attack independently of storage.
	expected, attacks := view.Map, 0
	for tick := uint16(1); tick <= 48; tick++ {
		var changed []uint16
		expected, changed, err = applyDivaProgressiveInvasion(expected, tick)
		if err != nil {
			t.Fatal(err)
		}
		if len(changed) > 0 {
			attacks++
		}
	}
	// Two simultaneous catch-ups serialize through the event lock.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := NewDivaRepository(db).GetDivaMap(char, guild, event.ID, start.Add(48*time.Hour))
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	after, err := r.GetDivaMap(char, guild, event.ID, start.Add(48*time.Hour))
	if err != nil || !reflect.DeepEqual(after.Map, expected) {
		t.Fatalf("catchup differs: %v", err)
	}
	selection, err := selectDivaProgressiveTreasures(expected)
	if err != nil || !reflect.DeepEqual(selection, after.SpecialTreasures) || after.SpecialTreasureError != nil {
		t.Fatal("automatic treasure policy missing", err)
	}
	var snapshots, journal int
	if err := db.Get(&snapshots, `SELECT COUNT(*) FROM diva_map_snapshots WHERE event_id=$1`, event.ID); err != nil || snapshots != 49 {
		t.Fatalf("no-attack ticks not persisted: %d %v", snapshots, err)
	}
	if err := db.Get(&journal, `SELECT COUNT(*) FROM diva_map_invasions WHERE event_id=$1`, event.ID); err != nil || journal != attacks {
		t.Fatalf("invasion journal: %d want %d %v", journal, attacks, err)
	}
	logs, err := r.GetDivaTacticsLog(char, start.Add(48*time.Hour))
	if err != nil || divaLogKinds(logs)[3] != attacks {
		t.Fatalf("native invasion log missing: %+v %v", logs, err)
	}
	for _, row := range logs {
		if row.Kind == 3 && (row.CharID != 0 || row.Name != "" || row.Value != 0 || row.Branch != 0) {
			t.Fatal("invasion fixed text supplied arguments")
		}
	}
	// Final settlement must not create an attack at or beyond the period end.
	_, end := divaInterceptionWindow(event)
	beforeEnd, err := r.GetDivaMap(char, guild, event.ID, end.Add(-time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.GetDivaMap(char, guild, event.ID, end.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(beforeEnd.Map, final.Map) {
		t.Fatalf("final cutoff attacked map: %v", err)
	}
	if err := db.Get(&journal, `SELECT COUNT(*) FROM diva_map_invasions WHERE event_id=$1 AND happened_at>=$2`, event.ID, end); err != nil || journal != 0 {
		t.Fatal("late invasion", journal, err)
	}
}

func TestRepoDivaProgressiveLateCreationAndPageBirth(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	enableDivaProgressiveFixture(t, db, start)
	firstUse := start.Add(48*time.Hour + 15*time.Minute)
	initial, err := r.GetDivaMap(char, guild, event.ID, firstUse)
	if err != nil || initial.Map.States[0].InvasionTick != 0 {
		t.Fatalf("pre-creation attacks: %v", err)
	}
	one, err := r.GetDivaMap(char, guild, event.ID, start.Add(49*time.Hour))
	if err != nil || one.Map.States[0].InvasionTick != 1 {
		t.Fatalf("first hourly tick: %v", err)
	}
	// A legitimate capture in the bucket precedes the attack and survives it.
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 5000, start.Add(49*time.Hour+time.Minute), start.Add(49*time.Hour+2*time.Minute))
	partial, err := r.GetDivaMap(char, guild, event.ID, start.Add(50*time.Hour))
	if err != nil || partial.Map.AcquiredAreas < 1 {
		t.Fatalf("capture: %+v %v", partial, err)
	}
	captured := partial.Map.States[0].Nodes[1]
	later, err := r.GetDivaMap(char, guild, event.ID, start.Add(60*time.Hour))
	if err != nil || later.Map.States[0].Nodes[1] != captured {
		t.Fatalf("captured tile invaded: %v", err)
	}
	// Enough points finish page 1. Page 2 must be born at tick zero, not one.
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyTwo, 200000, start.Add(60*time.Hour+time.Minute), start.Add(60*time.Hour+2*time.Minute))
	next, err := r.GetDivaMap(char, guild, event.ID, start.Add(61*time.Hour))
	if err != nil || next.Map.States[0].MapNumber != 2 || next.Map.States[0].InvasionTick != 0 || next.Map.AcquiredAreas != 20 {
		t.Fatalf("page birth: %+v %v", next, err)
	}
	if next.Map.States[1].InvasionTick != later.Map.States[0].InvasionTick {
		t.Fatal("old page age changed during transition")
	}
	expected, err := divaProgressiveMapAt(next.Map.GenerationSeed, 2)
	if err != nil || !reflect.DeepEqual(expected.States[0], next.Map.States[0]) {
		t.Fatal("birth bucket fortified fresh page", err)
	}
	var awards int
	if err := db.Get(&awards, `SELECT COUNT(*) FROM diva_map_area_awards WHERE event_id=$1`, event.ID); err != nil || awards != 20 {
		t.Fatal("capture records changed", awards, err)
	}
	progress, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || progress.Points != 205000 {
		t.Fatal("personal points changed", progress, err)
	}
	// Historical reward lookups must still validate a captured, reinforced map.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	history, err := loadDivaTreasureMapTx(tx, event.ID, guild, 1)
	if err != nil || history.States[0].MapNumber != 1 || history.RulesVersion != divaProgressiveMapRules {
		t.Fatalf("history unusable: %v", err)
	}
}

func TestRepoDivaProgressiveTreasureReceiptSurvivesInvasionAndPaging(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	enableDivaProgressiveFixture(t, db, start)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 25000, start.Add(time.Minute), start.Add(2*time.Minute))
	view, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	branchQuest := view.Map.States[0].Nodes[21].BranchQuests[0]
	divaMapTestReport(t, r, char, guild, event, branchQuest, divaRunKeyTwo, 5000, start.Add(61*time.Minute), start.Add(62*time.Minute))
	offers, err := r.offerDivaMapRewardsAt(char, 5, start.Add(2*time.Hour), 2)
	if err != nil || len(offers) != 2 {
		t.Fatalf("treasure offer: %+v %v", offers, err)
	}
	if offers[0].ItemType != 7 || offers[0].ItemID != 1026 || offers[0].Quantity != 5 || offers[1].ItemType != 26 || offers[1].Quantity != 500 {
		t.Fatal("treasure payout changed", offers)
	}
	if _, err := r.prepareDivaMapRewardClaimsAt(char, 5, divaGuildRewardTestIDs(offers, 0), start.Add(2*time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	// Model the existing savedata receipt acknowledgement, not a new payout.
	if _, err := db.Exec(`UPDATE diva_reward_receipts SET claimed_at=$2 WHERE id=$1`, offers[0].ID, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"12345678-1234-4567-89ab-123456789abe", "12345678-1234-4567-89ab-123456789abf"} {
		at := start.Add(time.Duration(12+i) * time.Hour)
		divaMapTestReport(t, r, char, guild, event, quest, key, 300000, at.Add(time.Minute), at.Add(2*time.Minute))
		view, err = r.GetDivaMap(char, guild, event.ID, at.Add(time.Hour))
		if err != nil || view.Map.States[0].MapNumber != uint16(i+2) {
			t.Fatalf("page transition: %v", err)
		}
	}
	remaining, err := NewDivaRepository(db).offerDivaMapRewardsAt(char, 5, start.Add(14*time.Hour), 2)
	if err != nil || len(remaining) != 1 || remaining[0] != offers[1] {
		t.Fatalf("old claimed/pending receipt changed: %+v %v", remaining, err)
	}
	if _, err := r.prepareDivaMapRewardClaimsAt(char, 5, []uint32{offers[1].ID}, start.Add(14*time.Hour), 2); err != nil {
		t.Fatal("historical reward no longer claimable", err)
	}
}
