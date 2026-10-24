package channelserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func TestQuestLicenseKeysCanonical(t *testing.T) {
	input := []string{"visa:gothic-1", "visa.veil"}
	keys, hash, err := canonicalQuestLicenseKeys(input)
	if err != nil || len(hash) != 64 || !slices.Equal(keys, []string{"visa.veil", "visa:gothic-1"}) {
		t.Fatalf("canonical keys/hash: %v %q %v", keys, hash, err)
	}
	if !slices.Equal(input, []string{"visa:gothic-1", "visa.veil"}) {
		t.Fatal("caller catalog mutated")
	}
	_, reordered, err := canonicalQuestLicenseKeys([]string{"visa.veil", "visa:gothic-1"})
	if err != nil || reordered != hash {
		t.Fatal("input order changed quote identity")
	}
	_, different, err := canonicalQuestLicenseKeys([]string{"visa:gothic-1", "visa.other"})
	if err != nil || different == hash {
		t.Fatal("different mapping reused quote identity")
	}
	for _, invalid := range [][]string{nil, {}, {""}, {"same", "same"}, {"Upper"}, {"with space"},
		{"line\nbreak"}, {"한글"}, {".leading"}, {strings.Repeat("a", 97)}, make([]string, 513)} {
		if _, _, err := canonicalQuestLicenseKeys(invalid); err == nil {
			t.Errorf("accepted invalid keys %q", invalid)
		}
	}
}

func TestQuestLicenseRequestCode(t *testing.T) {
	for _, test := range []struct {
		code string
		want bool
	}{
		{"!0032ABCDEFGHIJK", true}, {strings.Repeat("a", 128), true},
		{"", false}, {strings.Repeat("a", 129), false}, {"white space", false},
		{"tab\t", false}, {"line\n", false}, {"nul\x00", false}, {"delete\x7f", false}, {"한글", false},
	} {
		if got := validQuestLicenseRequestCode(test.code); got != test.want {
			t.Errorf("code %q: %v want %v", test.code, got, test.want)
		}
	}
}

func setupQuestLicenseRepo(t *testing.T) (*QuestLicenseRepository, *sqlx.DB, uint32, uint32) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("requires isolated localhost:5433/erupe_test")
	}
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "quest_license_repo")
	char := CreateTestCharacter(t, db, user, "license")
	db.MustExec(`UPDATE users SET gacha_premium=200,gacha_trial=77 WHERE id=$1`, user)
	repo := NewQuestLicenseRepository(db)
	repo.now = func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) }
	return repo, db, user, char
}

func questLicenseTestPurchase(user, char uint32) (QuestLicensePurchaseRequest, QuestLicenseSale) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return QuestLicensePurchaseRequest{
			UserID: user, CharacterID: char, CampaignID: 123, RequestCode: "!0032ABCDEFGHIJK",
			QuotedPrice: 50, LicenseKeys: []string{"test.veil", "test.gothic"},
		}, QuestLicenseSale{
			CampaignID: 123, Enabled: true, Price: 50, LicenseKeys: []string{"test.gothic", "test.veil"},
			Start: now.Add(-time.Hour), End: now.Add(time.Hour),
		}
}

func assertQuestLicensePurchaseState(t *testing.T, db *sqlx.DB, user uint32, balance, receipts, licenses int) {
	t.Helper()
	var gotBalance, gotTrial, gotReceipts, gotLicenses, legacy int
	if err := db.QueryRow(`SELECT gacha_premium,gacha_trial FROM users WHERE id=$1`, user).Scan(&gotBalance, &gotTrial); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&gotReceipts, `SELECT count(*) FROM quest_license_purchases WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&gotLicenses, `SELECT count(*) FROM quest_licenses WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&legacy, `SELECT (SELECT count(*) FROM campaign_state)+
		(SELECT count(*) FROM campaign_quest)+(SELECT count(*) FROM campaign_rewards_claimed)+
		(SELECT count(*) FROM campaign_coin_purchases)+(SELECT count(*) FROM campaign_coin_rewards)`); err != nil {
		t.Fatal(err)
	}
	if gotBalance != balance || gotTrial != 77 || gotReceipts != receipts || gotLicenses != licenses || legacy != 0 {
		t.Fatalf("balance/trial/receipts/licenses/legacy=%d/%d/%d/%d/%d want %d/77/%d/%d/0",
			gotBalance, gotTrial, gotReceipts, gotLicenses, legacy, balance, receipts, licenses)
	}
}

