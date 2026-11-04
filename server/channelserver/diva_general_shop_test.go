package channelserver

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestRepoDivaGeneralCatalogWireAndUnlimitedCounts(t *testing.T) {
	r, db, char := setupShopRepo(t)
	seed, err := os.ReadFile("../migrations/seed/DivaShops.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(seed)); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Items []struct {
			ID, Cost, Level int
			Rank            string
		}
	}
	data, err := os.ReadFile("../migrations/testdata/diva-general-official.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &evidence); err != nil || len(evidence.Items) != 172 {
		t.Fatal("official evidence", err)
	}
	expected := make(map[uint32]struct{ cost, level, hr uint32 })
	for _, row := range evidence.Items {
		hr := uint32(0)
		if row.Rank == "HR5" {
			hr = 299
		}
		expected[uint32(row.ID)] = struct{ cost, level, hr uint32 }{uint32(row.Cost), uint32(row.Level), hr}
	}
	var hash uint32
	if err = db.Get(&hash, `SELECT id FROM shop_items WHERE shop_type=8 AND shop_id=0 AND item_id=2159`); err != nil {
		t.Fatal(err)
	}
	// An unlimited product's cumulative history can exceed the wire's u16.
	// Reading it must neither overflow nor consume a limited purchase allowance.
	if _, err = db.Exec(`INSERT INTO shop_items_bought(character_id,shop_item_id,bought) VALUES($1,$2,70000)`, char, hash); err != nil {
		t.Fatal(err)
	}
	if err = r.RecordExchangeShopPurchases(char, []exchangeShopPurchase{{hash, 3}}, -1); err != nil {
		t.Fatal("ordinary purchase rejected", err)
	}
	for _, mode := range []int{-1, 0, 1, 2, 3} {
		items, err := r.getDivaShopItemsAt(0, char, mode, time.Now())
		if err != nil || len(items) != 172 {
			t.Fatalf("mode=%d catalog=%d err=%v", mode, len(items), err)
		}
		for _, item := range items {
			if item.Quantity != 1 || item.MaxQuantity != 0 || item.UsedQuantity != 0 {
				t.Fatal("unlimited product became limited", item)
			}
		}
	}
	var bought, events int
	if err = db.Get(&bought, `SELECT bought FROM shop_items_bought WHERE character_id=$1 AND shop_item_id=$2`, char, hash); err != nil || bought != 70003 {
		t.Fatal("unlimited history changed", bought, err)
	}
	if err = db.Get(&events, `SELECT count(*) FROM events WHERE event_type='diva'`); err != nil || events != 0 {
		t.Fatal("general listing unexpectedly changed lifecycle", events, err)
	}
	srv := createMockServer()
	srv.shopRepo = r
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(char, srv)
	handleMsgMhfEnumerateShop(s, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 100, ShopType: 8, ShopID: 0, Limit: 512})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 4+172*30 {
		t.Fatal("general catalog wire changed", ack.ErrorCode, len(ack.Payload))
	}
	bf := byteframe.NewByteFrameFromBytes(ack.Payload)
	if bf.ReadUint16() != 172 || bf.ReadUint16() != 172 {
		t.Fatal("empty/truncated count")
	}
	seen := make(map[uint32]bool)
	for i := 0; i < 172; i++ {
		if bf.ReadUint32() == 0 {
			t.Fatal("missing product hash")
		}
		id, cost := bf.ReadUint32(), bf.ReadUint32()
		quantity, hr, sr, gr := bf.ReadUint16(), bf.ReadUint16(), bf.ReadUint16(), bf.ReadUint16()
		unknown, level := bf.ReadUint8(), bf.ReadUint8()
		maximum, used, floors, fatalis := bf.ReadUint16(), bf.ReadUint16(), bf.ReadUint16(), bf.ReadUint16()
		want, ok := expected[id]
		if !ok || seen[id] || cost != want.cost || uint32(level) != want.level || uint32(hr) != want.hr ||
			quantity != 1 || sr != 0 || gr != 0 || unknown != 0 || maximum != 0 || used != 0 || floors != 0 || fatalis != 0 {
			t.Fatalf("official general wire row %d differs: id=%d cost=%d level=%d hr=%d", i, id, cost, level, hr)
		}
		seen[id] = true
	}
}
