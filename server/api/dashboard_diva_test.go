package api

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"github.com/jmoiron/sqlx"
)

func dashboardDivaFixture(now time.Time) []dashboardDivaBeadRow {
	start := now.Add(-8 * 24 * time.Hour)
	var rows []dashboardDivaBeadRow
	for _, id := range []int{17, 23, 18, 3} {
		rows = append(rows, dashboardDivaBeadRow{CharID: 1, EventID: 5,
			StartTime: uint32(start.Unix()), Continuous: true, ActivatedAt: now.Add(-30 * time.Minute), EffectID: id})
	}
	return rows
}

func TestDashboardDivaActiveBeadsAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	rows := dashboardDivaFixture(now)
	got := dashboardDivaBeadsByCharacter(rows, now)[1]
	if got == nil || got.Round != 5 || !reflect.DeepEqual(got.Beads, []string{"결집", "봉뢰", "가호", "단력"}) ||
		!got.ExpiresAt.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("activation not preserved: %+v", got)
	}
	for _, elapsed := range []time.Duration{-31 * time.Minute, 30 * time.Minute, 24 * time.Hour} {
		if len(dashboardDivaBeadsByCharacter(rows, now.Add(elapsed))) != 0 {
			t.Fatalf("inactive activation exposed at %s", elapsed)
		}
	}
	end := time.Unix(int64(rows[0].StartTime), 0).Add(14 * 24 * time.Hour)
	for i := range rows {
		rows[i].ActivatedAt = end.Add(-15 * time.Minute)
	}
	got = dashboardDivaBeadsByCharacter(rows, end.Add(-time.Minute))[1]
	if got == nil || !got.ExpiresAt.Equal(end) {
		t.Fatal("tooltip must expire at the battle boundary")
	}
	if len(dashboardDivaBeadsByCharacter(rows, end)) != 0 {
		t.Fatal("battle-end activation remained visible")
	}
	for i := range rows {
		rows[i].ActivatedAt = end.Add(-7 * 24 * time.Hour).Add(-time.Minute)
	}
	if len(dashboardDivaBeadsByCharacter(rows, end.Add(-7*24*time.Hour))) != 0 {
		t.Fatal("activation from the prayer phase was exposed")
	}
}

