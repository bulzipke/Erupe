package channelserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type capturedCoinBundle struct {
	CampaignID   uint32 `json:"campaign_id"`
	RewardType   int    `json:"reward_type"`
	ReceiveType  int    `json:"receive_type"`
	Stamps       int    `json:"stamps"`
	Price        int    `json:"price_gold_g"`
	CategoryID   int    `json:"category_id"`
	CategoryType int    `json:"category_type"`
	Rewards      []struct {
		ID       int `json:"reward_id"`
		ItemType int `json:"item_type"`
		ItemID   int `json:"item_id"`
		Quantity int `json:"quantity"`
	} `json:"rewards"`
}

// Clone catalog data into an owned schema, with synthetic users and a relative
// test window. No SetupTestDB, public reset, game inventory or production DB.
func setupCapturedCoinBundle(t *testing.T) (*sqlx.DB, capturedCoinBundle) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("bundle fixture requires isolated localhost:5433/erupe_test")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=3", c.Host, c.Port, c.User, c.Password, c.DBName)
	admin, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot initialize isolated bundle fixture")
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		if os.Getenv("ERUPE_KIT_BUNDLE_DB_REQUIRED") == "1" {
			t.Fatal("required isolated bundle database is unavailable")
		}
		t.Skip("isolated localhost:5433/erupe_test is unavailable")
	}
	var database, address string
	var port int
	if err := admin.QueryRow(`SELECT current_database(),host(inet_server_addr()),inet_server_port()`).Scan(&database, &address, &port); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if database != "erupe_test" || address != "127.0.0.1" || port != 5433 {
		_ = admin.Close()
		t.Fatal("unexpected isolated bundle database identity")
	}
	schema := fmt.Sprintf("quest_kit_bundle_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("bundle fixture cleanup: %v", err)
		}
		_ = admin.Close()
	})
	db, err := sqlx.Open("postgres", dsn+" search_path="+schema+" application_name="+schema)
	if err != nil {
		t.Fatal("cannot open isolated bundle schema")
	}
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE users(id integer PRIMARY KEY,gacha_premium integer,gacha_trial integer);
		CREATE TABLE characters(id integer PRIMARY KEY,user_id integer REFERENCES users(id),deleted boolean NOT NULL DEFAULT false);
		CREATE TABLE campaigns(id integer PRIMARY KEY,reward_type integer,receive_type integer,stamps integer,start_time timestamptz,end_time timestamptz);
		CREATE TABLE campaign_categories(id integer PRIMARY KEY,type integer);
		CREATE TABLE campaign_category_links(id serial PRIMARY KEY,campaign_id integer REFERENCES campaigns(id),category_id integer REFERENCES campaign_categories(id));
		CREATE TABLE campaign_rewards(id serial PRIMARY KEY,campaign_id integer REFERENCES campaigns(id),item_type integer,quantity integer,item_id integer);
		CREATE TABLE campaign_state(character_id integer,campaign_id integer);
		CREATE TABLE campaign_rewards_claimed(character_id integer,reward_id integer)`)
	migration, err := os.ReadFile(filepath.Join("..", "migrations", "sql", "0086_campaign_coin_purchase.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "special_hunting_kit_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture capturedCoinBundle
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.CampaignID != 1900000076 || fixture.Price != 5 || len(fixture.Rewards) != 6 {
		t.Fatal("unexpected captured bundle fixture")
	}
	db.MustExec(`INSERT INTO users VALUES(11,15,77); INSERT INTO characters VALUES(21,11,false)`)
	db.MustExec(`INSERT INTO campaigns VALUES($1,$2,$3,$4,now()-interval '1 day',now()+interval '1 day')`,
		fixture.CampaignID, fixture.RewardType, fixture.ReceiveType, fixture.Stamps)
	db.MustExec(`INSERT INTO campaign_categories VALUES($1,$2)`, fixture.CategoryID, fixture.CategoryType)
	db.MustExec(`INSERT INTO campaign_category_links(campaign_id,category_id) VALUES($1,$2)`, fixture.CampaignID, fixture.CategoryID)
	db.MustExec(`INSERT INTO campaign_coin_prices VALUES($1,$2)`, fixture.CampaignID, fixture.Price)
	for _, item := range fixture.Rewards {
		db.MustExec(`INSERT INTO campaign_rewards(id,campaign_id,item_type,quantity,item_id) VALUES($1,$2,$3,$4,$5)`,
			item.ID, fixture.CampaignID, item.ItemType, item.Quantity, item.ItemID)
	}
	return db, fixture
}

func checkCapturedCoinBundleState(t *testing.T, db *sqlx.DB, balance, purchases, receipts, unclaimed int) {
	t.Helper()
	var gotBalance, trial, gotPurchases, gotReceipts, gotUnclaimed int
	if err := db.QueryRow(`SELECT gacha_premium,gacha_trial,(SELECT count(*) FROM campaign_coin_purchases),
		(SELECT count(*) FROM campaign_coin_rewards),(SELECT count(*) FROM campaign_coin_rewards WHERE NOT claimed)
		FROM users WHERE id=11`).Scan(&gotBalance, &trial, &gotPurchases, &gotReceipts, &gotUnclaimed); err != nil {
		t.Fatal(err)
	}
	if gotBalance != balance || trial != 77 || gotPurchases != purchases || gotReceipts != receipts || gotUnclaimed != unclaimed {
		t.Fatalf("balance/trial/purchases/receipts/pending=%d/%d/%d/%d/%d, want %d/77/%d/%d/%d", gotBalance, trial, gotPurchases, gotReceipts, gotUnclaimed, balance, purchases, receipts, unclaimed)
	}
}

func TestCampaignCoinIsolatedSpecialHuntingBundleFlow(t *testing.T) {
	db, fixture := setupCapturedCoinBundle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := campaignCoinPreview(ctx, db, 11, 21, fixture.CampaignID)
	if err != nil || len(items) != len(fixture.Rewards) {
		t.Fatalf("six-row preview: %v %v", items, err)
	}
	for i, item := range items {
		want := fixture.Rewards[i]
		if int(item.ItemID) != want.ItemID || int(item.Quantity) != want.Quantity || int(item.Price) != fixture.Price {
			t.Fatalf("preview omitted or changed row%d", i)
		}
	}
	bf := byteframe.NewByteFrame()
	if err := writeCampaignPreview(bf, items); err != nil {
		t.Fatal(err)
	}
	preview := bf.Data()
	if len(preview) != 32 || binary.BigEndian.Uint32(preview) != campaignPreviewMagic || binary.BigEndian.Uint16(preview[4:]) != 5 || binary.BigEndian.Uint16(preview[6:]) != 6 {
		t.Fatal("preview must price the entire six-row bundle at5G")
	}
	const first = "!0005ABCDEFGHIJK"
	const second = "!0005KLMNOPQRSTU"
	buy := func(code string, want uint32) {
		t.Helper()
		got, err := purchaseCampaignCoins(ctx, db, 11, 21, fixture.CampaignID, code)
		if err != nil || got != want {
			t.Fatalf("purchase status%x, want%x: %v", got, want, err)
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := purchaseCampaignCoins(ctx, db, 11, 21, fixture.CampaignID, first)
			if err != nil || status != campaignCoinOK {
				errors <- fmt.Errorf("same-code retry status%x: %v", status, err)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	checkCapturedCoinBundleState(t, db, 10, 1, 6, 6)
	rewards, err := campaignCoinItems(db, 21, fixture.CampaignID)
	if err != nil || len(rewards) != 6 {
		t.Fatalf("pending rewards: %v %v", rewards, err)
	}
	ids := make([]uint32, len(rewards))
	for i, reward := range rewards {
		want := fixture.Rewards[i]
		if reward.ID < campaignCoinRewardBase || reward.ItemType != 7 || int(reward.ItemID) != want.ItemID || int(reward.Quantity) != want.Quantity || reward.Deadline.Unix() != 0 {
			t.Fatalf("receipt%d does not preserve the captured item row", i)
		}
		ids[i] = reward.ID
	}
	if err := acquireCampaignCoins(ctx, db, 11, 21, []uint32{ids[0], campaignCoinRewardBase + 999}); err == nil {
		t.Fatal("mixed valid/unknown claim should roll back every row")
	}
	checkCapturedCoinBundleState(t, db, 10, 1, 6, 6)
	if err := acquireCampaignCoins(ctx, db, 11, 21, ids[:1]); err != nil {
		t.Fatal(err)
	}
	buy(second, campaignCoinPending)
	checkCapturedCoinBundleState(t, db, 10, 1, 6, 5)
	if err := acquireCampaignCoins(ctx, db, 11, 21, ids[1:]); err != nil {
		t.Fatal(err)
	}
	buy(first, campaignCoinOK)
	checkCapturedCoinBundleState(t, db, 10, 1, 6, 0)
	buy(second, campaignCoinOK)
	checkCapturedCoinBundleState(t, db, 5, 2, 12, 6)
	if err := acquireCampaignCoins(ctx, db, 11, 21, ids); err != nil {
		t.Fatal(err)
	}
	checkCapturedCoinBundleState(t, db, 5, 2, 12, 6)
	newRewards, err := campaignCoinItems(db, 21, fixture.CampaignID)
	if err != nil || len(newRewards) != 6 {
		t.Fatal("old receipt replay changed the later purchase")
	}
	for i, reward := range newRewards {
		if reward.ID == ids[i] {
			t.Fatal("repeat purchase reused a receipt")
		}
		ids[i] = reward.ID
	}
	db.MustExec(`UPDATE campaigns SET end_time=now()-interval '1 second' WHERE id=$1`, fixture.CampaignID)
	if _, err := campaignCoinPreview(ctx, db, 11, 21, fixture.CampaignID); err == nil {
		t.Fatal("expired bundle preview should be unavailable")
	}
	buy("!0005VWXYZ012345", campaignCoinUnavailable)
	buy(second, campaignCoinOK)
	if err := acquireCampaignCoins(ctx, db, 11, 21, ids); err != nil {
		t.Fatal("direct pending collection should retain the existing receipt after expiry")
	}
	checkCapturedCoinBundleState(t, db, 5, 2, 12, 0)
}

func TestCampaignCoinIsolatedSpecialHuntingBundleRejectsInvalidMember(t *testing.T) {
	for _, tc := range []struct {
		name, column string
		value        int
	}{
		{"quest_in_item_bundle", "item_type", 9},
		{"zero_quantity", "quantity", 0},
		{"quantity_overflow", "quantity", 65536},
		{"zero_item", "item_id", 0},
		{"item_overflow", "item_id", 65536},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, fixture := setupCapturedCoinBundle(t)
			// The column comes exclusively from this closed test table.
			db.MustExec(`UPDATE campaign_rewards SET `+pq.QuoteIdentifier(tc.column)+`=$1 WHERE id=2671`, tc.value)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := campaignCoinPreview(ctx, db, 11, 21, fixture.CampaignID); err == nil {
				t.Fatal("one invalid member must reject the entire preview")
			}
			status, err := purchaseCampaignCoins(ctx, db, 11, 21, fixture.CampaignID, "!0005ABCDEFGHIJK")
			if err != nil || status != campaignCoinUnavailable {
				t.Fatalf("invalid bundle status%x: %v", status, err)
			}
			checkCapturedCoinBundleState(t, db, 15, 0, 0, 0)
		})
	}
}
