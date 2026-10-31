package api

import (
	"context"
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"os"
	"strings"
	"testing"
)

func TestDashboardShopPopularityJSON(t *testing.T) {
	rankings := emptyDashboardRankings()
	rankings.PopularGachas = append(rankings.PopularGachas, ProductPopularityEntry{ID: 7, Name: "뽑기", Count: 12345})
	b, err := json.Marshal(rankings)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Gachas []ProductPopularityEntry `json:"popularGachas"`
		Kits   []ProductPopularityEntry `json:"popularKits"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Gachas) != 1 || got.Gachas[0].Count != 12345 || got.Kits == nil {
		t.Fatalf("JSON=%s", b)
	}
}

// All SQL writes in this fixture create session-local temporary tables.
func TestDashboardShopPopularityPostgres(t *testing.T) {
	dsn := os.Getenv("DASHBOARD_SHOP_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_SHOP_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	migration, err := os.ReadFile("../migrations/sql/0094_gacha_play_totals.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.Replace(string(migration), "CREATE TABLE gacha_play_totals", "CREATE TEMP TABLE gacha_play_totals", 1)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TEMP TABLE gacha_shop(id integer PRIMARY KEY,name text);
 CREATE TEMP TABLE campaigns(id integer PRIMARY KEY,title text);
 CREATE TEMP TABLE campaign_coin_purchases(id bigint PRIMARY KEY,campaign_id integer,user_id integer);
 CREATE TEMP TABLE campaign_rewards(campaign_id integer,item_id integer);
 INSERT INTO gacha_shop VALUES(2,'<b>뽑기</b>'),(3,' '),(4,'D'),(5,'E'),(6,'F'),(7,'G');
 INSERT INTO gacha_play_totals(gacha_id,play_count,draw_count) VALUES(1,8,80),(2,8,80),(3,7,70),(4,2,20),(5,1,11),(6,1,10),(7,1,10);
 INSERT INTO campaigns VALUES(10,'키트 A'),(20,'키트 B'),(30,' ');
 INSERT INTO campaign_coin_purchases VALUES(1,10,1),(2,10,1),(3,10,2),(4,20,1),(5,20,2),(6,20,3),(7,30,1);
 INSERT INTO campaign_rewards VALUES(10,100),(10,200),(10,300);`)
	if err != nil {
		t.Fatal(err)
	}
	server := &APIServer{db: db}
	rankings := emptyDashboardRankings()
	if err := server.loadDashboardShopRankings(context.Background(), &rankings); err != nil {
		t.Fatal(err)
	}
	if len(rankings.PopularGachas) != 7 || rankings.PopularGachas[0].ID != 1 || rankings.PopularGachas[1].ID != 2 || rankings.PopularGachas[0].Count != 80 {
		t.Fatalf("gachas=%+v", rankings.PopularGachas)
	}
	if rankings.PopularGachas[0].Name != "뽑기 #1" || rankings.PopularGachas[2].Name != "뽑기 #3" {
		t.Fatal("missing/blank name fallback lost")
	}
	if len(rankings.PopularKits) != 3 || rankings.PopularKits[0].ID != 10 || rankings.PopularKits[0].Count != 3 || rankings.PopularKits[1].ID != 20 || rankings.PopularKits[2].Name != "키트 #30" {
		t.Fatalf("kits=%+v", rankings.PopularKits)
	}
}
