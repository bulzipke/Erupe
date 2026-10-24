// Package questlicense describes permanent account quest rights. Ownership
// never replaces publication schedules, entry items, character progression,
// equipment checks, or character-specific completion and reward limits.
package questlicense

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"time"
)

const (
	MaxDocumentBytes = 4 << 20
	MaxDefinitions   = 512
	MaxProductKeys   = 64
	MaxQuestMappings = 32768

	MaxStarterDefinitions     = 512
	MaxProductStarters        = 64
	MaxStarterLegacyCampaigns = 64
)

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
var fieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Definition is one reusable permanent right. Multiple independent rights may
// allow the same quest; an account needs any one, rather than every matching key.
type Definition struct {
	Key      string `json:"key"`
	QuestIDs []int  `json:"quest_ids"`
}

// StarterDefinition is a separate, once-per-character standard-item benefit.
// LicenseKeys are equivalent qualifying rights: owning any one qualifies.
// LegacyCampaignIDs identify historical delivery paths to check for duplicate
// receipt; they do not grant account rights or waive existing entry conditions.
// Persistent binding must retain the complete policy after products are retired.
type StarterDefinition struct {
	Key               string   `json:"key"`
	LicenseKeys       []string `json:"license_keys"`
	ItemID            uint16   `json:"item_id"`
	Quantity          uint16   `json:"quantity"`
	LegacyCampaignIDs []uint32 `json:"legacy_campaign_ids"`
}

