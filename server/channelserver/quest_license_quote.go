package channelserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"erupe-ce/common/questlicense"
)

const (
	questLicenseQuoteTTL      = 2 * time.Minute
	questLicenseQuoteLimit    = 4096
	questLicenseAccountQuotes = 8
	questLicensePaymentPrefix = '^'
)

// QuestLicenseQuote is a detached preview contract. PaymentCode fits the existing
// 16-byte campaign field, but its prefix is reserved for the license-aware
// client. It must never be submitted through the ordinary item-kit handler.
type QuestLicenseQuote struct {
	CampaignID  uint32
	Price       uint32
	QuestIDs    []int
	PaymentCode string
	ExpiresAt   time.Time
	Starters    []QuestLicenseStarterPolicy
}

type questLicenseQuoteEntry struct {
	request QuestLicensePurchaseRequest
	digest  [32]byte
	expires time.Time
}

// Uncommitted quotes intentionally live only in bounded process memory. A lost
// quote needs a fresh preview. Committed receipts are recovered from PostgreSQL.
type questLicenseQuotes struct {
	mu      sync.Mutex
	entries map[string]questLicenseQuoteEntry
	now     func() time.Time
	random  io.Reader
}

func newQuestLicenseQuotes() *questLicenseQuotes {
	return &questLicenseQuotes{entries: make(map[string]questLicenseQuoteEntry), now: time.Now, random: rand.Reader}
}

func validQuestLicenseIdentity(userID, characterID, campaignID uint32) bool {
	return userID > 0 && userID <= math.MaxInt32 && characterID > 0 && characterID <= math.MaxInt32 && campaignID > 0 && campaignID <= math.MaxInt32
}

// A separate prefix and canonical encoding prevent ordinary kit nonces, aliases,
// truncated input or a modified price from becoming the same quote identity.
func validQuestLicensePaymentCode(code string) bool {
	if len(code) != 16 || code[0] != questLicensePaymentPrefix || code[1:5] != strings.ToUpper(code[1:5]) {
		return false
	}
	price, err := strconv.ParseUint(code[1:5], 16, 16)
	if err != nil || price == 0 || fmt.Sprintf("%04X", price) != code[1:5] {
		return false
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(code[5:])
	return err == nil && len(token) == 8 && base64.RawURLEncoding.EncodeToString(token) == code[5:]
}

func (q *questLicenseQuotes) prune(now time.Time) {
	for code, entry := range q.entries {
		if !now.Before(entry.expires) {
			delete(q.entries, code)
		}
	}
}

func (q *questLicenseQuotes) issue(catalog *questlicense.Catalog, userID, characterID, campaignID uint32) (QuestLicenseQuote, bool, error) {
	if q == nil || !validQuestLicenseIdentity(userID, characterID, campaignID) {
		return QuestLicenseQuote{}, false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	q.prune(now)
	offer, ok := catalog.Offer(campaignID)
	digest, hasDigest := catalog.OfferDigest(campaignID)
	if !ok || !hasDigest || !offer.Enabled || now.Before(offer.StartsAt) || !now.Before(offer.EndsAt) {
		return QuestLicenseQuote{}, false, nil
	}
	accountCount := 0
	for _, entry := range q.entries {
		if entry.request.UserID == userID {
			accountCount++
		}
	}
	if len(q.entries) >= questLicenseQuoteLimit || accountCount >= questLicenseAccountQuotes {
		return QuestLicenseQuote{}, false, nil
	}
	expires := now.Add(questLicenseQuoteTTL)
	if offer.EndsAt.Before(expires) {
		expires = offer.EndsAt
	}
	for range 3 {
		var token [8]byte
		if _, err := io.ReadFull(q.random, token[:]); err != nil {
			return QuestLicenseQuote{}, false, fmt.Errorf("generate quest license quote: %w", err)
		}
		code := fmt.Sprintf("%c%04X%s", questLicensePaymentPrefix, offer.Price, base64.RawURLEncoding.EncodeToString(token[:]))
		if _, exists := q.entries[code]; exists {
			continue
		}
		q.entries[code] = questLicenseQuoteEntry{request: QuestLicensePurchaseRequest{
			UserID: userID, CharacterID: characterID, CampaignID: campaignID, RequestCode: code,
			QuotedPrice: offer.Price, LicenseKeys: offer.LicenseKeys,
		}, digest: digest, expires: expires}
		return QuestLicenseQuote{CampaignID: campaignID, Price: offer.Price, QuestIDs: catalog.QuestIDsForOffer(campaignID), PaymentCode: code, ExpiresAt: expires,
			Starters: catalog.StartersForOffer(campaignID)}, true, nil
	}
	return QuestLicenseQuote{}, false, fmt.Errorf("quest license quote token collision")
}

func (q *questLicenseQuotes) resolve(catalog *questlicense.Catalog, userID, characterID, campaignID uint32, code string) (questLicenseQuoteEntry, bool) {
	if q == nil || !validQuestLicenseIdentity(userID, characterID, campaignID) || !validQuestLicensePaymentCode(code) {
		return questLicenseQuoteEntry{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	q.prune(now)
	entry, ok := q.entries[code]
	if !ok || entry.request.UserID != userID || entry.request.CharacterID != characterID || entry.request.CampaignID != campaignID {
		return questLicenseQuoteEntry{}, false
	}
	digest, current := catalog.OfferDigest(campaignID)
	if !current || digest != entry.digest {
		return questLicenseQuoteEntry{}, false
	}
	entry.request.LicenseKeys = append([]string(nil), entry.request.LicenseKeys...)
	return entry, true
}

// Quote and PurchaseQuoted accept identity only from an authenticated session.
// Native handlers apply the separate purchase and starter collection gates.
func (s *QuestLicenseService) Quote(ctx context.Context, userID, characterID, campaignID uint32) (QuestLicenseQuote, bool, error) {
	if s == nil || !s.enabled || s.catalog == nil || s.store == nil {
		return QuestLicenseQuote{}, false, nil
	}
	if !validQuestLicenseIdentity(userID, characterID, campaignID) {
		return QuestLicenseQuote{}, false, nil
	}
	if err := s.ensurePurchaseContracts(ctx); err != nil {
		return QuestLicenseQuote{}, false, err
	}
	return s.quotes.issue(s.catalog, userID, characterID, campaignID)
}

func (s *QuestLicenseService) PurchaseQuoted(ctx context.Context, userID, characterID, campaignID uint32, code string) (QuestLicensePurchaseResult, error) {
	unavailable := QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}
	if s == nil || !s.enabled || s.catalog == nil || s.store == nil || !validQuestLicenseIdentity(userID, characterID, campaignID) || !validQuestLicensePaymentCode(code) {
		return unavailable, nil
	}
	// Receipt lookup precedes cache/expiry/catalog checks. It never creates or
	// debits a purchase, including after a restart or a product's removal.
	prior, found, err := s.store.Replay(ctx, userID, characterID, campaignID, code)
	if err != nil || found {
		return prior, err
	}
	entry, ok := s.quotes.resolve(s.catalog, userID, characterID, campaignID, code)
	if !ok {
		return unavailable, nil
	}
	if err := s.ensurePurchaseContracts(ctx); err != nil {
		return unavailable, err
	}
	return s.purchase(ctx, entry.request, entry.expires)
}
