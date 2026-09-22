package migrations

import "testing"

func TestDivaBattleSongSchemaPreservesCountersAndReceipts(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY);
		CREATE TABLE events(id integer PRIMARY KEY);
		INSERT INTO characters VALUES(1),(2);
		INSERT INTO events VALUES(10),(20);`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0053_diva_battle_songs.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_battle_songs
		VALUES(1,10,2,nextval('diva_battle_song_activation_ids'),'2026-09-22 12:00:00+09'),
		      (2,20,1,nextval('diva_battle_song_activation_ids'),'2026-09-22 12:00:00+09');
		INSERT INTO diva_battle_song_effects VALUES(1,10,8,3),(2,20,8,1);
		INSERT INTO diva_battle_song_receipts
		VALUES(1,10,'00000000-0000-4000-8000-000000000001',1,'[8]',58101,'2026-09-22 12:01:00+09'),
		      (2,20,'00000000-0000-4000-8000-000000000002',2,'[8]',58101,'2026-09-22 12:01:00+09');`); err != nil {
		t.Fatal(err)
	}
	// Existing installations can lose schema_version and reapply all migrations.
	// Recovery must not grant uses by resetting counters, receipts or the sequence.
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatalf("schema recovery failed: %v", err)
	}
	var preserved bool
	if err = db.Get(&preserved, `SELECT s.used_count=2 AND e.used_count=3
		AND s.activation_id=r.activation_id AND r.effects='[8]'
		FROM diva_battle_songs s JOIN diva_battle_song_effects e USING(char_id,event_id)
		JOIN diva_battle_song_receipts r USING(char_id,event_id) WHERE s.char_id=1`); err != nil || !preserved {
		t.Fatalf("stored song state changed: %v %v", preserved, err)
	}
	var next int64
	if err = db.Get(&next, `SELECT nextval('diva_battle_song_activation_ids')`); err != nil || next != 3 {
		t.Fatalf("activation sequence reset: %d %v", next, err)
	}
	for _, statement := range []string{
		`UPDATE diva_battle_songs SET used_count=-1 WHERE char_id=1`,
		`UPDATE diva_battle_songs SET used_count=65 WHERE char_id=1`,
		`UPDATE diva_battle_songs SET activation_id=0 WHERE char_id=1`,
		`UPDATE diva_battle_songs SET activation_id=4294967296 WHERE char_id=1`,
		`UPDATE diva_battle_song_effects SET used_count=256 WHERE char_id=1`,
		`UPDATE diva_battle_song_effects SET effect_id=0 WHERE char_id=1`,
		`UPDATE diva_battle_song_effects SET effect_id=26 WHERE char_id=1`,
		`INSERT INTO diva_battle_song_receipts SELECT * FROM diva_battle_song_receipts WHERE char_id=1`,
		`INSERT INTO diva_battle_song_effects VALUES(1,20,8,0)`,
	} {
		if _, err = db.Exec(statement); err == nil {
			t.Errorf("invalid state accepted: %s", statement)
		}
	}
	// Parent deletion removes only its own event/character state, including receipts.
	if _, err = db.Exec(`DELETE FROM characters WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"diva_battle_songs", "diva_battle_song_effects", "diva_battle_song_receipts"} {
		var count int
		if err = db.Get(&count, "SELECT COUNT(*) FROM "+table); err != nil || count != 1 {
			t.Fatalf("character cascade on %s: %d %v", table, count, err)
		}
	}
	if _, err = db.Exec(`DELETE FROM events WHERE id=20`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"diva_battle_songs", "diva_battle_song_effects", "diva_battle_song_receipts"} {
		var count int
		if err = db.Get(&count, "SELECT COUNT(*) FROM "+table); err != nil || count != 0 {
			t.Fatalf("event cascade on %s: %d %v", table, count, err)
		}
	}
}
