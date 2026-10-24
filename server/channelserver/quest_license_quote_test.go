package channelserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

func quoteTestCatalog(t *testing.T, version string, questID int, price uint32) *questlicense.Catalog {
	t.Helper()
	start := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	c, err := questlicense.New(questlicense.Document{Version: version,
		Licenses: []questlicense.Definition{{Key: "veil", QuestIDs: []int{questID}}},
		Products: []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"veil"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func quoteTestService(t *testing.T, store *questLicenseStoreStub) (*QuestLicenseService, time.Time) {
	t.Helper()
	s, err := NewQuestLicenseService(true, quoteTestCatalog(t, "v1", 40142, 50), store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s.quotes.now = func() time.Time { return now }
	return s, now
}

func TestQuestLicenseQuoteCanonicalPaymentCode(t *testing.T) {
	valid := "^0032AQIDBAUGBwg"
	if !validQuestLicensePaymentCode(valid) {
		t.Fatal("valid code rejected")
	}
	for _, code := range []string{"", "!0032AQIDBAUGBwg", "^0000AQIDBAUGBwg", "^003aAQIDBAUGBwg", "^+032AQIDBAUGBwg", "^0032AQIDBAUGBwh", "^0032AQIDBAUGBw=", valid + "x", valid[:15]} {
		if validQuestLicensePaymentCode(code) {
			t.Errorf("accepted %q", code)
		}
	}
}

func TestQuestLicenseQuoteBindsPreviewAndExpiry(t *testing.T) {
	store := &questLicenseStoreStub{}
	s, now := quoteTestService(t, store)
	quote, ok, err := s.Quote(context.Background(), 1, 2, 123)
	if err != nil || !ok || quote.Price != 50 || quote.CampaignID != 123 || !quote.ExpiresAt.Equal(now.Add(2*time.Minute)) || len(quote.QuestIDs) != 1 || quote.QuestIDs[0] != 40142 || !validQuestLicensePaymentCode(quote.PaymentCode) {
		t.Fatal(quote, ok, err)
	}
	quote.QuestIDs[0] = 999
	store.purchase = func(r QuestLicensePurchaseRequest, sale QuestLicenseSale) (QuestLicensePurchaseResult, error) {
		if r.UserID != 1 || r.CharacterID != 2 || r.CampaignID != 123 || r.QuotedPrice != 50 || r.LicenseKeys[0] != "veil" || r.RequestCode != quote.PaymentCode || !sale.End.Equal(quote.ExpiresAt) {
			t.Fatal(r, sale)
		}
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: 9}, nil
	}
	got, err := s.PurchaseQuoted(context.Background(), 1, 2, 123, quote.PaymentCode)
	if err != nil || got.PurchaseID != 9 || store.calls != 2 {
		t.Fatal(got, store.calls, err)
	}
	for _, identity := range [][3]uint32{{3, 2, 123}, {1, 3, 123}, {1, 2, 124}} {
		before := store.calls
		got, err = s.PurchaseQuoted(context.Background(), identity[0], identity[1], identity[2], quote.PaymentCode)
		if err != nil || got.Status != QuestLicensePurchaseUnavailable || store.calls != before+1 {
			t.Fatal("changed identity reached purchase", got, err)
		}
	}
	for _, changed := range []*questlicense.Catalog{
		quoteTestCatalog(t, "v2", 40142, 50), quoteTestCatalog(t, "v1", 40143, 50), quoteTestCatalog(t, "v1", 40142, 51),
	} {
		s.catalog = changed
		before := store.calls
		got, err = s.PurchaseQuoted(context.Background(), 1, 2, 123, quote.PaymentCode)
		if err != nil || got.Status != QuestLicensePurchaseUnavailable || store.calls != before+1 {
			t.Fatal("changed contract reached purchase", got, err)
		}
	}
	s.catalog = quoteTestCatalog(t, "v1", 40142, 50)
	s.quotes.now = func() time.Time { return quote.ExpiresAt }
	before := store.calls
	got, err = s.PurchaseQuoted(context.Background(), 1, 2, 123, quote.PaymentCode)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable || store.calls != before+1 || len(s.quotes.entries) != 0 {
		t.Fatal("expired quote reached purchase", got, err)
	}
}

func TestQuestLicenseQuoteCommittedReplaySurvivesCacheLoss(t *testing.T) {
	store := &questLicenseStoreStub{}
	s, _ := quoteTestService(t, store)
	store.replay = func(u, c, p uint32, code string) (QuestLicensePurchaseResult, bool, error) {
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: 9, BalanceAfter: 150, Replay: true}, true, nil
	}
	got, err := s.PurchaseQuoted(context.Background(), 1, 2, 123, "^0032AQIDBAUGBwg")
	if err != nil || !got.Replay || got.PurchaseID != 9 || store.calls != 1 {
		t.Fatal(got, err)
	}
	store.replay = func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error) {
		return QuestLicensePurchaseResult{}, false, errors.New("receipt database unavailable")
	}
	if _, err = s.PurchaseQuoted(context.Background(), 1, 2, 123, "^0032AQIDBAUGBwg"); err == nil {
		t.Fatal("receipt failure did not stop purchase")
	}
	store.calls = 0
	for _, disabled := range []*QuestLicenseService{nil, {}, {store: store}} {
		if _, ok, err := disabled.Quote(context.Background(), 1, 2, 123); ok || err != nil {
			t.Fatal("disabled quote")
		}
		if got, err = disabled.PurchaseQuoted(context.Background(), 1, 2, 123, "^0032AQIDBAUGBwg"); err != nil || got.Status != QuestLicensePurchaseUnavailable {
			t.Fatal("disabled purchase")
		}
	}
	if store.calls != 0 {
		t.Fatal("disabled service touched database")
	}
}

func TestQuestLicenseQuoteBoundedAndConcurrent(t *testing.T) {
	s, now := quoteTestService(t, &questLicenseStoreStub{})
	var wg sync.WaitGroup
	codes := make(chan string, 32)
	for range 32 {
		wg.Go(func() {
			quote, ok, err := s.Quote(context.Background(), 1, 2, 123)
			if err != nil {
				t.Error(err)
			}
			if ok {
				codes <- quote.PaymentCode
			}
		})
	}
	wg.Wait()
	close(codes)
	seen := map[string]bool{}
	for code := range codes {
		if seen[code] {
			t.Fatal("duplicate random token")
		}
		seen[code] = true
	}
	if len(seen) != questLicenseAccountQuotes || len(s.quotes.entries) != questLicenseAccountQuotes {
		t.Fatal("account quote bound failed", len(seen))
	}
	for i := len(s.quotes.entries); i < questLicenseQuoteLimit; i++ {
		s.quotes.entries[fmt.Sprintf("test-%d", i)] = questLicenseQuoteEntry{request: QuestLicensePurchaseRequest{UserID: uint32(i + 100)}, expires: now.Add(time.Minute)}
	}
	if _, ok, err := s.Quote(context.Background(), 9, 2, 123); ok || err != nil {
		t.Fatal("global quote bound failed", err)
	}
	s.quotes.now = func() time.Time { return now.Add(3 * time.Minute) }
	if _, ok, err := s.Quote(context.Background(), 1, 2, 123); !ok || err != nil || len(s.quotes.entries) != 1 {
		t.Fatal("expired quote slots not reclaimed", err)
	}
}

func TestQuestLicenseQuoteRandomFailureAndSaleBoundary(t *testing.T) {
	s, _ := quoteTestService(t, &questLicenseStoreStub{})
	s.quotes.random = bytes.NewReader(nil)
	if _, ok, err := s.Quote(context.Background(), 1, 2, 123); ok || err == nil || len(s.quotes.entries) != 0 {
		t.Fatal("entropy failure issued quote")
	}
	s.quotes.random = bytes.NewReader(make([]byte, 64))
	quote, ok, err := s.Quote(context.Background(), 1, 2, 123)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, ok, err = s.Quote(context.Background(), 1, 2, 123); ok || err == nil || len(s.quotes.entries) != 1 {
		t.Fatal("repeated token accepted")
	}
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	s.quotes.now = func() time.Time { return end.Add(-time.Second) }
	s.quotes.random = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	quote, ok, err = s.Quote(context.Background(), 1, 2, 123)
	if err != nil || !ok || !quote.ExpiresAt.Equal(end) {
		t.Fatal("quote outlived sale", quote, err)
	}
	s.quotes.now = func() time.Time { return end }
	if _, ok, err = s.Quote(context.Background(), 1, 2, 123); ok || err != nil {
		t.Fatal("quote issued at sale end")
	}
}

func TestQuestLicenseDefinitionGuardBlocksNewFlowButPreservesReceipt(t *testing.T) {
	store := &questLicenseStoreStub{}
	s, _ := quoteTestService(t, store)
	store.bind = func(definitions []questlicense.Definition) error {
		return errors.New("registered quest mapping changed")
	}
	if _, ok, err := s.Quote(context.Background(), 1, 2, 123); ok || err == nil {
		t.Fatal("unbound catalog issued a preview")
	}
	if allowed, err := s.HasQuestLicense(context.Background(), 1, 40142); allowed || err == nil {
		t.Fatal("unbound catalog granted account authority")
	}
	if store.calls != 0 || len(s.quotes.entries) != 0 {
		t.Fatal("definition failure reached purchase or account reads")
	}
	// A transient failure can be retried; successful binding is cached only for
	// this immutable catalog. A separate process must check the DB again.
	store.bind = nil
	quote, ok, err := s.Quote(context.Background(), 1, 2, 123)
	if !ok || err != nil || store.binds != 3 {
		t.Fatal(quote, ok, err, store.binds)
	}
	if _, ok, err = s.Quote(context.Background(), 1, 2, 123); !ok || err != nil || store.binds != 3 {
		t.Fatal("successful immutable binding was not reused")
	}
	s.definitionsBoundTo = nil
	store.bind = func([]questlicense.Definition) error { return errors.New("bad catalog after restart") }
	store.replay = func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error) {
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: 7, Replay: true}, true, nil
	}
	got, err := s.PurchaseQuoted(context.Background(), 1, 2, 123, quote.PaymentCode)
	if err != nil || !got.Replay || got.PurchaseID != 7 || store.binds != 3 {
		t.Fatal("definition failure blocked committed receipt recovery", got, err)
	}
	store.replay = nil
	if got, err = s.PurchaseQuoted(context.Background(), 1, 2, 123, quote.PaymentCode); err == nil || got.Status != QuestLicensePurchaseUnavailable {
		t.Fatal("definition failure permitted a new purchase", got, err)
	}
}
