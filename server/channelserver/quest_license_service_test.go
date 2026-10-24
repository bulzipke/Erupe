package channelserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

type questLicenseStoreStub struct {
	purchase     func(QuestLicensePurchaseRequest, QuestLicenseSale) (QuestLicensePurchaseResult, error)
	owned        func(uint32, []string) ([]string, error)
	replay       func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error)
	calls        int
	bind         func([]questlicense.Definition) error
	binds        int
	starterBind  func([]QuestLicenseStarterPolicy) error
	starterBinds int
}

func (m *questLicenseStoreStub) BindStarterPolicies(_ context.Context, policies []QuestLicenseStarterPolicy) error {
	m.starterBinds++
	if m.starterBind != nil {
		return m.starterBind(policies)
	}
	return nil
}

func (m *questLicenseStoreStub) BindDefinitions(_ context.Context, definitions []questlicense.Definition) error {
	m.binds++
	if m.bind != nil {
		return m.bind(definitions)
	}
	return nil
}

func (m *questLicenseStoreStub) Replay(_ context.Context, u, c, p uint32, code string) (QuestLicensePurchaseResult, bool, error) {
	m.calls++
	if m.replay != nil {
		return m.replay(u, c, p, code)
	}
	return QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}, false, nil
}

func (m *questLicenseStoreStub) Purchase(_ context.Context, r QuestLicensePurchaseRequest, s QuestLicenseSale) (QuestLicensePurchaseResult, error) {
	m.calls++
	if m.purchase != nil {
		return m.purchase(r, s)
	}
	return QuestLicensePurchaseResult{}, nil
}
func (m *questLicenseStoreStub) Owned(_ context.Context, u uint32, k []string) ([]string, error) {
	m.calls++
	if m.owned != nil {
		return m.owned(u, k)
	}
	return nil, nil
}

