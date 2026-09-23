package migrations

import "testing"

func TestDivaMapSpecialMigrationPreservesExistingRowsAndReplay(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("special-map migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE diva_map_events(event_id INTEGER PRIMARY KEY,rules_version TEXT NOT NULL,generation_seed BIGINT NOT NULL);
		INSERT INTO diva_map_events VALUES(1,'custom-v1',0),(2,'custom-random-v2',91);
		CREATE TABLE preserved_diva_state(map_data JSONB,receipt JSONB);
		INSERT INTO preserved_diva_state VALUES('{"AcquiredAreas":17,"States":[{"MapNumber":2}]}','{"id":31,"quantity":5,"claimed_at":"2026-09-22"}')`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.Get(&before, `SELECT row_to_json(s)::text FROM preserved_diva_state s`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0061_diva_map_special_treasures.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE diva_map_special_policy SET mode='random-one';
		INSERT INTO diva_map_events VALUES(3,'custom-random-v2',92,'random-one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.Get(&after, `SELECT row_to_json(s)::text FROM preserved_diva_state s`); err != nil || after != before {
		t.Fatal("migration rewrote map/receipt state")
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM diva_map_events WHERE event_id IN (1,2) AND red_treasure_mode='off'`, 2},
		{`SELECT COUNT(*) FROM diva_map_events WHERE event_id=3 AND red_treasure_mode='random-one'`, 1},
		{`SELECT COUNT(*) FROM diva_map_special_policy WHERE mode='random-one'`, 1},
		{`SELECT COUNT(*) FROM diva_map_special_policy_history`, 1},
	} {
		var count int
		if err := db.Get(&count, tc.query); err != nil || count != tc.want {
			t.Fatalf("%s got=%d want=%d err=%v", tc.query, count, tc.want, err)
		}
	}
	for _, query := range []string{
		`UPDATE diva_map_events SET red_treasure_mode='all' WHERE event_id=3`,
		`INSERT INTO diva_map_events VALUES(4,'custom-v1',0,'all')`,
		`INSERT INTO diva_map_events VALUES(4,'custom-random-v2',93,'unknown')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("accepted unsafe policy SQL: %s", query)
		}
	}
}
