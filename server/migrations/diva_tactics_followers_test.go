package migrations

import "testing"

func TestDivaTacticsFollowerSchema(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY);
		CREATE TABLE events(id integer PRIMARY KEY);
		INSERT INTO characters VALUES(1),(2);
		INSERT INTO events VALUES(10),(20);`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0055_diva_tactics_followers.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_tactics_followers
		VALUES(1,10,30,39,3,1,2,2300,'2026-09-23 12:00:00+09','2026-09-24 12:00:00+09'),
		(2,20,40,0,0,0,0,800,'2026-09-23 12:00:00+09','2026-09-24 12:00:00+09');`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal("idempotent migration", err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_tactics_followers WHERE available_at='2026-09-24 12:00:00+09'`); err != nil || count != 2 {
		t.Fatal("reapply changed contracts", count, err)
	}
	for _, statement := range []string{
		`UPDATE diva_tactics_followers SET name_index=40 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET name_index=-1 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET voice=4 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET weapon=2 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET strength=3 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET cost=800 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET guild_id=0 WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET available_at=hired_at WHERE char_id=1`,
		`UPDATE diva_tactics_followers SET hired_at=to_timestamp(4294880895),available_at=to_timestamp(4294967295) WHERE char_id=1`,
		`INSERT INTO diva_tactics_followers SELECT char_id,20,guild_id,name_index,voice,weapon,strength,cost,hired_at,available_at FROM diva_tactics_followers WHERE char_id=1`,
	} {
		if _, err = db.Exec(statement); err == nil {
			t.Errorf("invalid contract accepted: %s", statement)
		}
	}
	if _, err = db.Exec(`DELETE FROM characters WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_tactics_followers`); err != nil || count != 1 {
		t.Fatal("character cascade", count, err)
	}
	if _, err = db.Exec(`DELETE FROM events WHERE id=20`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM diva_tactics_followers`); err != nil || count != 0 {
		t.Fatal("event cascade", count, err)
	}
}
