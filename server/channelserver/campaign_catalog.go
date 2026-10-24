package channelserver

import (
	"fmt"

	cfg "erupe-ce/config"
)

// These are Event House table bounds in both ZZ clients, independent of the
// event-quest table: HD 1151b7c0/1151bc30/1151bd60 and non-HD equivalents.
const (
	campaignCatalogEventCapacity    = 512
	campaignCatalogCategoryCapacity = 16
	campaignCatalogLinkCapacity     = 512
)

type CampaignCatalogSnapshot struct {
	Events     []CampaignEvent
	Categories []CampaignCategory
	Links      []CampaignLink
}

// The preflight is part of the opt-in ZZ rollout. Leave unaudited versions and
// the disabled legacy path unchanged. Sale-off still needs a check: expired,
// unlinked and currently unavailable legacy events are all sent to the client.
func (s *Server) campaignCatalogGuardEnabled() bool {
	return s != nil && s.erupeConfig != nil && s.erupeConfig.RealClientMode == cfg.ZZ &&
		(s.erupeConfig.QuestDelivery.Enabled || s.erupeConfig.QuestLicenses.Enabled)
}

func validateCampaignCatalogSize(events, categories, links int) error {
	if events > campaignCatalogEventCapacity || categories > campaignCatalogCategoryCapacity || links > campaignCatalogLinkCapacity {
		return fmt.Errorf("event house catalog exceeds native capacity: events %d/%d, categories %d/%d, links %d/%d",
			events, campaignCatalogEventCapacity, categories, campaignCatalogCategoryCapacity, links, campaignCatalogLinkCapacity)
	}
	return nil
}

// Check the complete final snapshot after virtual products have been appended.
// Counts are wire rows, not distinct IDs or rows in the current sale window.
// Never trim a section: that could hide an existing redemption or pending gift.
func validateCampaignCatalog(snapshot CampaignCatalogSnapshot) error {
	if err := validateCampaignCatalogSize(len(snapshot.Events), len(snapshot.Categories), len(snapshot.Links)); err != nil {
		return err
	}
	events := make(map[uint32]bool, len(snapshot.Events))
	for _, event := range snapshot.Events {
		if event.ID == 0 || events[event.ID] {
			return fmt.Errorf("event house catalog has invalid or duplicate campaign ID %d", event.ID)
		}
		events[event.ID] = true
	}
	categories := make(map[uint16]bool, len(snapshot.Categories))
	for _, category := range snapshot.Categories {
		if category.ID == 0 || categories[category.ID] {
			return fmt.Errorf("event house catalog has invalid or duplicate category ID %d", category.ID)
		}
		categories[category.ID] = true
	}
	for _, link := range snapshot.Links {
		if !events[link.CampaignID] || !categories[link.CategoryID] {
			return fmt.Errorf("event house catalog has dangling category link %d/%d", link.CategoryID, link.CampaignID)
		}
	}
	return nil
}
