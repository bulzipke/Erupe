package channelserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

type questLicenseStarterStub struct {
	bind         func([]QuestLicenseStarterPolicy) error
	ensure       func(uint32, uint32, QuestLicenseStarterPolicy) (QuestLicenseStarterResult, error)
	pending      func(uint32, uint32, string) ([]CampaignReward, error)
	acquire      func(uint32, uint32, []uint32) error
	binds, calls int
}

func (m *questLicenseStarterStub) PrepareSave(_ context.Context, _, _ uint32, _ []uint32) ([]CampaignReward, error) {
	return nil, nil
}

func (m *questLicenseStarterStub) BindPolicies(_ context.Context, p []QuestLicenseStarterPolicy) error {
	m.binds++
	if m.bind != nil {
		return m.bind(p)
	}
	return nil
}
func (m *questLicenseStarterStub) Ensure(_ context.Context, u, c uint32, p QuestLicenseStarterPolicy) (QuestLicenseStarterResult, error) {
	m.calls++
	if m.ensure != nil {
		return m.ensure(u, c, p)
	}
	return QuestLicenseStarterResult{Status: StarterPending, RewardID: campaignCoinRewardBase}, nil
}
func (m *questLicenseStarterStub) Pending(_ context.Context, u, c uint32, k string) ([]CampaignReward, error) {
	m.calls++
	if m.pending != nil {
		return m.pending(u, c, k)
	}
	return nil, nil
}
func (m *questLicenseStarterStub) Acquire(_ context.Context, u, c uint32, ids []uint32) error {
	m.calls++
	if m.acquire != nil {
		return m.acquire(u, c, ids)
	}
	return nil
}

func starterLicenseCatalog(t *testing.T, now time.Time, selling bool) *questlicense.Catalog {
	t.Helper()
	price := uint32(10)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	product := questlicense.Product{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end,
		LicenseKeys: []string{"visa_veil"}, StarterKeys: []string{"visa_gothic_base_v1"}}
	doc := questlicense.Document{Version: "visa-starter-test", Licenses: []questlicense.Definition{{Key: "visa_veil", QuestIDs: []int{40081, 40142, 40143, 40161, 40173, 40174, 40201, 40218}}},
		Starters: []questlicense.StarterDefinition{{Key: "visa_gothic_base_v1", LicenseKeys: []string{"visa_veil"}, ItemID: 1859, Quantity: 1, LegacyCampaignIDs: []uint32{492821212}}}}
	if selling {
		doc.Products = []questlicense.Product{product}
	}
	catalog, err := questlicense.New(doc)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestQuestLicenseStarterServiceRequiresDeliveryAndBindsBeforeQuote(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	catalog := starterLicenseCatalog(t, now, true)
	store := &questLicenseStoreStub{}
	if _, err := NewQuestLicenseService(true, catalog, store); err == nil {
		t.Fatal("starter benefits could be sold without delivery store")
	}
	starters := &questLicenseStarterStub{bind: func(p []QuestLicenseStarterPolicy) error {
		if len(p) != 1 || p[0].Quantity != 1 || p[0].ItemID != 1859 {
			t.Fatal(p)
		}
		p[0].LicenseKeys[0] = "mutated"
		return nil
	}}
	store.starterBind = func(p []QuestLicenseStarterPolicy) error { return starters.BindPolicies(ctx, p) }
	s, err := NewQuestLicenseServiceWithStarters(true, catalog, store, starters)
	if err != nil {
		t.Fatal(err)
	}
	s.quotes.now = func() time.Time { return now }
	quote, ok, err := s.Quote(ctx, 1, 2, 123)
	if err != nil || !ok || quote.Price != 10 || len(quote.Starters) != 1 || quote.Starters[0].Quantity != 1 || quote.Starters[0].LicenseKeys[0] != "visa_veil" {
		t.Fatal(quote, ok, err)
	}
	quote.Starters[0].Quantity = 220
	quote.Starters[0].LegacyCampaignIDs[0] = 1
	policy, _ := catalog.Starter("visa_gothic_base_v1")
	if policy.Quantity != 1 || policy.LegacyCampaignIDs[0] != 492821212 {
		t.Fatal("quote/store mutated permanent starter policy")
	}
	if _, ok, err = s.Quote(ctx, 1, 2, 123); err != nil || !ok || starters.binds != 1 || store.binds != 1 {
		t.Fatal(ok, err, starters.binds, store.binds)
	}
}

func TestQuestLicenseStarterServicePolicyFailurePreventsNewDebitOnly(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	store := &questLicenseStoreStub{owned: func(uint32, []string) ([]string, error) { return []string{"visa_veil"}, nil }}
	starters := &questLicenseStarterStub{bind: func([]QuestLicenseStarterPolicy) error { return errors.New("changed starter contract") }}
	store.starterBind = func(p []QuestLicenseStarterPolicy) error { return starters.BindPolicies(ctx, p) }
	s, err := NewQuestLicenseServiceWithStarters(true, starterLicenseCatalog(t, now, true), store, starters)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Quote(ctx, 1, 2, 123); err == nil || ok {
		t.Fatal("unbound starter was sold", ok, err)
	}
	if store.calls != 0 {
		t.Fatal("quote failure reached debit")
	}
	if owned, err := s.HasQuestLicense(ctx, 1, 40081); err != nil || !owned {
		t.Fatal("delivery failure revoked earned quest right", owned, err)
	}
	store.replay = func(uint32, uint32, uint32, string) (QuestLicensePurchaseResult, bool, error) {
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: 7, Replay: true}, true, nil
	}
	if replay, err := s.PurchaseQuoted(ctx, 1, 2, 123, "^000AAQIDBAUGBwg"); err != nil || !replay.Replay || replay.PurchaseID != 7 {
		t.Fatal("delivery failure blocked committed payment replay", replay, err)
	}
	if _, err := s.EnsureStarter(ctx, 1, 2, "visa_gothic_base_v1"); err == nil || starters.calls != 0 {
		t.Fatal("unbound policy granted material", err)
	}
}

