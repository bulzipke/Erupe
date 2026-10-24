package channelserver

import (
	"context"
	"fmt"

	"erupe-ce/common/questlicense"
)

// QuestLicenseShop adapts authenticated Event House operations to the existing
// license backend. It never accepts client price, key, ownership or display text.
// Handler/configuration gates remain responsible for enabling the native flow.
type QuestLicenseShop struct {
	licenses  *QuestLicenseService
	ownership QuestLicensePreviewStore
	display   *questlicense.DisplayCatalog
}

func NewQuestLicenseShop(licenses *QuestLicenseService, ownership QuestLicensePreviewStore, display *questlicense.DisplayCatalog) (*QuestLicenseShop, error) {
	if licenses != nil && licenses.enabled {
		if licenses.catalog == nil || licenses.store == nil || ownership == nil || display == nil {
			return nil, fmt.Errorf("enabled quest license shop requires backend, ownership and display stores")
		}
		if display.CatalogVersion() != licenses.catalog.Version() {
			return nil, fmt.Errorf("quest license shop display version mismatch")
		}
	}
	return &QuestLicenseShop{licenses: licenses, ownership: ownership, display: display}, nil
}

func (s *QuestLicenseShop) active() bool {
	return s != nil && s.licenses != nil && s.licenses.enabled && s.licenses.catalog != nil && s.licenses.store != nil
}

// Preview returns a complete LPV2 document or nil. Ownership is advisory and is
// checked again at purchase, including coverage by equivalent registered keys.
// A failed/unauthenticated preview never issues a payment quote. An already or
// partially owned product remains viewable; the client and repository both deny
// a fresh debit for those states. The caller must derive identity from session.
func (s *QuestLicenseShop) Preview(ctx context.Context, userID, characterID, campaignID uint32) ([]byte, error) {
	if !s.active() || s.ownership == nil || s.display == nil || !validQuestLicenseIdentity(userID, characterID, campaignID) {
		return nil, nil
	}
	offer, exists := s.licenses.catalog.Offer(campaignID)
	product, displayed := s.display.Offer(campaignID)
	if !exists || !offer.Enabled || !displayed {
		return nil, nil
	}
	if s.display.CatalogVersion() != s.licenses.catalog.Version() || !questLicenseDisplayMatches(product, s.licenses.catalog.QuestIDsForOffer(campaignID)) {
		return nil, fmt.Errorf("quest license shop display contract mismatch")
	}
	if err := s.licenses.ensurePurchaseContracts(ctx); err != nil {
		return nil, err
	}
	ownership, authenticated, err := s.ownership.PreviewOwnership(ctx, userID, characterID, append([]string(nil), offer.LicenseKeys...))
	if err != nil || !authenticated {
		return nil, err
	}
	if ownership > QuestLicenseOwnershipPartial {
		return nil, fmt.Errorf("invalid quest license preview ownership")
	}
	quote, ok, err := s.licenses.Quote(ctx, userID, characterID, campaignID)
	if err != nil || !ok {
		return nil, err
	}
	if !questLicenseDisplayMatches(product, quote.QuestIDs) {
		return nil, fmt.Errorf("quest license quote display contract mismatch")
	}
	rows := make([]QuestLicensePreviewQuest, len(product.Quests))
	for i, quest := range product.Quests {
		rows[i] = QuestLicensePreviewQuest{ID: quest.ID, Name: quest.Name, Conditions: quest.Conditions, Publication: quest.Publication}
	}
	// LPV2 is the only private shop reply. An offer without a starter uses an
	// empty LPV2 extension rather than silently switching the client to LPV1.
	return encodeQuestLicenseStarterPreview(QuestLicensePreview{Quote: quote, Title: product.Title, Ownership: ownership, Quests: rows})
}

func questLicenseDisplayMatches(product questlicense.DisplayProduct, ids []int) bool {
	if len(ids) == 0 || len(product.Quests) != len(ids) {
		return false
	}
	for i, id := range ids {
		if product.Quests[i].ID != id {
			return false
		}
	}
	return true
}

// Apply accepts only the server-issued quote code; its embedded price is never
// used as a fresh client proposal. PurchaseQuoted resolves committed replay
// before current display/catalog/definition checks. No item grant is performed.
func (s *QuestLicenseShop) Apply(ctx context.Context, userID, characterID, campaignID uint32, code string) (uint32, error) {
	if !s.active() {
		return questlicense.WireUnavailable, nil
	}
	result, err := s.licenses.PurchaseQuoted(ctx, userID, characterID, campaignID, code)
	if err != nil {
		return questlicense.WireUnavailable, err
	}
	switch result.Status {
	case QuestLicensePurchaseOK:
		return questlicense.WireOK, nil
	case QuestLicensePurchaseInsufficient:
		return questlicense.WireInsufficient, nil
	case QuestLicensePurchasePriceChanged:
		return questlicense.WirePriceChanged, nil
	case QuestLicensePurchaseAlreadyOwned:
		return questlicense.WireAlreadyOwned, nil
	case QuestLicensePurchasePartialOwned:
		return questlicense.WirePartialOwned, nil
	case QuestLicensePurchaseUnavailable, QuestLicensePurchaseConflict:
		return questlicense.WireUnavailable, nil
	default:
		return questlicense.WireUnavailable, fmt.Errorf("unknown quest license purchase result")
	}
}
