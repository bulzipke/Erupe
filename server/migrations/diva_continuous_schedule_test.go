package migrations

import "testing"

func TestDivaContinuousScheduleMigrationPreservesExistingRounds(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz);
		CREATE TABLE diva_reward_receipts(id integer PRIMARY KEY,event_id integer,claimed_at timestamptz);
		INSERT INTO events VALUES(1,'diva','2026-09-24T00:00:00+09:00'),(2,'festa','2026-09-25T00:00:00+09:00');
		INSERT INTO diva_reward_receipts VALUES(7,1,NULL),(8,1,'2026-10-08T12:00:00+09:00');`); err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0096_diva_continuous_schedule.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var unchanged bool
	if err := db.Get(&unchanged, `SELECT
		(SELECT count(*)=2 AND bool_and(NOT diva_continuous) FROM events)
		AND (SELECT start_time='2026-09-24T00:00:00+09:00' FROM events WHERE id=1)
		AND (SELECT count(*)=2 AND count(claimed_at)=1 FROM diva_reward_receipts)
		AND (SELECT claimed_at='2026-10-08T12:00:00+09:00' FROM diva_reward_receipts WHERE id=8)`); err != nil || !unchanged {
		t.Fatalf("old state modified: %v %v", unchanged, err)
	}
}
