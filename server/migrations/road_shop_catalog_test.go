package migrations

import (
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestRoadShopServerCatalog(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeedData(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM shop_items WHERE shop_type=10", 61},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id=4", 3},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id=7", 47},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id=8", 11},
		{"SELECT count(*) FROM shop_items WHERE road_weekly_limit", 58},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND quantity<>1", 0},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id NOT IN (4,7,8)", 0},
		{"SELECT sum(cost*max_quantity) FROM shop_items WHERE shop_type=10 AND shop_id=8 AND item_id<>14446", 19800},
		{"SELECT sum(cost*max_quantity) FROM shop_items WHERE shop_type=10 AND shop_id=7 AND item_id BETWEEN 12460 AND 12469", 547500},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND item_id=9958", 0},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id=7 AND cost=1000 AND quantity=1 AND min_gr=1 AND max_quantity=5 AND road_weekly_limit AND ((item_id=14433 AND road_floors=100) OR (item_id=14434 AND road_floors=110))", 2},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND item_id IN (16340,16341) AND road_floors=50 AND max_quantity=46", 2},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND item_id=15093 AND road_floors=11", 1},
		{"SELECT count(*) FROM shop_items WHERE shop_type=10 AND item_id=14742 AND ((cost=4000 AND road_floors=20 AND max_quantity=10) OR (cost=2000 AND road_floors=40 AND max_quantity=20))", 2},
	}
	for _, check := range checks {
		var got int
		if err := db.Get(&got, check.query); err != nil || got != check.want {
			t.Fatalf("%s: got=%d want=%d err=%v", check.query, got, check.want, err)
		}
	}
	var before, after string
	const digest = "SELECT md5(string_agg(row_to_json(s)::text,',' ORDER BY id)) FROM shop_items s"
	if err := db.Get(&before, digest); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile("seed/RoadShopItems.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(seed)); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&after, digest); err != nil || before != after {
		t.Fatalf("seed changed existing rows: before=%s after=%s err=%v", before, after, err)
	}
	// Test the targeted undo, including refusing edits and real purchases.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE road_shop_live_coupons_added_20261003 AS SELECT * FROM shop_items WHERE shop_type=10 AND item_id IN (14433,14434)"); err != nil {
		t.Fatal(err)
	}
	couponUndo, err := os.ReadFile("manual/undo_road_shop_live_coupons_20261003.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(couponUndo)); err != nil {
		t.Fatal(err)
	}
	var withoutCoupons int
	if err := db.Get(&withoutCoupons, "SELECT count(*) FROM shop_items WHERE shop_type=10"); err != nil || withoutCoupons != 59 {
		t.Fatalf("coupon-only undo changed previous catalog: rows=%d err=%v", withoutCoupons, err)
	}
	if _, err := db.Exec("INSERT INTO shop_items SELECT * FROM road_shop_live_coupons_added_20261003"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE road_shop_catalog_added_20261003 AS SELECT * FROM shop_items WHERE shop_type=10"); err != nil {
		t.Fatal(err)
	}
	undo, err := os.ReadFile("manual/undo_road_shop_catalog_20261003.sql")
	if err != nil {
		t.Fatal(err)
	}
	runUndo := func(wantFailure bool) {
		t.Helper()
		_, err := db.Exec(string(undo))
		if err != nil {
			if _, rollbackErr := db.Exec("ROLLBACK"); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
		}
		if (err != nil) != wantFailure {
			t.Fatalf("undo err=%v wantFailure=%v", err, wantFailure)
		}
	}
	if _, err := db.Exec("UPDATE shop_items SET cost=cost+1 WHERE shop_type=10 AND item_id=14445"); err != nil {
		t.Fatal(err)
	}
	runUndo(true)
	if _, err := db.Exec("UPDATE shop_items SET cost=cost-1 WHERE shop_type=10 AND item_id=14445"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO shop_items_bought (character_id,shop_item_id,bought) SELECT 123456,id,1 FROM shop_items WHERE shop_type=10 AND item_id=14445"); err != nil {
		t.Fatal(err)
	}
	runUndo(true)
	if _, err := db.Exec("DELETE FROM shop_items_bought WHERE character_id=123456"); err != nil {
		t.Fatal(err)
	}
	runUndo(false)
	runUndo(false)
	var remaining int
	if err := db.Get(&remaining, "SELECT count(*) FROM shop_items WHERE shop_type=10"); err != nil || remaining != 0 {
		t.Fatalf("restored rows remain after undo: %d err=%v", remaining, err)
	}
}
