package channelserver

import (
	"errors"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestRoadShopInterceptionFlags(t *testing.T) {
	now := TimeAdjusted()
	warStart := now.Add(-time.Hour).Unix() - divaPhaseDuration - divaInterlude
	for _, test := range []struct {
		name     string
		override int
		event    DivaEvent
		err      error
		want     int
	}{
		{"disabled", 0, DivaEvent{ID: 1, StartTime: uint32(warStart)}, nil, 1},
		{"no event", -1, DivaEvent{}, nil, 1},
		{"prayer", -1, DivaEvent{ID: 1, StartTime: uint32(now.Unix())}, nil, 1},
		{"battle song", -1, DivaEvent{ID: 1, StartTime: uint32(warStart)}, nil, 4},
		{"welcome", -1, DivaEvent{ID: 1, StartTime: uint32(now.Unix() - divaPhaseDuration - divaWeekDuration - divaInterlude - 3600)}, nil, 1},
		{"lookup failure", -1, DivaEvent{}, errors.New("db failure"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.DebugOptions.DivaOverride = test.override
			server.divaRepo = &mockDivaRepo{events: []DivaEvent{test.event}, eventsErr: test.err}
			items := []ShopItem{{ItemID: 14445}, {ItemID: 13895}, {ItemID: 13896}, {ItemID: 13897}}
			got := filterRoadShopItems(createMockSession(1, server), items)
			if len(got) != test.want || got[0].ItemID != 14445 || len(items) != 4 {
				t.Fatalf("items=%v want=%d original=%v", got, test.want, items)
			}
		})
	}
}

func TestRoadShopFiltersBeforePacketLimit(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.shopRepo = &mockShopRepo{shopItems: []ShopItem{{ItemID: 13895}, {ItemID: 14445}}}
	s := createMockSession(1, server)
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 1, ShopType: 10, ShopID: 7, Limit: 1})
	bf := byteframe.NewByteFrameFromBytes(extractAckData(t, s))
	if got := bf.ReadUint16(); got != 1 {
		t.Fatalf("count=%d want=1", got)
	}
	bf.ReadUint16()
	bf.ReadUint32()
	if got := bf.ReadUint32(); got != 14445 {
		t.Fatalf("remaining product=%d want=14445", got)
	}
}

func TestRepoRoadShopWeeklyLimits(t *testing.T) {
	repo, db, charID := setupShopRepo(t)
	_, err := db.Exec(`INSERT INTO shop_items
        (id,shop_type,shop_id,item_id,cost,quantity,min_hr,min_sr,min_gr,
         store_level,max_quantity,road_floors,road_fatalis,road_weekly_limit)
        VALUES (90001,10,7,14389,500,1,0,0,1,0,46,10,0,true),
               (90002,10,8,14390,200,1,0,0,1,0,10,0,1,true),
               (90003,10,7,9958,1,1,0,0,1,0,10,0,0,false),
               (90004,8,7,14389,500,1,0,0,1,0,46,0,0,true),
               (90005,10,4,14950,1000,1,0,0,1,0,0,1,0,true)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{90001, 90002, 90003, 90004, 90005} {
		if err := repo.RecordPurchase(charID, id, 2); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordPurchase(charID, id, 3); err != nil {
			t.Fatal(err)
		}
	}
	read := func(shopType uint8, tab, id uint32, want uint16) {
		t.Helper()
		items, err := repo.GetShopItems(shopType, tab, charID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == id {
				if item.UsedQuantity != want {
					t.Fatalf("id=%d used=%d want=%d", id, item.UsedQuantity, want)
				}
				return
			}
		}
		t.Fatalf("missing shop row %d", id)
	}
	read(10, 7, 90001, 5)
	read(10, 8, 90002, 5)
	other, err := repo.GetShopItems(10, 8, charID+1)
	if err != nil || len(other) != 1 || other[0].UsedQuantity != 0 {
		t.Fatalf("another character shares receipt: items=%v err=%v", other, err)
	}
	if _, err := db.Exec("UPDATE shop_items_bought SET week_start=$1 WHERE character_id=$2", TimeWeekStart().Add(-7*24*time.Hour), charID); err != nil {
		t.Fatal(err)
	}
	read(10, 7, 90001, 0)
	read(10, 8, 90002, 0)
	read(10, 7, 90003, 5) // Custom Road row did not opt in.
	read(8, 7, 90004, 5)  // Another shop must never reset.
	read(10, 4, 90005, 5) // Unlimited decoration is not a weekly product.
	if err := repo.RecordPurchase(charID, 90001, 1); err != nil {
		t.Fatal(err)
	}
	read(10, 7, 90001, 1)
	if _, err := db.Exec("UPDATE shop_items_bought SET week_start=NULL WHERE shop_item_id=90002"); err != nil {
		t.Fatal(err)
	}
	read(10, 8, 90002, 0) // Legacy receipt from before weekly accounting.
	if err := repo.RecordPurchase(charID, 90002, 1); err != nil {
		t.Fatal(err)
	}
	read(10, 8, 90002, 1)
	if _, err := db.Exec("UPDATE shop_items_bought SET week_start=$1 WHERE shop_item_id=90001", TimeWeekStart().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := repo.RecordPurchase(charID, 90001, 1); err != nil {
				failures <- err
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	read(10, 7, 90001, 16) // Exactly one reset, with no concurrent count loss.
	// Simulate a new-week receipt followed by a delayed old-week request.
	futureWeek := TimeWeekStart().Add(7 * 24 * time.Hour)
	if _, err := db.Exec("UPDATE shop_items_bought SET week_start=$1 WHERE shop_item_id=90001", futureWeek); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordPurchase(charID, 90001, 1); err != nil {
		t.Fatal(err)
	}
	read(10, 7, 90001, 17)
	var recordedWeek time.Time
	if err := db.Get(&recordedWeek, "SELECT week_start FROM shop_items_bought WHERE character_id=$1 AND shop_item_id=90001", charID); err != nil || !recordedWeek.Equal(futureWeek) {
		t.Fatalf("receipt week rewound: got=%v want=%v err=%v", recordedWeek, futureWeek, err)
	}
}