func TestQuestLicenseStarterServiceDisabledAndAuthenticatedBoundaries(t *testing.T) {
	ctx := context.Background()
	store := &questLicenseStoreStub{}
	starters := &questLicenseStarterStub{}
	s, _ := NewQuestLicenseServiceWithStarters(false, nil, store, starters)
	for _, service := range []*QuestLicenseService{s, nil, {}} {
		if result, err := service.EnsureStarter(ctx, 1, 2, "visa_gothic_base_v1"); err != nil || result.Status != StarterUnavailable {
			t.Fatal(result, err)
		}
		if rows, err := service.PendingStarter(ctx, 1, 2, "visa_gothic_base_v1"); err != nil || len(rows) != 0 {
			t.Fatal(rows, err)
		}
		if err := service.AcquireStarters(ctx, 1, 2, []uint32{campaignCoinRewardBase}); err == nil {
			t.Fatal("disabled collection accepted")
		}
	}
	if starters.calls != 0 || starters.binds != 0 || store.calls != 0 {
		t.Fatal("disabled service reached storage")
	}
	s, _ = NewQuestLicenseServiceWithStarters(true, starterLicenseCatalog(t, time.Now(), false), store, starters)
	for _, ids := range [][2]uint32{{0, 2}, {1, 0}, {1 << 31, 2}, {1, 1 << 31}} {
		if result, err := s.EnsureStarter(ctx, ids[0], ids[1], "visa_gothic_base_v1"); err != nil || result.Status != StarterUnavailable {
			t.Fatal(result, err)
		}
	}
	if result, err := s.EnsureStarter(ctx, 1, 2, "unrelated"); err != nil || result.Status != StarterUnavailable || starters.calls != 0 {
		t.Fatal(result, err)
	}
}

func TestQuestLicenseStarterPreviewCannotSilentlyOmitIncludedMaterial(t *testing.T) {
	preview := questLicensePreviewFixture()
	preview.Quote.Starters = []QuestLicenseStarterPolicy{{Key: "visa_gothic_base_v1", ItemID: 1859, Quantity: 1}}
	data, err := encodeQuestLicensePreview(preview)
	if err != nil || string(data[:4]) != "LPV2" || binary.BigEndian.Uint16(data[4:6]) != 2 {
		t.Fatal("included material missing typed preview", len(data), err)
	}
	basePreview := preview
	basePreview.Quote.Starters = nil
	base, _ := encodeQuestLicensePreview(basePreview)
	row := data[len(base):]
	if binary.BigEndian.Uint16(row[:2]) != 1 || row[2] != 1 || row[3] != 0 || binary.BigEndian.Uint16(row[4:6]) != 7 || binary.BigEndian.Uint16(row[6:8]) != 1859 || binary.BigEndian.Uint16(row[8:10]) != 1 || int(binary.BigEndian.Uint16(row[10:12])) != len("visa_gothic_base_v1") || string(row[12:]) != "visa_gothic_base_v1" {
		t.Fatal("incorrect per-character starting material contract", row)
	}
	for _, change := range []func(*QuestLicensePreview){
		func(p *QuestLicensePreview) { p.Quote.Starters[0].Quantity = 0 },
		func(p *QuestLicensePreview) { p.Quote.Starters[0].ItemID = 0 },
		func(p *QuestLicensePreview) { p.Quote.Starters[0].Key = "Bad key" },
		func(p *QuestLicensePreview) { p.Quote.Starters = append(p.Quote.Starters, p.Quote.Starters[0]) },
	} {
		p := questLicensePreviewFixture()
		p.Quote.Starters = []QuestLicenseStarterPolicy{{Key: "visa_gothic_base_v1", ItemID: 1859, Quantity: 1}}
		change(&p)
		if data, err := encodeQuestLicensePreview(p); err == nil || data != nil {
			t.Fatal("malformed included material exposed partial preview", len(data), err)
		}
	}
}

// The client overlay consumes the same fixture. Comparing the real encoder to
// its bytes prevents independent parser/serializer tests from drifting apart.
func TestQuestLicenseStarterPreviewMatchesSharedClientFixture(t *testing.T) {
	quote := QuestLicenseQuote{CampaignID: 123, Price: 10, QuestIDs: []int{40081, 40142, 40143, 40161, 40173, 40174, 40201, 40218},
		PaymentCode: "^000AAQIDBAUGBwg", ExpiresAt: time.Date(2026, 10, 23, 12, 2, 0, 0, time.UTC),
		Starters: []QuestLicenseStarterPolicy{{Key: questlicense.VisaStarterKey, ItemID: 1859, Quantity: 1}}}
	preview := QuestLicensePreview{Quote: quote, Title: "VISA permanent license"}
	for _, id := range quote.QuestIDs {
		preview.Quests = append(preview.Quests, QuestLicensePreviewQuest{ID: id, Name: "VISA quest", Conditions: "Original equipment and rank", Publication: "Independent publication"})
	}
	data, err := encodeQuestLicensePreview(preview)
	if err != nil {
		t.Fatal(err)
	}
	hexData, err := os.ReadFile("testdata/quest_license_visa_lpv2.hex")
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(string(bytes.TrimSpace(hexData)))
	if err != nil || !bytes.Equal(data, want) {
		t.Fatal("server encoder differs from shared client fixture", len(data), len(want), err)
	}
}
