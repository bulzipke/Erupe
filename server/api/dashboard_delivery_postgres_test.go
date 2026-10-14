package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
)

func newDeliveryPostgresServer(t *testing.T) *APIServer {
	t.Helper()
	dsn := os.Getenv("DASHBOARD_DELIVERY_TEST_DSN")
	if dsn == "" {
		t.Skip("DASHBOARD_DELIVERY_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// All fixture data is session-local temporary tables, never production rows.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TEMP TABLE users (id integer PRIMARY KEY,gacha_premium integer,gacha_trial integer);
		INSERT INTO users VALUES (1,100,50),(2,500,0);
		CREATE TEMP TABLE sign_sessions (user_id integer,char_id integer,server_id integer,
			token text,last_used_at timestamptz);
		CREATE TEMP TABLE characters (id integer PRIMARY KEY,name text,user_id integer DEFAULT 1,
			savedata bytea DEFAULT '\x0102'::bytea,
		deleted boolean NOT NULL DEFAULT false,is_new_character boolean NOT NULL DEFAULT false);
		CREATE TEMP TABLE mail (id serial PRIMARY KEY,sender_id integer NOT NULL REFERENCES characters(id),
		recipient_id integer NOT NULL REFERENCES characters(id),subject text NOT NULL,body text NOT NULL,
		attached_item integer,attached_item_amount integer NOT NULL,is_guild_invite boolean NOT NULL DEFAULT false,
		is_sys_message boolean NOT NULL DEFAULT false,deleted boolean NOT NULL DEFAULT false,created_at timestamptz DEFAULT now());
		INSERT INTO characters (id,name) VALUES (10,'테스트'),(11,'테스트'),(12,'테스트%_'),(13,'새 캐릭터'),(14,'삭제됨');
		UPDATE characters SET is_new_character=true WHERE id=13;
		UPDATE characters SET deleted=true WHERE id=14;`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/sql/0079_dashboard_delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.Replace(string(migration), "CREATE TABLE dashboard_item_deliveries", "CREATE TEMP TABLE dashboard_item_deliveries", 1)); err != nil {
		t.Fatal(err)
	}
	migration, err = os.ReadFile("../migrations/sql/0081_dashboard_currency_delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	server := newOperatorChatServer()
	server.db = db
	server.deliveryCatalog.fetch = func(context.Context) ([]dashboardDeliveryItem, error) {
		return []dashboardDeliveryItem{{ID: 7, Hex: "0007", Name: "회복약"}}, nil
	}
	return server
}

func postDeliveryTest(server *APIServer, request dashboardDeliveryRequest) *httptest.ResponseRecorder {
	data, _ := json.Marshal(request)
	r := authorizeDashboard(httptest.NewRequest(http.MethodPost, "/api/dashboard/delivery", strings.NewReader(string(data))), server)
	r.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.DashboardDelivery(response, r)
	return response
}

func deliveryTestCounts(t *testing.T, server *APIServer) (int, int) {
	t.Helper()
	var mails, receipts int
	if err := server.db.QueryRow(`SELECT COUNT(*) FROM mail`).Scan(&mails); err != nil {
		t.Fatal(err)
	}
	if err := server.db.QueryRow(`SELECT COUNT(*) FROM dashboard_item_deliveries`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	return mails, receipts
}

func TestDashboardDeliveryPostgresGrantAndDurableReplay(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	notifications := 0
	server.SetDeliveryMailNotifier(func(id uint32) {
		if id != 10 {
			t.Error(id)
		}
		notifications++
	})
	request := testDeliveryRequest()
	first := postDeliveryTest(server, request)
	if first.Code != 201 {
		t.Fatal(first.Code, first.Body.String())
	}
	var receipt dashboardDeliveryReceipt
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.MailCount != 3 || receipt.Quantity != 199 || receipt.Repeated {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	var amounts []int
	if err := server.db.Select(&amounts, `SELECT attached_item_amount FROM mail ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	if len(amounts) != 3 || amounts[0] != 99 || amounts[1] != 99 || amounts[2] != 1 {
		t.Fatal(amounts)
	}
	var invalid int
	if err := server.db.QueryRow(`SELECT COUNT(*) FROM mail WHERE sender_id IS NOT NULL OR attached_item<>7 OR NOT is_sys_message`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatal(invalid, err)
	}
	// Simulate a restarted API with an empty catalog and an unavailable upstream.
	restarted := newOperatorChatServer()
	restarted.db = server.db
	restarted.deliveryCatalog.fetch = func(context.Context) ([]dashboardDeliveryItem, error) {
		t.Fatal("replay fetched upstream")
		return nil, errors.New("down")
	}
	retry := postDeliveryTest(restarted, request)
	if retry.Code != 200 || !strings.Contains(retry.Body.String(), `"alreadyDelivered":true`) {
		t.Fatal(retry.Code, retry.Body.String())
	}
	if mails, receipts := deliveryTestCounts(t, server); mails != 3 || receipts != 1 || notifications != 1 {
		t.Fatal(mails, receipts, notifications)
	}
	request.Quantity++
	if conflict := postDeliveryTest(server, request); conflict.Code != 409 {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
}

func TestDashboardDeliveryPostgresConcurrentRequests(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	var wait sync.WaitGroup
	for i := 0; i < 5; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := postDeliveryTest(server, testDeliveryRequest())
			if response.Code != 200 && response.Code != 201 {
				t.Error(response.Code, response.Body.String())
			}
		}()
	}
	wait.Wait()
	if mails, receipts := deliveryTestCounts(t, server); mails != 3 || receipts != 1 {
		t.Fatal(mails, receipts)
	}
}

func TestDashboardDeliveryPostgresRejectsWithoutPartialMail(t *testing.T) {
	for _, scenario := range []string{"full", "renamed", "deleted", "new", "invalid item", "changed translation", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			server := newDeliveryPostgresServer(t)
			request := testDeliveryRequest()
			status, beforeMails := 409, 0
			switch scenario {
			case "full":
				beforeMails = 30
				_, err := server.db.Exec(`INSERT INTO mail (sender_id,recipient_id,subject,body,attached_item_amount)
					SELECT 11,10,'旧','',0 FROM generate_series(1,30)`)
				if err != nil {
					t.Fatal(err)
				}
			case "renamed":
				request.CharacterName = "다른 이름"
			case "deleted":
				request.CharacterID, request.CharacterName, request.CharacterCode = 14, "삭제됨", dashboardCharacterCode(14)
				status = 404
			case "new":
				request.CharacterID, request.CharacterName, request.CharacterCode = 13, "새 캐릭터", dashboardCharacterCode(13)
				status = 404
			case "invalid item":
				request.ItemID = 99
			case "changed translation":
				request.ItemName = "이전 번역명"
			case "write failure":
				// The third chunk fails after two successful inserts: all must roll back.
				_, err := server.db.Exec(`ALTER TABLE mail ADD CHECK (attached_item_amount>1)`)
				if err != nil {
					t.Fatal(err)
				}
				status = 503
			}
			response := postDeliveryTest(server, request)
			if response.Code != status {
				t.Fatal(response.Code, response.Body.String())
			}
			if mails, receipts := deliveryTestCounts(t, server); mails != beforeMails || receipts != 0 {
				t.Fatal("partial delivery", mails, receipts)
			}
		})
	}
}

