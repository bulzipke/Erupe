package channelserver

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

var _ DivaTacticsLogRepository = (*DivaRepository)(nil)

func divaLogKinds(rows []divaTacticsLogEntry) map[uint8]int {
	result := make(map[uint8]int)
	for _, row := range rows {
		result[row.Kind]++
	}
	return result
}

func TestRepoDivaTacticsLogActualJournalAndSettlement(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	depart, reported := start.Add(time.Minute), start.Add(2*time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 12500, depart, reported)
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 12500, guild, divaRunKeyOne, depart, reported.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	before, err := r.GetDivaTacticsLog(char, start.Add(59*time.Minute))
	if err != nil || divaLogKinds(before)[0] != 1 || divaLogKinds(before)[1] != 0 {
		t.Fatalf("premature or duplicate events: %+v %v", before, err)
	}
	after, err := r.GetDivaTacticsLog(char, start.Add(time.Hour))
	if err != nil || divaLogKinds(after)[0] != 1 || divaLogKinds(after)[1] != 1 || divaLogKinds(after)[4] != 0 {
		t.Fatalf("actual settlement log: %+v %v", after, err)
	}
	for _, row := range after {
		if row.Kind == 1 && row.Value != 5 {
			t.Fatalf("area count is not cumulative: %+v", row)
		}
		if row.Kind == 0 && (row.Value != 12500 || row.CharID != char || !row.At.Equal(reported)) {
			t.Fatalf("points re-multiplied or re-timestamped: %+v", row)
		}
	}
	again, err := r.GetDivaTacticsLog(char, start.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(again, after) {
		t.Fatalf("query replay changed log: %+v %v", again, err)
	}
	if _, err = db.Exec(`UPDATE characters SET name=$2 WHERE id=$1`, char, "%n한글\n"); err != nil {
		t.Fatal(err)
	}
	named, err := r.GetDivaTacticsLog(char, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range named {
		if (row.Kind == 0 || row.Kind == 2) && row.Name != "n한글" {
			t.Fatalf("native printf-safe display: %+v", row)
		}
	}
}

func divaLogMilestones(rows []divaTacticsLogEntry) []divaTacticsLogEntry {
	var result []divaTacticsLogEntry
	for _, row := range rows {
		if row.Kind == 2 {
			result = append(result, row)
		}
	}
	return result
}

func TestRepoDivaTacticsLogPersonalMilestoneBoundariesAndReplay(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 9999, start.Add(time.Minute), start.Add(2*time.Minute))
	before, err := r.GetDivaTacticsLog(char, start.Add(3*time.Minute))
	if err != nil || len(divaLogMilestones(before)) != 0 {
		t.Fatalf("milestone before threshold: %+v %v", before, err)
	}
	depart, reported := start.Add(4*time.Minute), start.Add(5*time.Minute)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyTwo, 20001, depart, reported)
	if err := r.AddDivaInterceptionPoints(char, event.ID, quest, 20001, guild, divaRunKeyTwo, depart, reported.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A stored later report must not backfill a journal requested before it.
	before, err = r.GetDivaTacticsLog(char, reported.Add(-time.Microsecond))
	if err != nil || len(divaLogMilestones(before)) != 0 {
		t.Fatalf("future milestone leaked: %+v %v", before, err)
	}
	after, err := r.GetDivaTacticsLog(char, reported)
	if err != nil {
		t.Fatal(err)
	}
	milestones := divaLogMilestones(after)
	if len(milestones) != 3 {
		t.Fatalf("exact or multi-threshold crossing lost: %+v", milestones)
	}
	for i, row := range milestones {
		if row.Value != uint32(30000-i*10000) || row.CharID != char || row.Name != "MapHunter" || !row.At.Equal(reported) || row.Branch != 0 {
			t.Fatalf("milestone differs from accepted journal: %+v", row)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, queryErr := r.GetDivaTacticsLog(char, reported)
			if queryErr == nil && !reflect.DeepEqual(rows, after) {
				queryErr = errors.New("concurrent replay changed milestone history")
			}
			errs <- queryErr
		}()
	}
	wg.Wait()
	close(errs)
	for queryErr := range errs {
		if queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	var points, runs int64
	if err := db.QueryRow(`SELECT SUM(points),COUNT(*) FROM diva_interception_runs WHERE event_id=$1 AND char_id=$2`, event.ID, char).Scan(&points, &runs); err != nil || points != 30000 || runs != 2 {
		t.Fatalf("presentation mutated points or duplicated a run: %d %d %v", points, runs, err)
	}
}

func TestRepoDivaTacticsLogPersonalMilestoneTransferPreservesTotal(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 15000, start.Add(time.Minute), start.Add(2*time.Minute))
	user := CreateTestUser(t, db, "milestone_other")
	otherChar := CreateTestCharacter(t, db, user, "OtherHunter")
	otherGuild := CreateTestGuild(t, db, otherChar, "OtherGuild")
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=$2,joined_at=$3 WHERE character_id=$1`, char, otherGuild, start.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	divaMapTestReport(t, r, char, otherGuild, event, quest, divaRunKeyTwo, 5000, start.Add(4*time.Minute), start.Add(5*time.Minute))
	rows, err := r.GetDivaTacticsLog(char, start.Add(6*time.Minute))
	milestones := divaLogMilestones(rows)
	if err != nil || len(milestones) != 1 || milestones[0].Value != 20000 || milestones[0].CharID != char {
		t.Fatalf("transfer reset total or exposed old guild milestone: %+v %v", milestones, err)
	}
	// Returning to the original guild restores only its own actual crossing.
	if _, err := db.Exec(`UPDATE guild_characters SET guild_id=$2,joined_at=$3 WHERE character_id=$1`, char, guild, start.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, err = r.GetDivaTacticsLog(char, start.Add(8*time.Minute))
	milestones = divaLogMilestones(rows)
	if err != nil || len(milestones) != 1 || milestones[0].Value != 10000 {
		t.Fatalf("another guild milestone leaked after returning: %+v %v", milestones, err)
	}
}

func TestRepoDivaTacticsLogPersonalMilestoneSignedAndRowCaps(t *testing.T) {
	r, _, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	// Synthetic extreme input checks bounded expansion, not a retail score.
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, math.MaxUint32, start.Add(time.Minute), start.Add(2*time.Minute))
	rows, err := r.GetDivaTacticsLog(char, start.Add(3*time.Minute))
	if err != nil || len(rows) != divaTacticsLogMaxRows || rows[0].Kind != 5 {
		t.Fatalf("bounded maximum journal: %d %v", len(rows), err)
	}
	milestones := divaLogMilestones(rows)
	if len(milestones) != divaTacticsLogMaxRows-1 || milestones[0].Value != uint32(math.MaxInt32/divaTacticsLogPersonalStep*divaTacticsLogPersonalStep) {
		t.Fatalf("native signed limit or newest milestones: %+v", milestones)
	}
	for i, row := range milestones {
		if row.Value > math.MaxInt32 || row.Value == 0 || (i > 0 && row.Value != milestones[i-1].Value-divaTacticsLogPersonalStep) {
			t.Fatalf("unbounded or incorrectly ordered milestone: %+v", row)
		}
	}
	if _, err := divaTacticsLogPayload(rows); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDivaTacticsLogBranchAndGuildIsolation(t *testing.T) {
	r, db, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	// Locked branch reports must neither expose its hidden treasure nor appear
	// as accepted branch contributions, even after the route unlocks later.
	divaMapTestReport(t, r, char, guild, event, 58079, divaRunKeyTwo, 5000, start.Add(time.Minute), start.Add(2*time.Minute))
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 12500, start.Add(3*time.Minute), start.Add(4*time.Minute))
	if _, err := r.GetDivaMap(char, guild, event.ID, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	const branchKey = "12345678-1234-4567-89ab-123456789abe"
	divaMapTestReport(t, r, char, guild, event, 58079, branchKey, 5000, start.Add(61*time.Minute), start.Add(70*time.Minute))
	before, err := r.GetDivaTacticsLog(char, start.Add(119*time.Minute))
	if err != nil || divaLogKinds(before)[6] != 1 || divaLogKinds(before)[4] != 0 {
		t.Fatalf("unearned treasure or locked branch leaked: %+v %v", before, err)
	}
	after, err := r.GetDivaTacticsLog(char, start.Add(2*time.Hour))
	if err != nil || divaLogKinds(after)[6] != 1 || divaLogKinds(after)[4] != 1 {
		t.Fatalf("branch result missing: %+v %v", after, err)
	}
	for _, row := range after {
		if row.Kind == 6 && (row.Branch != 1 || row.Value != 5000) {
			t.Fatalf("branch ordinal: %+v", row)
		}
		if row.Kind == 4 && (row.Name != "" || row.Value != 0 || row.Branch != 0) {
			t.Fatal("hidden reward details exposed")
		}
	}
	user := CreateTestUser(t, db, "log_other_guild")
	otherChar := CreateTestCharacter(t, db, user, "OtherHunter")
	otherGuild := CreateTestGuild(t, db, otherChar, "OtherGuild")
	if rows, err := r.GetDivaTacticsLog(otherChar, start.Add(2*time.Hour)); err != nil || len(rows) != 0 {
		t.Fatalf("foreign history leaked: %+v %v", rows, err)
	}
	if _, err = db.Exec(`UPDATE guild_characters SET joined_at=NULL WHERE character_id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if rows, err := r.GetDivaTacticsLog(char, start.Add(2*time.Hour)); err != nil || len(rows) != 0 {
		t.Fatalf("applicant history leaked: %+v %v", rows, err)
	}
	if _, err = db.Exec(`UPDATE guild_characters SET guild_id=$2,joined_at=$3 WHERE character_id=$1`, char, otherGuild, start); err != nil {
		t.Fatal(err)
	}
	if rows, err := r.GetDivaTacticsLog(char, start.Add(2*time.Hour)); err != nil || len(rows) != 0 {
		t.Fatalf("prior guild retained after transfer: %+v %v", rows, err)
	}
}

func TestRepoDivaTacticsLogCappedContributionsRemainHistorical(t *testing.T) {
	r, _, char, guild, event, start, quest := setupDivaMapRepoTest(t)
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyOne, 80000, start.Add(time.Minute), start.Add(2*time.Minute))
	divaMapTestReport(t, r, char, guild, event, quest, divaRunKeyTwo, 5000, start.Add(3*time.Minute), start.Add(4*time.Minute))
	before, err := r.GetDivaTacticsLog(char, start.Add(59*time.Minute))
	if err != nil || divaLogKinds(before)[0] != 2 {
		t.Fatalf("pending journal: %+v %v", before, err)
	}
	after, err := r.GetDivaTacticsLog(char, start.Add(time.Hour))
	if err != nil || divaLogKinds(after)[0] != 2 || divaLogKinds(after)[1] != 1 {
		t.Fatalf("overflow deleted real personal-point history: %+v %v", after, err)
	}
}
