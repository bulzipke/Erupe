package channelserver

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

type questLicensePreviewStoreStub struct {
	calls int
	load  func(uint32, uint32, []string) (QuestLicenseOwnership, bool, error)
}

func (m *questLicensePreviewStoreStub) PreviewOwnership(_ context.Context, user, character uint32, keys []string) (QuestLicenseOwnership, bool, error) {
	m.calls++
	if m.load != nil {
		return m.load(user, character, keys)
	}
	return QuestLicenseOwnershipNone, true, nil
}

func shopDisplay(t *testing.T, catalog *questlicense.Catalog, campaignID uint32) *questlicense.DisplayCatalog {
	t.Helper()
	doc := questlicense.DisplayDocument{CatalogVersion: catalog.Version()}
	product := questlicense.DisplayProduct{CampaignID: campaignID, Title: "VISA permanent license"}
	for _, id := range catalog.QuestIDsForOffer(campaignID) {
		product.Quests = append(product.Quests, questlicense.DisplayQuest{ID: id, Name: "VISA quest", Conditions: "Original equipment and rank", Publication: "Independent publication"})
	}
	doc.Products = []questlicense.DisplayProduct{product}
	display, err := questlicense.NewDisplay(doc, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return display
}

func TestQuestLicenseShopPreviewUsesAuthenticatedOwnershipAndExactQuote(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	for _, state := range []QuestLicenseOwnership{QuestLicenseOwnershipNone, QuestLicenseOwnershipOwned, QuestLicenseOwnershipPartial} {
		store := &questLicenseStoreStub{}
		catalog := starterLicenseCatalog(t, now, true)
		licenses, err := NewQuestLicenseServiceWithStarters(true, catalog, store, &questLicenseStarterStub{})
		if err != nil {
			t.Fatal(err)
		}
		licenses.quotes.now = func() time.Time { return now }
		ownership := &questLicensePreviewStoreStub{load: func(user, character uint32, keys []string) (QuestLicenseOwnership, bool, error) {
			if user != 1 || character != 2 || !slices.Equal(keys, []string{"visa_veil"}) {
				t.Fatal("untrusted preview identity or keys", user, character, keys)
			}
			keys[0] = "mutated"
			return state, true, nil
		}}
		shop, err := NewQuestLicenseShop(licenses, ownership, shopDisplay(t, catalog, 123))
		if err != nil {
			t.Fatal(err)
		}
		data, err := shop.Preview(ctx, 1, 2, 123)
		if err != nil || len(data) < 40 || string(data[:4]) != "LPV2" || data[8] != byte(state) ||
			binary.BigEndian.Uint32(data[10:14]) != 123 || binary.BigEndian.Uint16(data[14:16]) != 10 || binary.BigEndian.Uint16(data[16:18]) != 8 {
			t.Fatal("incorrect authenticated typed preview", len(data), err)
		}
		code := string(data[22:38])
		entry, ok := licenses.quotes.resolve(catalog, 1, 2, 123, code)
		if !ok || entry.request.QuotedPrice != 10 || !slices.Equal(entry.request.LicenseKeys, []string{"visa_veil"}) || ownership.calls != 1 || store.calls != 0 {
			t.Fatal("display does not use its exact server quote", entry, ok, ownership.calls, store.calls)
		}
		store.purchase = func(request QuestLicensePurchaseRequest, sale QuestLicenseSale) (QuestLicensePurchaseResult, error) {
			if request.RequestCode != code || request.QuotedPrice != 10 || !slices.Equal(request.LicenseKeys, []string{"visa_veil"}) || sale.Price != 10 {
				t.Fatal("apply rebuilt the previewed payment contract", request, sale)
			}
			status := QuestLicensePurchaseOK
			if state == QuestLicenseOwnershipOwned {
				status = QuestLicensePurchaseAlreadyOwned
			} else if state == QuestLicenseOwnershipPartial {
				status = QuestLicensePurchasePartialOwned
			}
			return QuestLicensePurchaseResult{Status: status}, nil
		}
		want := questlicense.WireOK
		if state == QuestLicenseOwnershipOwned {
			want = questlicense.WireAlreadyOwned
		} else if state == QuestLicenseOwnershipPartial {
			want = questlicense.WirePartialOwned
		}
		if status, err := shop.Apply(ctx, 1, 2, 123, code); err != nil || status != want {
			t.Fatal(status, err)
		}
	}
}

func TestQuestLicenseShopRefusesDisabledForeignAndFailedPreviews(t *testing.T) {
	ctx := context.Background()
	store := &questLicenseStoreStub{}
	owner := &questLicensePreviewStoreStub{}
	licenses, _ := NewQuestLicenseService(false, nil, store)
	disabled, err := NewQuestLicenseShop(licenses, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, shop := range []*QuestLicenseShop{nil, {}, disabled} {
		if data, err := shop.Preview(ctx, 1, 2, 123); err != nil || data != nil {
			t.Fatal(data, err)
		}
		if status, err := shop.Apply(ctx, 1, 2, 123, "^000AAQIDBAUGBwg"); err != nil || status != questlicense.WireUnavailable {
			t.Fatal(status, err)
		}
	}
	if owner.calls != 0 || store.calls != 0 || store.binds != 0 || store.starterBinds != 0 {
		t.Fatal("disabled shop reached storage")
	}
	now := time.Now()
	catalog := starterLicenseCatalog(t, now, true)
	licenses, _ = NewQuestLicenseServiceWithStarters(true, catalog, store, &questLicenseStarterStub{})
	if _, err := NewQuestLicenseShop(licenses, nil, nil); err == nil {
		t.Fatal("enabled shop accepted absent dependencies")
	}
	shop, _ := NewQuestLicenseShop(licenses, owner, shopDisplay(t, catalog, 123))
	for _, pair := range [][2]uint32{{0, 2}, {1, 0}, {1 << 31, 2}} {
		if data, err := shop.Preview(ctx, pair[0], pair[1], 123); err != nil || data != nil {
			t.Fatal(data, err)
		}
	}
	if owner.calls != 0 {
		t.Fatal("invalid identity reached ownership store")
	}
	for _, test := range []struct {
		state QuestLicenseOwnership
		auth  bool
		err   error
	}{{QuestLicenseOwnershipNone, false, nil}, {QuestLicenseOwnershipNone, false, errors.New("read failure")}, {99, true, nil}} {
		owner.load = func(uint32, uint32, []string) (QuestLicenseOwnership, bool, error) {
			return test.state, test.auth, test.err
		}
		data, err := shop.Preview(ctx, 1, 2, 123)
		if data != nil || test.err != nil && err == nil || test.state == 99 && err == nil {
			t.Fatal("failed ownership exposed a preview", data, err)
		}
		if len(licenses.quotes.entries) != 0 {
			t.Fatal("failed/foreign preview issued a payment quote")
		}
	}
}

func TestQuestLicenseShopApplyWireMappingAndReplayIndependence(t *testing.T) {
	ctx := context.Background()
	store := &questLicenseStoreStub{}
	catalog := serviceLicenseCatalog(t, true)
	licenses, _ := NewQuestLicenseService(true, catalog, store)
	shop, _ := NewQuestLicenseShop(licenses, &questLicensePreviewStoreStub{}, shopDisplay(t, catalog, 123))
	for _, tt := range []struct {
		status QuestLicensePurchaseStatus
		wire   uint32
	}{
		{QuestLicensePurchaseOK, questlicense.WireOK},
		{QuestLicensePurchaseUnavailable, questlicense.WireUnavailable},
		{QuestLicensePurchasePriceChanged, questlicense.WirePriceChanged},
		{QuestLicensePurchaseConflict, questlicense.WireUnavailable},
		{QuestLicensePurchaseAlreadyOwned, questlicense.WireAlreadyOwned},
		{QuestLicensePurchasePartialOwned, questlicense.WirePartialOwned},
		{QuestLicensePurchaseInsufficient, questlicense.WireInsufficient},
	} {
		store.replay = func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error) {
			return QuestLicensePurchaseResult{Status: tt.status}, true, nil
		}
		if got, err := shop.Apply(ctx, 1, 2, 123, "^0032AQIDBAUGBwg"); err != nil || got != tt.wire {
			t.Fatal(tt, got, err)
		}
	}
	store.replay = func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error) {
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, Replay: true}, true, nil
	}
	store.starterBind = func([]QuestLicenseStarterPolicy) error { return errors.New("current policy unavailable") }
	shop.display = nil // Committed replay must not require current display text.
	if got, err := shop.Apply(ctx, 1, 2, 123, "^0032AQIDBAUGBwg"); err != nil || got != questlicense.WireOK || store.starterBinds != 0 {
		t.Fatal("current presentation blocked a committed replay", got, err)
	}
	before := store.calls
	if got, err := shop.Apply(ctx, 1, 2, 123, "!0032AQIDBAUGBwg"); err != nil || got != questlicense.WireUnavailable || store.calls != before {
		t.Fatal("ordinary kit code reached license storage", got, err)
	}
}

func TestQuestLicenseShopNoStarterStillUsesLPV2(t *testing.T) {
	catalog := serviceLicenseCatalog(t, true)
	licenses, _ := NewQuestLicenseService(true, catalog, &questLicenseStoreStub{})
	shop, _ := NewQuestLicenseShop(licenses, &questLicensePreviewStoreStub{}, shopDisplay(t, catalog, 123))
	data, err := shop.Preview(context.Background(), 1, 2, 123)
	if err != nil || len(data) < 42 || string(data[:4]) != "LPV2" || binary.BigEndian.Uint16(data[len(data)-2:]) != 0 {
		t.Fatal("starter-free offer switched private protocol", len(data), err)
	}
}
