package channelserver

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/questlicense"
	"erupe-ce/network/mhfpacket"
)

func licenseHandlerAck(t *testing.T, s *Session) *mhfpacket.MsgSysAck {
	t.Helper()
	select {
	case p := <-s.sendPackets:
		bf := byteframe.NewByteFrameFromBytes(p.data)
		bf.ReadUint16()
		ack := &mhfpacket.MsgSysAck{}
		if err := ack.Parse(bf, s.clientContext); err != nil {
			t.Fatal(err)
		}
		return ack
	case <-time.After(time.Second):
		t.Fatal("missing handler acknowledgement")
		return nil
	}
}

func licenseHandlerSession(t *testing.T, starter bool) (*Session, *questLicenseStoreStub, time.Time) {
	t.Helper()
	now := time.Now()
	catalog := serviceLicenseCatalog(t, true)
	if starter {
		catalog = starterLicenseCatalog(t, now, true)
	}
	store := &questLicenseStoreStub{}
	licenses, err := NewQuestLicenseServiceWithStarters(true, catalog, store, &questLicenseStarterStub{})
	if err != nil {
		t.Fatal(err)
	}
	licenses.quotes.now = func() time.Time { return now }
	display := shopDisplay(t, catalog, 123)
	shop, err := NewQuestLicenseShop(licenses, &questLicensePreviewStoreStub{}, display)
	if err != nil {
		t.Fatal(err)
	}
	srv := createMockServer()
	srv.questLicenses = licenses
	srv.questLicenseShop = shop
	srv.erupeConfig.QuestLicenses.Enabled = true
	srv.erupeConfig.QuestLicenseCatalog = catalog
	srv.erupeConfig.QuestLicenseDisplay = display
	s := createMockSession(2, srv)
	s.userID = 1
	return s, store, now
}

func TestQuestLicenseHandlerPreviewIsPrivateAndBoundToSession(t *testing.T) {
	s, store, _ := licenseHandlerSession(t, true)
	if !handleQuestLicensePreview(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 1, CampaignID: 123 | campaignPreviewFlag}) {
		t.Fatal("known license preview fell through to kits")
	}
	a := licenseHandlerAck(t, s)
	if a.ErrorCode != 0 || !a.IsBufferResponse || len(a.AckData) < 40 || string(a.AckData[:4]) != "LPV2" || binary.BigEndian.Uint16(a.AckData[14:16]) != 10 {
		t.Fatal(a)
	}
	code := string(a.AckData[22:38])
	if _, ok := s.server.questLicenses.quotes.resolve(s.server.questLicenses.catalog, 1, 2, 123, code); !ok {
		t.Fatal("preview did not bind session")
	}
	if store.calls != 0 {
		t.Fatal("preview reached debit")
	}
	if handleQuestLicensePreview(s, &mhfpacket.MsgMhfEnumerateItem{CampaignID: 456 | campaignPreviewFlag}) {
		t.Fatal("unknown legacy campaign consumed")
	}
}

