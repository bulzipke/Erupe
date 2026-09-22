package channelserver

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaMapRepository = (*DivaRepository)(nil)

func setupDivaMapRepoTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32, DivaEvent, time.Time, uint16) {
	t.Helper()
	cfg := DefaultTestDBConfig()
	if (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") || cfg.Port != "5433" || cfg.DBName != "erupe_test" {
		t.Fatal("map tests require isolated localhost:5433/erupe_test before schema reset")
	}
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "diva_map_test")
	charID := CreateTestCharacter(t, db, user, "MapHunter")
	guildID := CreateTestGuild(t, db, charID, "지도 수렵단")
	now := divaTestTime(21, 12, 0)
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	start, _ := divaInterceptionWindow(event)
	if _, err = db.Exec(`INSERT INTO diva_map_cutover(singleton,installed_at) VALUES(TRUE,$1)
		ON CONFLICT(singleton) DO UPDATE SET installed_at=EXCLUDED.installed_at`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	m, err := divaCustomMapInitial()
	if err != nil {
		t.Fatal(err)
	}
	var quest uint16
	for q := uint16(udTacticsQuestMin); q <= udTacticsQuestMax; q++ {
		if route, ok := divaCustomMapQuestRoute(m, q); ok && route == 0 {
			quest = q
			break
		}
	}
	if quest == 0 {
		t.Fatal("custom map has no verified main quest")
	}
	return r, db, charID, guildID, event, start, quest
}

