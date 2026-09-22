package migrations

import (
	"database/sql"
	"testing"
	"time"
)

func TestDivaMapActivationMigration(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("map activation tests require isolated localhost:5433/erupe_test")
	}
	for _, tc := range []struct {
		name    string
		prepare string
		want    int64
	}{
		{"latest_active_only", "", 3},
		{"already_has_custom_map", `INSERT INTO diva_map_events SELECT event_id,'custom-v1',starts_at,ends_at FROM diva_interception_periods WHERE event_id=3`, 0},
		{"latest_is_normal_do_not_fall_back_to_older_legacy", `UPDATE diva_map_cutover SET installed_at='2000-01-01'; DELETE FROM diva_map_legacy_events WHERE event_id=3`, 0},
		{"personal_legacy_exception", `UPDATE diva_map_cutover SET installed_at='2000-01-01'; DELETE FROM diva_map_legacy_events WHERE event_id=3; INSERT INTO diva_interception_legacy_events VALUES(3)`, 3},
		{"no_current_period", `UPDATE diva_interception_periods SET ends_at=clock_timestamp()-INTERVAL '1 second' WHERE event_id IN (2,3)`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			defer db.Close()
			_, err := db.Exec(`CREATE TABLE events(id INTEGER PRIMARY KEY,event_type TEXT NOT NULL);
				CREATE TABLE characters(id INTEGER PRIMARY KEY,legacy_points INTEGER NOT NULL);
				CREATE TABLE diva_interception_periods(event_id INTEGER PRIMARY KEY,starts_at TIMESTAMPTZ,ends_at TIMESTAMPTZ);
				CREATE TABLE diva_interception_legacy_events(event_id INTEGER PRIMARY KEY);
				INSERT INTO events VALUES(1,'diva'),(2,'diva'),(3,'diva'),(4,'diva'),(5,'diva');
				INSERT INTO characters VALUES(10,123456);
				INSERT INTO diva_interception_periods VALUES
				(1,clock_timestamp()-INTERVAL '10 days',clock_timestamp()-INTERVAL '9 days'),
				(2,clock_timestamp()-INTERVAL '2 hours',clock_timestamp()+INTERVAL '5 hours'),
				(3,clock_timestamp()-INTERVAL '1 hour',clock_timestamp()+INTERVAL '6 hours'),
				(4,clock_timestamp()+INTERVAL '1 day',clock_timestamp()+INTERVAL '2 days');`)
			if err != nil {
				t.Fatal(err)
			}
			base, err := migrationFS.ReadFile("sql/0054_diva_maps.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(base)); err != nil {
				t.Fatal(err)
			}
			if tc.prepare != "" {
				if _, err = db.Exec(tc.prepare); err != nil {
					t.Fatal(err)
				}
			}
			migration, err := migrationFS.ReadFile("sql/0056_diva_current_map_activation.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(migration)); err != nil {
				t.Fatal(err)
			}
			var decision sql.NullInt64
			var decided time.Time
			if err = db.QueryRow(`SELECT event_id,decided_at FROM diva_map_activation_decision`).Scan(&decision, &decided); err != nil {
				t.Fatal(err)
			}
			if decision.Valid != (tc.want != 0) || decision.Int64 != tc.want {
				t.Fatalf("decision=%v want=%d", decision, tc.want)
			}
			var count int
			if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_activations`); err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.want != 0 {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("activation count=%d", count)
			}
			if tc.want != 0 {
				var activated time.Time
				if err = db.Get(&activated, `SELECT activated_at FROM diva_map_activations WHERE event_id=$1`, tc.want); err != nil || !activated.Equal(decided) {
					t.Fatalf("activation timestamp=%v decision=%v err=%v", activated, decided, err)
				}
			}
			// A later, newly started period must not become another exception
			// when the migration is replayed after either a grant or a no-op.
			if _, err = db.Exec(`UPDATE diva_interception_periods SET ends_at=clock_timestamp()-INTERVAL '1 second' WHERE event_id IN (2,3);
				INSERT INTO diva_interception_periods VALUES(5,clock_timestamp()-INTERVAL '1 second',clock_timestamp()+INTERVAL '1 day');
				INSERT INTO diva_map_legacy_events VALUES(5);`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(migration)); err != nil {
				t.Fatal(err)
			}
			var replayDecision sql.NullInt64
			var replayTime time.Time
			if err = db.QueryRow(`SELECT event_id,decided_at FROM diva_map_activation_decision`).Scan(&replayDecision, &replayTime); err != nil {
				t.Fatal(err)
			}
			if replayDecision != decision || !replayTime.Equal(decided) {
				t.Fatal("replay changed permanent activation decision")
			}
			if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_activations WHERE event_id=5`); err != nil || count != 0 {
				t.Fatalf("replay activated a later round: %d %v", count, err)
			}
			if err = db.Get(&count, `SELECT legacy_points FROM characters WHERE id=10`); err != nil || count != 123456 {
				t.Fatalf("legacy total changed: %d %v", count, err)
			}
			if tc.name == "already_has_custom_map" {
				if err = db.Get(&count, `SELECT COUNT(*) FROM diva_map_events m JOIN diva_interception_periods p USING(event_id) WHERE m.event_id=3 AND m.starts_at=p.starts_at`); err != nil || count != 1 {
					t.Fatalf("existing map anchor changed: %d %v", count, err)
				}
			}
		})
	}
}