func TestQuestLicenseRepoPurchaseReplayAndOwned(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	ctx := context.Background()
	got, err := repo.Purchase(ctx, request, sale)
	if err != nil || got.Status != QuestLicensePurchaseOK || got.Replay || got.PurchaseID <= 0 || got.BalanceAfter != 150 {
		t.Fatalf("purchase: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
	var savedKeys pq.StringArray
	var savedHash string
	if err := db.QueryRow(`SELECT license_keys,license_hash FROM quest_license_purchases WHERE id=$1`, got.PurchaseID).
		Scan(&savedKeys, &savedHash); err != nil {
		t.Fatal(err)
	}
	keys, hash, err := canonicalQuestLicenseKeys(request.LicenseKeys)
	if err != nil || !slices.Equal([]string(savedKeys), keys) || savedHash != hash {
		t.Fatal("receipt did not preserve the canonical immutable mapping")
	}

	// Retry the identical trusted snapshot after current catalog removal, a
	// new price and a new mapping: acknowledge only the committed old request.
	currentSale := QuestLicenseSale{CampaignID: 999, Enabled: false, Price: 80, LicenseKeys: []string{"new.key"}}
	request.LicenseKeys = []string{"test.gothic", "test.veil"}
	replay, err := repo.Purchase(ctx, request, currentSale)
	if err != nil || replay.Status != QuestLicensePurchaseOK || !replay.Replay || replay.PurchaseID != got.PurchaseID || replay.BalanceAfter != 150 {
		t.Fatalf("committed replay: %+v %v", replay, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)

	secondChar := CreateTestCharacter(t, db, user, "second")
	for _, test := range []struct {
		name   string
		change func(*QuestLicensePurchaseRequest)
	}{
		{"character", func(r *QuestLicensePurchaseRequest) { r.CharacterID = secondChar }},
		{"product", func(r *QuestLicensePurchaseRequest) { r.CampaignID++ }},
		{"price", func(r *QuestLicensePurchaseRequest) { r.QuotedPrice++ }},
		{"mapping", func(r *QuestLicensePurchaseRequest) { r.LicenseKeys = []string{"test.other"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			test.change(&changed)
			conflict, err := repo.Purchase(ctx, changed, sale)
			if err != nil || conflict.Status != QuestLicensePurchaseConflict || conflict.Replay {
				t.Fatalf("nonce conflict: %+v %v", conflict, err)
			}
		})
	}
	owned, err := repo.Owned(ctx, user, []string{"test.other", "test.veil", "test.gothic"})
	if err != nil || !slices.Equal(owned, keys) {
		t.Fatalf("account-owned subset: %v %v", owned, err)
	}
	other := CreateTestUser(t, db, "license_other")
	owned, err = repo.Owned(ctx, other, keys)
	if err != nil || len(owned) != 0 {
		t.Fatalf("other account inherited licenses: %v %v", owned, err)
	}
	request.RequestCode = "another.confirmation"
	request.CharacterID = secondChar
	got, err = repo.Purchase(ctx, request, sale)
	if err != nil || got.Status != QuestLicensePurchaseAlreadyOwned {
		t.Fatalf("same account second character charged again: %+v %v", got, err)
	}
	request.RequestCode = "partial.confirmation"
	request.LicenseKeys = []string{"test.gothic", "test.third"}
	sale.LicenseKeys = slices.Clone(request.LicenseKeys)
	got, err = repo.Purchase(ctx, request, sale)
	if err != nil || got.Status != QuestLicensePurchasePartialOwned {
		t.Fatalf("partial bundle must not silently charge: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)

	// Character deletion never cascades a permanent account license or its
	// historical purchase snapshot. No legacy account promotion is involved.
	db.MustExec(`DELETE FROM characters WHERE id=$1`, char)
	owned, err = repo.Owned(ctx, user, keys)
	if err != nil || !slices.Equal(owned, keys) {
		t.Fatalf("character deletion removed account licenses: %v %v", owned, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}

func TestQuestLicenseRepoRejectsBeforeDebit(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*QuestLicensePurchaseRequest, *QuestLicenseSale)
		want   QuestLicensePurchaseStatus
	}{
		{"disabled", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Enabled = false }, QuestLicensePurchaseUnavailable},
		{"not-started", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Start = s.End.Add(-time.Minute) }, QuestLicensePurchaseUnavailable},
		{"end-exclusive", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.End = s.End.Add(-time.Hour) }, QuestLicensePurchaseUnavailable},
		{"zero-start", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Start = time.Time{} }, QuestLicensePurchaseUnavailable},
		{"zero-end", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.End = time.Time{} }, QuestLicensePurchaseUnavailable},
		{"invalid-window", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.End = s.Start }, QuestLicensePurchaseUnavailable},
		{"wrong-product", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.CampaignID++ }, QuestLicensePurchaseUnavailable},
		{"sale-zero-price", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Price = 0 }, QuestLicensePurchaseUnavailable},
		{"sale-large-price", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Price = 65536 }, QuestLicensePurchaseUnavailable},
		{"request-zero-price", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.QuotedPrice = 0 }, QuestLicensePurchaseUnavailable},
		{"request-large-price", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.QuotedPrice = 65536 }, QuestLicensePurchaseUnavailable},
		{"price-changed", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.Price = 51 }, QuestLicensePurchasePriceChanged},
		{"mapping-changed", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.LicenseKeys = []string{"replacement"} }, QuestLicensePurchaseConflict},
		{"empty-mapping", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.LicenseKeys = nil }, QuestLicensePurchaseUnavailable},
		{"duplicate-mapping", func(_ *QuestLicensePurchaseRequest, s *QuestLicenseSale) { s.LicenseKeys = []string{"same", "same"} }, QuestLicensePurchaseUnavailable},
		{"bad-request-keys", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.LicenseKeys = []string{"bad key"} }, QuestLicensePurchaseUnavailable},
		{"invalid-code", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.RequestCode = "" }, QuestLicensePurchaseUnavailable},
		{"missing-user", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.UserID = 0 }, QuestLicensePurchaseUnavailable},
		{"missing-character", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.CharacterID = 0 }, QuestLicensePurchaseUnavailable},
		{"invalid-product-id", func(r *QuestLicensePurchaseRequest, _ *QuestLicenseSale) { r.CampaignID = 0xffffffff }, QuestLicensePurchaseUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, user, char := setupQuestLicenseRepo(t)
			request, sale := questLicenseTestPurchase(user, char)
			test.change(&request, &sale)
			got, err := repo.Purchase(context.Background(), request, sale)
			if err != nil || got.Status != test.want || got.PurchaseID != 0 || got.Replay {
				t.Fatalf("rejection: %+v %v want %v", got, err, test.want)
			}
			assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
		})
	}
}

