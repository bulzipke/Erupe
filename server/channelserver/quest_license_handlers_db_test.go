package channelserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
	"erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestQuestLicenseHandlersKeepNativeRewardsEmptyAndSalesClosed(t *testing.T) {
	_, db, user, char := setupQuestLicenseRepo(t)
	now := time.Now()
	catalog := starterLicenseCatalog(t, now, true)
	srv := createMockServer()
	srv.db = db
	srv.erupeConfig.RealClientMode = config.ZZ
	srv.erupeConfig.QuestLicenses = config.QuestLicenseOptions{Enabled: true}
	srv.erupeConfig.QuestLicenseCatalog = catalog
	srv.erupeConfig.QuestLicenseDisplay = shopDisplay(t, catalog, 123)
	if err := srv.initializeQuestLicenses(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := createMockSession(char, srv)
	s.userID = user
	for _, packet := range []mhfpacket.MHFPacket{
		&mhfpacket.MsgMhfStateCampaign{AckHandle: 1, CampaignID: 123},
		&mhfpacket.MsgMhfEnumerateItem{AckHandle: 2, CampaignID: 123},
	} {
		switch packet.(type) {
		case *mhfpacket.MsgMhfStateCampaign:
			handleMsgMhfStateCampaign(s, packet)
		case *mhfpacket.MsgMhfEnumerateItem:
			handleMsgMhfEnumerateItem(s, packet)
		}
		ack := licenseHandlerAck(t, s)
		if ack.ErrorCode != 0 || !ack.IsBufferResponse || !bytes.Equal(ack.AckData, make([]byte, 4)) {
			t.Fatal("license escaped into native collectible rewards", ack)
		}
	}
	handleMsgMhfEnumerateItem(s, &mhfpacket.MsgMhfEnumerateItem{AckHandle: 3, CampaignID: 123 | campaignPreviewFlag})
	preview := licenseHandlerAck(t, s)
	if preview.ErrorCode != 0 || len(preview.AckData) < 40 || string(preview.AckData[:4]) != "LPV2" || binary.BigEndian.Uint16(preview.AckData[14:16]) != 10 {
		t.Fatal("private preview did not reach wired shop", preview)
	}
	code := string(preview.AckData[22:38])
	handleMsgMhfApplyCampaign(s, &mhfpacket.MsgMhfApplyCampaign{AckHandle: 4, CampaignID: 123, Code: code})
	if ack := licenseHandlerAck(t, s); ack.ErrorCode != 1 || binary.BigEndian.Uint32(ack.AckData) != questlicense.WireUnavailable {
		t.Fatal("closed sale accepted new payment", ack)
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
	// Seed only through the backend to verify recovery of an already committed
	// receipt while the native payment gate stays off throughout this test.
	if paid, err := srv.questLicenseShop.Apply(context.Background(), user, char, 123, code); err != nil || paid != questlicense.WireOK {
		t.Fatal(paid, err)
	}
	handleMsgMhfApplyCampaign(s, &mhfpacket.MsgMhfApplyCampaign{AckHandle: 5, CampaignID: 123, Code: code})
	if ack := licenseHandlerAck(t, s); ack.ErrorCode != 0 || binary.BigEndian.Uint32(ack.AckData) != questlicense.WireOK {
		t.Fatal("closed sale lost committed replay", ack)
	}
	assertQuestLicensePurchaseState(t, db, user, 190, 1, 1)
	var native, starters int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM campaigns WHERE id=123),
		(SELECT count(*) FROM quest_license_starters WHERE user_id=$1)`, user).Scan(&native, &starters); err != nil || native != 0 || starters != 0 {
		t.Fatal("preview/payment seeded native campaign or collected starter", native, starters, err)
	}
}
