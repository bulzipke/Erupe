package channelserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type campaignCatalogStub struct {
	snapshot CampaignCatalogSnapshot
	err      error
	calls    int
}

func (r *campaignCatalogStub) Load(ctx context.Context) (CampaignCatalogSnapshot, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		return CampaignCatalogSnapshot{}, fmt.Errorf("catalog load must be bounded")
	}
	r.calls++
	return r.snapshot, r.err
}

func campaignCatalogFixture(events, categories, links int) CampaignCatalogSnapshot {
	snapshot := CampaignCatalogSnapshot{}
	for i := 0; i < events; i++ {
		// Every event is expired and unpriced. They still occupy native slots.
		snapshot.Events = append(snapshot.Events, CampaignEvent{ID: uint32(1000 + i), Prefix: "TEST", End: time.Unix(1, 0)})
	}
	for i := 0; i < categories; i++ {
		snapshot.Categories = append(snapshot.Categories, CampaignCategory{ID: uint16(i + 1), Type: 1})
	}
	for i := 0; i < links; i++ {
		snapshot.Links = append(snapshot.Links, CampaignLink{CategoryID: uint16(1 + i%categories), CampaignID: uint32(1000 + i%events)})
	}
	return snapshot
}

func TestCampaignCatalogNativeCapacityBoundaries(t *testing.T) {
	for _, row := range []struct {
		events, categories, links int
		valid                     bool
	}{
		{0, 0, 0, true}, {511, 15, 511, true}, {512, 16, 512, true},
		{513, 16, 512, false}, {512, 17, 512, false}, {512, 16, 513, false},
	} {
		t.Run(fmt.Sprintf("%d_%d_%d", row.events, row.categories, row.links), func(t *testing.T) {
			snapshot := campaignCatalogFixture(row.events, row.categories, row.links)
			before := fmt.Sprintf("%#v", snapshot)
			if err := validateCampaignCatalog(snapshot); (err == nil) != row.valid {
				t.Fatalf("valid=%v, want %v: %v", err == nil, row.valid, err)
			}
			if before != fmt.Sprintf("%#v", snapshot) {
				t.Fatal("preflight changed the original catalog")
			}
		})
	}
}

