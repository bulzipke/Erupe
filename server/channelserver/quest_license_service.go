package channelserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"erupe-ce/common/questlicense"
)

// QuestLicenseService is an opt-in backend. Its zero value is disabled and no
// gameplay handler constructs or invokes it yet. Quote/PurchaseQuoted bind a
// future authenticated client preview to its exact permanent right contract.
type QuestLicenseService struct {
	enabled  bool
	catalog  *questlicense.Catalog
	store    QuestLicenseStore
	quotes   *questLicenseQuotes
	starters QuestLicenseStarterStore

	definitionsMu      sync.Mutex
	definitionsBoundTo *questlicense.Catalog
	starterMu          sync.Mutex
	startersBoundTo    *questlicense.Catalog
}

// Bind once per immutable catalog. Failed or cancelled binding is retryable;
// a definition mismatch never permits a new purchase or an account right read.
// This service mutex is independent of all Server/Stage/Session gameplay locks.
func (s *QuestLicenseService) ensureDefinitions(ctx context.Context) error {
	s.definitionsMu.Lock()
	defer s.definitionsMu.Unlock()
	if s.definitionsBoundTo == s.catalog {
		return nil
	}
	if err := s.store.BindDefinitions(ctx, s.catalog.Definitions()); err != nil {
		return fmt.Errorf("bind permanent quest license definitions: %w", err)
	}
	s.definitionsBoundTo = s.catalog
	return nil
}

func NewQuestLicenseService(enabled bool, catalog *questlicense.Catalog, store QuestLicenseStore) (*QuestLicenseService, error) {
	return NewQuestLicenseServiceWithStarters(enabled, catalog, store, nil)
}

func NewQuestLicenseServiceWithStarters(enabled bool, catalog *questlicense.Catalog, store QuestLicenseStore, starters QuestLicenseStarterStore) (*QuestLicenseService, error) {
	if enabled && (catalog == nil || store == nil) {
		return nil, fmt.Errorf("enabled quest license service requires a catalog and store")
	}
	if enabled && len(catalog.Starters()) > 0 && starters == nil {
		return nil, fmt.Errorf("enabled quest license starter policy requires a store")
	}
	s := &QuestLicenseService{enabled: enabled, catalog: catalog, store: store, starters: starters}
	if enabled {
		s.quotes = newQuestLicenseQuotes()
	}
	return s, nil
}

// Purchase accepts an authenticated, server-validated quote snapshot. Never
// create request.LicenseKeys from an untrusted packet or from the current offer
// in place of the previewed snapshot. The repository compares committed replay
// identity before the current sale so an old successful request cannot be charged
// again. An unknown or globally disabled product never initiates a new purchase.
func (s *QuestLicenseService) Purchase(ctx context.Context, request QuestLicensePurchaseRequest) (QuestLicensePurchaseResult, error) {
	if s != nil && s.enabled && s.catalog != nil && s.store != nil {
		// The legacy internal trusted-snapshot API also binds definitions. New
		// packet integration must use PurchaseQuoted, whose committed replay
		// deliberately precedes every catalog and definition check.
		if err := s.ensurePurchaseContracts(ctx); err != nil {
			return QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}, err
		}
	}
	return s.purchase(ctx, request, time.Time{})
}

func (s *QuestLicenseService) purchase(ctx context.Context, request QuestLicensePurchaseRequest, quoteExpiry time.Time) (QuestLicensePurchaseResult, error) {
	unavailable := QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}
	if s == nil || !s.enabled || s.catalog == nil || s.store == nil {
		return unavailable, nil
	}
	product, _ := s.catalog.Offer(request.CampaignID)
	// The repository reads its clock after acquiring the account row lock. This
	// also rejects a quote that expires while waiting behind another purchase.
	if !quoteExpiry.IsZero() && quoteExpiry.Before(product.EndsAt) {
		product.EndsAt = quoteExpiry
	}
	// A removed offer is an empty, disabled sale. The store may acknowledge its
	// exact committed receipt but cannot initiate a new purchase from that sale.
	// Copy both sides of the store boundary. A caller changing the input slice
	// after return or a store retaining its arguments cannot mutate the catalog.
	request.LicenseKeys = append([]string(nil), request.LicenseKeys...)
	return s.store.Purchase(ctx, request, QuestLicenseSale{
		CampaignID: product.CampaignID, Enabled: product.Enabled, Price: product.Price,
		LicenseKeys: product.LicenseKeys, Start: product.StartsAt, End: product.EndsAt,
	})
}

// HasQuestLicense checks only the new account right, using OR for alternative
// licenses that map to the same quest. False means this backend provides no right;
// it must not revoke a legacy character code. True is not an entry permission:
// publication, original items, equipment and completion limits still apply.
func (s *QuestLicenseService) HasQuestLicense(ctx context.Context, userID uint32, questID int) (bool, error) {
	if s == nil || !s.enabled || s.catalog == nil || s.store == nil {
		return false, nil
	}
	if err := s.ensureDefinitions(ctx); err != nil {
		return false, err
	}
	keys := s.catalog.KeysForQuest(questID)
	if len(keys) == 0 {
		return false, nil
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	owned, err := s.store.Owned(ctx, userID, keys)
	if err != nil {
		return false, err
	}
	for _, key := range owned {
		if !allowed[key] {
			return false, fmt.Errorf("store returned an unrelated quest license")
		}
	}
	return len(owned) > 0, nil
}
