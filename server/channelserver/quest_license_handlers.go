package channelserver

import (
	"context"
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/questlicense"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

// A private query never populates native collectible item rows. The campaign
// namespace was checked before accepting clients and is checked again in lists.
func handleQuestLicensePreview(s *Session, pkt *mhfpacket.MsgMhfEnumerateItem) bool {
	id := pkt.CampaignID &^ campaignPreviewFlag
	if !s.server.isQuestLicenseCampaign(id) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	var err error
	if s.server.questLicenseShop != nil {
		data, err = s.server.questLicenseShop.Preview(ctx, s.userID, s.charID, id)
	}
	if err != nil || len(data) == 0 {
		if err != nil && s.logger != nil {
			s.logger.Warn("Quest license preview unavailable", zap.Uint32("campaign_id", id), zap.Error(err))
		}
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
	} else {
		doAckBufSucceed(s, pkt.AckHandle, data)
	}
	return true
}

func handleQuestLicenseApply(s *Session, pkt *mhfpacket.MsgMhfApplyCampaign) bool {
	licenseCode := len(pkt.Code) > 0 && pkt.Code[0] == questLicensePaymentPrefix
	if !licenseCode && !s.server.isQuestLicenseCampaign(pkt.CampaignID) {
		return false
	}
	status := questlicense.WireUnavailable
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	licenses := s.server.questLicenses
	if licenseCode && licenses != nil && licenses.enabled && validQuestLicenseIdentity(s.userID, s.charID, pkt.CampaignID) && validQuestLicensePaymentCode(pkt.Code) {
		if s.server.erupeConfig.QuestLicenses.PurchasesEnabled && s.server.questLicenseShop != nil && len(licenses.catalog.Starters()) == 0 {
			status, err = s.server.questLicenseShop.Apply(ctx, s.userID, s.charID, pkt.CampaignID, pkt.Code)
		} else {
			// Stopping sales cannot turn a committed receipt into a second
			// purchase. This branch only recovers exact authenticated replay.
			var prior QuestLicensePurchaseResult
			var found bool
			prior, found, err = licenses.store.Replay(ctx, s.userID, s.charID, pkt.CampaignID, pkt.Code)
			if err == nil && found {
				status = questLicenseWireStatus(prior.Status)
			}
		}
	}
	if err != nil && s.logger != nil {
		s.logger.Warn("Quest license payment failed", zap.Uint32("campaign_id", pkt.CampaignID), zap.Error(err))
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(status)
	if status == questlicense.WireOK {
		doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
	} else {
		doAckSimpleFail(s, pkt.AckHandle, bf.Data())
	}
	return true
}

func (s *Server) appendQuestLicenseCampaigns(events []CampaignEvent, categories []CampaignCategory, links []CampaignLink, now time.Time) ([]CampaignEvent, []CampaignLink, error) {
	rows := s.questLicenseEvents(now)
	if len(rows) == 0 {
		return events, links, nil
	}
	if err := validateCampaignCatalogSize(len(events)+len(rows), len(categories), len(links)+len(rows)); err != nil {
		return nil, nil, err
	}
	categoryOK := false
	for _, c := range categories {
		if c.ID == 6 && c.Type == 1 {
			categoryOK = true
		}
	}
	if !categoryOK {
		return nil, nil, fmt.Errorf("quest license event house category6 is unavailable")
	}
	used := make(map[uint32]bool, len(events)+len(rows))
	for _, e := range events {
		used[e.ID] = true
	}
	events = append([]CampaignEvent(nil), events...)
	links = append([]CampaignLink(nil), links...)
	for _, e := range rows {
		if used[e.ID] {
			return nil, nil, fmt.Errorf("quest license campaign ID collision: %d", e.ID)
		}
		used[e.ID] = true
		events = append(events, e)
		links = append(links, CampaignLink{CategoryID: 6, CampaignID: e.ID})
	}
	return events, links, nil
}
