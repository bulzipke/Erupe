package migrations

import (
	"encoding/json"
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestDivaShopOfficialCatalogAndIdempotence(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeedData(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Limited []struct {
			ID, Level, Cost, Limit int
			Rank                   string
		}
		Songs []struct {
			ID, Level, Cost int
		}
	}
	data, err := os.ReadFile("testdata/diva-shop-official.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []struct{ shop, want int }{{7, 131}, {5, 31}} {
		var count int
		if err = db.Get(&count, `SELECT count(*) FROM shop_items WHERE shop_type=8 AND shop_id=$1`, kind.shop); err != nil || count != kind.want {
			t.Fatalf("shop %d rows %d want %d: %v", kind.shop, count, kind.want, err)
		}
	}
	for _, r := range evidence.Limited {
		hr, gr := 0, 0
		if r.Rank == "HR5" {
			hr = 299 // Native old-HR protocol value used by the existing eight rows.
		}
		if r.Rank == "GR1" {
			gr = 1
		}
		var correct bool
		err = db.Get(&correct, `SELECT cost=$2 AND store_level=$3 AND max_quantity=$4
			AND quantity=1 AND min_hr=$5 AND min_gr=$6 AND diva_round_limit
			FROM shop_items WHERE shop_type=8 AND shop_id=7 AND item_id=$1`,
			r.ID, r.Cost, r.Level, r.Limit, hr, gr)
		if err != nil || !correct {
			t.Fatalf("official GP item %d differs: %v", r.ID, err)
		}
	}
	for _, r := range evidence.Songs {
		var correct bool
		err = db.Get(&correct, `SELECT cost=$2 AND store_level=$3 AND quantity=10
			AND max_quantity=10 AND NOT diva_round_limit
			FROM shop_items WHERE shop_type=8 AND shop_id=5 AND item_id=$1`, r.ID, r.Cost, r.Level)
		if err != nil || !correct {
			t.Fatalf("official song %d differs: %v", r.ID, err)
		}
	}
	const digest = `SELECT md5(string_agg(row_to_json(s)::text,',' ORDER BY id)) FROM shop_items s`
	var before, after string
	if err = db.Get(&before, digest); err != nil {
		t.Fatal(err)
	}
	seed, err := seedFS.ReadFile("seed/DivaShops.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(seed)); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&after, digest); err != nil || before != after {
		t.Fatalf("catalog/IDs changed on repeated seed: %s %s %v", before, after, err)
	}
}

func TestDivaShopUpgradePreservesHashesAndLegacyUses(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY);
		CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz);
		CREATE TABLE diva_debug_song_event(event_id integer);
		CREATE TABLE diva_event_lifecycle(mode integer,event_id integer);
		CREATE TABLE shop_items(id serial PRIMARY KEY,shop_type integer,shop_id integer,
		item_id integer,cost integer,quantity integer,min_hr integer,min_sr integer,min_gr integer,
		store_level integer,max_quantity integer,road_floors integer,road_fatalis integer);
		CREATE TABLE shop_items_bought(character_id integer,shop_item_id integer,bought integer);
		INSERT INTO characters VALUES(1);
		INSERT INTO events VALUES(10,'diva','2026-10-26 12:00+09'),(11,'diva','2026-11-02 12:00+09');
		INSERT INTO diva_event_lifecycle VALUES(-1,10),(2,11);
		INSERT INTO shop_items VALUES(8001,8,7,2209,400,1,299,0,0,2,5,0,0),
		(8002,8,7,65000,1,1,0,0,0,0,9,0,0),(8003,10,7,2209,100,1,0,0,1,0,5,10,0);
		INSERT INTO shop_items_bought VALUES(1,8001,3),(1,8002,2),(1,8003,4);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0104_diva_gp_shop.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	err = db.Get(&preserved, `SELECT s.id=8001 AND s.diva_round_limit AND p.bought=3 AND b.bought=3
		FROM shop_items s JOIN diva_shop_purchases p ON p.shop_item_id=s.id
		JOIN shop_items_bought b ON b.shop_item_id=s.id
		WHERE s.item_id=2209 AND s.shop_type=8 AND p.event_id=10`)
	if err != nil || !preserved {
		t.Fatal("old hash/count changed", preserved, err)
	}
	// A recovery must keep the original import anchor and must not re-import
	// old lifetime counts into a newer, otherwise unused round.
	if _, err = db.Exec(`UPDATE diva_event_lifecycle SET event_id=11 WHERE mode=-1;
		UPDATE diva_shop_purchases SET bought=4 WHERE shop_item_id=8001;`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.Get(&count, `SELECT count(*) FROM diva_shop_purchases WHERE event_id=11`); err != nil || count != 0 {
		t.Fatal("legacy counts imported into future round", count, err)
	}
	if err = db.Get(&count, `SELECT bought FROM diva_shop_purchases WHERE event_id=10 AND shop_item_id=8001`); err != nil || count != 4 {
		t.Fatal("current receipt reset by recovery", count, err)
	}
	if err = db.Get(&count, `SELECT sum(bought) FROM shop_items_bought WHERE shop_item_id IN (8002,8003)`); err != nil || count != 6 {
		t.Fatal("other shop receipts changed", count, err)
	}
}
