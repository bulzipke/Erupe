package migrations

import (
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestRoadShopNativeCatalogRestore(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()
	// Keep ROLLBACK on the same connection after an intentionally failed script.
	db.SetMaxOpenConns(1)
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySeedData(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Get(&count, "SELECT count(*) FROM shop_items WHERE shop_type=10"); err != nil || count != 0 {
		t.Fatalf("fresh seed must leave native Road catalog intact: count=%d err=%v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO shop_items
		(shop_type, shop_id, item_id, cost, quantity, min_hr, min_sr, min_gr,
		 store_level, max_quantity, road_floors, road_fatalis)
		VALUES (10,8,9958,1,999,0,0,1,1,0,0,0),
		       (10,7,16341,500,1,0,0,1,1,0,20,0),
		       (10,9,1,1,1,0,0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shop_items_bought (character_id, shop_item_id, bought)
		SELECT 123456, id, 7 FROM shop_items WHERE shop_type=10 AND shop_id=8`); err != nil {
		t.Fatal(err)
	}
	var sequenceBefore int64
	if err := db.Get(&sequenceBefore, "SELECT last_value FROM shop_items_id_seq"); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.Get(&before, "SELECT count(*) FROM shop_items WHERE shop_type<>10 OR shop_id=9"); err != nil {
		t.Fatal(err)
	}
	runSQL := func(path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatal(err)
		}
	}
	failSQL := func(path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, scriptErr := db.Exec(string(data))
		if _, err := db.Exec("ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		if scriptErr == nil {
			t.Fatalf("unsafe script unexpectedly succeeded: %s", path)
		}
	}
	runSQL("manual/restore_native_road_shop.sql")
	failSQL("manual/restore_native_road_shop.sql")
	if err := db.Get(&count, "SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id BETWEEN 0 AND 8"); err != nil || count != 0 {
		t.Fatalf("overrides remain: count=%d err=%v", count, err)
	}
	if err := db.Get(&count, "SELECT count(*) FROM road_shop_overrides_backup_20260921"); err != nil || count != 2 {
		t.Fatalf("backup must preserve both rows: count=%d err=%v", count, err)
	}
	if err := db.Get(&count, "SELECT count(*) FROM shop_items WHERE shop_type<>10 OR shop_id=9"); err != nil || count != before {
		t.Fatalf("unrelated shop rows changed: count=%d want=%d err=%v", count, before, err)
	}
	runSQL("manual/undo_restore_native_road_shop.sql")
	runSQL("manual/undo_restore_native_road_shop.sql")
	if err := db.Get(&count, "SELECT count(*) FROM shop_items WHERE shop_type=10 AND shop_id BETWEEN 0 AND 8"); err != nil || count != 2 {
		t.Fatalf("recovery must be idempotent: count=%d err=%v", count, err)
	}
	var exact bool
	if err := db.Get(&exact, `SELECT bool_and(to_jsonb(s) = (to_jsonb(b) - 'backed_up_at'))
		FROM road_shop_overrides_backup_20260921 b JOIN shop_items s USING(id)`); err != nil || !exact {
		t.Fatalf("recovery changed backed-up data: exact=%v err=%v", exact, err)
	}
	var sequenceAfter int64
	if err := db.Get(&sequenceAfter, "SELECT last_value FROM shop_items_id_seq"); err != nil || sequenceAfter != sequenceBefore {
		t.Fatalf("sequence changed: before=%d after=%d err=%v", sequenceBefore, sequenceAfter, err)
	}
	if err := db.Get(&count, "SELECT count(*) FROM shop_items_bought WHERE character_id=123456 AND bought=7"); err != nil || count != 1 {
		t.Fatalf("purchase history changed: count=%d err=%v", count, err)
	}
	if _, err := db.Exec("UPDATE shop_items SET cost=2 WHERE shop_type=10 AND shop_id=8"); err != nil {
		t.Fatal(err)
	}
	failSQL("manual/undo_restore_native_road_shop.sql")
	if _, err := db.Exec("UPDATE shop_items SET cost=1 WHERE shop_type=10 AND shop_id=8"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO shop_items (shop_type, shop_id, item_id) VALUES (10,8,9958)"); err != nil {
		t.Fatal(err)
	}
	failSQL("manual/undo_restore_native_road_shop.sql")
}