func serviceLicenseCatalog(t *testing.T, enabled bool) *questlicense.Catalog {
	t.Helper()
	price := uint32(50)
	start := time.Now().Add(-time.Hour)
	end := start.Add(2 * time.Hour)
	c, err := questlicense.New(questlicense.Document{Version: "service-test", Licenses: []questlicense.Definition{
		{Key: "veil", QuestIDs: []int{40142}}, {Key: "alternate", QuestIDs: []int{40142}},
	}, Products: []questlicense.Product{{CampaignID: 123, Enabled: enabled, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"veil"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestQuestLicenseServiceDisabledNeverTouchesStore(t *testing.T) {
	store := &questLicenseStoreStub{}
	s, err := NewQuestLicenseService(false, serviceLicenseCatalog(t, true), store)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []*QuestLicenseService{s, nil, {}} {
		got, e := service.Purchase(context.Background(), QuestLicensePurchaseRequest{CampaignID: 123})
		if e != nil || got.Status != QuestLicensePurchaseUnavailable {
			t.Fatal(got, e)
		}
		ok, e := service.HasQuestLicense(context.Background(), 1, 40142)
		if ok || e != nil {
			t.Fatal(ok, e)
		}
	}
	if store.calls != 0 {
		t.Fatal("disabled service reached DB")
	}
	if _, err := NewQuestLicenseService(true, nil, store); err == nil {
		t.Fatal("enabled without catalog")
	}
	if _, err := NewQuestLicenseService(true, serviceLicenseCatalog(t, true), nil); err == nil {
		t.Fatal("enabled without store")
	}
}

func TestQuestLicenseServicePreservesQuoteAndCurrentSaleSeparately(t *testing.T) {
	catalog := serviceLicenseCatalog(t, false)
	request := QuestLicensePurchaseRequest{UserID: 1, CharacterID: 2, CampaignID: 123, RequestCode: "server-quote", QuotedPrice: 40, LicenseKeys: []string{"alternate"}}
	store := &questLicenseStoreStub{purchase: func(r QuestLicensePurchaseRequest, s QuestLicenseSale) (QuestLicensePurchaseResult, error) {
		if r.CampaignID == 999 {
			if s.Enabled || s.CampaignID != 0 || len(s.LicenseKeys) != 0 {
				t.Fatal("unknown product became an active sale")
			}
			return QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}, nil
		}
		if r.QuotedPrice != 40 || r.LicenseKeys[0] != "alternate" || s.Price != 50 || s.Enabled || s.LicenseKeys[0] != "veil" {
			t.Fatal(r, s)
		}
		// A committed replay can be acknowledged by the repository even after
		// sale availability, price or mapping change. Do not rewrite its quote.
		r.LicenseKeys[0] = "mutated"
		s.LicenseKeys[0] = "mutated"
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: 7, Replay: true}, nil
	}}
	s, err := NewQuestLicenseService(true, catalog, store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Purchase(context.Background(), request)
	if err != nil || got.PurchaseID != 7 || !got.Replay || request.LicenseKeys[0] != "alternate" {
		t.Fatal(got, request, err)
	}
	offer, _ := catalog.Offer(123)
	if offer.LicenseKeys[0] != "veil" {
		t.Fatal("store mutated catalog")
	}
	request.CampaignID = 999
	got, err = s.Purchase(context.Background(), request)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable || store.calls != 2 {
		t.Fatal("unknown product was not checked as a disabled replay-only sale")
	}
}

func TestQuestLicenseServiceAlternativeRightsDoNotWaiveOtherConditions(t *testing.T) {
	store := &questLicenseStoreStub{owned: func(user uint32, keys []string) ([]string, error) {
		if user != 17 || len(keys) != 2 || keys[0] != "alternate" || keys[1] != "veil" {
			t.Fatal(user, keys)
		}
		keys[0] = "local-mutation"
		return []string{"veil"}, nil
	}}
	s, err := NewQuestLicenseService(true, serviceLicenseCatalog(t, true), store)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.HasQuestLicense(context.Background(), 17, 40142)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	ok, err = s.HasQuestLicense(context.Background(), 17, 65535)
	if ok || err != nil || store.calls != 1 {
		t.Fatal("unmapped quest queried store")
	}
	store.owned = func(uint32, []string) ([]string, error) { return []string{"unrelated"}, nil }
	if ok, err = s.HasQuestLicense(context.Background(), 17, 40142); ok || err == nil {
		t.Fatal("unrelated returned key granted rights")
	}
	store.owned = func(uint32, []string) ([]string, error) { return nil, errors.New("DB failure") }
	if ok, err = s.HasQuestLicense(context.Background(), 17, 40142); ok || err == nil {
		t.Fatal("DB failure granted rights")
	}
}

func TestQuestLicenseServiceCommittedReplayAfterProductRemoval(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	start, end := repo.now().Add(-time.Hour), repo.now().Add(time.Hour)
	price := uint32(50)
	doc := questlicense.Document{Version: "before-removal", Licenses: []questlicense.Definition{{Key: "veil", QuestIDs: []int{40142}}},
		Products: []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"veil"}}}}
	catalog, err := questlicense.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewQuestLicenseService(true, catalog, repo)
	if err != nil {
		t.Fatal(err)
	}
	request := QuestLicensePurchaseRequest{UserID: user, CharacterID: char, CampaignID: 123, RequestCode: "server-issued-quote", QuotedPrice: 50, LicenseKeys: []string{"veil"}}
	first, err := s.Purchase(context.Background(), request)
	if err != nil || first.Status != QuestLicensePurchaseOK || first.Replay {
		t.Fatal(first, err)
	}
	doc.Version = "after-removal"
	doc.Products = nil
	catalog, err = questlicense.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	s, err = NewQuestLicenseService(true, catalog, repo)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Purchase(context.Background(), request)
	if err != nil || !replay.Replay || replay.Status != QuestLicensePurchaseOK || replay.PurchaseID != first.PurchaseID {
		t.Fatal(replay, err)
	}
	request.RequestCode = "different-quote-after-removal"
	denied, err := s.Purchase(context.Background(), request)
	if err != nil || denied.Status != QuestLicensePurchaseUnavailable {
		t.Fatal(denied, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 1)
	owned, err := s.HasQuestLicense(context.Background(), user, 40142)
	if !owned || err != nil {
		t.Fatal("removing a sale removed earned account ownership", owned, err)
	}
}
