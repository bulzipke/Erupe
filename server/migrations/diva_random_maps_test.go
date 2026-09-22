package migrations

import (
	"testing"
	"time"
)

func TestDivaRandomMapsMigrationPreservesExistingLedgersAndCutover(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("random map migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events(id INTEGER PRIMARY KEY);
		CREATE TABLE characters(id INTEGER PRIMARY KEY);
		CREATE TABLE diva_interception_periods(event_id INTEGER PRIMARY KEY,starts_at TIMESTAMPTZ,ends_at TIMESTAMPTZ);
		INSERT INTO events VALUES(1),(2),(3),(4),(5),(6),(7);
		INSERT INTO characters VALUES(10);
		INSERT INTO diva_interception_periods VALUES(1,'2026-01-01','2026-01-08');`); err != nil {
		t.Fatal(err)
	}
	base, err := migrationFS.ReadFile("sql/0054_diva_maps.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(base)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_map_events VALUES(1,'custom-v1','2026-01-01','2026-01-08');
		INSERT INTO diva_map_guilds VALUES(1,22,'original',2,21,'2026-01-02','{"legacy":"unchanged","AcquiredAreas":21}');
		INSERT INTO diva_map_snapshots VALUES(1,22,'2026-01-01',1,'{"old":"snapshot"}');
		INSERT INTO diva_map_departures VALUES(10,1,'00000000-0000-4000-8000-000000000001',22,1,58079,407,TRUE,'2026-01-01');
		INSERT INTO diva_map_contributions VALUES(10,1,'00000000-0000-4000-8000-000000000001',22,1,407,9000,5000,TRUE,'2026-01-01 00:01','2026-01-01 00:01','2026-01-01 01:00');
		INSERT INTO diva_map_area_awards VALUES(1,22,1,407,TRUE,'2026-01-01 01:00');
		CREATE TABLE preserved_reward_receipts(reward_key TEXT PRIMARY KEY,claimed BOOLEAN NOT NULL);
		INSERT INTO preserved_reward_receipts VALUES('custom-treasure-map-1-branch-407-ticket',TRUE);`); err != nil {
		t.Fatal(err)
	}
	var guildJSON, snapshotJSON string
	if err = db.Get(&guildJSON, `SELECT map_data::text FROM diva_map_guilds`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&snapshotJSON, `SELECT map_data::text FROM diva_map_snapshots`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0058_diva_random_maps.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var installed time.Time
	if err = db.Get(&installed, `SELECT installed_at FROM diva_random_map_cutover`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,generation_seed,starts_at,ends_at)
		VALUES(2,'custom-random-v2',9223372036854775807,'2026-02-01','2026-02-08')`); err != nil {
		t.Fatal(err)
	}
	// Reapply after a new random event exists. Neither the permanent timestamp
	// nor legacy JSON/seed pins/ledgers may be converted by reinstallation.
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var replay time.Time
	if err = db.Get(&replay, `SELECT installed_at FROM diva_random_map_cutover`); err != nil || !replay.Equal(installed) {
		t.Fatalf("cutover changed: %v %v", replay, err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM diva_map_events WHERE event_id=1 AND rules_version='custom-v1' AND generation_seed=0`, 1},
		{`SELECT COUNT(*) FROM diva_map_events WHERE event_id=2 AND rules_version='custom-random-v2' AND generation_seed=9223372036854775807`, 1},
		{`SELECT COUNT(*) FROM diva_map_guilds WHERE current_number=2 AND acquired_areas=21`, 1},
		{`SELECT COUNT(*) FROM diva_map_departures WHERE eligible AND route=407`, 1},
		{`SELECT COUNT(*) FROM diva_map_contributions WHERE points=9000 AND credited_points=5000 AND treasure_participation AND settled_at IS NOT NULL`, 1},
		{`SELECT COUNT(*) FROM diva_map_area_awards WHERE coordinate=407 AND is_branch`, 1},
		{`SELECT COUNT(*) FROM preserved_reward_receipts WHERE claimed`, 1},
		{`SELECT COUNT(*) FROM pg_indexes WHERE indexname='diva_map_snapshots_map_history'`, 1},
	} {
		var count int
		if err = db.Get(&count, tc.query); err != nil || count != tc.want {
			t.Fatalf("preservation %s = %d: %v", tc.query, count, err)
		}
	}
	var after string
	if err = db.Get(&after, `SELECT map_data::text FROM diva_map_guilds`); err != nil || after != guildJSON {
		t.Fatalf("guild JSON rewritten: %v", err)
	}
	if err = db.Get(&after, `SELECT map_data::text FROM diva_map_snapshots`); err != nil || after != snapshotJSON {
		t.Fatalf("snapshot JSON rewritten: %v", err)
	}
	for _, tc := range []struct {
		id      int
		version string
		seed    int64
	}{
		{3, "custom-v1", 1}, {4, "custom-random-v2", 0}, {5, "custom-random-v2", -1}, {6, "unknown", 1},
	} {
		if _, err = db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,generation_seed,starts_at,ends_at)
			VALUES($1,$2,$3,'2026-03-01','2026-03-08')`, tc.id, tc.version, tc.seed); err == nil {
			t.Fatalf("accepted invalid rules %s/%d", tc.version, tc.seed)
		}
	}
	if _, err = db.Exec(`INSERT INTO diva_map_events(event_id,rules_version,generation_seed,starts_at,ends_at)
		VALUES(7,'custom-v1',0,'2026-03-01','2026-03-08')`); err != nil {
		t.Fatalf("valid legacy insert rejected: %v", err)
	}
}
