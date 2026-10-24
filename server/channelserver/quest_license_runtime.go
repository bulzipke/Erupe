package channelserver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"erupe-ce/common/questlicense"
)

func (s *Server) initializeQuestLicenses(ctx context.Context) error {
	if s == nil || s.erupeConfig == nil || !s.erupeConfig.QuestLicenses.Enabled {
		return nil
	}
	c := s.erupeConfig
	if c.QuestLicenseCatalog == nil || c.QuestLicenseDisplay == nil || s.db == nil {
		return fmt.Errorf("quest license runtime requires validated documents and database")
	}
	if c.QuestLicenses.PurchasesEnabled && len(c.QuestLicenseCatalog.Starters()) > 0 {
		return fmt.Errorf("starter purchase collection/save integration is not enabled")
	}
	state, err := NewQuestLicenseRuntimeRepository(s.db).Load(ctx)
	if err != nil {
		return err
	}
	if err = validateQuestLicenseRuntime(c.QuestLicenseCatalog, state, c.QuestLicenses.PurchasesEnabled); err != nil {
		return err
	}
	store := NewQuestLicenseRepository(s.db)
	licenses, err := NewQuestLicenseServiceWithStarters(true, c.QuestLicenseCatalog, store, NewQuestLicenseStarterRepository(s.db))
	if err != nil {
		return err
	}
	shop, err := NewQuestLicenseShop(licenses, NewQuestLicensePreviewRepository(s.db), c.QuestLicenseDisplay)
	if err != nil {
		return err
	}
	access, err := NewQuestAccessService(NewQuestAccessRepository(s.db), licenses)
	if err != nil {
		return err
	}
	ids := append([]int(nil), state.ProtectedQuestIDs...)
	for _, d := range c.QuestLicenseCatalog.Definitions() {
		ids = append(ids, d.QuestIDs...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	gameplay, err := NewQuestAccessGameplay(access, ids)
	if err != nil {
		return err
	}
	s.questLicenses, s.questLicenseShop, s.questAccess = licenses, shop, gameplay
	return nil
}

func validateQuestLicenseRuntime(catalog *questlicense.Catalog, state QuestLicenseRuntimeSnapshot, purchases bool) error {
	existing := make(map[uint32]bool, len(state.CampaignIDs))
	for _, id := range state.CampaignIDs {
		existing[id] = true
	}
	for _, id := range catalog.CampaignIDs() {
		if existing[id] {
			return fmt.Errorf("quest license campaign ID %d collides with a legacy campaign", id)
		}
	}
	if purchases && !state.PremiumCategoryAvailable {
		return fmt.Errorf("quest license shop requires existing type1 category6")
	}
	return nil
}

func (s *Server) isQuestLicenseCampaign(id uint32) bool {
	if s == nil || s.questLicenses == nil || !s.questLicenses.enabled || s.questLicenses.catalog == nil {
		return false
	}
	_, ok := s.questLicenses.catalog.Offer(id)
	return ok
}

// Sale-off hides native purchase rows; authenticated private preview RPC remains
// available for local verification. It cannot make a disabled product sellable.
func (s *Server) questLicenseEvents(now time.Time) []CampaignEvent {
	if s == nil || s.erupeConfig == nil || !s.erupeConfig.QuestLicenses.PurchasesEnabled || s.questLicenseShop == nil || s.questLicenses == nil || s.erupeConfig.QuestLicenseDisplay == nil {
		return nil
	}
	var rows []CampaignEvent
	for _, d := range s.erupeConfig.QuestLicenseDisplay.Products() {
		o, ok := s.questLicenses.catalog.Offer(d.CampaignID)
		if !ok || !o.Enabled || now.Before(o.StartsAt) || !now.Before(o.EndsAt) {
			continue
		}
		rows = append(rows, CampaignEvent{ID: o.CampaignID, CoinPrice: o.Price, MinHR: -1, MaxHR: -1, MinSR: -1, MaxSR: -1, MinGR: -1, MaxGR: -1,
			RewardType: 1, Stamps: 1, ReceiveType: 1, BackgroundID: 12, Start: o.StartsAt, End: o.EndsAt, Title: d.Title})
	}
	return rows
}
