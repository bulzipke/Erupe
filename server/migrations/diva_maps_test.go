package migrations

import "testing"

func TestDivaMapSchemaCutoverAndRecovery(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("map migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events(id INTEGER PRIMARY KEY);
		CREATE TABLE characters(id INTEGER PRIMARY KEY);
		CREATE TABLE diva_interception_periods(event_id INTEGER PRIMARY KEY,starts_at TIMESTAMPTZ,ends_at TIMESTAMPTZ);
		INSERT INTO events VALUES(1),(2),(3); INSERT INTO characters VALUES(10),(20);
		INSERT INTO diva_interception_periods VALUES(1,'2000-01-01','2000-01-08'),(2,'2100-01-01','2100-01-08');`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0054_diva_maps.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err = db.Get(&legacy, `SELECT event_id FROM diva_map_legacy_events`); err != nil || legacy != 1 {
		t.Fatalf("cutover legacy=%d %v", legacy, err)
	}
	if _, err = db.Exec(`INSERT INTO diva_interception_periods SELECT 3,installed_at+INTERVAL '1 second',installed_at+INTERVAL '7 days' FROM diva_map_cutover;
		INSERT INTO diva_map_events SELECT 2,'custom-v1',starts_at,ends_at FROM diva_interception_periods WHERE event_id=2;
		INSERT INTO diva_map_guilds VALUES(2,77,'Historical guild',1,1,'2100-01-01 01:00','{}');
		INSERT INTO diva_map_snapshots VALUES(2,77,'2100-01-01',1,'{}');
		INSERT INTO diva_map_departures VALUES(10,2,'11111111-1111-4111-8111-111111111111',77,1,58079,407,TRUE,'2100-01-01 00:10');
		INSERT INTO diva_map_contributions(char_id,event_id,run_key,guild_id,map_number,route,points,credited_points,treasure_participation,submitted_at,eligible_at,settled_at)
		VALUES(10,2,'11111111-1111-4111-8111-111111111111',77,1,407,5000,5000,TRUE,'2100-01-01 00:20','2100-01-01 00:20','2100-01-01 01:00');
		INSERT INTO diva_map_area_awards VALUES(2,77,1,407,TRUE,'2100-01-01 01:00');
		INSERT INTO diva_map_publications VALUES(2,'2100-01-01 04:00',FALSE);
		INSERT INTO diva_map_ranks VALUES(2,'2100-01-01 04:00',77,'Historical guild',1,1);
		INSERT INTO diva_guild_reward_memberships VALUES(10,2,77);`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatalf("recovery migration: %v", err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_legacy_events`); err != nil || count != 1 {
		t.Fatalf("reapply excluded later round: %d %v", count, err)
	}
	var preserved bool
	if err = db.Get(&preserved, `SELECT c.credited_points=5000 AND c.treasure_participation AND g.acquired_areas=1 AND r.rank=1
		FROM diva_map_contributions c JOIN diva_map_guilds g USING(event_id,guild_id)
		JOIN diva_map_ranks r USING(event_id,guild_id) WHERE c.char_id=10`); err != nil || !preserved {
		t.Fatalf("progress reset: %v %v", preserved, err)
	}
	for _, statement := range []string{
		`UPDATE diva_map_guilds SET current_number=0`,
		`UPDATE diva_map_guilds SET current_number=65536`,
		`UPDATE diva_map_guilds SET acquired_areas=-1`,
		`UPDATE diva_map_guilds SET map_data='[]'`,
		`UPDATE diva_map_contributions SET points=0`,
		`UPDATE diva_map_contributions SET points=4294967296`,
		`UPDATE diva_map_contributions SET credited_points=5001`,
		`UPDATE diva_map_contributions SET eligible_at=submitted_at-INTERVAL '1 second'`,
		`INSERT INTO diva_map_area_awards SELECT * FROM diva_map_area_awards`,
		`INSERT INTO diva_map_contributions SELECT * FROM diva_map_contributions`,
		`INSERT INTO diva_guild_reward_memberships VALUES(10,2,88)`,
		`DELETE FROM events WHERE id=2`,
	} {
		if _, err = db.Exec(statement); err == nil {
			t.Errorf("invalid state accepted: %s", statement)
		}
	}
	// Character deletion removes its departure/receipt, but never erases the
	// guild's earned area or historical publication.
	if _, err = db.Exec(`DELETE FROM characters WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"diva_map_departures", "diva_map_contributions", "diva_guild_reward_memberships"} {
		if err = db.Get(&count, "SELECT COUNT(*) FROM "+table); err != nil || count != 0 {
			t.Fatalf("cascade %s=%d %v", table, count, err)
		}
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_area_awards`); err != nil || count != 1 {
		t.Fatalf("guild award erased: %d %v", count, err)
	}
}