func TestDashboardDeliveryPostgresCharacterSearch(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	for query, count := range map[string]int{"테스트": 3, "테스트%_": 1, "%": 1, "삭제됨": 0, "새 캐릭터": 0} {
		request := authorizeDashboard(httptest.NewRequest("GET", "/api/dashboard/delivery/characters?q="+urlQueryForDeliveryTest(query), nil), server)
		response := httptest.NewRecorder()
		server.DashboardDeliveryCharacters(response, request)
		var result struct {
			Characters []dashboardDeliveryCharacter `json:"characters"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || len(result.Characters) != count {
			t.Fatal(query, response.Code, response.Body.String(), err)
		}
		for _, character := range result.Characters {
			if len(character.Code) != 6 {
				t.Fatal(character)
			}
		}
	}
}

func urlQueryForDeliveryTest(value string) string {
	// Keep Unicode fixture names readable; escape only URL query separators.
	return strings.NewReplacer("%", "%25", "_", "%5F", " ", "%20").Replace(value)
}

func TestDashboardDeliveryPostgresCoinsAndReplay(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	server.deliveryCatalog.fetch = func(context.Context) ([]dashboardDeliveryItem, error) {
		t.Fatal("currency delivery must not depend on Ferias")
		return nil, errors.New("unavailable")
	}
	server.SetDeliveryMailNotifier(func(uint32) { t.Fatal("currency delivery must not notify an item mail") })
	request := testCoinDeliveryRequest()
	response := postDeliveryTest(server, request)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code, response.Body.String())
	}
	var receipt dashboardDeliveryReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Kind != dashboardDeliveryCoins ||
		receipt.MailCount != 0 || receipt.CoinBalance == nil || *receipt.CoinBalance != 299 {
		t.Fatal(receipt, err)
	}
	var premium, trial int
	if err := server.db.QueryRow(`SELECT gacha_premium,gacha_trial FROM users WHERE id=1`).Scan(&premium, &trial); err != nil || premium != 299 || trial != 50 {
		t.Fatal(premium, trial, err)
	}
	var unchanged bool
	if err := server.db.Get(&unchanged, `SELECT bool_and(savedata='\x0102'::bytea) FROM characters`); err != nil || !unchanged {
		t.Fatal("character save changed", err)
	}
	// A replay must still succeed after login and never credit the coins again.
	if _, err := server.db.Exec(`INSERT INTO sign_sessions(user_id,char_id,server_id) VALUES(1,11,1)`); err != nil {
		t.Fatal(err)
	}
	if retry := postDeliveryTest(server, request); retry.Code != 200 || !strings.Contains(retry.Body.String(), `"alreadyDelivered":true`) {
		t.Fatal(retry.Code, retry.Body.String())
	}
	if err := server.db.Get(&premium, `SELECT gacha_premium FROM users WHERE id=1`); err != nil || premium != 299 {
		t.Fatal(premium, err)
	}
	if conflict := postDeliveryTest(server, testDeliveryRequest()); conflict.Code != 409 {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
	if mails, receipts := deliveryTestCounts(t, server); mails != 0 || receipts != 1 {
		t.Fatal(mails, receipts)
	}
}

func TestDashboardDeliveryPostgresCoinsRejectAndRollback(t *testing.T) {
	for _, scenario := range []string{"online target", "online sibling", "overflow", "negative", "renamed", "deleted", "new", "receipt failure"} {
		t.Run(scenario, func(t *testing.T) {
			server := newDeliveryPostgresServer(t)
			request := testCoinDeliveryRequest()
			status, before := http.StatusConflict, int64(100)
			var err error
			switch scenario {
			case "online target":
				_, err = server.db.Exec(`INSERT INTO sign_sessions(user_id,char_id,server_id) VALUES(1,10,1)`)
			case "online sibling":
				_, err = server.db.Exec(`INSERT INTO sign_sessions(user_id,char_id,server_id) VALUES(1,11,1)`)
			case "overflow":
				before = dashboardDeliveryMaxCoins
				_, err = server.db.Exec(`UPDATE users SET gacha_premium=$1 WHERE id=1`, before)
			case "negative":
				before = -1
				_, err = server.db.Exec(`UPDATE users SET gacha_premium=-1 WHERE id=1`)
			case "renamed":
				request.CharacterName = "변경된 이름"
			case "deleted":
				request.CharacterID, request.CharacterName, request.CharacterCode = 14, "삭제됨", dashboardCharacterCode(14)
				status = http.StatusNotFound
			case "new":
				request.CharacterID, request.CharacterName, request.CharacterCode = 13, "새 캐릭터", dashboardCharacterCode(13)
				status = http.StatusNotFound
			case "receipt failure":
				_, err = server.db.Exec(`ALTER TABLE dashboard_item_deliveries ADD CHECK (quantity<>199)`)
				status = http.StatusServiceUnavailable
			}
			if err != nil {
				t.Fatal(err)
			}
			response := postDeliveryTest(server, request)
			if response.Code != status {
				t.Fatal(response.Code, response.Body.String())
			}
			var balance int64
			if err := server.db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=1`); err != nil || balance != before {
				t.Fatal("partial currency delivery", balance, before, err)
			}
			if mails, receipts := deliveryTestCounts(t, server); mails != 0 || receipts != 0 {
				t.Fatal(mails, receipts)
			}
		})
	}
}

