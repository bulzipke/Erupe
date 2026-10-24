package channelserver

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

// This fixture has its own schema and never runs SetupTestDB's public-schema
// reset. A concurrent subsystem's tests cannot erase it by truncating public.
func setupCampaignCatalogRepository(t *testing.T) (*CampaignCatalogRepository, *sqlx.DB) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("catalog fixture requires isolated localhost:5433/erupe_test")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=3", c.Host, c.Port, c.User, c.Password, c.DBName)
	admin, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot initialize isolated catalog fixture")
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Skip("isolated localhost:5433/erupe_test is unavailable")
	}
	schema := fmt.Sprintf("quest_campaign_catalog_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("catalog fixture cleanup: %v", err)
		}
		_ = admin.Close()
	})
	db, err := sqlx.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal("cannot open isolated catalog schema")
	}
	t.Cleanup(func() { _ = db.Close() })
	// The guarded view proves that every production SELECT runs inside the
	// read-only repeatable-read transaction, including the first price lookup.
	db.MustExec(`CREATE TABLE campaigns (
		id integer PRIMARY KEY, min_hr smallint NOT NULL DEFAULT -1, max_hr smallint NOT NULL DEFAULT -1,
		min_sr smallint NOT NULL DEFAULT -1, max_sr smallint NOT NULL DEFAULT -1,
		min_gr smallint NOT NULL DEFAULT -1, max_gr smallint NOT NULL DEFAULT -1,
		reward_type smallint NOT NULL DEFAULT 1, stamps smallint NOT NULL DEFAULT 1,
		receive_type smallint NOT NULL DEFAULT 1, background_id smallint NOT NULL DEFAULT 12,
		start_time timestamptz NOT NULL DEFAULT now()-interval '2 days',
		end_time timestamptz NOT NULL DEFAULT now()-interval '1 day',
		title text NOT NULL DEFAULT '',reward text NOT NULL DEFAULT '',
		link text NOT NULL DEFAULT '',code_prefix text NOT NULL DEFAULT '');
		CREATE TABLE catalog_categories (id integer PRIMARY KEY,type integer,title text DEFAULT '',description text DEFAULT '');
		CREATE FUNCTION require_catalog_snapshot() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
		BEGIN
		  IF current_setting('transaction_read_only')<>'on' OR current_setting('transaction_isolation')<>'repeatable read' THEN
		    RAISE EXCEPTION 'catalog read requires a consistent read-only transaction';
		  END IF;
		  RETURN true;
		END $$;
		CREATE VIEW campaign_categories AS SELECT * FROM catalog_categories WHERE require_catalog_snapshot();
		CREATE TABLE campaign_category_links (id serial PRIMARY KEY,campaign_id integer,category_id integer);
		CREATE TABLE campaign_coin_prices (campaign_id integer PRIMARY KEY,price integer NOT NULL);
		INSERT INTO campaigns(id,title,code_prefix) VALUES(11,'A','TEST'),(12,'B',''),(13,'C','');
		INSERT INTO catalog_categories(id,type) VALUES(1,0),(6,1),(7,1),(8,1);
		INSERT INTO campaign_category_links(campaign_id,category_id) VALUES(11,6),(12,7),(13,1),(13,8);
		INSERT INTO campaign_coin_prices(campaign_id,price) VALUES(11,10),(13,99);`)
	return NewCampaignCatalogRepository(db), db
}

func TestCampaignCatalogRepositoryConsistentReadOnlySnapshot(t *testing.T) {
	repo, db := setupCampaignCatalogRepository(t)
	// Assert the fixture actually refuses a plain read outside the transaction.
	var forbidden []CampaignCategory
	if err := db.Select(&forbidden, `SELECT id,type,title,description FROM campaign_categories`); err == nil {
		t.Fatal("fixture did not check read-only repeatable-read semantics")
	}
	snapshot, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 3 || len(snapshot.Categories) != 2 || len(snapshot.Links) != 2 {
		t.Fatal("expired or hidden/unlinked event records were dropped", snapshot)
	}
	prices := make(map[uint32]uint32)
	for _, event := range snapshot.Events {
		prices[event.ID] = event.CoinPrice
		if !event.End.Before(time.Now()) {
			t.Fatal("fixture event no longer expired")
		}
	}
	if !reflect.DeepEqual(prices, map[uint32]uint32{11: 10, 12: 0, 13: 0}) || snapshot.Categories[0].ID != 6 || snapshot.Categories[1].ID != 7 ||
		!reflect.DeepEqual(snapshot.Links, []CampaignLink{{CategoryID: 6, CampaignID: 11}, {CategoryID: 7, CampaignID: 12}}) {
		t.Fatal("legacy visibility, price scope or link order changed", snapshot)
	}
	if err := validateCampaignCatalog(snapshot); err != nil {
		t.Fatal(err)
	}
	var campaignCount, linkCount, priceCount int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM campaigns),(SELECT count(*) FROM campaign_category_links),(SELECT count(*) FROM campaign_coin_prices)`).Scan(&campaignCount, &linkCount, &priceCount); err != nil || campaignCount != 3 || linkCount != 4 || priceCount != 2 {
		t.Fatal("read mutated catalog source", campaignCount, linkCount, priceCount, err)
	}
	// A subsequent enumeration sees an operator change; snapshots are not a
	// process-wide cache. Historical expired records remain in both responses.
	db.MustExec(`UPDATE campaign_coin_prices SET price=20 WHERE campaign_id=11`)
	next, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range next.Events {
		if event.ID == 11 && event.CoinPrice != 20 {
			t.Fatal("next enumeration used the previous snapshot")
		}
	}
}

func TestCampaignCatalogRepositoryDiscardsPartialFailure(t *testing.T) {
	repo, db := setupCampaignCatalogRepository(t)
	for _, statement := range []string{"SELECT id, type, title, description FROM campaign_categories", "SELECT l.campaign_id, l.category_id"} {
		t.Run(statement, func(t *testing.T) {
			// Price lookup also joins categories/links, so dropping either table
			// would fail the FIRST SELECT. Fail only the intended later query.
			db.MustExec(fmt.Sprintf(`CREATE OR REPLACE FUNCTION require_catalog_snapshot() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
			BEGIN
			  IF starts_with(current_query(),'%s') THEN
			    RAISE EXCEPTION 'injected later catalog SELECT failure';
			  END IF;
			  RETURN true;
			END $$;`, statement))
			snapshot, err := repo.Load(context.Background())
			if err == nil || !strings.Contains(err.Error(), "injected later catalog SELECT failure") || len(snapshot.Events)+len(snapshot.Categories)+len(snapshot.Links) != 0 {
				t.Fatal("later query failure leaked a partial catalog", snapshot, err)
			}
			if err := db.Ping(); err != nil {
				t.Fatal("failed transaction was not released", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot, err := repo.Load(ctx); err == nil || len(snapshot.Events)+len(snapshot.Categories)+len(snapshot.Links) != 0 {
		t.Fatal("cancelled read returned a partial response", snapshot, err)
	}
}

func TestCampaignCatalogRepositoryUnavailable(t *testing.T) {
	for _, repo := range []*CampaignCatalogRepository{nil, NewCampaignCatalogRepository(nil)} {
		if snapshot, err := repo.Load(context.Background()); err == nil || len(snapshot.Events)+len(snapshot.Categories)+len(snapshot.Links) != 0 {
			t.Fatal("unavailable repository returned a catalog")
		}
	}
}
