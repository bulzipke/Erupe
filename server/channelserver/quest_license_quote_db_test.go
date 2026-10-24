package channelserver

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
	"github.com/jmoiron/sqlx"
)

func quotedLicenseDBCatalog(t *testing.T, now time.Time, withProduct bool) *questlicense.Catalog {
	t.Helper()
	doc := questlicense.Document{Version: "quote-db", Licenses: []questlicense.Definition{
		{Key: "gothic", QuestIDs: []int{40079}},
		{Key: "veil", QuestIDs: []int{40142}},
	}}
	if withProduct {
		price := uint32(50)
		start, end := now.Add(-time.Hour), now.Add(time.Hour)
		doc.Products = []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price,
			StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"veil", "gothic"}}}
	}
	catalog, err := questlicense.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func quotedLicenseDBService(t *testing.T, catalog *questlicense.Catalog, store QuestLicenseStore, now time.Time) *QuestLicenseService {
	t.Helper()
	service, err := NewQuestLicenseService(true, catalog, store)
	if err != nil {
		t.Fatal(err)
	}
	service.quotes.now = func() time.Time { return now }
	return service
}

func quotedLicenseDBPreview(t *testing.T, service *QuestLicenseService, user, character uint32) QuestLicenseQuote {
	t.Helper()
	quote, available, err := service.Quote(context.Background(), user, character, 123)
	if err != nil || !available || quote.Price != 50 || quote.CampaignID != 123 ||
		!validQuestLicensePaymentCode(quote.PaymentCode) || !slices.Equal(quote.QuestIDs, []int{40079, 40142}) {
		t.Fatalf("authenticated preview: %+v available=%t err=%v", quote, available, err)
	}
	return quote
}

