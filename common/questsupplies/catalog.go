// Package questsupplies describes seasonal standard-item supplies independently
// of permanent quest licenses. It prepares trusted local intents only: it never
// charges currency, grants items, records claims, allocates campaign IDs, or
// enables a live sale. A future adapter must preserve the original quest rules,
// perform durable idempotency and payment/delivery transactions, and verify the
// native inventory/save path before enabling anything.
package questsupplies

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

type SupplyKind string

const (
	FreeCharacterWindow SupplyKind = "free_character_once_per_window"
	PaidRepeatable      SupplyKind = "paid_repeatable"
	StandardItemType               = uint8(7)
	MaxGoldGPrice                  = uint32(65535)
)

// QuestRules deliberately has no bypass mode. Publication and supply do not
// waive rank, equipment, party/carry restrictions, entry consumption, failure
// behavior, completion/reward limits, or existing progression and receipts.
type QuestRules string

const PreserveOriginalQuestRules QuestRules = "preserve_original_quest_rules"

type ItemStack struct {
	ItemType uint8  `json:"item_type"`
	ItemID   uint16 `json:"item_id"`
	Quantity uint16 `json:"quantity"`
}

// QuestRequirement distinguishes the amount used by an individual quest from
// the amount supplied by one gift or purchase. These are metadata, not proof
// that the current client actually consumes the item.
type QuestRequirement struct {
	QuestID   uint16    `json:"quest_id"`
	EntryItem ItemStack `json:"entry_item"`
}

type Definition struct {
	Key        string             `json:"key"`
	Kind       SupplyKind         `json:"kind"`
	Delivery   ItemStack          `json:"delivery"`
	PriceGoldG uint32             `json:"price_gold_g"`
	QuestRules QuestRules         `json:"quest_rules"`
	Quests     []QuestRequirement `json:"quests"`
}