func TestQuestLicenseRepoOwnershipAndShortage(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	other := CreateTestUser(t, db, "license_wrong_owner")
	foreign := CreateTestCharacter(t, db, other, "foreign")
	for _, id := range []uint32{foreign, 2147483647} {
		request.CharacterID = id
		got, err := repo.Purchase(context.Background(), request, sale)
		if err != nil || got.Status != QuestLicensePurchaseUnavailable {
			t.Fatalf("wrong/missing character: %+v %v", got, err)
		}
	}
	request.CharacterID = char
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, char)
	got, err := repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable {
		t.Fatalf("deleted character: %+v %v", got, err)
	}
	db.MustExec(`UPDATE characters SET deleted=false WHERE id=$1`, char)
	db.MustExec(`UPDATE users SET gacha_premium=49 WHERE id=$1`, user)
	got, err = repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseInsufficient {
		t.Fatalf("shortage: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 49, 0, 0)

	// Sale start is inclusive, and a minimum positive price is valid.
	sale.Start = repo.now()
	request.QuotedPrice, sale.Price = 1, 1
	got, err = repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseOK || got.BalanceAfter != 48 {
		t.Fatalf("start boundary/minimum price: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 48, 1, 2)
	request.RequestCode = "maximum.price"
	request.LicenseKeys = []string{"maximum.price"}
	sale.LicenseKeys = slices.Clone(request.LicenseKeys)
	request.QuotedPrice, sale.Price = 65535, 65535
	db.MustExec(`UPDATE users SET gacha_premium=65535 WHERE id=$1`, user)
	got, err = repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseOK || got.BalanceAfter != 0 {
		t.Fatalf("maximum uint16 price: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 0, 2, 3)
}

func TestQuestLicenseRepoRollbackIsAtomic(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	request.LicenseKeys = []string{"rollback.a", "rollback.z"}
	sale.LicenseKeys = slices.Clone(request.LicenseKeys)
	// The second grant fails after debit, receipt and first grant. The
	// transaction must remove all three writes, not just the failing grant.
	db.MustExec(`ALTER TABLE quest_licenses ADD CONSTRAINT quest_license_test_rollback CHECK (license_key <> 'rollback.z')`)
	t.Cleanup(func() {
		db.MustExec(`ALTER TABLE quest_licenses DROP CONSTRAINT IF EXISTS quest_license_test_rollback`)
	})
	got, err := repo.Purchase(context.Background(), request, sale)
	if err == nil || got.Status == QuestLicensePurchaseOK || got.PurchaseID != 0 {
		t.Fatalf("forced grant failure did not fail: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
	db.MustExec(`ALTER TABLE quest_licenses DROP CONSTRAINT quest_license_test_rollback`)
	got, err = repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseOK {
		t.Fatalf("retry after rolled-back attempt: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}

func TestQuestLicenseRepoConcurrentReplay(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type outcome struct {
		result QuestLicensePurchaseResult
		err    error
	}
	results := make(chan outcome, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := repo.Purchase(ctx, request, sale)
			results <- outcome{got, err}
		}()
	}
	workers.Wait()
	close(results)
	var purchaseID int64
	commits := 0
	for got := range results {
		if got.err != nil || got.result.Status != QuestLicensePurchaseOK {
			t.Fatalf("concurrent retry: %+v %v", got.result, got.err)
		}
		if purchaseID == 0 {
			purchaseID = got.result.PurchaseID
		}
		if got.result.PurchaseID != purchaseID || got.result.BalanceAfter != 150 {
			t.Fatalf("retries did not reuse committed receipt: %+v", got.result)
		}
		if !got.result.Replay {
			commits++
		}
	}
	if commits != 1 {
		t.Fatalf("fresh purchases=%d want 1", commits)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}

func TestQuestLicenseRepoConcurrentOverlappingProducts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			repo, db, user, first := setupQuestLicenseRepo(t)
			second := CreateTestCharacter(t, db, user, "otherchar")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results := make(chan QuestLicensePurchaseResult, 2)
			errors := make(chan error, 2)
			var workers sync.WaitGroup
			for i, char := range []uint32{first, second} {
				request, sale := questLicenseTestPurchase(user, char)
				request.CampaignID += uint32(i)
				sale.CampaignID = request.CampaignID
				request.RequestCode = fmt.Sprintf("overlapping.%d", i)
				request.LicenseKeys = []string{"shared"}
				if partial {
					request.LicenseKeys = append(request.LicenseKeys, fmt.Sprintf("exclusive.%d", i))
				}
				sale.LicenseKeys = slices.Clone(request.LicenseKeys)
				workers.Add(1)
				go func() {
					defer workers.Done()
					got, err := repo.Purchase(ctx, request, sale)
					results <- got
					errors <- err
				}()
			}
			workers.Wait()
			close(results)
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			statuses := map[QuestLicensePurchaseStatus]int{}
			for got := range results {
				statuses[got.Status]++
			}
			denied, count := QuestLicensePurchaseAlreadyOwned, 1
			if partial {
				denied, count = QuestLicensePurchasePartialOwned, 2
			}
			if statuses[QuestLicensePurchaseOK] != 1 || statuses[denied] != 1 {
				encoded, _ := json.Marshal(statuses)
				t.Fatalf("overlapping products both charged: %s", encoded)
			}
			assertQuestLicensePurchaseState(t, db, user, 150, 1, count)
		})
	}
}

func TestQuestLicenseRepoUnavailableAndValidation(t *testing.T) {
	var repo *QuestLicenseRepository
	if _, err := repo.Owned(context.Background(), 0, []string{"valid"}); err == nil {
		t.Fatal("zero owner accepted")
	}
	if got, err := repo.Owned(context.Background(), 1, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty key query should not access DB: %v %v", got, err)
	}
	if _, err := repo.Owned(context.Background(), 1, []string{"valid"}); err == nil {
		t.Fatal("nil repository accepted")
	}
	if _, err := repo.Owned(context.Background(), 1, []string{"invalid key"}); err == nil {
		t.Fatal("invalid key accepted")
	}
	request, sale := questLicenseTestPurchase(1, 1)
	if got, err := repo.Purchase(context.Background(), request, sale); err == nil || got.Status == QuestLicensePurchaseOK {
		t.Fatalf("nil repository purchase: %+v %v", got, err)
	}
}

func TestQuestLicenseRepoReplayValidation(t *testing.T) {
	var repo *QuestLicenseRepository
	for _, test := range []struct {
		name                      string
		user, character, campaign uint32
		code                      string
	}{
		{"zero-user", 0, 1, 1, "server.token"},
		{"zero-character", 1, 0, 1, "server.token"},
		{"zero-campaign", 1, 1, 0, "server.token"},
		{"large-user", 0xffffffff, 1, 1, "server.token"},
		{"large-character", 1, 0xffffffff, 1, "server.token"},
		{"large-campaign", 1, 1, 0xffffffff, "server.token"},
		{"empty-token", 1, 1, 1, ""},
		{"oversized-token", 1, 1, 1, strings.Repeat("a", 129)},
		{"token-whitespace", 1, 1, 1, "not a token"},
		{"token-control", 1, 1, 1, "token\x00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found, err := repo.Replay(context.Background(), test.user, test.character, test.campaign, test.code)
			if err != nil || found || got.Status != QuestLicensePurchaseUnavailable || got.Replay || got.PurchaseID != 0 {
				t.Fatalf("invalid replay touched repository: %+v found=%t err=%v", got, found, err)
			}
		})
	}
	for _, empty := range []*QuestLicenseRepository{nil, NewQuestLicenseRepository(nil)} {
		got, found, err := empty.Replay(context.Background(), 1, 1, 1, "server.token")
		if err == nil || found || got.Status != QuestLicensePurchaseUnavailable {
			t.Fatalf("unavailable replay: %+v found=%t err=%v", got, found, err)
		}
	}
}

func TestQuestLicenseRepoReplayAfterRestart(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	request.RequestCode = "server.issued.token.for.purchase"
	ctx := context.Background()
	purchase, err := repo.Purchase(ctx, request, sale)
	if err != nil || purchase.Status != QuestLicensePurchaseOK {
		t.Fatalf("original purchase: %+v %v", purchase, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)

	// A fresh repository has no in-memory quote or catalog. The original sale
	// and mapping are not passed to Replay at all, including after expiry or
	// product deletion. Return the receipt's balance, not today's balance.
	restarted := NewQuestLicenseRepository(db)
	db.MustExec(`UPDATE users SET gacha_premium=5 WHERE id=$1`, user)
	got, found, err := restarted.Replay(ctx, user, char, request.CampaignID, request.RequestCode)
	if err != nil || !found || got.Status != QuestLicensePurchaseOK || !got.Replay ||
		got.PurchaseID != purchase.PurchaseID || got.BalanceAfter != purchase.BalanceAfter {
		t.Fatalf("durable replay: %+v found=%t err=%v", got, found, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 5, 1, 2)

	missing, found, err := restarted.Replay(ctx, user, char, request.CampaignID, "uncommitted.server.token")
	if err != nil || found || missing.Status != QuestLicensePurchaseUnavailable || missing.Replay {
		t.Fatalf("absent token: %+v found=%t err=%v", missing, found, err)
	}
	second := CreateTestCharacter(t, db, user, "replaysecond")
	for _, test := range []struct {
		name                string
		character, campaign uint32
	}{
		{"another-character", second, request.CampaignID},
		{"another-product", char, request.CampaignID + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			conflict, found, err := restarted.Replay(ctx, user, test.character, test.campaign, request.RequestCode)
			if err != nil || !found || conflict.Status != QuestLicensePurchaseConflict || conflict.Replay || conflict.PurchaseID != 0 {
				t.Fatalf("wrong token binding: %+v found=%t err=%v", conflict, found, err)
			}
		})
	}

	other := CreateTestUser(t, db, "replay_other_user")
	foreign := CreateTestCharacter(t, db, other, "replayforeign")
	for _, test := range []struct {
		name            string
		user, character uint32
	}{
		{"foreign-character", user, foreign},
		{"other-account", other, foreign},
		{"missing-character", user, 2147483647},
		{"missing-account", 2147483647, char},
	} {
		t.Run(test.name, func(t *testing.T) {
			denied, found, err := restarted.Replay(ctx, test.user, test.character, request.CampaignID, request.RequestCode)
			if err != nil || found || denied.Status != QuestLicensePurchaseUnavailable || denied.Replay || denied.PurchaseID != 0 {
				t.Fatalf("unauthorized token lookup: %+v found=%t err=%v", denied, found, err)
			}
		})
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, char)
	denied, found, err := restarted.Replay(ctx, user, char, request.CampaignID, request.RequestCode)
	if err != nil || found || denied.Status != QuestLicensePurchaseUnavailable || denied.Replay {
		t.Fatalf("deleted-character token lookup: %+v found=%t err=%v", denied, found, err)
	}
	db.MustExec(`UPDATE characters SET deleted=false WHERE id=$1`, char)
	got, found, err = restarted.Replay(ctx, user, char, request.CampaignID, request.RequestCode)
	if err != nil || !found || got.PurchaseID != purchase.PurchaseID || !got.Replay {
		t.Fatalf("active owner replay after restore: %+v found=%t err=%v", got, found, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 5, 1, 2)
}

func TestQuestLicenseRepoConcurrentDurableReplay(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	request, sale := questLicenseTestPurchase(user, char)
	purchase, err := repo.Purchase(context.Background(), request, sale)
	if err != nil || purchase.Status != QuestLicensePurchaseOK {
		t.Fatalf("original purchase: %+v %v", purchase, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type outcome struct {
		result QuestLicensePurchaseResult
		found  bool
		err    error
	}
	results := make(chan outcome, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, found, err := NewQuestLicenseRepository(db).Replay(ctx, user, char, request.CampaignID, request.RequestCode)
			results <- outcome{got, found, err}
		}()
	}
	workers.Wait()
	close(results)
	for got := range results {
		if got.err != nil || !got.found || got.result.Status != QuestLicensePurchaseOK || !got.result.Replay ||
			got.result.PurchaseID != purchase.PurchaseID || got.result.BalanceAfter != purchase.BalanceAfter {
			t.Fatalf("concurrent durable replay: %+v found=%t err=%v", got.result, got.found, got.err)
		}
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}