func TestQuestLicenseQuotedDBConcurrentCommitAndDurableReplay(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	now := repo.now()
	catalog := quotedLicenseDBCatalog(t, now, true)
	service := quotedLicenseDBService(t, catalog, repo, now)
	quote := quotedLicenseDBPreview(t, service, user, character)
	second := CreateTestCharacter(t, db, user, "quotedsecond")
	other := CreateTestUser(t, db, "quoted_other_account")
	foreign := CreateTestCharacter(t, db, other, "quotedforeign")
	modifiedPriceCode := "^0033" + quote.PaymentCode[5:]
	if !validQuestLicensePaymentCode(modifiedPriceCode) {
		t.Fatal("test must change the price while retaining a canonical token")
	}
	for _, test := range []struct {
		name                      string
		user, character, campaign uint32
		code                      string
	}{
		{"another-character", user, second, 123, quote.PaymentCode},
		{"another-account", other, foreign, 123, quote.PaymentCode},
		{"foreign-character", user, foreign, 123, quote.PaymentCode},
		{"another-product", user, character, 124, quote.PaymentCode},
		{"modified-price", user, character, 123, modifiedPriceCode},
	} {
		t.Run("before-commit/"+test.name, func(t *testing.T) {
			got, err := service.PurchaseQuoted(context.Background(), test.user, test.character, test.campaign, test.code)
			if err != nil || got.Status != QuestLicensePurchaseUnavailable || got.Replay || got.PurchaseID != 0 {
				t.Fatalf("uncommitted quote binding bypass: %+v %v", got, err)
			}
			assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
		})
	}

	// Preview fields are detached display data. Modifying them must not change
	// the trusted quote's keys, price or catalog mapping used by the purchase.
	quote.QuestIDs[0] = 65535
	quote.Price = 1
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
			got, err := service.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
			results <- outcome{got, err}
		}()
	}
	workers.Wait()
	close(results)
	var purchaseID int64
	fresh := 0
	for got := range results {
		if got.err != nil || got.result.Status != QuestLicensePurchaseOK || got.result.PurchaseID <= 0 || got.result.BalanceAfter != 150 {
			t.Fatalf("concurrent quoted purchase: %+v %v", got.result, got.err)
		}
		if purchaseID == 0 {
			purchaseID = got.result.PurchaseID
		}
		if got.result.PurchaseID != purchaseID {
			t.Fatalf("duplicate receipt: %+v want receipt %d", got.result, purchaseID)
		}
		if !got.result.Replay {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh commits=%d want 1", fresh)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
	owned, err := repo.Owned(ctx, user, []string{"gothic", "veil"})
	if err != nil || !slices.Equal(owned, []string{"gothic", "veil"}) {
		t.Fatalf("preview mutation changed account rights: %v %v", owned, err)
	}

	// Simulate a restart with a new repository and no in-memory quote cache.
	// Keep definitions so earned rights remain resolvable, but remove every
	// product and move the clock beyond both the old quote and sale windows.
	restartedRepo := NewQuestLicenseRepository(db)
	restartedRepo.now = func() time.Time { return now.Add(24 * time.Hour) }
	restarted := quotedLicenseDBService(t, quotedLicenseDBCatalog(t, now, false), restartedRepo, now.Add(24*time.Hour))
	if len(restarted.quotes.entries) != 0 {
		t.Fatal("new process unexpectedly retained uncommitted quotes")
	}
	db.MustExec(`UPDATE users SET gacha_premium=5 WHERE id=$1`, user)
	replay, err := restarted.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
	if err != nil || replay.Status != QuestLicensePurchaseOK || !replay.Replay || replay.PurchaseID != purchaseID || replay.BalanceAfter != 150 {
		t.Fatalf("committed quote lost after restart/removal/expiry: %+v %v", replay, err)
	}
	for _, questID := range []int{40079, 40142} {
		if allowed, err := restarted.HasQuestLicense(ctx, user, questID); err != nil || !allowed {
			t.Fatalf("product removal removed earned license for %d: %t %v", questID, allowed, err)
		}
	}
	for _, test := range []struct {
		name                      string
		user, character, campaign uint32
		code                      string
		want                      QuestLicensePurchaseStatus
	}{
		{"another-character", user, second, 123, quote.PaymentCode, QuestLicensePurchaseConflict},
		{"another-account", other, foreign, 123, quote.PaymentCode, QuestLicensePurchaseUnavailable},
		{"foreign-character", user, foreign, 123, quote.PaymentCode, QuestLicensePurchaseUnavailable},
		{"another-product", user, character, 124, quote.PaymentCode, QuestLicensePurchaseConflict},
		{"modified-price", user, character, 123, modifiedPriceCode, QuestLicensePurchaseUnavailable},
	} {
		t.Run("after-restart/"+test.name, func(t *testing.T) {
			got, err := restarted.PurchaseQuoted(ctx, test.user, test.character, test.campaign, test.code)
			if err != nil || got.Status != test.want || got.Replay || got.PurchaseID != 0 {
				t.Fatalf("committed quote binding bypass: %+v %v want %v", got, err, test.want)
			}
			assertQuestLicensePurchaseState(t, db, user, 5, 1, 2)
		})
	}
	assertQuestLicensePurchaseState(t, db, user, 5, 1, 2)
}

func TestQuestLicenseQuotedDBLostUncommittedQuoteDoesNotCharge(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	now := repo.now()
	catalog := quotedLicenseDBCatalog(t, now, true)
	original := quotedLicenseDBService(t, catalog, repo, now)
	quote := quotedLicenseDBPreview(t, original, user, character)
	restarted := quotedLicenseDBService(t, catalog, NewQuestLicenseRepository(db), now)
	got, err := restarted.PurchaseQuoted(context.Background(), user, character, 123, quote.PaymentCode)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable || got.Replay || got.PurchaseID != 0 {
		t.Fatalf("lost uncommitted quote charged: %+v %v", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)

	// A fresh authenticated preview recovers the flow; the stale token is not
	// silently translated into the new quote or its payment receipt.
	restartedRepo := NewQuestLicenseRepository(db)
	restartedRepo.now = func() time.Time { return now }
	restarted = quotedLicenseDBService(t, catalog, restartedRepo, now)
	fresh := quotedLicenseDBPreview(t, restarted, user, character)
	if fresh.PaymentCode == quote.PaymentCode {
		t.Fatal("new preview reused a lost token")
	}
	got, err = restarted.PurchaseQuoted(context.Background(), user, character, 123, fresh.PaymentCode)
	if err != nil || got.Status != QuestLicensePurchaseOK || got.Replay {
		t.Fatalf("fresh preview recovery: %+v %v", got, err)
	}
	stale, err := restarted.PurchaseQuoted(context.Background(), user, character, 123, quote.PaymentCode)
	if err != nil || stale.Status != QuestLicensePurchaseUnavailable || stale.Replay {
		t.Fatalf("lost token became another quote's receipt: %+v %v", stale, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}

func TestQuestLicenseQuotedDBExpiredUncommittedQuoteDoesNotCharge(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	now := repo.now()
	service := quotedLicenseDBService(t, quotedLicenseDBCatalog(t, now, true), repo, now)
	quote := quotedLicenseDBPreview(t, service, user, character)
	if !quote.ExpiresAt.Equal(now.Add(questLicenseQuoteTTL)) {
		t.Fatalf("quote expiry: %v", quote.ExpiresAt)
	}
	service.quotes.now = func() time.Time { return quote.ExpiresAt }
	repo.now = func() time.Time { return quote.ExpiresAt }
	got, err := service.PurchaseQuoted(context.Background(), user, character, 123, quote.PaymentCode)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable || got.Replay || got.PurchaseID != 0 {
		t.Fatalf("expired uncommitted quote charged: %+v %v", got, err)
	}
	if len(service.quotes.entries) != 0 {
		t.Fatal("expired quote was retained")
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
}

// The test gate pauses only after the service has resolved the authenticated
// quote and computed the effective sale window, before the repository locks.
type quotedLicenseDBGateStore struct {
	*QuestLicenseRepository
	entered chan QuestLicenseSale
	proceed chan struct{}
}

func (g *quotedLicenseDBGateStore) Purchase(ctx context.Context, request QuestLicensePurchaseRequest, sale QuestLicenseSale) (QuestLicensePurchaseResult, error) {
	select {
	case g.entered <- sale:
	case <-ctx.Done():
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}, ctx.Err()
	}
	select {
	case <-g.proceed:
		return g.QuestLicenseRepository.Purchase(ctx, request, sale)
	case <-ctx.Done():
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}, ctx.Err()
	}
}

func awaitQuotedLicenseDBAccountLock(t *testing.T, ctx context.Context, db *sqlx.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := db.GetContext(ctx, &waiting, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'
			AND query LIKE '%SELECT COALESCE(gacha_premium,0) FROM users%FOR UPDATE%')`); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("purchase never waited on the authenticated account row")
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestQuestLicenseQuotedDBExpiryWhileAccountLockedDoesNotCharge(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	now := repo.now()
	var repositoryTime atomic.Int64
	repositoryTime.Store(now.UnixNano())
	repo.now = func() time.Time { return time.Unix(0, repositoryTime.Load()) }
	gate := &quotedLicenseDBGateStore{QuestLicenseRepository: repo, entered: make(chan QuestLicenseSale, 1), proceed: make(chan struct{})}
	service := quotedLicenseDBService(t, quotedLicenseDBCatalog(t, now, true), gate, now)
	quote := quotedLicenseDBPreview(t, service, user, character)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type outcome struct {
		result QuestLicensePurchaseResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := service.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
		done <- outcome{got, err}
	}()
	var effectiveSale QuestLicenseSale
	select {
	case effectiveSale = <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !effectiveSale.End.Equal(quote.ExpiresAt) {
		t.Fatalf("sale outlived the confirmed preview: sale=%v quote=%v", effectiveSale.End, quote.ExpiresAt)
	}
	blocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	var lockedUser uint32
	if err := blocker.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&lockedUser); err != nil {
		t.Fatal(err)
	}
	close(gate.proceed)
	awaitQuotedLicenseDBAccountLock(t, ctx, db)
	// The preview was valid when resolved, but expires while the account row
	// is busy. Repository time is read only after that lock has been acquired.
	repositoryTime.Store(quote.ExpiresAt.UnixNano())
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.result.Status != QuestLicensePurchaseUnavailable || got.result.Replay || got.result.PurchaseID != 0 {
			t.Fatalf("quote expired during account lock but charged: %+v %v", got.result, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
}
