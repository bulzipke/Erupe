package channelserver

import (
	"context"
	"encoding/binary"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

func TestCampaignPreviewWire(t *testing.T) {
	items := []campaignPreviewItem{{ItemID: 100, Quantity: 2, Price: 30}, {ItemID: 101, Quantity: 65535, Price: 30}}
	bf := byteframe.NewByteFrame()
	if err := writeCampaignPreview(bf, items); err != nil {
		t.Fatal(err)
	}
	b := bf.Data()
	if string(b[:4]) != "KPV1" || len(b) != 16 || binary.BigEndian.Uint16(b[4:6]) != 30 || binary.BigEndian.Uint16(b[6:8]) != 2 || binary.BigEndian.Uint16(b[8:10]) != 100 || binary.BigEndian.Uint16(b[10:12]) != 2 || binary.BigEndian.Uint16(b[14:16]) != 65535 {
		t.Fatal("preview format differs", b)
	}
	max := make([]campaignPreviewItem, campaignPreviewMaxItems)
	for i := range max {
		max[i] = items[0]
	}
	bf = byteframe.NewByteFrame()
	if err := writeCampaignPreview(bf, max); err != nil || len(bf.Data()) != 0x4000 {
		t.Fatal("native buffer boundary", err, len(bf.Data()))
	}
	for _, invalid := range [][]campaignPreviewItem{nil, append(max, items[0]), {{ItemID: 0, Quantity: 1, Price: 30}}, {{ItemID: 1, Quantity: 0, Price: 30}}, {{ItemID: 1, Quantity: 1, Price: 0}}, {items[0], {ItemID: 1, Quantity: 1, Price: 31}}} {
		bf = byteframe.NewByteFrame()
		if err := writeCampaignPreview(bf, invalid); err == nil || len(bf.Data()) != 0 {
			t.Fatal("invalid preview emitted a partial buffer", invalid)
		}
	}
}

func TestCampaignPreviewReadOnly(t *testing.T) {
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("requires isolated localhost:5433/erupe_test")
	}
	db := SetupTestDB(t)
	u := CreateTestUser(t, db, "kit_preview")
	char := CreateTestCharacter(t, db, u, "preview")
	other := CreateTestUser(t, db, "preview_other")
	db.MustExec(`INSERT INTO campaign_categories(id,type,title) VALUES(6,1,'kit');
		INSERT INTO campaigns(id,stamps,receive_type,reward_type,start_time,end_time) VALUES(123,1,1,1,now()-interval '1 day',now()+interval '1 day');
		INSERT INTO campaign_category_links(campaign_id,category_id) VALUES(123,6);
		INSERT INTO campaign_coin_prices VALUES(123,30);
		INSERT INTO campaign_rewards(campaign_id,item_type,quantity,item_id) VALUES(123,7,2,100),(123,7,1,101);`)
	db.MustExec(`UPDATE users SET gacha_premium=60 WHERE id=$1`, u)
	server := createMockServer()
	server.db = db
	s := createMockSession(char, server)
	s.userID = u
	ack := func() *mhfpacket.MsgSysAck {
		t.Helper()
		p := <-s.sendPackets
		bf := byteframe.NewByteFrameFromBytes(p.data)
		bf.ReadUint16()
		a := &mhfpacket.MsgSysAck{}
		if err := a.Parse(bf, s.clientContext); err != nil {
			t.Fatal(err)
		}
		return a
	}
	preview := func() {
		t.Helper()
		handleMsgMhfEnumerateItem(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 1, CampaignID: campaignPreviewFlag | 123})
		a := ack()
		if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.AckData) != 16 || string(a.AckData[:4]) != "KPV1" || binary.BigEndian.Uint16(a.AckData[6:8]) != 2 {
			t.Fatal("read-only preview ACK", a)
		}
	}
	preview()
	preview()
	var balance, count int
	db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=$1`, u)
	if balance != 60 {
		t.Fatal("preview spent coins")
	}
	for _, table := range []string{"campaign_state", "campaign_coin_purchases", "campaign_coin_rewards", "campaign_rewards_claimed"} {
		db.Get(&count, "SELECT count(*) FROM "+table)
		if count != 0 {
			t.Fatal("preview created an entitlement or claim", table)
		}
	}
	// Ordinary item requests still have no collectible rewards before buying.
	handleMsgMhfEnumerateItem(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 2, CampaignID: 123})
	if a := ack(); a.ErrorCode != 0 || binary.BigEndian.Uint16(a.AckData[:2]) != 0 {
		t.Fatal("preview leaked into native collectible items", a)
	}
	ctx := context.Background()
	for _, ids := range [][3]uint32{{other, char, 123}, {u, 0, 123}, {0, char, 123}, {u, char, 999}, {u, char, campaignPreviewFlag | 123}} {
		if _, err := campaignCoinPreview(ctx, db, ids[0], ids[1], ids[2]); err == nil {
			t.Fatal("invalid preview identity accepted", ids)
		}
	}
	for _, mutation := range []struct{ change, restore string }{
		{`UPDATE campaigns SET end_time=now()-interval '1 hour' WHERE id=123`, `UPDATE campaigns SET end_time=now()+interval '1 day' WHERE id=123`},
		{`UPDATE campaigns SET start_time=now()+interval '1 hour' WHERE id=123`, `UPDATE campaigns SET start_time=now()-interval '1 day' WHERE id=123`},
		{`UPDATE campaign_categories SET type=0 WHERE id=6`, `UPDATE campaign_categories SET type=1 WHERE id=6`},
		{`DELETE FROM campaign_category_links WHERE campaign_id=123`, `INSERT INTO campaign_category_links(campaign_id,category_id) VALUES(123,6)`},
		{`UPDATE campaign_rewards SET item_type=9 WHERE campaign_id=123`, `UPDATE campaign_rewards SET item_type=7 WHERE campaign_id=123`},
		{`UPDATE campaign_rewards SET quantity=0 WHERE campaign_id=123`, `UPDATE campaign_rewards SET quantity=1 WHERE campaign_id=123`},
	} {
		db.MustExec(mutation.change)
		if _, err := campaignCoinPreview(ctx, db, u, char, 123); err == nil {
			t.Fatal("unavailable/unsupported preview accepted", mutation.change)
		}
		db.MustExec(mutation.restore)
	}
	if status, err := purchaseCampaignCoins(ctx, db, u, char, 123, "!001EABCDEFGHIJK"); err != nil || status != campaignCoinOK {
		t.Fatal("native purchase regression", status, err)
	}
	paid, err := campaignCoinItems(db, char, 123)
	if err != nil || len(paid) != 2 || paid[0].ID < campaignCoinRewardBase {
		t.Fatal("purchase receipts", paid, err)
	}
	if err = acquireCampaignCoins(ctx, db, u, char, []uint32{paid[0].ID}); err != nil {
		t.Fatal(err)
	}
	preview() // Always the full catalog, not the remaining paid-reward list.
	paid, err = campaignCoinItems(db, char, 123)
	if err != nil || len(paid) != 1 {
		t.Fatal("preview changed pending reward state", paid, err)
	}
	db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=$1`, u)
	if balance != 30 {
		t.Fatal("preview after purchase spent coins", balance)
	}
}
