package migrations

import (
	"encoding/json"
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestDivaGeneralOfficialCatalogAndReseed(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeedData(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Items []struct {
			ID, Cost, Level int
			Rank            string
		}
	}
	data, err := os.ReadFile("testdata/diva-general-official.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &evidence); err != nil || len(evidence.Items) != 172 {
		t.Fatal("official evidence", len(evidence.Items), err)
	}
	levels := [4]int{}
	for _, r := range evidence.Items {
		hr := 0
		if r.Rank == "HR5" {
			hr = 299
		}
		var correct bool
		err = db.Get(&correct, `SELECT cost=$2 AND store_level=$3 AND min_hr=$4
			AND quantity=1 AND min_sr=0 AND min_gr=0 AND max_quantity=0
			AND road_floors=0 AND road_fatalis=0 AND NOT diva_round_limit AND NOT road_weekly_limit
			FROM shop_items WHERE shop_type=8 AND shop_id=0 AND item_id=$1`, r.ID, r.Cost, r.Level, hr)
		if err != nil || !correct {
			t.Fatalf("official general item %d differs: %v", r.ID, err)
		}
		levels[r.Level]++
	}
	if levels != [4]int{29, 53, 25, 65} || levels[0]+levels[1] != 82 {
		t.Fatal("unlock distribution", levels)
	}
	for shop, want := range map[int]int{0: 172, 5: 31, 7: 131} {
		var count int
		if err = db.Get(&count, `SELECT count(*) FROM shop_items WHERE shop_type=8 AND shop_id=$1`, shop); err != nil || count != want {
			t.Fatal("catalog count", shop, count, err)
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
		t.Fatal("reseed changed IDs or fields", before, after, err)
	}
}

func TestDivaGeneralUpgradePreservesHashesCountsAndOtherShops(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE shop_items(id serial PRIMARY KEY,shop_type integer,shop_id integer,
		item_id integer,cost integer,quantity integer,min_hr integer,min_sr integer,min_gr integer,
		store_level integer,max_quantity integer,road_floors integer,road_fatalis integer,
		diva_round_limit boolean DEFAULT false,road_weekly_limit boolean DEFAULT false);
		CREATE TABLE shop_items_bought(character_id integer,shop_item_id integer REFERENCES shop_items(id),bought integer);
		INSERT INTO shop_items VALUES(8001,8,0,2159,999,9,299,9,1,3,5,9,9,true,true),
		(8002,8,0,65000,17,1,0,0,0,0,0,0,0,false,false),
		(8003,5,0,2159,123,3,0,0,0,0,0,0,0,false,false),
		(8004,8,5,1,30,10,0,0,0,0,10,0,0,false,false),
		(8005,8,7,2209,400,1,299,0,0,2,5,0,0,true,false);
		INSERT INTO shop_items_bought VALUES(1,8001,70000),(1,8002,2),(1,8003,3),(1,8005,4);`)
	if err != nil {
		t.Fatal(err)
	}
	var otherBefore, otherAfter string
	const others = `SELECT md5(string_agg(row_to_json(s)::text,',' ORDER BY id)) FROM shop_items s WHERE id IN (8002,8003,8004,8005)`
	if err = db.Get(&otherBefore, others); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0106_diva_general_shop.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := db.Beginx()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(string(migration)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var correct bool
		err = db.Get(&correct, `SELECT id=8001 AND cost=30 AND quantity=1 AND min_hr=0 AND min_sr=0 AND min_gr=0
			AND store_level=0 AND max_quantity=0 AND road_floors=0 AND road_fatalis=0
			AND NOT diva_round_limit AND NOT road_weekly_limit FROM shop_items WHERE shop_type=8 AND shop_id=0 AND item_id=2159`)
		if err != nil || !correct {
			t.Fatal("old product ID changed or canonical fields not restored", correct, err)
		}
		var count, bought int
		if err = db.Get(&count, `SELECT count(*) FROM shop_items WHERE shop_type=8 AND shop_id=0`); err != nil || count != 173 {
			t.Fatal("missing/duplicate general products or lost custom row", count, err)
		}
		if err = db.Get(&bought, `SELECT bought FROM shop_items_bought WHERE shop_item_id=8001`); err != nil || bought != 70000 {
			t.Fatal("purchase history changed", bought, err)
		}
	}
	if err = db.Get(&otherAfter, others); err != nil || otherBefore != otherAfter {
		t.Fatal("custom or another shop changed", otherBefore, otherAfter, err)
	}
}
