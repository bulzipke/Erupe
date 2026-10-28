package migrations

import "testing"

func TestFestaContinuousMigrationKeepsActiveChapterAndRecords(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz);
	CREATE TABLE festa_registrations(guild_id integer,team text);
	CREATE TABLE festa_submissions(character_id integer,guild_id integer,trial_type integer,souls integer,timestamp timestamptz);
	CREATE TABLE festa_prizes_accepted(prize_id integer,character_id integer);
	CREATE TABLE rotation_schedule_adjustments(event_type text,event_id integer,previous_schedule jsonb,PRIMARY KEY(event_type,event_id));
	INSERT INTO events VALUES(11,'festa',now()-interval '60 days'),(12,'festa',now()-interval '8 days'),(13,'diva',now());
	INSERT INTO festa_registrations VALUES(1,'blue'),(2,'red');
	INSERT INTO festa_submissions VALUES(3,1,1,123,now());
	INSERT INTO festa_prizes_accepted VALUES(7,3),(0,3);
	CREATE TEMP TABLE original_events AS SELECT * FROM events;`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0099_festa_continuous_schedule.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var valid bool
	err = db.Get(&valid, `SELECT
	(SELECT event_id=12 FROM festa_event_lifecycle WHERE singleton)
	AND (SELECT festa_continuous AND extract(isodow FROM start_time AT TIME ZONE 'Asia/Seoul')=1
	AND (start_time AT TIME ZONE 'Asia/Seoul')::time='12:00' AND now()>=start_time+interval '7 days'
	AND now()<start_time+interval '14 days' FROM events WHERE id=12)
	AND (SELECT count(*)=2 AND bool_and(event_id=12) FROM festa_registrations)
	AND (SELECT count(*)=1 AND sum(souls)=123 AND bool_and(event_id=12) FROM festa_submissions)
	AND (SELECT count(*)=2 AND bool_and(event_id=12) FROM festa_prizes_accepted)
	AND (SELECT count(*)=1 FROM rotation_schedule_adjustments WHERE event_type='festa')
	AND NOT EXISTS(SELECT id,event_type,start_time FROM events WHERE id<>12 EXCEPT SELECT * FROM original_events WHERE id<>12)`)
	if err != nil || !valid {
		t.Fatalf("lost active soul phase or records: %v %v", valid, err)
	}
}
