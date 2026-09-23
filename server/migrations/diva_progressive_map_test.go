package migrations

import (
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func setupDivaProgressiveMigrationTest(t *testing.T) (*sqlx.DB, string) {
	t.Helper()
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("progressive-map migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE events(id INTEGER PRIMARY KEY);
		CREATE TABLE characters(id INTEGER PRIMARY KEY);
		CREATE TABLE diva_interception_periods(event_id INTEGER PRIMARY KEY,starts_at TIMESTAMPTZ,ends_at TIMESTAMPTZ);
		INSERT INTO events SELECT generate_series(1,40);
		INSERT INTO characters VALUES(10);`); err != nil {
		t.Fatal(err)
	}
	// Use the actual preceding constraints and immutable-policy trigger, not
	// permissive stand-in map tables that could hide an upgrade incompatibility.
	for _, name := range []string{"sql/0054_diva_maps.sql", "sql/0058_diva_random_maps.sql", "sql/0061_diva_map_special_treasures.sql"} {
		migration, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatalf("prerequisite %s: %v", name, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,starts_at,ends_at,generation_seed,red_treasure_mode) VALUES
		(1,'custom-v1','2026-01-01','2026-01-08',0,'off'),
		(2,'custom-random-v2','2026-02-01','2026-02-08',91,'off'),
		(3,'custom-random-v2','2026-02-01','2026-02-08',92,'random-one'),
		(4,'custom-random-v2','2026-02-01','2026-02-08',93,'all');
		INSERT INTO diva_map_guilds
		SELECT event_id,22,'historical guild',2,21,'2026-02-02','{"AcquiredAreas":21,"States":[{"MapNumber":2}],"legacy":"unchanged"}'::jsonb
		FROM diva_map_events;
		INSERT INTO diva_map_snapshots
		SELECT event_id,22,'2026-02-01',1,'{"snapshot":"unchanged"}'::jsonb FROM diva_map_events;
		INSERT INTO diva_map_departures
		SELECT 10,event_id,'00000000-0000-4000-8000-000000000001'::uuid,22,1,58079,407,TRUE,'2026-02-01' FROM diva_map_events;
		INSERT INTO diva_map_contributions
		SELECT 10,event_id,'00000000-0000-4000-8000-000000000001'::uuid,22,1,407,9000,5000,TRUE,
		'2026-02-01 00:01'::timestamptz,'2026-02-01 00:01'::timestamptz,'2026-02-01 01:00'::timestamptz FROM diva_map_events;
		INSERT INTO diva_map_area_awards
		SELECT event_id,22,1,407,TRUE,'2026-02-01 01:00' FROM diva_map_events;
		INSERT INTO diva_map_publications
		SELECT event_id,'2026-02-01 01:00',FALSE FROM diva_map_events;
		INSERT INTO diva_map_ranks
		SELECT event_id,'2026-02-01 01:00',22,'historical guild',21,1 FROM diva_map_events;
		CREATE TABLE preserved_diva_reward_receipts(event_id INTEGER PRIMARY KEY,receipt JSONB NOT NULL);
		INSERT INTO preserved_diva_reward_receipts
		SELECT event_id,'{"id":31,"quantity":5,"claimed_at":"2026-02-02"}'::jsonb FROM diva_map_events;`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0062_diva_progressive_maps.sql")
	if err != nil {
		t.Fatal(err)
	}
	return db, string(migration)
}

func divaProgressiveMigrationLegacySnapshot(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	var snapshot string
	if err := db.Get(&snapshot, `SELECT jsonb_build_object(
		'events',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id) FROM diva_map_events s WHERE event_id<=4),
		'guilds',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,guild_id) FROM diva_map_guilds s WHERE event_id<=4),
		'snapshots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,guild_id,settled_at) FROM diva_map_snapshots s WHERE event_id<=4),
		'departures',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,char_id,run_key) FROM diva_map_departures s WHERE event_id<=4),
		'contributions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,char_id,run_key) FROM diva_map_contributions s WHERE event_id<=4),
		'awards',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,guild_id,map_number,coordinate) FROM diva_map_area_awards s WHERE event_id<=4),
		'publications',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,published_at) FROM diva_map_publications s WHERE event_id<=4),
		'ranks',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id,published_at,guild_id) FROM diva_map_ranks s WHERE event_id<=4),
		'receipts',(SELECT jsonb_agg(to_jsonb(s) ORDER BY event_id) FROM preserved_diva_reward_receipts s),
		'policy',(SELECT jsonb_agg(to_jsonb(s) ORDER BY singleton) FROM diva_map_special_policy s),
		'policy_history',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM diva_map_special_policy_history s)
	)::text`); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func requireDivaProgressiveMigrationSQLState(t *testing.T, db *sqlx.DB, state string, query string, args ...interface{}) {
	t.Helper()
	_, err := db.Exec(query, args...)
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || string(pgErr.Code) != state {
		t.Fatalf("expected SQLSTATE %s for %s, got %v", state, query, err)
	}
}

func TestDivaProgressiveMapMigrationFirstInstallAndReplayPreserveHistory(t *testing.T) {
	db, migration := setupDivaProgressiveMigrationTest(t)
	before := divaProgressiveMigrationLegacySnapshot(t, db)
	var lower, upper, installed time.Time
	if err := db.Get(&lower, `SELECT clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&upper, `SELECT clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&installed, `SELECT installed_at FROM diva_progressive_map_cutover WHERE singleton`); err != nil || installed.Before(lower) || installed.After(upper) {
		t.Fatalf("first cutover not set at installation: %v err=%v", installed, err)
	}
	if after := divaProgressiveMigrationLegacySnapshot(t, db); after != before {
		t.Fatalf("first installation rewrote v1/v2 history:\n%s\n%s", before, after)
	}
	if _, err := db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,starts_at,ends_at,generation_seed,red_treasure_mode)
		VALUES(5,'custom-progressive-v3','2026-03-01','2026-03-08',9223372036854775807,'progressive');
		INSERT INTO diva_map_guilds VALUES(5,22,'progressive guild',1,0,'2026-03-01','{"InvasionTick":1}');
		INSERT INTO diva_map_invasions VALUES(5,22,'2026-03-01 01:00',1,1,3)`); err != nil {
		t.Fatal(err)
	}
	var newBefore string
	if err := db.Get(&newBefore, `SELECT row_to_json(s)::text FROM diva_map_invasions s`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("replay with v3 data failed: %v", err)
		}
		var replay time.Time
		if err := db.Get(&replay, `SELECT installed_at FROM diva_progressive_map_cutover WHERE singleton`); err != nil || !replay.Equal(installed) {
			t.Fatalf("permanent cutover changed: %v err=%v", replay, err)
		}
		if after := divaProgressiveMigrationLegacySnapshot(t, db); after != before {
			t.Fatal("replay converted legacy seeds, policies, map JSON, progress, awards, or receipts")
		}
		var newAfter string
		if err := db.Get(&newAfter, `SELECT row_to_json(s)::text FROM diva_map_invasions s`); err != nil || newAfter != newBefore {
			t.Fatalf("replay changed invasion history: %s err=%v", newAfter, err)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM diva_progressive_map_cutover`, 1},
		{`SELECT COUNT(*) FROM diva_map_events WHERE event_id=5 AND rules_version='custom-progressive-v3' AND generation_seed=9223372036854775807 AND red_treasure_mode='progressive'`, 1},
		{`SELECT COUNT(*) FROM diva_map_guilds WHERE event_id=5 AND map_data='{"InvasionTick":1}'::jsonb`, 1},
	} {
		var count int
		if err := db.Get(&count, tc.query); err != nil || count != tc.want {
			t.Fatalf("%s: count=%d err=%v", tc.query, count, err)
		}
	}
	requireDivaProgressiveMigrationSQLState(t, db, "23514", `INSERT INTO diva_progressive_map_cutover VALUES(FALSE,NOW())`)
	requireDivaProgressiveMigrationSQLState(t, db, "23505", `INSERT INTO diva_progressive_map_cutover VALUES(TRUE,NOW())`)
}