func TestQuestLicenseHandlerSaleOffOnlyAcknowledgesCommittedReplay(t *testing.T) {
	s, store, _ := licenseHandlerSession(t, true)
	code := "^000AAQIDBAUGBwg"
	for _, request := range []*mhfpacket.MsgMhfApplyCampaign{{CampaignID: 123, Code: code}, {CampaignID: 123, Code: "!000AAQIDBAUGBwg"}, {CampaignID: 123, Code: "legacy-code"}} {
		if !handleQuestLicenseApply(s, request) {
			t.Fatal("license fell through to legacy")
		}
		ack := licenseHandlerAck(t, s)
		if ack.ErrorCode != 1 || binary.BigEndian.Uint32(ack.AckData) != questlicense.WireUnavailable {
			t.Fatal(ack)
		}
	}
	if store.calls != 1 {
		t.Fatal("sale-off initiated a purchase or parsed legacy codes", store.calls)
	}
	store.replay = func(u, c, p uint32, token string) (QuestLicensePurchaseResult, bool, error) {
		if u != 1 || c != 2 || p != 123 || token != code {
			t.Fatal(u, c, p, token)
		}
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, Replay: true}, true, nil
	}
	if !handleQuestLicenseApply(s, &mhfpacket.MsgMhfApplyCampaign{CampaignID: 123, Code: code}) || licenseHandlerAck(t, s).ErrorCode != 0 {
		t.Fatal("committed replay lost when sales stopped")
	}
	s.server.erupeConfig.QuestLicenses.PurchasesEnabled = true
	store.replay = nil
	if !handleQuestLicenseApply(s, &mhfpacket.MsgMhfApplyCampaign{CampaignID: 123, Code: code}) || licenseHandlerAck(t, s).ErrorCode != 1 {
		t.Fatal("starter bypassed purchase gate")
	}
	if handleQuestLicenseApply(s, &mhfpacket.MsgMhfApplyCampaign{CampaignID: 456, Code: "ordinary-code"}) {
		t.Fatal("ordinary legacy campaign intercepted")
	}
}

func TestQuestLicenseNativeRowsAreGatedDetachedAndCapacityChecked(t *testing.T) {
	s, _, now := licenseHandlerSession(t, false)
	if rows := s.server.questLicenseEvents(now); len(rows) != 0 {
		t.Fatal("sale-off displayed purchase rows")
	}
	s.server.erupeConfig.QuestLicenses.PurchasesEnabled = true
	rows := s.server.questLicenseEvents(now)
	if len(rows) != 1 || rows[0].ID != 123 || rows[0].CoinPrice != 50 {
		t.Fatal(rows)
	}
	categories := []CampaignCategory{{ID: 6, Type: 1}}
	base := []CampaignEvent{{ID: 999}}
	links := []CampaignLink{{CategoryID: 6, CampaignID: 999}}
	result, joined, err := s.server.appendQuestLicenseCampaigns(base, categories, links, now)
	if err != nil || len(result) != 2 || len(joined) != 2 || len(base) != 1 || len(links) != 1 {
		t.Fatal(result, joined, err)
	}
	for _, test := range []struct {
		events     []CampaignEvent
		categories []CampaignCategory
		links      []CampaignLink
	}{
		{[]CampaignEvent{{ID: 123}}, categories, nil}, {make([]CampaignEvent, 512), categories, nil}, {nil, categories, make([]CampaignLink, 512)}, {nil, make([]CampaignCategory, 17), nil}, {nil, nil, nil},
	} {
		if _, _, err := s.server.appendQuestLicenseCampaigns(test.events, test.categories, test.links, now); err == nil {
			t.Fatal("invalid native catalog accepted")
		}
	}
}

func TestQuestLicenseRuntimeNamespaceIncludesDisabledOffers(t *testing.T) {
	catalog := serviceLicenseCatalog(t, false)
	for _, purchases := range []bool{false, true} {
		if err := validateQuestLicenseRuntime(catalog, QuestLicenseRuntimeSnapshot{CampaignIDs: []uint32{123}, PremiumCategoryAvailable: true}, purchases); err == nil {
			t.Fatal("disabled offer shadowed legacy campaign")
		}
	}
	if err := validateQuestLicenseRuntime(catalog, QuestLicenseRuntimeSnapshot{}, true); err == nil {
		t.Fatal("purchase category missing")
	}
	if err := validateQuestLicenseRuntime(catalog, QuestLicenseRuntimeSnapshot{}, false); err != nil {
		t.Fatal("preview-only catalog needs purchase category", err)
	}
	ids := catalog.CampaignIDs()
	ids[0] = 999
	if got := catalog.CampaignIDs(); len(got) != 1 || got[0] != 123 {
		t.Fatal("namespace accessor leaked mutable storage")
	}
	if err := (&Server{}).initializeQuestLicenses(context.Background()); err != nil {
		t.Fatal("disabled initialization used DB", err)
	}
}
