package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestSpecialGachaDokidokiCatalog(t *testing.T) {
	repo, db, charID := setupGachaRepo(t)
	var userID uint32
	if err := db.Get(&userID, `SELECT user_id FROM characters WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	db.MustExec(`INSERT INTO gacha_shop (id,min_gr,min_hr,name,url_banner,url_feature,url_thumbnail,wide,recommended,gacha_type,hidden)
		VALUES (65,0,0,'특전 뽑기','','','',false,false,0,false);
		INSERT INTO gacha_entries (gacha_id,entry_type,item_type,item_number,item_quantity,weight,rarity,rolls,frontier_points,daily_limit,name)
		VALUES (65,0,19,1,0,0,0,1,0,0,''),(65,1,19,10,0,0,0,11,0,0,'');`)
	for i := range 10 {
		entry := CreateTestGachaEntry(t, db, specialGachaID, 100, 1000)
		CreateTestGachaItem(t, db, entry, 7, uint16(500+i), 1)
	}
	script, err := os.ReadFile("../../tools/special_gacha_dokidoki.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatal(err)
		}
	}
	shops, err := repo.ListShop()
	if err != nil || len(shops) != 1 || shops[0].ID != specialGachaID {
		t.Fatalf("special gacha should remain a single shop: %#v, %v", shops, err)
	}
	var bundleID uint32
	if err := db.Get(&bundleID, `SELECT id FROM gacha_entries WHERE gacha_id=65 AND entry_type=100 AND name='비각인 14종 묶음'`); err != nil {
		t.Fatal(err)
	}
	pool, err := repo.GetRewardPool(specialGachaID)
	if err != nil || len(pool) != 11 {
		t.Fatalf("expected eleven prizes: %+v, %v", pool, err)
	}
	for _, prize := range pool {
		if prize.Weight != 1000 {
			t.Fatalf("prizes must keep identical weights: %+v", prize)
		}
	}

	// Confirm the three prices and the safe five-item preview at the native
	// shop-detail boundary. The underlying prize still contains 14 items.
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.gachaRepo = repo
	session := createMockSession(charID, server)
	handleMsgMhfEnumerateShop(session, &mhfpacket.MsgMhfEnumerateShop{AckHandle: 100, ShopType: 2, ShopID: specialGachaID})
	data := byteframe.NewByteFrameFromBytes(extractAckData(t, session))
	if shop, count := data.ReadUint32(), data.ReadUint16(); shop != specialGachaID || count != 14 {
		t.Fatalf("detail header %d/%d", shop, count)
	}
	type option struct {
		kind     uint8
		number   uint32
		quantity uint16
		rolls    uint8
	}
	want := map[uint8]option{0: {19, 1, 0, 1}, 1: {19, 10, 0, 11}, 2: {7, 0x3B7F, 1, 1}}
	bundlePreviews := 0
	for range 14 {
		entryType, id := data.ReadUint8(), data.ReadUint32()
		kind, number, quantity := data.ReadUint8(), data.ReadUint32(), data.ReadUint16()
		_ = data.ReadUint16() // weight
		_ = data.ReadUint8()  // rarity
		rolls, itemCount := data.ReadUint8(), data.ReadUint8()
		_ = data.ReadUint16() // frontier points
		_ = data.ReadUint8()  // daily limit
		nameBytes := data.ReadUint8()
		for range nameBytes {
			_ = data.ReadUint8()
		}
		if entryType == 100 {
			if id == bundleID {
				bundlePreviews++
				if itemCount != 5 {
					t.Fatalf("bundle preview contains %d items", itemCount)
				}
			}
		} else {
			if expected, ok := want[entryType]; !ok || expected != (option{kind, number, quantity, rolls}) {
				t.Fatalf("unexpected cost option %d: %d %d %d %d", entryType, kind, number, quantity, rolls)
			}
			delete(want, entryType)
		}
		for range itemCount {
			_ = data.ReadUint16()
			_ = data.ReadUint16()
			_ = data.ReadUint16()
		}
	}
	if len(want) != 0 || bundlePreviews != 1 {
		t.Fatalf("missing options %v or wrong number of bundle previews %d", want, bundlePreviews)
	}

	// Force the bundle in the isolated test database. This covers the maximum
	// result size, including eleven consecutive bundle wins in one purchase.
	db.MustExec(`UPDATE gacha_entries SET weight=0 WHERE gacha_id=65 AND entry_type=100 AND id<>$1`, bundleID)
	charRepo := NewCharacterRepository(db)
	svc := newTestGachaService(repo, NewUserRepository(db), charRepo)
	for rollType, rolls := range []uint16{1, 11, 1} {
		db.MustExec(`UPDATE users SET gacha_trial=1,gacha_premium=20 WHERE id=$1`, userID)
		db.MustExec(`UPDATE characters SET gacha_items=NULL WHERE id=$1`, charID)
		result, err := svc.PlayNormalGacha(userID, charID, specialGachaID, uint8(rollType))
		if err != nil || len(result.Rewards) != 14 {
			t.Fatalf("option %d: %+v, %v", rollType, result, err)
		}
		pending, err := charRepo.LoadColumn(charID, "gacha_items")
		if err != nil || len(pending) != 71 || pending[0] != 14 {
			t.Fatalf("expected fourteen queued items: %x, %v", pending, err)
		}
		for i, reward := range result.Rewards {
			if reward.ItemType != 7 || reward.ItemID != uint16(0x3BA3+i) || reward.Quantity != rolls {
				t.Fatalf("option %d gave wrong quantity or item: %+v", rollType, reward)
			}
			record := pending[1+i*gachaItemRecordBytes:]
			if record[0] != 7 || binary.BigEndian.Uint16(record[1:]) != reward.ItemID || binary.BigEndian.Uint16(record[3:]) != rolls {
				t.Fatalf("stored item differs from the draw result: %+v", reward)
			}
		}
		_, gold, trial, err := svc.userRepo.GetGachaPoints(userID)
		if err != nil || trial != []uint32{0, 1, 1}[rollType] || gold != []uint32{20, 10, 20}[rollType] {
			t.Fatalf("option %d charged the wrong account coins: %d/%d, %v", rollType, gold, trial, err)
		}
	}

	badPending := []byte{1, 7}
	if err := charRepo.SaveColumn(charID, "gacha_items", badPending); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlayNormalGacha(userID, charID, specialGachaID, 1); !errors.Is(err, errGachaItemsBlocked) {
		t.Fatalf("expected corrupt pending data to reject the draw, got %v", err)
	}
	_, gold, _, err := svc.userRepo.GetGachaPoints(userID)
	pending, loadErr := charRepo.LoadColumn(charID, "gacha_items")
	if err != nil || loadErr != nil || gold != 20 || !bytes.Equal(pending, badPending) {
		t.Fatalf("rejected draw changed gold or pending data: %d/%x, %v/%v", gold, pending, err, loadErr)
	}
}