func TestDivaProgressiveMapMigrationVersionSeedAndPolicyConstraints(t *testing.T) {
	db, migration := setupDivaProgressiveMigrationTest(t)
	if _, err := db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO diva_map_events(event_id,rules_version,starts_at,ends_at,generation_seed,red_treasure_mode)
		VALUES($1,$2,'2026-03-01','2026-03-08',$3,$4)`
	for _, tc := range []struct {
		id      int
		version string
		seed    int64
		policy  string
	}{
		{5, "custom-progressive-v3", 1, "progressive"},
		{6, "custom-progressive-v3", 9223372036854775807, "progressive"},
		{7, "custom-v1", 0, "off"},
		{8, "custom-random-v2", 1, "random-one"},
	} {
		if _, err := db.Exec(insert, tc.id, tc.version, tc.seed, tc.policy); err != nil {
			t.Fatalf("valid rules %+v rejected: %v", tc, err)
		}
	}
	for _, tc := range []struct {
		version string
		seed    int64
		policy  string
	}{
		{"custom-progressive-v3", 0, "progressive"},
		{"custom-progressive-v3", -1, "progressive"},
		{"custom-progressive-v3", 1, "off"},
		{"custom-progressive-v3", 1, "random-one"},
		{"custom-progressive-v3", 1, "all"},
		{"custom-progressive-v3", 1, "unknown"},
		{"custom-v1", 1, "off"},
		{"custom-v1", -1, "off"},
		{"custom-v1", 0, "progressive"},
		{"custom-random-v2", 0, "off"},
		{"custom-random-v2", -1, "all"},
		{"custom-random-v2", 1, "progressive"},
		{"unknown", 1, "progressive"},
	} {
		requireDivaProgressiveMigrationSQLState(t, db, "23514", insert, 20, tc.version, tc.seed, tc.policy)
	}
	// 0061's immutable-presentation trigger must survive the constraint upgrade.
	requireDivaProgressiveMigrationSQLState(t, db, "23514", `UPDATE diva_map_events SET red_treasure_mode='all' WHERE event_id=3`)
	requireDivaProgressiveMigrationSQLState(t, db, "23514", `UPDATE diva_map_events SET red_treasure_mode='off' WHERE event_id=5`)
	if _, err := db.Exec(`UPDATE diva_map_events SET red_treasure_mode=red_treasure_mode WHERE event_id IN (3,5)`); err != nil {
		t.Fatalf("no-op immutable policy update rejected: %v", err)
	}
}

func TestDivaProgressiveMapMigrationInvasionKeysForeignKeyAndBounds(t *testing.T) {
	db, migration := setupDivaProgressiveMigrationTest(t)
	if _, err := db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,starts_at,ends_at,generation_seed,red_treasure_mode) VALUES
		(5,'custom-progressive-v3','2026-03-01','2026-03-08',1,'progressive'),
		(6,'custom-progressive-v3','2026-03-01','2026-03-08',2,'progressive');
		INSERT INTO diva_map_guilds VALUES
		(5,22,'first',1,0,'2026-03-01','{}'),
		(5,23,'second',1,0,'2026-03-01','{}'),
		(6,22,'next event',1,0,'2026-03-01','{}');
		INSERT INTO diva_map_invasions VALUES
		(5,22,'2026-03-01 01:00',1,1,1),
		(5,22,'2026-03-01 02:00',65535,168,20),
		(5,22,'2026-03-01 03:00',2,1,1),
		(5,23,'2026-03-01 01:00',1,1,1),
		(6,22,'2026-03-01 01:00',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	// One guild/event cannot journal two results for an hour, or record the
	// same page-local tick again using a different wall-clock timestamp.
	requireDivaProgressiveMigrationSQLState(t, db, "23505", `INSERT INTO diva_map_invasions VALUES(5,22,'2026-03-01 01:00',2,2,1)`)
	requireDivaProgressiveMigrationSQLState(t, db, "23505", `INSERT INTO diva_map_invasions VALUES(5,22,'2026-03-01 04:00',1,1,1)`)
	for _, pair := range [][2]int{{5, 999}, {6, 23}, {7, 22}, {41, 22}} {
		requireDivaProgressiveMigrationSQLState(t, db, "23503", `INSERT INTO diva_map_invasions VALUES($1,$2,'2026-03-01 03:00',1,2,1)`, pair[0], pair[1])
	}
	for _, values := range [][3]int{{0, 2, 1}, {65536, 2, 1}, {2, 0, 1}, {2, 169, 1}, {2, 2, 0}, {2, 2, 21}} {
		requireDivaProgressiveMigrationSQLState(t, db, "23514", `INSERT INTO diva_map_invasions VALUES(5,22,'2026-03-01 04:00',$1,$2,$3)`, values[0], values[1], values[2])
	}
	requireDivaProgressiveMigrationSQLState(t, db, "23502", `INSERT INTO diva_map_invasions VALUES(5,22,NULL,2,2,1)`)
	// No other child rows reference this new guild: this specifically proves
	// the invasion journal's ON DELETE RESTRICT, not a legacy ledger's FK.
	requireDivaProgressiveMigrationSQLState(t, db, "23503", `DELETE FROM diva_map_guilds WHERE event_id=5 AND guild_id=22`)
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_map_invasions`); err != nil || count != 5 {
		t.Fatalf("rejected statements changed invasion history: count=%d err=%v", count, err)
	}
}
