package channelserver

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestCampaignCoinQuote(t *testing.T) {
	for _, tt := range []struct {
		code  string
		price int
		ok    bool
	}{
		{"!0032ABCDEFGHIJK", 50, true}, {"!FFFF0123456789_", 65535, true},
		{"!0000ABCDEFGHIJK", 0, false}, {"!003XABCDEFGHIJK", 0, false},
		{"!0032ABC DEFGHIJ", 0, false}, {"!0032ABCDEFGHIJ", 0, false},
		{"RLKTABCDEFGHIJKL", 0, false},
	} {
		n, ok := campaignCoinQuote(tt.code)
		if n != tt.price || ok != tt.ok {
			t.Errorf("quote %q: %d/%v", tt.code, n, ok)
		}
	}
}

func TestCampaignCoinProtocol(t *testing.T) {
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("requires isolated localhost:5433/erupe_test")
	}
	db := SetupTestDB(t)
	u := CreateTestUser(t, db, "kit_protocol")
	char := CreateTestCharacter(t, db, u, "kitproto")
	db.MustExec(`INSERT INTO campaign_categories(id,type,title,description) VALUES(6,1,'kit','description');
	INSERT INTO campaigns VALUES(123,-1,-1,-1,-1,-1,-1,1,1,1,0,now()-interval '1 day',now()+interval '1 day','kit','reward','','TEST');
	INSERT INTO campaign_category_links(campaign_id,category_id) VALUES(123,6);
	INSERT INTO campaign_coin_prices VALUES(123,50);
	INSERT INTO campaign_rewards(campaign_id,item_type,quantity,item_id) VALUES(123,7,2,100);`)
	db.MustExec(`UPDATE users SET gacha_premium=50 WHERE id=$1`, u)
	server := createMockServer()
	server.db = db
	server.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(char, server)
	s.userID = u
	ack := func() *mhfpacket.MsgSysAck {
		t.Helper()
		p := <-s.sendPackets
		bf := byteframe.NewByteFrameFromBytes(p.data)
		bf.ReadUint16()
		a := &mhfpacket.MsgSysAck{}
		if e := a.Parse(bf, s.clientContext); e != nil {
			t.Fatal(e)
		}
		return a
	}
	handleMsgMhfEnumerateCampaign(s, &mhfpacket.MsgMhfEnumerateCampaign{AckHandle: 1})
	a := ack()
	if a.ErrorCode != 0 || len(a.AckData) < 9 || a.AckData[0] != 1 || binary.BigEndian.Uint32(a.AckData[5:9]) != 0x80000032 {
		t.Fatal("catalog price metadata", a)
	}
	request := &mhfpacket.MsgMhfApplyCampaign{AckHandle: 2, CampaignID: 123, Code: "!0032ABCDEFGHIJK"}
	handleMsgMhfApplyCampaign(s, request)
	a = ack()
	if a.ErrorCode != 0 || a.IsBufferResponse {
		t.Fatal("paid apply ACK", a)
	}
	handleMsgMhfStateCampaign(s, &mhfpacket.MsgMhfStateCampaign{AckHandle: 3, CampaignID: 123})
	a = ack()
	if a.ErrorCode != 0 || binary.BigEndian.Uint16(a.AckData[:2]) != 1 || binary.BigEndian.Uint16(a.AckData[2:4]) != 2 {
		t.Fatal("pending stamps", a)
	}
	handleMsgMhfEnumerateItem(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 4, CampaignID: 123})
	a = ack()
	if a.ErrorCode != 0 || len(a.AckData) != 22 || binary.BigEndian.Uint16(a.AckData[:2]) != 1 {
		t.Fatal("purchase reward format", a)
	}
	id := binary.BigEndian.Uint32(a.AckData[2:6])
	if id < campaignCoinRewardBase {
		t.Fatal("not a per-purchase receipt")
	}
	handleMsgMhfAcquireItem(s, &mhfpacket.MsgMhfAcquireItem{AckHandle: 5, RewardIDs: []uint32{id}})
	if ack().ErrorCode != 0 {
		t.Fatal("purchase reward claim failed")
	}
	request.Code = "!0032KLMNOPQRSTU"
	handleMsgMhfApplyCampaign(s, request)
	a = ack()
	if a.ErrorCode != 1 || binary.BigEndian.Uint32(a.AckData) != campaignCoinInsufficient {
		t.Fatal("coin shortage status", a)
	}
	request.Code = "TESTABCDEFGHIJKL"
	handleMsgMhfApplyCampaign(s, request)
	if ack().ErrorCode != 1 {
		t.Fatal("priced kit bypass through legacy code")
	}
}