// Product is a trusted server-side proposal. A disabled product may omit price
// and sale dates. Enabled products require a positive price and bounded sale
// window. The sale window limits purchases, never the lifetime of earned rights.
type Product struct {
	CampaignID  uint32     `json:"campaign_id"`
	Enabled     bool       `json:"enabled"`
	Price       *uint32    `json:"price_gold_g,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	LicenseKeys []string   `json:"license_keys"`
	StarterKeys []string   `json:"starter_keys,omitempty"`
}

type Document struct {
	Version  string              `json:"version"`
	Licenses []Definition        `json:"licenses"`
	Products []Product           `json:"products"`
	Starters []StarterDefinition `json:"starters,omitempty"`
}

// Offer is a validated, detached server-side snapshot. It must not be rebuilt
// from client-provided price, keys or dates. Its keys are in canonical order.
type Offer struct {
	CampaignID  uint32
	Enabled     bool
	Price       uint32
	StartsAt    time.Time
	EndsAt      time.Time
	LicenseKeys []string
	StarterKeys []string
}

// Catalog is immutable and safe for concurrent reads. All slice accessors copy.
type Catalog struct {
	version       string
	products      map[uint32]Offer
	questKeys     map[int][]string
	offerDigests  map[uint32][32]byte
	offerQuestIDs map[uint32][]int
	definitions   []Definition
	starters      map[string]StarterDefinition
	starterKeys   []string
}

func Parse(data []byte) (*Catalog, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("quest license document exceeds %d bytes", MaxDocumentBytes)
	}
	if err := uniqueFields(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return nil, fmt.Errorf("quest license document: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("quest license document: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("quest license document must contain exactly one JSON value")
	}
	return New(doc)
}

// encoding/json otherwise accepts duplicate fields and case-insensitive names.
// Reject those ambiguities before decoding purchase policy into typed fields.
func uniqueFields(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting is too deep")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			nameToken, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || !fieldPattern.MatchString(name) || seen[name] {
				return fmt.Errorf("duplicate or noncanonical JSON field %q", name)
			}
			seen[name] = true
			if err := uniqueFields(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueFields(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func New(doc Document) (*Catalog, error) {
	if !versionPattern.MatchString(doc.Version) {
		return nil, fmt.Errorf("invalid quest license document version")
	}
	if len(doc.Licenses) > MaxDefinitions || len(doc.Products) > MaxDefinitions || len(doc.Starters) > MaxStarterDefinitions {
		return nil, fmt.Errorf("too many quest license definitions, products or starters")
	}
	c := &Catalog{version: doc.Version, products: make(map[uint32]Offer), questKeys: make(map[int][]string),
		offerDigests: make(map[uint32][32]byte), offerQuestIDs: make(map[uint32][]int),
		starters: make(map[string]StarterDefinition)}
	known := make(map[string]bool, len(doc.Licenses))
	licenseQuests := make(map[string][]int, len(doc.Licenses))
	mappings := 0
	for _, license := range doc.Licenses {
		if !keyPattern.MatchString(license.Key) || known[license.Key] || len(license.QuestIDs) == 0 {
			return nil, fmt.Errorf("invalid, duplicate or empty license %q", license.Key)
		}
		known[license.Key] = true
		mappings += len(license.QuestIDs)
		if mappings > MaxQuestMappings {
			return nil, fmt.Errorf("too many quest license mappings")
		}
		seen := make(map[int]bool, len(license.QuestIDs))
		for _, id := range license.QuestIDs {
			if id < 1 || id > 65535 || seen[id] {
				return nil, fmt.Errorf("invalid or duplicate quest ID in license %q", license.Key)
			}
			seen[id] = true
			c.questKeys[id] = append(c.questKeys[id], license.Key)
		}
		licenseQuests[license.Key] = append([]int(nil), license.QuestIDs...)
		sort.Ints(licenseQuests[license.Key])
	}
	for id := range c.questKeys {
		sort.Strings(c.questKeys[id])
	}
	for key, ids := range licenseQuests {
		c.definitions = append(c.definitions, Definition{Key: key, QuestIDs: ids})
	}
	sort.Slice(c.definitions, func(i, j int) bool { return c.definitions[i].Key < c.definitions[j].Key })
	for _, starter := range doc.Starters {
		if _, exists := c.starters[starter.Key]; exists {
			return nil, fmt.Errorf("duplicate quest starter %q", starter.Key)
		}
		canonical, err := canonicalStarter(starter, known)
		if err != nil {
			return nil, err
		}
		c.starters[canonical.Key] = canonical
		c.starterKeys = append(c.starterKeys, canonical.Key)
	}
	sort.Strings(c.starterKeys)
	for _, product := range doc.Products {
		if product.CampaignID == 0 || product.CampaignID > math.MaxInt32 {
			return nil, fmt.Errorf("invalid quest license campaign ID")
		}
		if _, exists := c.products[product.CampaignID]; exists {
			return nil, fmt.Errorf("duplicate quest license campaign %d", product.CampaignID)
		}
		if len(product.LicenseKeys) == 0 || len(product.LicenseKeys) > MaxProductKeys {
			return nil, fmt.Errorf("invalid key count for campaign %d", product.CampaignID)
		}
		offer := Offer{CampaignID: product.CampaignID, Enabled: product.Enabled}
		if product.Price != nil {
			if *product.Price < 1 || *product.Price > 65535 {
				return nil, fmt.Errorf("invalid price for campaign %d", product.CampaignID)
			}
			offer.Price = *product.Price
		}
		if (product.StartsAt == nil) != (product.EndsAt == nil) {
			return nil, fmt.Errorf("incomplete sale dates for campaign %d", product.CampaignID)
		}
		if product.StartsAt != nil {
			if product.StartsAt.IsZero() || product.EndsAt.IsZero() || !product.StartsAt.Before(*product.EndsAt) {
				return nil, fmt.Errorf("invalid sale dates for campaign %d", product.CampaignID)
			}
			offer.StartsAt, offer.EndsAt = *product.StartsAt, *product.EndsAt
		}
		if product.Enabled && (offer.Price == 0 || offer.StartsAt.IsZero()) {
			return nil, fmt.Errorf("enabled campaign %d requires a price and sale dates", product.CampaignID)
		}
		seen := make(map[string]bool, len(product.LicenseKeys))
		for _, key := range product.LicenseKeys {
			if !known[key] || seen[key] {
				return nil, fmt.Errorf("unknown or duplicate license %q in campaign %d", key, product.CampaignID)
			}
			seen[key] = true
			offer.LicenseKeys = append(offer.LicenseKeys, key)
		}
		sort.Strings(offer.LicenseKeys)
		if len(product.StarterKeys) > MaxProductStarters {
			return nil, fmt.Errorf("too many starters for campaign %d", product.CampaignID)
		}
		seenStarters := make(map[string]bool, len(product.StarterKeys))
		for _, key := range product.StarterKeys {
			starter, exists := c.starters[key]
			if !exists || seenStarters[key] {
				return nil, fmt.Errorf("unknown or duplicate starter %q in campaign %d", key, product.CampaignID)
			}
			qualifies := false
			for _, licenseKey := range starter.LicenseKeys {
				if seen[licenseKey] {
					qualifies = true
					break
				}
			}
			if !qualifies {
				return nil, fmt.Errorf("campaign %d grants no qualifying right for starter %q", product.CampaignID, key)
			}
			seenStarters[key] = true
			offer.StarterKeys = append(offer.StarterKeys, key)
		}
		// A right's once-per-character benefit exists independently of which
		// product sold it. Require every qualifying product to disclose that
		// policy, rather than allowing an incomplete preview/quote snapshot.
		for _, key := range c.starterKeys {
			if seenStarters[key] {
				continue
			}
			for _, licenseKey := range c.starters[key].LicenseKeys {
				if seen[licenseKey] {
					return nil, fmt.Errorf("campaign %d omits qualifying starter %q", product.CampaignID, key)
				}
			}
		}
		sort.Strings(offer.StarterKeys)
		digest, questIDs, err := digestOffer(doc.Version, offer, licenseQuests, c.starters)
		if err != nil {
			return nil, fmt.Errorf("quest license campaign %d snapshot: %w", product.CampaignID, err)
		}
		c.products[offer.CampaignID] = offer
		c.offerDigests[offer.CampaignID] = digest
		c.offerQuestIDs[offer.CampaignID] = questIDs
	}
	return c, nil
}

func canonicalStarter(starter StarterDefinition, known map[string]bool) (StarterDefinition, error) {
	if !keyPattern.MatchString(starter.Key) || starter.ItemID == 0 || starter.Quantity == 0 ||
		len(starter.LicenseKeys) == 0 || len(starter.LicenseKeys) > MaxProductKeys ||
		len(starter.LegacyCampaignIDs) > MaxStarterLegacyCampaigns {
		return StarterDefinition{}, fmt.Errorf("invalid quest starter %q", starter.Key)
	}
	canonical := cloneStarter(starter)
	sort.Strings(canonical.LicenseKeys)
	for i, key := range canonical.LicenseKeys {
		if !known[key] || i > 0 && key == canonical.LicenseKeys[i-1] {
			return StarterDefinition{}, fmt.Errorf("unknown or duplicate qualifying right %q in starter %q", key, starter.Key)
		}
	}
	sort.Slice(canonical.LegacyCampaignIDs, func(i, j int) bool { return canonical.LegacyCampaignIDs[i] < canonical.LegacyCampaignIDs[j] })
	for i, id := range canonical.LegacyCampaignIDs {
		if id == 0 || id > math.MaxInt32 || i > 0 && id == canonical.LegacyCampaignIDs[i-1] {
			return StarterDefinition{}, fmt.Errorf("invalid or duplicate legacy campaign in starter %q", starter.Key)
		}
	}
	return canonical, nil
}

func cloneStarter(starter StarterDefinition) StarterDefinition {
	starter.LicenseKeys = append([]string(nil), starter.LicenseKeys...)
	starter.LegacyCampaignIDs = append([]uint32(nil), starter.LegacyCampaignIDs...)
	return starter
}

// digestOffer includes each key's own mapping, not only the union of quest IDs.
// Keeping the canonical snapshot typed makes boundaries unambiguous and avoids
// dependence on map iteration, input ordering or the sale timestamps' offsets.
func digestOffer(version string, offer Offer, licenseQuests map[string][]int, starters map[string]StarterDefinition) ([32]byte, []int, error) {
	snapshot := struct {
		Schema      string              `json:"schema"`
		Version     string              `json:"version"`
		CampaignID  uint32              `json:"campaign_id"`
		Enabled     bool                `json:"enabled"`
		Price       uint32              `json:"price_gold_g"`
		StartsAt    string              `json:"starts_at"`
		EndsAt      string              `json:"ends_at"`
		LicenseKeys []string            `json:"license_keys"`
		Licenses    []Definition        `json:"licenses"`
		StarterKeys []string            `json:"starter_keys"`
		Starters    []StarterDefinition `json:"starters"`
	}{
		Schema: "quest-license-offer-v2", Version: version,
		CampaignID: offer.CampaignID, Enabled: offer.Enabled, Price: offer.Price,
		StartsAt: offer.StartsAt.UTC().Format(time.RFC3339Nano), EndsAt: offer.EndsAt.UTC().Format(time.RFC3339Nano),
		LicenseKeys: offer.LicenseKeys, StarterKeys: offer.StarterKeys,
	}
	unique := make(map[int]bool)
	for _, key := range offer.LicenseKeys {
		ids := licenseQuests[key]
		snapshot.Licenses = append(snapshot.Licenses, Definition{Key: key, QuestIDs: ids})
		for _, id := range ids {
			unique[id] = true
		}
	}
	for _, key := range offer.StarterKeys {
		snapshot.Starters = append(snapshot.Starters, starters[key])
	}
	questIDs := make([]int, 0, len(unique))
	for id := range unique {
		questIDs = append(questIDs, id)
	}
	sort.Ints(questIDs)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return [32]byte{}, nil, err
	}
	return sha256.Sum256(encoded), questIDs, nil
}

func (c *Catalog) Version() string {
	if c == nil {
		return ""
	}
	return c.version
}

func (c *Catalog) Offer(campaignID uint32) (Offer, bool) {
	if c == nil {
		return Offer{}, false
	}
	offer, ok := c.products[campaignID]
	offer.LicenseKeys = append([]string(nil), offer.LicenseKeys...)
	offer.StarterKeys = append([]string(nil), offer.StarterKeys...)
	return offer, ok
}

// Starter returns a detached policy; it does not authorize or deliver a claim.
func (c *Catalog) Starter(key string) (StarterDefinition, bool) {
	if c == nil {
		return StarterDefinition{}, false
	}
	starter, ok := c.starters[key]
	return cloneStarter(starter), ok
}

// Starters is the sorted, detached lifetime policy registry, including policies
// with no active product. Persistent binding must reject removal or mutation.
func (c *Catalog) Starters() []StarterDefinition {
	if c == nil {
		return nil
	}
	starters := make([]StarterDefinition, 0, len(c.starterKeys))
	for _, key := range c.starterKeys {
		starters = append(starters, cloneStarter(c.starters[key]))
	}
	return starters
}

// StartersForOffer returns its complete policies sorted by key, detached from
// storage. Disabled products retain their metadata. Unknown products return nil.
func (c *Catalog) StartersForOffer(campaignID uint32) []StarterDefinition {
	if c == nil {
		return nil
	}
	starters := make([]StarterDefinition, 0, len(c.products[campaignID].StarterKeys))
	for _, key := range c.products[campaignID].StarterKeys {
		starters = append(starters, cloneStarter(c.starters[key]))
	}
	if len(starters) == 0 {
		return nil
	}
	return starters
}

func (c *Catalog) KeysForQuest(questID int) []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.questKeys[questID]...)
}

// Definitions is the complete, detached lifetime contract, including rights
// whose products are no longer sold. Persistent definition binding must reject
// removal or mutation of an already registered key before any new purchase.
func (c *Catalog) Definitions() []Definition {
	if c == nil {
		return nil
	}
	definitions := make([]Definition, len(c.definitions))
	for i, definition := range c.definitions {
		definitions[i] = Definition{Key: definition.Key, QuestIDs: append([]int(nil), definition.QuestIDs...)}
	}
	return definitions
}

// OfferDigest binds a quote to the complete validated offer, including catalog
// version, every key's quest mapping and its full starter policies. Disabled
// offers still have a digest.
// Unknown offers and a nil catalog return a zero digest with false.
func (c *Catalog) OfferDigest(campaignID uint32) ([32]byte, bool) {
	if c == nil {
		return [32]byte{}, false
	}
	digest, ok := c.offerDigests[campaignID]
	return digest, ok
}

// QuestIDsForOffer returns the sorted distinct quest IDs covered by the offer's
// keys, detached from catalog storage. An unknown offer or nil catalog returns nil.
func (c *Catalog) QuestIDsForOffer(campaignID uint32) []int {
	if c == nil {
		return nil
	}
	return append([]int(nil), c.offerQuestIDs[campaignID]...)
}