func TestCampaignCatalogInvalidGraph(t *testing.T) {
	for _, row := range []struct {
		name string
		edit func(*CampaignCatalogSnapshot)
	}{
		{"zero_campaign", func(s *CampaignCatalogSnapshot) { s.Events[0].ID = 0 }},
		{"duplicate_campaign", func(s *CampaignCatalogSnapshot) { s.Events[1].ID = s.Events[0].ID }},
		{"zero_category", func(s *CampaignCatalogSnapshot) { s.Categories[0].ID = 0 }},
		{"duplicate_category", func(s *CampaignCatalogSnapshot) { s.Categories[1].ID = s.Categories[0].ID }},
		{"unknown_campaign", func(s *CampaignCatalogSnapshot) { s.Links[0].CampaignID = 99 }},
		{"unknown_category", func(s *CampaignCatalogSnapshot) { s.Links[0].CategoryID = 99 }},
	} {
		t.Run(row.name, func(t *testing.T) {
			snapshot := campaignCatalogFixture(2, 2, 2)
			row.edit(&snapshot)
			if err := validateCampaignCatalog(snapshot); err == nil {
				t.Fatal("invalid native references accepted")
			}
		})
	}
	// Duplicate links are preserved and counted as wire rows, not silently
	// deduplicated. An event with no visible link still occupies a slot.
	snapshot := campaignCatalogFixture(2, 1, 2)
	snapshot.Links[1] = snapshot.Links[0]
	if err := validateCampaignCatalog(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignCatalogGuardOptIn(t *testing.T) {
	var absent *Server
	if absent.campaignCatalogGuardEnabled() || (&Server{}).campaignCatalogGuardEnabled() {
		t.Fatal("absent configuration enabled the guard")
	}
	for _, mode := range []cfg.Mode{cfg.Z2, cfg.ZZ} {
		for _, delivery := range []bool{false, true} {
			for _, licenses := range []bool{false, true} {
				s := createMockServer()
				s.erupeConfig.RealClientMode = mode
				s.erupeConfig.QuestDelivery.Enabled = delivery
				s.erupeConfig.QuestLicenses.Enabled = licenses
				if got, want := s.campaignCatalogGuardEnabled(), mode == cfg.ZZ && (delivery || licenses); got != want {
					t.Fatal(mode, delivery, licenses, got, want)
				}
			}
		}
	}
}

func TestCampaignCatalogHandlerRejectsWholeSnapshot(t *testing.T) {
	for _, row := range []struct {
		events, categories, links int
	}{
		{513, 1, 1}, {1, 17, 1}, {1, 1, 513},
	} {
		for _, useDelivery := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d_%d_%d_delivery_%v", row.events, row.categories, row.links, useDelivery), func(t *testing.T) {
				srv := createMockServer()
				srv.erupeConfig.RealClientMode = cfg.ZZ
				srv.erupeConfig.QuestLicenses.Enabled = !useDelivery
				srv.erupeConfig.QuestDelivery.Enabled = useDelivery
				// No shop or active products: sale-off must not skip the guard.
				session := createMockSession(1, srv)
				store := &campaignCatalogStub{snapshot: campaignCatalogFixture(row.events, row.categories, row.links)}
				enumerateCampaignCatalog(session, &mhfpacket.MsgMhfEnumerateCampaign{AckHandle: 99}, store)
				ack := licenseHandlerAck(t, session)
				if store.calls != 1 || ack.AckHandle != 99 || ack.ErrorCode == 0 || !ack.IsBufferResponse || !bytes.Equal(ack.AckData, make([]byte, 4)) {
					t.Fatal("oversized catalog was partly delivered", ack)
				}
			})
		}
	}
}

func readCampaignTestCount(bf *byteframe.ByteFrame) int {
	count := int(bf.ReadUint8())
	if count == 255 {
		count = int(bf.ReadUint16())
	}
	return count
}

func TestCampaignCatalogHandlerFullBoundaryWire(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.QuestDelivery.Enabled = true
	session := createMockSession(1, srv)
	snapshot := campaignCatalogFixture(512, 16, 512)
	before := fmt.Sprintf("%#v", snapshot)
	enumerateCampaignCatalog(session, &mhfpacket.MsgMhfEnumerateCampaign{AckHandle: 99}, &campaignCatalogStub{snapshot: snapshot})
	ack := licenseHandlerAck(t, session)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatal(ack)
	}
	bf := byteframe.NewByteFrameFromBytes(ack.AckData)
	if readCampaignTestCount(bf) != 512 {
		t.Fatal("event boundary count")
	}
	for i := 0; i < 512; i++ {
		if bf.ReadUint32() != uint32(i+1000) {
			t.Fatal("event order or ID changed")
		}
		bf.ReadBytes(32) // metadata, ranks, reward/receive fields and dates
		if !bf.ReadBool() {
			t.Fatal("expired event was not sent as expired")
		}
		for field := 0; field < 5; field++ {
			bf.ReadBytes(uint(bf.ReadUint8()))
		}
	}
	if readCampaignTestCount(bf) != 512 {
		t.Fatal("prefix boundary count")
	}
	for i := 0; i < 512; i++ {
		if bf.ReadUint32() != uint32(i+1000) || bf.ReadUint8() != 0 || !bytes.Equal(bf.ReadBytes(4), []byte("TEST")) {
			t.Fatal("prefix row changed")
		}
	}
	if readCampaignTestCount(bf) != 16 {
		t.Fatal("category boundary count")
	}
	for i := 0; i < 16; i++ {
		if bf.ReadUint16() != uint16(i+1) || bf.ReadUint8() != 1 {
			t.Fatal("category row changed")
		}
		titleLength, descriptionLength := bf.ReadUint8(), bf.ReadUint8()
		bf.ReadBytes(uint(titleLength) + uint(descriptionLength))
	}
	if readCampaignTestCount(bf) != 512 {
		t.Fatal("link boundary count")
	}
	for _, link := range snapshot.Links {
		if bf.ReadUint16() != link.CategoryID || bf.ReadUint32() != link.CampaignID {
			t.Fatal("link order or target changed")
		}
	}
	if bf.Err() != nil || len(bf.DataFromCurrent()) != 0 || before != fmt.Sprintf("%#v", snapshot) {
		t.Fatal("wire mismatch or source mutation", bf.Err())
	}
}

