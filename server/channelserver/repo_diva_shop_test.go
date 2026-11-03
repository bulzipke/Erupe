package channelserver

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func divaShopFixture(t *testing.T) (*ShopRepository, *sqlx.DB, uint32, DivaEvent, time.Time, uint32) {
	t.Helper()
	r, db, char := setupShopRepo(t)
	seed, err := os.ReadFile("../migrations/seed/DivaShops.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(seed)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 14, 0, 0, 0, divaLocation)
	event := DivaEvent{Continuous: true, StartTime: uint32(now.Add(-8 * 24 * time.Hour).Unix())}
	if err = db.QueryRow(`INSERT INTO events(event_type,start_time,diva_continuous)
		VALUES('diva',to_timestamp($1),true) RETURNING id`, event.StartTime).Scan(&event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_event_lifecycle(mode,event_id) VALUES(-1,$1)`, event.ID); err != nil {
		t.Fatal(err)
	}
	var id uint32
	if err = db.Get(&id, `SELECT id FROM shop_items WHERE shop_type=8 AND shop_id=7 AND item_id=2209`); err != nil {
		t.Fatal(err)
	}
	return r, db, char, event, now, id
}

func TestRepoDivaShopRoundResetAndOldReceipts(t *testing.T) {
	r, db, char, event, now, id := divaShopFixture(t)
	buy := func(at time.Time, count uint32) error {
		return r.recordExchangeShopPurchasesAt(char, []exchangeShopPurchase{{id, count}}, -1, at)
	}
	used := func(at time.Time, want uint16) {
		t.Helper()
		items, err := r.getDivaShopItemsAt(7, char, -1, at)
		if err != nil || len(items) != 131 {
			t.Fatalf("catalog=%d err=%v", len(items), err)
		}
		for _, item := range items {
			if item.ID == id {
				if item.UsedQuantity != want || item.MaxQuantity != 5 {
					t.Fatalf("used=%d want=%d item=%+v", item.UsedQuantity, want, item)
				}
				return
			}
		}
		t.Fatal("missing old product hash")
	}
	if err := buy(now, 5); err != nil {
		t.Fatal(err)
	}
	used(now, 5)
	if err := buy(now, 1); !errors.Is(err, errDivaShopUnavailable) {
		t.Fatal("exhausted purchase accepted", err)
	}
	// Restart/reconnect and the Monday calendar boundary cannot reset a round.
	r = NewShopRepository(db)
	used(now.Add(4*24*time.Hour), 5)
	// Three weeks later, the normal lifecycle creates a new event. The old
	// receipt is retained while the new event begins with zero uses.
	next := now.Add(3 * 7 * 24 * time.Hour)
	used(next, 0)
	if err := buy(next, 2); err != nil {
		t.Fatal(err)
	}
	used(next, 2)
	var old int
	if err := db.Get(&old, `SELECT bought FROM diva_shop_purchases WHERE event_id=$1 AND char_id=$2 AND shop_item_id=$3`, event.ID, char, id); err != nil || old != 5 {
		t.Fatal("old receipt changed", old, err)
	}
}

func TestRepoDivaShopAtomicBatchAndConcurrentLimit(t *testing.T) {
	r, db, char, _, now, id := divaShopFixture(t)
	// Another shop's legacy cumulative counter must roll back together with a
	// rejected Diva batch, and must not adopt the Diva round's reset policy.
	const ordinary = 900001
	if _, err := db.Exec(`INSERT INTO shop_items(id,shop_type,shop_id,item_id,cost,quantity,min_hr,min_sr,min_gr,store_level,max_quantity,road_floors,road_fatalis)
		VALUES($1,4,0,100,10,1,0,0,0,0,0,0,0)`, ordinary); err != nil {
		t.Fatal(err)
	}
	batch := []exchangeShopPurchase{{ordinary, 3}, {id, 3}, {id, 3}}
	if err := r.recordExchangeShopPurchasesAt(char, batch, -1, now); !errors.Is(err, errDivaShopUnavailable) {
		t.Fatal("over-limit duplicate accepted", err)
	}
	var count int
	if err := db.Get(&count, `SELECT (SELECT count(*) FROM diva_shop_purchases)+(SELECT count(*) FROM shop_items_bought)`); err != nil || count != 0 {
		t.Fatal("partial batch persisted", count, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.recordExchangeShopPurchasesAt(char, []exchangeShopPurchase{{id, 1}}, -1, now)
		}()
	}
	wg.Wait()
	close(results)
	var success, refused int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errDivaShopUnavailable) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if success != 5 || refused != 11 {
		t.Fatal("concurrent cap", success, refused)
	}
	if err := db.Get(&count, `SELECT bought FROM diva_shop_purchases WHERE char_id=$1 AND shop_item_id=$2`, char, id); err != nil || count != 5 {
		t.Fatal("concurrent receipt", count, err)
	}
	for _, p := range []exchangeShopPurchase{{ordinary, 4}, {ordinary, 2}} {
		if err := r.RecordPurchase(char, p.ItemHash, p.Quantity); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Get(&count, `SELECT bought FROM shop_items_bought WHERE character_id=$1 AND shop_item_id=$2`, char, ordinary); err != nil || count != 6 {
		t.Fatal("ordinary cumulative behavior changed", count, err)
	}
}

func TestRepoDivaShopRejectsNonBattleAndOtherCharacterIsIndependent(t *testing.T) {
	r, db, char, event, now, id := divaShopFixture(t)
	for _, at := range []time.Time{event.phaseTime(1).Add(-time.Second), event.phaseTime(3)} {
		if err := r.recordExchangeShopPurchasesAt(char, []exchangeShopPurchase{{id, 1}}, -1, at); !errors.Is(err, errDivaShopUnavailable) {
			t.Fatal("outside battle phase", at, err)
		}
	}
	if err := r.recordExchangeShopPurchasesAt(char, []exchangeShopPurchase{{id, 5}}, -1, now); err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "other_diva_shop")
	other := CreateTestCharacter(t, db, user, "OtherDivaShop")
	if err := r.recordExchangeShopPurchasesAt(other, []exchangeShopPurchase{{id, 5}}, -1, now); err != nil {
		t.Fatal("other character shares limit", err)
	}
}