func TestCampaignCoinPurchaseTransactions(t *testing.T) {
	cfg := DefaultTestDBConfig()
	if (cfg.Host != "localhost" && cfg.Host != "127.0.0.1") || cfg.Port != "5433" || cfg.DBName != "erupe_test" {
		t.Fatal("requires isolated localhost:5433/erupe_test")
	}
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "kit_coin_test")
	char := CreateTestCharacter(t, db, user, "kitcoin")
	other := CreateTestUser(t, db, "kit_coin_other")
	_, err := db.Exec(`INSERT INTO campaign_categories(id,type) VALUES(6,1);
        INSERT INTO campaigns(id,stamps,receive_type,reward_type,start_time,end_time) VALUES(123,1,1,1,now()-interval '1 day',now()+interval '1 day');
        INSERT INTO campaign_category_links(campaign_id,category_id) VALUES(123,6);
        INSERT INTO campaign_coin_prices VALUES(123,50);
        INSERT INTO campaign_rewards(campaign_id,item_type,quantity,item_id) VALUES(123,7,2,100),(123,7,1,101);`)
	if err != nil {
		t.Fatal(err)
	}
	db.MustExec(`UPDATE users SET gacha_premium=150,gacha_trial=77 WHERE id=$1`, user)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	buy := func(code string, want uint32) {
		t.Helper()
		got, e := purchaseCampaignCoins(ctx, db, user, char, 123, code)
		if e != nil || got != want {
			t.Fatalf("buy %s: %x want %x (%v)", code, got, want, e)
		}
	}
	buy("!0033ABCDEFGHIJK", campaignCoinPriceChanged)
	got, e := purchaseCampaignCoins(ctx, db, other, char, 123, "!0032ABCDEFGHIJK")
	if e == nil || got == campaignCoinOK {
		t.Fatal("wrong owner purchase accepted")
	}
	const first = "!0032ABCDEFGHIJK"
	// Simultaneous retries deduct exactly once, across independent DB connections.
	var wg sync.WaitGroup
	fail := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := purchaseCampaignCoins(ctx, db, user, char, 123, first)
			if e != nil || s != campaignCoinOK {
				fail <- fmt.Sprint(s, e)
			}
		}()
	}
	wg.Wait()
	close(fail)
	for f := range fail {
		t.Fatal(f)
	}
	var balance, trial, count int
	db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=$1`, user)
	db.Get(&trial, `SELECT gacha_trial FROM users WHERE id=$1`, user)
	db.Get(&count, `SELECT count(*) FROM campaign_coin_purchases`)
	if balance != 100 || trial != 77 || count != 1 {
		t.Fatalf("balance/trial/purchases %d/%d/%d", balance, trial, count)
	}
	firstItems, err := campaignCoinItems(db, char, 123)
	if err != nil || len(firstItems) != 2 {
		t.Fatal(firstItems, err)
	}
	buy("!0032KLMNOPQRSTU", campaignCoinPending)
	ids := []uint32{firstItems[0].ID, firstItems[1].ID}
	if err = acquireCampaignCoins(ctx, db, other, char, ids); err == nil {
		t.Fatal("wrong owner claim accepted")
	}
	if err = acquireCampaignCoins(ctx, db, user, char, []uint32{ids[0], campaignCoinRewardBase + 999}); err == nil {
		t.Fatal("invalid receipt accepted")
	}
	items, _ := campaignCoinItems(db, char, 123)
	if len(items) != 2 {
		t.Fatal("partial invalid claim committed")
	}
	if err = acquireCampaignCoins(ctx, db, user, char, ids); err != nil {
		t.Fatal(err)
	}
	buy("!0032KLMNOPQRSTU", campaignCoinOK) // Deliberate repeat, not replay.
	secondItems, _ := campaignCoinItems(db, char, 123)
	if len(secondItems) != 2 || secondItems[0].ID == ids[0] {
		t.Fatal("repeat did not create new receipts", secondItems)
	}
	if err = acquireCampaignCoins(ctx, db, user, char, ids); err != nil {
		t.Fatal(err)
	}
	items, _ = campaignCoinItems(db, char, 123)
	if len(items) != 2 {
		t.Fatal("late old ACK consumed new rewards")
	}
	if err = acquireCampaignCoins(ctx, db, user, char, []uint32{items[0].ID, items[1].ID}); err != nil {
		t.Fatal(err)
	}
	buy(first, campaignCoinOK) // Replay after receipt completion also remains free.
	buy("!0032VWXYZ012345", campaignCoinOK)
	third, _ := campaignCoinItems(db, char, 123)
	if err = acquireCampaignCoins(ctx, db, user, char, []uint32{third[0].ID, third[1].ID}); err != nil {
		t.Fatal(err)
	}
	buy("!00326789abcdefg", campaignCoinInsufficient)
	db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=$1`, user)
	if balance != 0 {
		t.Fatal(balance)
	}
	db.MustExec(`UPDATE campaigns SET end_time=now()-interval '1 second' WHERE id=123`)
	buy("!0032expired1234", campaignCoinUnavailable)
}
