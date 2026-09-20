package migrations

import "testing"

func TestDivaInterceptionCutoverPreservesLegacy(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY);
		CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamp with time zone);
		CREATE TABLE diva_debug_song_event(singleton boolean,event_id integer);
		CREATE TABLE guild_characters(character_id integer,interception_points jsonb);
		INSERT INTO characters VALUES(1);
		INSERT INTO guild_characters VALUES(1,'{"58043":100000,"58044":50000}');
		INSERT INTO events VALUES(1,'diva',NOW()-INTERVAL '1 day'),(2,'diva',NOW()+INTERVAL '1 day'),(3,'festa',NOW()-INTERVAL '1 day');`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0046_diva_interception_rounds.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var ids []int
	if err = db.Select(&ids, "SELECT event_id FROM diva_interception_legacy_events ORDER BY event_id"); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("wrong cutover exclusions: %v", ids)
	}
	var preserved bool
	if err = db.Get(&preserved, `SELECT interception_points='{"58043":100000,"58044":50000}'::jsonb FROM guild_characters WHERE character_id=1`); err != nil || !preserved {
		t.Fatalf("legacy total changed: %v %v", preserved, err)
	}
	var count int
	if err = db.Get(&count, "SELECT count(*) FROM diva_interception_runs"); err != nil || count != 0 {
		t.Fatalf("legacy totals imported as new progress: %d %v", count, err)
	}
}