func TestDashboardDeliveryPostgresCoinsNullBalanceAndUnboundLogin(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	if _, err := server.db.Exec(`UPDATE users SET gacha_premium=NULL WHERE id=1;
		INSERT INTO sign_sessions(user_id) VALUES(1);
		INSERT INTO sign_sessions(user_id,char_id,server_id) VALUES(2,12,1)`); err != nil {
		t.Fatal(err)
	}
	response := postDeliveryTest(server, testCoinDeliveryRequest())
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code, response.Body.String())
	}
	var balance int
	if err := server.db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=1`); err != nil || balance != 199 {
		t.Fatal(balance, err)
	}
}

func TestDashboardDeliveryPostgresCoinsConcurrentReplay(t *testing.T) {
	server := newDeliveryPostgresServer(t)
	var wait sync.WaitGroup
	for i := 0; i < 5; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := postDeliveryTest(server, testCoinDeliveryRequest())
			if response.Code != 200 && response.Code != 201 {
				t.Error(response.Code, response.Body.String())
			}
		}()
	}
	wait.Wait()
	var balance int
	if err := server.db.Get(&balance, `SELECT gacha_premium FROM users WHERE id=1`); err != nil || balance != 299 {
		t.Fatal(balance, err)
	}
	if mails, receipts := deliveryTestCounts(t, server); mails != 0 || receipts != 1 {
		t.Fatal(mails, receipts)
	}
}