func TestDashboardDivaModesAndInvalidEffects(t *testing.T) {
	options := &cfg.Config{RealClientMode: cfg.ZZ}
	for _, mode := range []int{-2, -1, 0, 1, 2, 3, 4} {
		options.DebugOptions.DivaOverride = mode
		if got := dashboardDivaBeadsEnabled(options); got != (mode == -1 || mode == 2) {
			t.Fatalf("mode %d enabled=%t", mode, got)
		}
	}
	options.DebugOptions.DivaOverride = -1
	hour := 1
	options.DebugOptions.InGameTimeOverrideHour = &hour
	if dashboardDivaBeadsEnabled(options) || dashboardDivaBeadsEnabled(nil) {
		t.Fatal("unsupported clock or configuration was enabled")
	}
	now := time.Now()
	for _, invalid := range []int{0, 2, 16, 26, 999, 17} {
		rows := dashboardDivaFixture(now)
		rows[1].EffectID = invalid
		if len(dashboardDivaBeadsByCharacter(rows, now)) != 0 {
			t.Fatalf("unknown/duplicate effect %d produced partial tooltip", invalid)
		}
	}
	data, err := json.Marshal(OnlineCharacter{Name: "헌터"})
	if err != nil || string(data) == "" {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	_ = json.Unmarshal(data, &fields)
	if _, exists := fields["divaBeads"]; exists {
		t.Fatal("inactive character exposed a Diva tooltip")
	}
}

func TestDashboardDivaAlignedLevels(t *testing.T) {
	now := time.Now()
	rows := dashboardDivaFixture(now)
	for i, level := range []int{1, 2, 0, 1} {
		rows[i].Level = &level
	}
	got := dashboardDivaBeadsByCharacter(rows, now)[1]
	if got == nil || !reflect.DeepEqual(got.Levels, []int{1, 2, 0, 1}) {
		t.Fatalf("bead levels lost their activation order: %+v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Levels []int `json:"levels"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded.Levels, got.Levels) {
		t.Fatalf("levels must be a JSON number array: %s (%v)", data, err)
	}
	rows[1].Level = nil
	got = dashboardDivaBeadsByCharacter(rows, now)[1]
	if got == nil || got.Levels != nil || len(got.Beads) != 4 {
		t.Fatalf("partial level evidence fabricated aligned levels: %+v", got)
	}
}

// Fixtures are session-local TEMP tables. No persistent schema or rows change.
func TestDashboardDivaBeadsPostgres(t *testing.T) {
	dsn := os.Getenv("RANKING_TEMP_TEST_DSN")
	if dsn == "" {
		t.Skip("RANKING_TEMP_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`SET search_path TO pg_temp;
		CREATE TEMP TABLE events(id int,event_type text,start_time timestamp,diva_continuous bool);
		CREATE TEMP TABLE diva_event_lifecycle(mode int,event_id int);
		CREATE TEMP TABLE diva_battle_songs(char_id int,event_id int,activation_id int,activated_at timestamptz);
		CREATE TEMP TABLE diva_battle_song_effects(char_id int,event_id int,effect_id smallint);
		CREATE TEMP TABLE diva_bead_rounds(event_id int,effects int[]);
		CREATE TEMP TABLE diva_song_records(event_id int,day_start timestamptz,bead_index int,quest_points bigint);`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	_, err = db.Exec(`INSERT INTO events VALUES(5,'diva',$1,true),(4,'diva',$1,true),(9,'diva',$1,true)`, now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO diva_event_lifecycle VALUES(-1,5),(2,9);
		INSERT INTO diva_bead_rounds VALUES(5,ARRAY[17,23,18,3]),(9,ARRAY[19]);
		INSERT INTO diva_battle_song_effects VALUES(1,5,3),(1,5,17),(1,5,18),(1,5,23),
		(2,5,1),(3,5,1),(4,5,1),(5,4,1),(6,9,19),(7,5,25);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO diva_battle_songs VALUES
		(1,5,10,$1),(2,5,11,$2),(3,5,12,$3),(4,5,0,$1),(5,4,13,$1),(6,9,14,$1),(7,5,15,$1)`,
		now.Add(-time.Minute), now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Noon in UTC+9, matching the client packet's completed winning colors.
	firstDay := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	for day, winner := range []int{1, 2, 2, 3, 4, 4, 4, 0} {
		if winner == 0 {
			continue
		}
		_, err = db.Exec(`INSERT INTO diva_song_records VALUES(5,$1,$2,100),(9,$1,1,100)`, firstDay.Add(time.Duration(day)*24*time.Hour), winner)
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &APIServer{db: db, erupeConfig: &cfg.Config{RealClientMode: cfg.ZZ}}
	s.erupeConfig.DebugOptions.DivaOverride = -1
	online := []OnlineCharacter{{CharID: 1}, {CharID: 2}, {CharID: 3}, {CharID: 4}, {CharID: 5}, {CharID: 6}}
	if err = s.enrichDashboardDivaBeads(context.Background(), online, now); err != nil {
		t.Fatal(err)
	}
	if online[0].DivaBeads == nil || !reflect.DeepEqual(online[0].DivaBeads.Beads, []string{"결집", "봉뢰", "가호", "단력"}) {
		t.Fatalf("activation order/identity mismatch: %+v", online[0].DivaBeads)
	}
	if !reflect.DeepEqual(online[0].DivaBeads.Levels, []int{1, 2, 1, 1}) {
		t.Fatalf("winning slot counts/caps mismatch: %+v", online[0].DivaBeads)
	}
	for _, character := range online[1:] {
		if character.DivaBeads != nil {
			t.Fatalf("inactive, old-round or forced-mode activation exposed: %+v", character)
		}
	}
	s.erupeConfig.DebugOptions.DivaOverride = 2
	online = []OnlineCharacter{{CharID: 6}}
	if err = s.enrichDashboardDivaBeads(context.Background(), online, now); err != nil || online[0].DivaBeads == nil || online[0].DivaBeads.Round != 9 {
		t.Fatalf("forced battle did not use its own event: %+v %v", online, err)
	}
	if !reflect.DeepEqual(online[0].DivaBeads.Levels, []int{3}) {
		t.Fatalf("forced event level did not use its own colors and cap: %+v", online[0].DivaBeads)
	}
	if _, err = db.Exec(`DELETE FROM diva_bead_rounds WHERE event_id=9`); err != nil {
		t.Fatal(err)
	}
	if err = s.enrichDashboardDivaBeads(context.Background(), online, now); err != nil || online[0].DivaBeads == nil || online[0].DivaBeads.Levels != nil {
		t.Fatalf("legacy activation without snapshot should keep names only: %+v (%v)", online, err)
	}
}
