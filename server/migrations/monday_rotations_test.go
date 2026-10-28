package migrations

import "testing"

func TestMondayRotationsMigrationPreservesProgress(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	// Use the DB clock so the migration exercises the actual current-round
	// selector, including running this test on a Monday before noon.
	_, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz,diva_continuous boolean);
		CREATE TABLE diva_debug_song_event(event_id integer);
		CREATE TABLE diva_event_lifecycle(mode integer PRIMARY KEY,event_id integer);
		CREATE TABLE diva_interception_periods(event_id integer PRIMARY KEY,starts_at timestamptz,ends_at timestamptz);
		CREATE TABLE tower_events(earth_id integer PRIMARY KEY,starts_at timestamptz,ends_at timestamptz,claim_until timestamptz);
		CREATE TABLE tower_event_lifecycle(singleton boolean,earth_id integer,anchor timestamptz,active_days integer,cycle_days integer);
		CREATE TABLE progress(event_id integer,points integer,claimed boolean);
		INSERT INTO events VALUES(1,'diva',now()-interval '30 days',true),(2,'diva',now()-interval '1 hour',true),
			(3,'diva',now()-interval '1 hour',false),(4,'festa',now()-interval '1 hour',false);
		INSERT INTO diva_event_lifecycle VALUES(-1,2),(2,3);
		INSERT INTO diva_interception_periods VALUES(1,now()-interval '23 days',now()-interval '16 days');
		INSERT INTO tower_events VALUES(36,now()-interval '1 day',now()+interval '6 days',now()+interval '20 days');
		INSERT INTO tower_event_lifecycle SELECT true,36,starts_at,7,21 FROM tower_events;
		INSERT INTO progress VALUES(2,12345,true),(36,500,false);
		CREATE TEMP TABLE original_events AS SELECT * FROM events;
		CREATE TEMP TABLE original_progress AS SELECT * FROM progress;
		CREATE TEMP TABLE original_periods AS SELECT * FROM diva_interception_periods;`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0098_monday_noon_rotations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var valid bool
	err = db.Get(&valid, `SELECT
		(SELECT extract(isodow FROM start_time AT TIME ZONE 'Asia/Seoul')=1
		 AND (start_time AT TIME ZONE 'Asia/Seoul')::time='12:00' AND diva_continuous FROM events WHERE id=2)
		AND NOT EXISTS(SELECT * FROM events WHERE id<>2 EXCEPT SELECT * FROM original_events WHERE id<>2)
		AND NOT EXISTS(SELECT * FROM original_events WHERE id<>2 EXCEPT SELECT * FROM events WHERE id<>2)
		AND NOT EXISTS(SELECT * FROM progress EXCEPT SELECT * FROM original_progress)
		AND NOT EXISTS(SELECT * FROM original_progress EXCEPT SELECT * FROM progress)
		AND NOT EXISTS(SELECT * FROM diva_interception_periods EXCEPT SELECT * FROM original_periods)
		AND (SELECT extract(isodow FROM starts_at AT TIME ZONE 'Asia/Seoul')=1
		 AND (starts_at AT TIME ZONE 'Asia/Seoul')::time='12:00'
		 AND ends_at-starts_at=interval '21 days' AND claim_until-starts_at=interval '42 days' FROM tower_events WHERE earth_id=36)
		AND (SELECT anchor=e.starts_at AND active_days=21 AND cycle_days=21 FROM tower_event_lifecycle l JOIN tower_events e USING(earth_id))
		AND (SELECT count(*)=2 FROM rotation_schedule_adjustments)
		AND (SELECT (previous_schedule->'event'->>'points') IS NULL
		 AND (previous_schedule->'event'->>'start_time')::timestamptz=(SELECT start_time FROM original_events WHERE id=2)
		 FROM rotation_schedule_adjustments WHERE event_type='diva')`)
	if err != nil || !valid {
		t.Fatalf("schedule correction lost progress/history or missed Monday noon: %v %v", valid, err)
	}
}
