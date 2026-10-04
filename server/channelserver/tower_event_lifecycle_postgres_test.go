package channelserver

import (
	"errors"
	cfg "erupe-ce/config"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTowerRotationPostgresRolloverPreservesGrowthAndHistory(t *testing.T) {
	db := towerD463DB(t)
	r := NewTowerRepository(db)
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, divaLocation)
	o := cfg.TowerRotationOptions{Enabled: true, StartAt: start.Format(time.RFC3339), ActiveDays: 7, CycleDays: 21}
	if _, err := db.Exec(`UPDATE tower SET tr=60,trp=35400,tsp=17,skills='1,2,3',block1=500,block2=40,guardian1=500,gems=$1 WHERE char_id=100`, "2,"+strings.TrimPrefix(EmptyTowerCSV(30), "0,")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guilds SET tower_mission_page=3,tower_rp=450 WHERE id=1;UPDATE guild_characters SET tower_mission_1=7 WHERE character_id=100`); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveTowerDailyBin(100, make([]byte, towerDailyBinSize)); err != nil {
		t.Fatal(err)
	}
	e, err := r.EnsureTowerEvent(start, o, 36)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != 36 {
		t.Fatalf("seed round %d", e.ID)
	}
	if _, err = r.EnsureTowerEvent(e.End, o, 36); err != nil {
		t.Fatal(err)
	}
	td, err := r.GetTowerData(100)
	if err != nil || td.Block1 != 500 {
		t.Fatalf("closing reset progress: %+v %v", td, err)
	}
	if _, err = r.RecordTowerRewardClaim(36, 100, towerRewardFloor, 1, 0x2B96, 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := r.EnsureTowerEvent(e.ClaimUntil, o, 36)
			if err == nil && next.ID != 37 {
				err = errors.New("wrong next round")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	td, err = r.GetTowerData(100)
	if err != nil || td.TR != 60 || td.TRP != 35400 || td.TSP != 17 || td.Skills != "1,2,3" || td.Block1 != 0 || td.Block2 != 0 || td.Guardian1 != 0 {
		t.Fatalf("reset damaged data: %+v %v", td, err)
	}
	var floor, page, rp, mission, bins, claims, events int
	if err = db.QueryRow(`SELECT (SELECT block1 FROM tower_round_character_state WHERE earth_id=36 AND character_id=100),
		(SELECT tower_mission_page FROM guilds WHERE id=1),(SELECT tower_rp FROM guilds WHERE id=1),
		(SELECT tower_mission_1 FROM guild_characters WHERE character_id=100),
		(SELECT COUNT(*) FROM tower_daily_bins),(SELECT COUNT(*) FROM tower_reward_claims WHERE earth_id=36),(SELECT COUNT(*) FROM tower_events)`).Scan(&floor, &page, &rp, &mission, &bins, &claims, &events); err != nil {
		t.Fatal(err)
	}
	if floor != 500 || page != 1 || rp != 0 || mission != 0 || bins != 0 || claims != 1 || events != 2 {
		t.Fatalf("archive/reset %d/%d/%d/%d/%d/%d/%d", floor, page, rp, mission, bins, claims, events)
	}
	old := r.ForTowerRound(36)
	if err = old.UpdateBlockFloors(100, 1, 700); !errors.Is(err, errTowerRoundChanged) {
		t.Fatalf("stale floor accepted %v", err)
	}
	// Late result still credits the OLD round and permanent points only.
	if gain, err := old.SettleTowerRun(100, strings.Repeat("d", 32), 36, start, TowerSettlement{Block: 1, TR: 60, TRP: 100, TSP: 1, Floors: 2}); err != nil || gain != 2 {
		t.Fatalf("old settlement %d %v", gain, err)
	}
	td, err = r.GetTowerData(100)
	if err != nil || td.Block1 != 0 || td.TRP != 35500 || td.TSP != 18 {
		t.Fatalf("late result polluted new round %+v %v", td, err)
	}
	if err = old.DepositTowerGem(100, strings.Repeat("d", 32), 0, 1, 1); !errors.Is(err, errTowerRoundChanged) {
		t.Fatalf("stale gem accepted %v", err)
	}
	if err = db.Get(&floor, `SELECT block1 FROM tower_round_character_state WHERE earth_id=36 AND character_id=100`); err != nil || floor != 502 {
		t.Fatalf("late archived floor %d %v", floor, err)
	}
	if _, err = db.Exec(`UPDATE tower SET guardian1=9999 WHERE char_id=100`); err != nil {
		t.Fatalf("9999 milestone constraint %v", err)
	}
	// Several offline cycles advance once to the actual current round.
	next, err := r.EnsureTowerEvent(start.Add(65*24*time.Hour), o, 36)
	if err != nil || next.ID != 39 {
		t.Fatalf("offline catchup %+v %v", next, err)
	}
	if _, err = r.EnsureTowerEvent(start.Add(66*24*time.Hour), cfg.TowerRotationOptions{Enabled: true, StartAt: o.StartAt, ActiveDays: 7, CycleDays: 28}, 36); err == nil {
		t.Fatal("silently changed persisted schedule")
	}
}
func TestTowerRotationPostgresAdoptsZeroAndResetIsAtomic(t *testing.T) {
	db := towerD463DB(t)
	r := NewTowerRepository(db)
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, divaLocation)
	o := cfg.TowerRotationOptions{Enabled: true, StartAt: start.Format(time.RFC3339), ActiveDays: 7, CycleDays: 21}
	if _, err := db.Exec(`UPDATE tower SET block1=10 WHERE char_id=100;INSERT INTO tower_event_progress VALUES(0,100,10,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordTowerRewardClaim(0, 100, towerRewardFloor, 1, 0x2B96, 1); err != nil {
		t.Fatal(err)
	}
	e, err := r.EnsureTowerEvent(start, o, 0)
	if err != nil || e.ID != 36 {
		t.Fatalf("adopt zero %+v %v", e, err)
	}
	state, err := r.GetTowerRewardState(36, 100, start)
	if err != nil || state.Floors != 10 || !state.Claimed[towerRewardKey(towerRewardFloor, 1)] {
		t.Fatalf("lost legacy receipts %+v %v", state, err)
	}
	if _, err = db.Exec(`ALTER TABLE tower_round_snapshots ADD CONSTRAINT injected_rollover_failure CHECK(kind<>'guild')`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.EnsureTowerEvent(e.ClaimUntil, o, 0); err == nil {
		t.Fatal("injected rollover failure succeeded")
	}
	var id, count, floor int
	if err = db.QueryRow(`SELECT (SELECT earth_id FROM tower_event_lifecycle),(SELECT COUNT(*) FROM tower_round_character_state),(SELECT block1 FROM tower WHERE char_id=100)`).Scan(&id, &count, &floor); err != nil {
		t.Fatal(err)
	}
	if id != 36 || count != 0 || floor != 10 {
		t.Fatalf("partial reset %d/%d/%d", id, count, floor)
	}
}

func TestTowerRotationPostgresDailyTRPAndOneConnection(t *testing.T) {
	db := towerD463DB(t)
	db.SetMaxOpenConns(1)
	r := NewTowerRepository(db)
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, divaLocation)
	o := cfg.TowerRotationOptions{Enabled: true, StartAt: start.Format(time.RFC3339), ActiveDays: 7, CycleDays: 21}
	if _, err := r.EnsureTowerEvent(start, o, 36); err != nil {
		t.Fatal(err)
	}
	bound := r.ForTowerRound(36)
	if _, err := bound.SettleTowerRun(100, strings.Repeat("a", 32), 36, start, TowerSettlement{Block: 3, TR: 2, TRP: 500}); err != nil {
		t.Fatal(err)
	}
	state, err := r.GetTowerRewardState(36, 100, start)
	if err != nil || len(state.Daily) != 1 || state.Daily[0].Counters.TRP != 500 || state.Floors != 0 {
		t.Fatalf("practice TRP missing %+v %v", state, err)
	}
	if err := r.RecordTowerDailyExtrasWithTRP(36, 100, start, TowerMissionStats{TRP: 200, Chests: 1}, true); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordTowerDailyExtrasWithTRP(36, 100, start, TowerMissionStats{TRP: 200, Chests: 1}, false); err != nil {
		t.Fatal(err)
	}
	state, err = r.GetTowerRewardState(36, 100, start)
	if err != nil || state.Daily[0].Counters.TRP != 700 || state.Daily[0].Counters.Chests != 2 {
		t.Fatalf("daily double counted %+v %v", state, err)
	}
	if err := bound.SaveTowerDailyBin(100, make([]byte, towerDailyBinSize)); err != nil {
		t.Fatal(err)
	}
	// Copy selected current round 0 without accidentally adopting older ID 50.
	db2 := towerD463DB(t)
	r2 := NewTowerRepository(db2)
	if _, err := db2.Exec(`INSERT INTO tower_reward_claims VALUES(50,100,1,1,11158,1,now());INSERT INTO tower_event_progress VALUES(0,100,3,0)`); err != nil {
		t.Fatal(err)
	}
	e, err := r2.EnsureTowerEvent(start, o, 0)
	if err != nil || e.ID != 51 {
		t.Fatalf("reused prior round ID %+v %v", e, err)
	}
	state, err = r2.GetTowerRewardState(51, 100, start)
	if err != nil || state.Floors != 3 || state.Claimed[towerRewardKey(towerRewardFloor, 1)] {
		t.Fatalf("old unrelated receipt adopted %+v %v", state, err)
	}
}