// Window.ID is an operator-approved stable season identity, not a rolling
// rotation, date, catalog version or campaign row. Editing its dates must not
// create another free claim. A durable adapter must retain the historical ID
// binding and reject relabeling the same season as a new ID.
type Window struct {
	ID        string    `json:"id"`
	SupplyKey string    `json:"supply_key"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
}

type Document struct {
	Version     string       `json:"version"`
	Definitions []Definition `json:"definitions"`
	Windows     []Window     `json:"windows"`
}

// Catalog is immutable and safe for concurrent reads. Constructors and
// accessors copy all slices; digests cover canonical semantic values.
type Catalog struct {
	version     string
	definitions map[string]Definition
	windows     map[string]Window
	digests     map[string][32]byte
	document    Document
	digest      [32]byte
}

var (
	keyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	versionPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	ErrUnknownSupply = errors.New("unknown seasonal supply")
	ErrUnknownWindow = errors.New("season window is not bound")
	ErrWrongWindow   = errors.New("season window belongs to a different supply")
	ErrWindowClosed  = errors.New("outside the season window")
	ErrIdentity      = errors.New("account and character IDs must be nonzero")
	ErrNonce         = errors.New("paid request nonce must be nonzero")
)

func cloneDefinition(d Definition) Definition {
	d.Quests = append([]QuestRequirement(nil), d.Quests...)
	return d
}

func cloneDocument(d Document) Document {
	d.Definitions = append([]Definition(nil), d.Definitions...)
	for i := range d.Definitions {
		d.Definitions[i] = cloneDefinition(d.Definitions[i])
	}
	d.Windows = append([]Window(nil), d.Windows...)
	return d
}

func digest(domain string, value any) [32]byte {
	// Only the package's concrete, validated structs reach this function.
	data, err := json.Marshal(value)
	if err != nil {
		panic("questsupplies: invalid internal digest value: " + err.Error())
	}
	return sha256.Sum256(append([]byte(domain+"\x00"), data...))
}

func validItem(i ItemStack) bool {
	return i.ItemType == StandardItemType && i.ItemID != 0 && i.Quantity != 0
}

func validTime(t time.Time) bool {
	u := t.UTC()
	return !t.IsZero() && u.Year() >= 1 && u.Year() <= 9999
}

func New(input Document) (*Catalog, error) {
	if !versionPattern.MatchString(input.Version) || len(input.Definitions) == 0 || len(input.Definitions) > 256 || len(input.Windows) > 1024 {
		return nil, fmt.Errorf("invalid seasonal supply document")
	}
	c := &Catalog{version: input.Version, definitions: make(map[string]Definition),
		windows: make(map[string]Window), digests: make(map[string][32]byte)}
	for _, value := range input.Definitions {
		d := cloneDefinition(value)
		if !keyPattern.MatchString(d.Key) || !validItem(d.Delivery) || d.QuestRules != PreserveOriginalQuestRules || len(d.Quests) == 0 || len(d.Quests) > 65535 {
			return nil, fmt.Errorf("invalid supply definition %q", d.Key)
		}
		if _, found := c.definitions[d.Key]; found {
			return nil, fmt.Errorf("duplicate supply key %q", d.Key)
		}
		if (d.Kind != FreeCharacterWindow && d.Kind != PaidRepeatable) || (d.Kind == FreeCharacterWindow && d.PriceGoldG != 0) || (d.Kind == PaidRepeatable && (d.PriceGoldG == 0 || d.PriceGoldG > MaxGoldGPrice)) {
			return nil, fmt.Errorf("invalid supply kind or price for %q", d.Key)
		}
		seen := make(map[uint16]bool)
		for _, q := range d.Quests {
			if q.QuestID == 0 || seen[q.QuestID] || !validItem(q.EntryItem) || q.EntryItem.ItemID != d.Delivery.ItemID {
				return nil, fmt.Errorf("invalid or duplicate quest requirement for %q", d.Key)
			}
			seen[q.QuestID] = true
		}
		sort.Slice(d.Quests, func(i, j int) bool { return d.Quests[i].QuestID < d.Quests[j].QuestID })
		c.definitions[d.Key] = d
		c.digests[d.Key] = digest("questsupplies-definition-v1", d)
	}
	bySupply := make(map[string][]Window)
	for _, w := range input.Windows {
		if !keyPattern.MatchString(w.ID) || !validTime(w.StartsAt) || !validTime(w.EndsAt) || !w.StartsAt.Before(w.EndsAt) {
			return nil, fmt.Errorf("invalid season window %q", w.ID)
		}
		if _, found := c.definitions[w.SupplyKey]; !found {
			return nil, fmt.Errorf("unknown supply in season window %q", w.ID)
		}
		if _, found := c.windows[w.ID]; found {
			return nil, fmt.Errorf("duplicate season window ID %q", w.ID)
		}
		w.StartsAt = w.StartsAt.Round(0).UTC()
		w.EndsAt = w.EndsAt.Round(0).UTC()
		c.windows[w.ID] = w
		bySupply[w.SupplyKey] = append(bySupply[w.SupplyKey], w)
	}
	for key, windows := range bySupply {
		sort.Slice(windows, func(i, j int) bool { return windows[i].StartsAt.Before(windows[j].StartsAt) })
		for i := 1; i < len(windows); i++ {
			if windows[i].StartsAt.Before(windows[i-1].EndsAt) {
				return nil, fmt.Errorf("overlapping season windows for %q", key)
			}
		}
	}
	c.document.Version = c.version
	for _, d := range c.definitions {
		c.document.Definitions = append(c.document.Definitions, cloneDefinition(d))
	}
	sort.Slice(c.document.Definitions, func(i, j int) bool { return c.document.Definitions[i].Key < c.document.Definitions[j].Key })
	for _, w := range c.windows {
		c.document.Windows = append(c.document.Windows, w)
	}
	sort.Slice(c.document.Windows, func(i, j int) bool { return c.document.Windows[i].ID < c.document.Windows[j].ID })
	c.digest = digest("questsupplies-catalog-v1", c.document)
	return c, nil
}

func (c *Catalog) Document() Document { return cloneDocument(c.document) }
func (c *Catalog) Digest() [32]byte   { return c.digest }

// DefinitionDigest excludes catalog version and dates, but includes the item,
// price, delivery quantity, quest mappings and preserved-rule contract.
func (c *Catalog) DefinitionDigest(key string) ([32]byte, bool) {
	d, ok := c.digests[key]
	return d, ok
}

func (c *Catalog) Definition(key string) (Definition, bool) {
	d, ok := c.definitions[key]
	return cloneDefinition(d), ok
}

// Identity is supplied by a trusted caller. Prepare does not authenticate an
// account or verify character ownership; that belongs to the future adapter.
type Identity struct {
	AccountID   uint32
	CharacterID uint32
}

type Nonce [16]byte

// FreeClaimScope is the required durable uniqueness contract. Dates, campaign
// IDs, account IDs and catalog versions deliberately do not change this scope.
// The package does not persist or enforce the once-only claim itself.
type FreeClaimScope struct {
	CharacterID uint32
	WindowID    string
}

// PaidScope must be unique across paid requests for the same account, including
// other seasons and characters. A different nonce represents a new purchase.
type PaidScope struct {
	AccountID uint32
	Nonce     Nonce
}

type PaidReplayContract struct {
	Scope         PaidScope
	ContentDigest [32]byte
}

type ReplayComparison uint8

const (
	ReplayInvalid ReplayComparison = iota
	ReplayDifferentScope
	ReplayExactRequest
	ReplayConflict
)

// ComparePaidReplay describes exact retry versus conflicting nonce reuse. A
// future durable adapter must check its committed receipt before fresh-window
// validation, and return an exact committed receipt without another charge or
// grant even after the season closes. This function performs no such DB check.
func ComparePaidReplay(committed, requested PaidReplayContract) ReplayComparison {
	if committed.Scope.AccountID == 0 || committed.Scope.Nonce == (Nonce{}) || committed.ContentDigest == ([32]byte{}) ||
		requested.Scope.AccountID == 0 || requested.Scope.Nonce == (Nonce{}) || requested.ContentDigest == ([32]byte{}) {
		return ReplayInvalid
	}
	if committed.Scope != requested.Scope {
		return ReplayDifferentScope
	}
	if committed.ContentDigest == requested.ContentDigest {
		return ReplayExactRequest
	}
	return ReplayConflict
}

// Preparation is a detached local preview. CampaignID and LiveEnabled remain
// zero/false; it is never a receipt or proof of inventory storage completion.
type Preparation struct {
	Identity         Identity
	Definition       Definition
	Window           Window
	CatalogVersion   string
	DefinitionDigest [32]byte
	RequestDigest    [32]byte
	CampaignID       uint32
	LiveEnabled      bool
}

type Prepared struct {
	preview Preparation
	nonce   Nonce
}

func (p Prepared) Preview() Preparation {
	x := p.preview
	x.Definition = cloneDefinition(x.Definition)
	return x
}

func (p Prepared) FreeClaimScope() (FreeClaimScope, bool) {
	if p.preview.Definition.Kind != FreeCharacterWindow {
		return FreeClaimScope{}, false
	}
	return FreeClaimScope{CharacterID: p.preview.Identity.CharacterID, WindowID: p.preview.Window.ID}, true
}

func (p Prepared) PaidReplayContract() (PaidReplayContract, bool) {
	if p.preview.Definition.Kind != PaidRepeatable {
		return PaidReplayContract{}, false
	}
	return PaidReplayContract{Scope: PaidScope{AccountID: p.preview.Identity.AccountID, Nonce: p.nonce}, ContentDigest: p.preview.RequestDigest}, true
}

// Prepare validates a fresh local intent in [start,end). It has no side effects,
// no receipt lookup and no balance/inventory checks. Drafts with no approved
// window cannot prepare a grant or purchase. Preparing the same scope twice
// remains possible: durable duplication prevention belongs to a later adapter.
func (c *Catalog) Prepare(key, windowID string, identity Identity, nonce Nonce, now time.Time) (Prepared, error) {
	d, exists := c.definitions[key]
	if !exists {
		return Prepared{}, ErrUnknownSupply
	}
	w, exists := c.windows[windowID]
	if !exists {
		return Prepared{}, ErrUnknownWindow
	}
	if w.SupplyKey != key {
		return Prepared{}, ErrWrongWindow
	}
	if identity.AccountID == 0 || identity.CharacterID == 0 {
		return Prepared{}, ErrIdentity
	}
	if d.Kind == PaidRepeatable && nonce == (Nonce{}) {
		return Prepared{}, ErrNonce
	}
	if now.IsZero() || now.Before(w.StartsAt) || !now.Before(w.EndsAt) {
		return Prepared{}, ErrWindowClosed
	}
	dd := c.digests[key]
	content := struct {
		Identity         Identity
		SupplyKey        string
		WindowID         string
		DefinitionDigest [32]byte
	}{identity, key, windowID, dd}
	return Prepared{preview: Preparation{Identity: identity, Definition: cloneDefinition(d), Window: w,
		CatalogVersion: c.version, DefinitionDigest: dd, RequestDigest: digest("questsupplies-request-v1", content)}, nonce: nonce}, nil
}
