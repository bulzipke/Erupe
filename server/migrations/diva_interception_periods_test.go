package migrations

import (
	"reflect"
	"testing"
)

func TestDivaInterceptionPeriodsBackfillOnlyRealSchedules(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz);
		CREATE TABLE diva_event_lifecycle(mode integer,event_id integer);
		CREATE TABLE diva_interception_runs(event_id integer);
		CREATE TABLE diva_reward_receipts(event_id integer,reward_type integer);
		INSERT INTO events SELECT x,'diva',NOW()-INTERVAL '20 days' FROM generate_series(1,8) x;
		INSERT INTO events VALUES(9,'festa',NOW()),(10,'diva',NOW()+INTERVAL '1 day');
		INSERT INTO diva_event_lifecycle VALUES(-1,1),(2,2),(3,3);
		INSERT INTO diva_interception_runs VALUES(4),(9),(10);
		INSERT INTO diva_reward_receipts VALUES(5,6),(6,0),(7,2);`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0047_diva_interception_periods.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var ids []int
	if err = db.Select(&ids, "SELECT event_id FROM diva_interception_periods ORDER BY event_id"); err != nil || !reflect.DeepEqual(ids, []int{1, 2, 4, 5}) {
		t.Fatalf("wrong backfill: %v %v", ids, err)
	}
	var exact bool
	if err = db.Get(&exact, `SELECT bool_and(p.starts_at=e.start_time+INTERVAL '605100 seconds'
		AND p.ends_at=e.start_time+INTERVAL '1206000 seconds') FROM diva_interception_periods p JOIN events e ON e.id=p.event_id`); err != nil || !exact {
		t.Fatalf("period timestamps changed: %v %v", exact, err)
	}
}