func TestCampaignCatalogHandlerPreservesLegacyGate(t *testing.T) {
	for _, mode := range []cfg.Mode{cfg.Z2, cfg.ZZ} {
		srv := createMockServer()
		srv.erupeConfig.RealClientMode = mode
		srv.erupeConfig.QuestDelivery.Enabled = mode != cfg.ZZ
		session := createMockSession(1, srv)
		enumerateCampaignCatalog(session, &mhfpacket.MsgMhfEnumerateCampaign{}, &campaignCatalogStub{snapshot: campaignCatalogFixture(513, 1, 1)})
		ack := licenseHandlerAck(t, session)
		if ack.ErrorCode != 0 || readCampaignTestCount(byteframe.NewByteFrameFromBytes(ack.AckData)) != 513 {
			t.Fatal("legacy wire behavior changed", mode, ack)
		}
	}
}

func TestCampaignCatalogHandlerRepositoryFailures(t *testing.T) {
	for _, store := range []CampaignCatalogStore{nil, &campaignCatalogStub{snapshot: campaignCatalogFixture(1, 1, 1), err: errors.New("load failed")}} {
		session := createMockSession(1, createMockServer())
		enumerateCampaignCatalog(session, &mhfpacket.MsgMhfEnumerateCampaign{AckHandle: 123}, store)
		ack := licenseHandlerAck(t, session)
		if ack.AckHandle != 123 || ack.ErrorCode == 0 || !reflect.DeepEqual(ack.AckData, make([]byte, 4)) {
			t.Fatal("repository failure did not ACK", ack)
		}
	}
}

func TestCampaignCatalogGuardAfterVirtualProducts(t *testing.T) {
	session, _, now := licenseHandlerSession(t, false)
	srv := session.server
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.QuestLicenses.PurchasesEnabled = true
	base := campaignCatalogFixture(511, 0, 0)
	base.Categories = []CampaignCategory{{ID: 6, Type: 1}}
	base.Links = []CampaignLink{{CategoryID: 6, CampaignID: 1000}}
	events, links, err := srv.appendQuestLicenseCampaigns(base.Events, base.Categories, base.Links, now)
	if err != nil || len(events) != 512 || len(links) != 2 {
		t.Fatal("valid last native slot rejected", err)
	}
	if err = validateCampaignCatalog(CampaignCatalogSnapshot{Events: events, Categories: base.Categories, Links: links}); err != nil {
		t.Fatal(err)
	}
	base.Events = append(base.Events, CampaignEvent{ID: 9999})
	if events, links, err = srv.appendQuestLicenseCampaigns(base.Events, base.Categories, base.Links, now); err == nil || events != nil || links != nil {
		t.Fatal("virtual product overflow did not reject as a whole")
	}
	base.Events = base.Events[:1]
	base.Links = make([]CampaignLink, 512)
	if events, links, err = srv.appendQuestLicenseCampaigns(base.Events, base.Categories, base.Links, now); err == nil || events != nil || links != nil {
		t.Fatal("virtual product link overflow did not reject as a whole")
	}
}
