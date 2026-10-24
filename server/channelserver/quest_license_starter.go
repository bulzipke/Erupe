package channelserver

import (
	"context"
	"fmt"

	"erupe-ce/common/questlicense"
)

// A starter's eligible keys are alternatives: owning any listed account right
// qualifies. A character still needs its original crafting and quest conditions.
type QuestLicenseStarterPolicy = questlicense.StarterDefinition

type QuestLicenseStarterStatus uint8

const (
	StarterUnavailable QuestLicenseStarterStatus = iota
	StarterPending
	StarterClaimed
	StarterLegacyClaimed
	StarterLegacyPending
	StarterNotOwned
)

type QuestLicenseStarterResult struct {
	Status   QuestLicenseStarterStatus
	RewardID uint32
}

// Starter contracts are bound separately from quest definitions: a broken
// material delivery policy must not revoke a previously earned quest right.
func (s *QuestLicenseService) ensureStarterPolicies(ctx context.Context) error {
	policies := s.catalog.Starters()
	if len(policies) > 0 && s.starters == nil {
		return fmt.Errorf("quest license starters require a store")
	}
	s.starterMu.Lock()
	defer s.starterMu.Unlock()
	if s.startersBoundTo == s.catalog {
		return nil
	}
	// The empty registry is also a contract. Skipping it would allow removing
	// every starter from a later catalog while continuing to sell its rights.
	if err := s.store.BindStarterPolicies(ctx, policies); err != nil {
		return fmt.Errorf("bind quest license starter policies: %w", err)
	}
	s.startersBoundTo = s.catalog
	return nil
}

func (s *QuestLicenseService) ensurePurchaseContracts(ctx context.Context) error {
	if err := s.ensureDefinitions(ctx); err != nil {
		return err
	}
	return s.ensureStarterPolicies(ctx)
}

// EnsureStarter can be retried after purchase, login, or a lost response. It also
// covers another character on the owning account without another paid purchase.
// Queueing is durable and separate from the native client's inventory save.
func (s *QuestLicenseService) EnsureStarter(ctx context.Context, userID, characterID uint32, key string) (QuestLicenseStarterResult, error) {
	unavailable := QuestLicenseStarterResult{Status: StarterUnavailable}
	if s == nil || !s.enabled || s.catalog == nil || s.store == nil || s.starters == nil ||
		!validQuestLicenseIdentity(userID, characterID, 1) {
		return unavailable, nil
	}
	policy, ok := s.catalog.Starter(key)
	if !ok {
		return unavailable, nil
	}
	if err := s.ensurePurchaseContracts(ctx); err != nil {
		return unavailable, err
	}
	return s.starters.Ensure(ctx, userID, characterID, policy)
}

// PendingStarter queues if eligible, then returns only its native item receipts.
// Legacy pending rewards remain on their original collection route.
func (s *QuestLicenseService) PendingStarter(ctx context.Context, userID, characterID uint32, key string) ([]CampaignReward, error) {
	result, err := s.EnsureStarter(ctx, userID, characterID, key)
	if err != nil || result.Status != StarterPending {
		return nil, err
	}
	return s.starters.Pending(ctx, userID, characterID, key)
}

// AcquireStarters acknowledges durable receipts. It does not insert inventory,
// manufacture campaign stamps, or bypass the client's normal collection/save.
// Exact committed receipt replay remains possible after a sale is removed.
func (s *QuestLicenseService) AcquireStarters(ctx context.Context, userID, characterID uint32, ids []uint32) error {
	if s == nil || !s.enabled || s.starters == nil || !validQuestLicenseIdentity(userID, characterID, 1) {
		return fmt.Errorf("quest license starter collection unavailable")
	}
	return s.starters.Acquire(ctx, userID, characterID, append([]uint32(nil), ids...))
}