func divaMapTestReport(t *testing.T, r *DivaRepository, charID, guildID uint32, event DivaEvent, quest uint16, key string, points uint32, started, reported time.Time) {
	t.Helper()
	if _, err := r.BindDivaMapDeparture(charID, guildID, event.ID, quest, key, started, started); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(charID, event.ID, quest, points, guildID, key, started, reported); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDivaMapHourlyIdempotenceAndOverflowDiscard(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	depart := start.Add(10 * time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 100000, depart, depart.Add(time.Minute))
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 100000, guild, divaRunKeyOne, depart, depart.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	before, err := r.GetDivaMap(char, guild, event.ID, start.Add(59*time.Minute))
	if err != nil || !before.Enabled || before.Map.AcquiredAreas != 0 {
		t.Fatalf("premature settlement: %+v %v", before, err)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
	if err != nil || after.Map.States[0].MapNumber != 2 || after.Map.AcquiredAreas != 20 || len(after.Map.States) != 2 {
		t.Fatalf("map transition: %+v %v", after, err)
	}
	for _, node := range after.Map.States[0].Nodes {
		if node.EarnedPoints != 0 {
			t.Fatal("overflow progressed the next map")
		}
	}
	var credited uint64
	var awards, receipts int
	if err = db.Get(&credited, `SELECT SUM(credited_points) FROM diva_map_contributions WHERE event_id=$1`, event.ID); err != nil || credited != 80000 {
		t.Fatalf("credited=%d %v", credited, err)
	}
	if err = db.Get(&awards, `SELECT COUNT(*) FROM diva_map_area_awards WHERE event_id=$1`, event.ID); err != nil || awards != 20 {
		t.Fatalf("awards=%d %v", awards, err)
	}
	if err = db.Get(&receipts, `SELECT COUNT(*) FROM diva_map_contributions WHERE event_id=$1`, event.ID); err != nil || receipts != 1 {
		t.Fatalf("receipts=%d %v", receipts, err)
	}
	personal, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || personal.Points != 100000 {
		t.Fatalf("personal points changed: %+v %v", personal, err)
	}
}

func TestRepoDivaMapBoundaryBindingAndLateOldMapReport(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	oldDeparture := start.Add(30 * time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 80000, start.Add(time.Minute), start.Add(2*time.Minute))
	if _, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The binding itself arrives after the transition, but must use the map
	// snapshot at actual departure, not whichever map is current now.
	bound, err := r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyTwo, oldDeparture, start.Add(time.Hour))
	if err != nil || !bound.Enabled || bound.MapNumber != 1 {
		t.Fatalf("late binding: %+v %v", bound, err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, quest, 9000, guild, divaRunKeyTwo, oldDeparture, start.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	view, err := r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour))
	if err != nil || view.Map.AcquiredAreas != 20 || view.Map.States[0].MapNumber != 2 {
		t.Fatalf("old map leaked: %+v %v", view, err)
	}
	var credited uint64
	if err = db.Get(&credited, `SELECT credited_points FROM diva_map_contributions WHERE run_key=$1`, divaRunKeyTwo); err != nil || credited != 0 {
		t.Fatalf("late credited=%d %v", credited, err)
	}
	personal, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || personal.Points != 89000 {
		t.Fatalf("late personal points lost: %+v %v", personal, err)
	}
}

func TestRepoDivaMapCutoverAndInvalidDeparture(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	if _, err := db.Exec(`UPDATE diva_map_cutover SET installed_at=$1`, start); err != nil {
		t.Fatal(err)
	}
	view, err := r.GetDivaMap(char, guild, event.ID, start)
	if err != nil || view.Enabled {
		t.Fatalf("existing actual interception was enabled: %+v %v", view, err)
	}
	if _, err = db.Exec(`UPDATE diva_map_cutover SET installed_at=$1`, start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild+1, event.ID, quest, divaRunKeyOne, start, start); !errors.Is(err, ErrDivaMapMembership) {
		t.Fatalf("wrong guild: %v", err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, start, start); err != nil {
		t.Fatal(err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest+1, divaRunKeyOne, start, start); !errors.Is(err, ErrDivaMapDeparture) {
		t.Fatalf("changed binding accepted: %v", err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyOne, start.Add(time.Microsecond), start.Add(time.Second)); !errors.Is(err, ErrDivaMapDeparture) {
		t.Fatalf("changed departure accepted: %v", err)
	}
	// An excluded quest is permanently recorded as excluded, never silently
	// turned into a main route by replaying the same run with another quest ID.
	bound, err := r.BindDivaMapDeparture(char, guild, event.ID, 1, divaRunKeyTwo, start, start)
	if err != nil || bound.Enabled {
		t.Fatalf("excluded quest: %+v %v", bound, err)
	}
	if _, err = r.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyTwo, start, start); !errors.Is(err, ErrDivaMapDeparture) {
		t.Fatalf("excluded quest rebound: %v", err)
	}
}

func TestRepoDivaMapPublicationAndFinalSettlement(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	// Forced battle starts at local midnight. At 03:59 the last first-four-hour
	// report has not yet settled; publication at 04:00 must include its awards.
	when := start.Add(3*time.Hour + 50*time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 2500, when, when.Add(time.Minute))
	before, err := r.GetDivaMapRanking(char, event.ID, start.Add(4*time.Hour-time.Nanosecond))
	if err != nil || len(before.Rows) != 0 {
		t.Fatalf("publication early: %+v %v", before, err)
	}
	at, err := r.GetDivaMapRanking(char, event.ID, start.Add(4*time.Hour))
	if err != nil || len(at.Rows) != 1 || at.Rows[0].Areas != 1 || at.Own == nil || at.Own.Rank != 1 {
		t.Fatalf("publication: %+v %v", at, err)
	}
	// Changing current guild name does not mutate the fixed published name.
	if _, err = db.Exec(`UPDATE guilds SET name='Renamed' WHERE id=$1`, guild); err != nil {
		t.Fatal(err)
	}
	again, err := r.GetDivaMapRanking(char, event.ID, start.Add(5*time.Hour))
	if err != nil || again.Rows[0].Name != at.Rows[0].Name {
		t.Fatalf("publication name changed: %+v %v", again, err)
	}
	_, end := divaInterceptionWindow(event)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyTwo, 2500, end.Add(-2*time.Minute), end.Add(-time.Minute))
	final, err := r.GetDivaMapRanking(char, event.ID, end)
	if err != nil || len(final.Rows) != 1 || final.Rows[0].Areas != 2 {
		t.Fatalf("final omitted partial-hour points: %+v %v", final, err)
	}
	var finals int
	if err = db.Get(&finals, `SELECT COUNT(*) FROM diva_map_publications WHERE event_id=$1 AND is_final`, event.ID); err != nil || finals != 1 {
		t.Fatalf("final count=%d %v", finals, err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyTwo, end.Add(-2*time.Minute), end.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	last, err := r.GetDivaMapRanking(char, event.ID, end.Add(time.Hour))
	if err != nil || last.Rows[0].Areas != 2 {
		t.Fatalf("final replay changed ranking: %+v %v", last, err)
	}
}

func TestRepoDivaMapRankTiesAndOwnOutside100(t *testing.T) {
	r, db, char, guild, event, start, _ := setupDivaMapRepoTest(t)
	if _, err := r.GetDivaMap(char, guild, event.ID, start); err != nil {
		t.Fatal(err)
	}
	// Synthetic publication fixture, not a retail map: a rank tie at first and
	// more than 100 guilds tests SQL ranking and the independent own block.
	_, err := db.Exec(`INSERT INTO diva_map_guilds(event_id,guild_id,guild_name,current_number,acquired_areas,last_settled_at,map_data)
		SELECT event_id,10000+i,'TestGuild'||i,1,0,last_settled_at,map_data FROM diva_map_guilds CROSS JOIN generate_series(1,101) i
		WHERE event_id=$1 AND guild_id=$2`, event.ID, guild)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_map_area_awards(event_id,guild_id,map_number,coordinate,is_branch,awarded_at)
		SELECT $1,10000+i,1,a,FALSE,$2 FROM generate_series(1,101) i
		CROSS JOIN LATERAL generate_series(1,CASE WHEN i=2 THEN 102 ELSE 103-i END) a`, event.ID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_map_area_awards(event_id,guild_id,map_number,coordinate,is_branch,awarded_at)
		VALUES($1,$2,1,101,FALSE,$3)`, event.ID, guild, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ranks, err := r.GetDivaMapRanking(char, event.ID, start.Add(4*time.Hour))
	if err != nil || len(ranks.Rows) != 100 || ranks.Own == nil || ranks.Own.Rank != 102 {
		t.Fatalf("own beyond100: %+v %v", ranks, err)
	}
	if ranks.Rows[0].Rank != 1 || ranks.Rows[1].Rank != 1 || ranks.Rows[2].Rank != 3 {
		t.Fatal("ties not 1,1,3")
	}
	if _, err = divaAreaRankingPayload(ranks.Rows, ranks.Own); err != nil {
		t.Fatal(err)
	}
}

func TestDivaMapBoundaryAndCreditCalculation(t *testing.T) {
	start := time.Date(2026, 9, 23, 16, 5, 0, 0, divaLocation)
	end := start.Add(61 * time.Minute)
	if got := nextDivaMapBoundary(start, end); got.Hour() != 17 || got.Minute() != 0 {
		t.Fatal(got)
	}
	if got := nextDivaMapBoundary(start.Add(55*time.Minute), end); !got.Equal(end) {
		t.Fatal(got)
	}
	before := syntheticDivaInterceptionMap()
	progress, err := advanceDivaMainMap(before, 300)
	if err != nil {
		t.Fatal(err)
	}
	used, err := divaMapCreditedByRoute(before, progress.Map, map[uint16]uint64{0: 300})
	if err != nil || used[0] != 260 {
		t.Fatalf("credited=%v err=%v", used, err)
	}
	if _, err = divaMapCreditedByRoute(before, progress.Map, map[uint16]uint64{0: 259}); err == nil {
		t.Fatal("over-credit accepted")
	}
}

func TestRepoDivaMapBranchIsolationAndWholePartyTreasure(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	locked, err := r.BindDivaMapDeparture(char, guild, event.ID, 58079, divaRunKeyTwo, start.Add(time.Minute), start.Add(time.Minute))
	if err != nil || locked.Enabled {
		t.Fatalf("locked branch bound: %+v %v", locked, err)
	}
	if err = r.AddDivaInterceptionPoints(char, event.ID, 58079, 5000, guild, divaRunKeyTwo, start.Add(time.Minute), start.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 12500, start.Add(3*time.Minute), start.Add(4*time.Minute))
	view, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour))
	if err != nil || view.Map.AcquiredAreas != 5 {
		t.Fatalf("locked branch progressed main: %+v %v", view, err)
	}
	// Retrying the excluded departure after the branch unlock cannot grant it.
	locked, err = r.BindDivaMapDeparture(char, guild, event.ID, 58079, divaRunKeyTwo, start.Add(time.Minute), start.Add(time.Hour))
	if err != nil || locked.Enabled {
		t.Fatalf("locked binding changed: %+v %v", locked, err)
	}
	chars := []uint32{char}
	user := CreateTestUser(t, db, "map_branch_party")
	for i := 1; i < 4; i++ {
		member := CreateTestCharacter(t, db, user, fmt.Sprintf("Party%d", i))
		if _, err = db.Exec(`INSERT INTO guild_characters(guild_id,character_id) VALUES($1,$2)`, guild, member); err != nil {
			t.Fatal(err)
		}
		chars = append(chars, member)
	}
	const branchKey = "12345678-1234-4567-89ab-123456789abe"
	for _, member := range chars {
		divaMapTestReport(t, r, member, guild, event, 58079, branchKey, 5000, start.Add(61*time.Minute), start.Add(70*time.Minute))
	}
	view, err = r.GetDivaMap(char, guild, event.ID, start.Add(2*time.Hour))
	if err != nil || view.Map.AcquiredAreas != 6 || view.Map.States[0].MapNumber != 1 {
		t.Fatalf("branch area: %+v %v", view, err)
	}
	var participants int
	var used uint64
	if err = db.QueryRow(`SELECT COUNT(*) FILTER(WHERE treasure_participation),SUM(credited_points)
		FROM diva_map_contributions WHERE event_id=$1 AND route=407`, event.ID).Scan(&participants, &used); err != nil || participants != 4 || used != 5000 {
		t.Fatalf("party credit: participants=%d used=%d %v", participants, used, err)
	}
	var mainEarned uint64
	for _, node := range view.Map.States[0].Nodes {
		if node.BranchQuests[0] == 0 {
			mainEarned += uint64(node.EarnedPoints)
		}
	}
	if mainEarned != 12500 {
		t.Fatalf("branch leaked %d points into main", mainEarned-12500)
	}
	var branchAwards int
	if err = db.Get(&branchAwards, `SELECT COUNT(*) FROM diva_map_area_awards WHERE event_id=$1 AND is_branch`, event.ID); err != nil || branchAwards != 1 {
		t.Fatalf("branch awards=%d %v", branchAwards, err)
	}
	personal, err := r.GetDivaInterceptionProgress(char, event.ID)
	if err != nil || personal.Points != 22500 {
		t.Fatalf("locked/branch personal points lost: %+v %v", personal, err)
	}
}

func TestRepoDivaMapRestartCatchupAndNoBackfill(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 2500, start.Add(time.Minute), start.Add(2*time.Minute))
	// A fresh repository holds no in-memory progress and must recover the
	// correct 01:00 award and 04:00 publication while catching up at 08:00.
	restarted := NewDivaRepository(db)
	ranking, err := restarted.GetDivaMapRanking(char, event.ID, start.Add(8*time.Hour))
	if err != nil || len(ranking.Rows) != 1 || ranking.Rows[0].Areas != 1 {
		t.Fatalf("catchup: %+v %v", ranking, err)
	}
	var awarded time.Time
	if err = db.Get(&awarded, `SELECT awarded_at FROM diva_map_area_awards WHERE event_id=$1`, event.ID); err != nil || !awarded.Equal(start.Add(time.Hour)) {
		t.Fatalf("catchup changed time: %v %v", awarded, err)
	}
	// A delayed call with an old timestamp cannot backfill the frozen 04:00
	// publication. It belongs to the next unclosed settlement bucket.
	lateStart := start.Add(30 * time.Minute)
	if _, err = restarted.BindDivaMapDeparture(char, guild, event.ID, quest, divaRunKeyTwo, lateStart, start.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = restarted.AddDivaInterceptionPoints(char, event.ID, quest, 2500, guild, divaRunKeyTwo, lateStart, start.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.GetDivaMap(char, guild, event.ID, start.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var past int
	if err = db.Get(&past, `SELECT areas FROM diva_map_ranks WHERE event_id=$1 AND published_at=$2 AND guild_id=$3`, event.ID, start.Add(4*time.Hour), guild); err != nil || past != 1 {
		t.Fatalf("old publication was backfilled: %d %v", past, err)
	}
	ranking, err = restarted.GetDivaMapRanking(char, event.ID, start.Add(12*time.Hour))
	if err != nil || ranking.Rows[0].Areas != 2 {
		t.Fatalf("later publication missing delayed credit: %+v %v", ranking, err)
	}
}

func TestRepoDivaMapBackgroundAndEndedCreationGuard(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 2500, start.Add(time.Minute), start.Add(2*time.Minute))
	if err := r.SettleDivaMaps(start.Add(time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	var areas uint32
	if err := db.Get(&areas, `SELECT acquired_areas FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil || areas != 1 {
		t.Fatalf("background settlement: %d %v", areas, err)
	}
	user := CreateTestUser(t, db, "map_late_guild")
	lateChar := CreateTestCharacter(t, db, user, "LateGuild")
	lateGuild := CreateTestGuild(t, db, lateChar, "LateGuild")
	_, end := divaInterceptionWindow(event)
	missing, err := r.GetDivaMap(lateChar, lateGuild, event.ID, end)
	if err != nil || missing.Enabled {
		t.Fatalf("created map after end: %+v %v", missing, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, event.ID, lateGuild); err != nil || count != 0 {
		t.Fatalf("historical map manufactured: %d %v", count, err)
	}
	existing, err := r.GetDivaMap(char, guild, event.ID, end)
	if err != nil || !existing.Enabled || existing.Map.AcquiredAreas != 1 {
		t.Fatalf("existing map unavailable after end: %+v %v", existing, err)
	}
	// Persisted arbitrary wire-valid maps cannot replace the approved catalog.
	if _, err = db.Exec(`UPDATE diva_map_guilds SET map_data=jsonb_set(map_data,'{Definitions,0,Nodes,1,BaseRequiredPoints}','1')
		WHERE event_id=$1 AND guild_id=$2`, event.ID, guild); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetDivaMap(char, guild, event.ID, end); err == nil {
		t.Fatal("changed custom catalog accepted")
	}
}
