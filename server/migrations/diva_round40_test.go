package migrations

import (
	"go.uber.org/zap"
	"testing"
)

func TestDivaRound40FreshCatalogAndSeed(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeedData(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var personal, guild, unsupported int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE type='personal'),
		count(*) FILTER (WHERE type='guild'),count(*) FILTER (WHERE item_type NOT IN (7,26) OR NOT gr OR repeatable)
		FROM diva_prizes`).Scan(&personal, &guild, &unsupported); err != nil {
		t.Fatal(err)
	}
	if personal != 21 || guild != 12 || unsupported != 0 {
		t.Fatalf("wrong restored catalog: personal=%d guild=%d unsupported=%d", personal, guild, unsupported)
	}
	var old int
	if err := db.Get(&old, "SELECT count(*) FROM diva_prizes WHERE points_req>=500000"); err != nil || old != 0 {
		t.Fatalf("old seed was reintroduced: %d %v", old, err)
	}
	if applied, err := Migrate(db, zap.NewNop()); err != nil || applied != 0 {
		t.Fatalf("migration is not idempotent: %d %v", applied, err)
	}
}

func TestDivaRound40ReplacesAndBacksUpOldCatalog(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE diva_prizes(id SERIAL PRIMARY KEY,type text,points_req int,item_type int,item_id int,quantity int,gr bool,repeatable bool);
		INSERT INTO diva_prizes(type,points_req,item_type,item_id,quantity,gr,repeatable)
		VALUES('personal',500000,26,0,1,false,false),('guild',777,7,888,9,true,true)`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0045_diva_round40_prizes.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Get(&count, "SELECT count(*) FROM diva_prizes_before_round40"); err != nil || count != 2 {
		t.Fatalf("backup %d %v", count, err)
	}
	if err := db.Get(&count, "SELECT count(*) FROM diva_prizes_before_round40 WHERE type='guild' AND points_req=777 AND item_id=888 AND quantity=9 AND gr AND repeatable"); err != nil || count != 1 {
		t.Fatalf("custom row not preserved %d %v", count, err)
	}
	if err := db.Get(&count, "SELECT count(*) FROM diva_prizes"); err != nil || count != 33 {
		t.Fatalf("restored %d %v", count, err)
	}
}
