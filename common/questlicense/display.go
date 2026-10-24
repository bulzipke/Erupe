package questlicense

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// DisplayDocument contains trusted source text, not player-supplied names or
// purchase policy. It references an exact catalog version. It never changes a
// quest's actual rank, equipment, publication or progression requirements.
type DisplayDocument struct {
	CatalogVersion string           `json:"catalog_version"`
	Products       []DisplayProduct `json:"products"`
}

type DisplayProduct struct {
	CampaignID uint32         `json:"campaign_id"`
	Title      string         `json:"title"`
	Quests     []DisplayQuest `json:"quests"`
}

type DisplayQuest struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Conditions  string `json:"conditions"`
	Publication string `json:"publication"`
}

// DisplayCatalog is immutable. Accessors return sorted, detached slices. Every
// enabled catalog product must have its complete display contract; disabled
// proposals may be omitted until their source text has been reviewed.
type DisplayCatalog struct {
	version  string
	products map[uint32]DisplayProduct
	ids      []uint32
}

func ParseDisplay(data []byte, catalog *Catalog) (*DisplayCatalog, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("quest license display exceeds document limit")
	}
	if err := uniqueFields(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return nil, fmt.Errorf("quest license display: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc DisplayDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("quest license display: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("quest license display must contain exactly one JSON value")
	}
	return NewDisplay(doc, catalog)
}

func NewDisplay(doc DisplayDocument, catalog *Catalog) (*DisplayCatalog, error) {
	if catalog == nil || !versionPattern.MatchString(doc.CatalogVersion) || doc.CatalogVersion != catalog.Version() {
		return nil, fmt.Errorf("quest license display catalog version mismatch")
	}
	if len(doc.Products) > MaxDefinitions {
		return nil, fmt.Errorf("too many quest license display products")
	}
	display := &DisplayCatalog{version: doc.CatalogVersion, products: make(map[uint32]DisplayProduct)}
	totalRows := 0
	for _, product := range doc.Products {
		if product.CampaignID == 0 || product.CampaignID > math.MaxInt32 {
			return nil, fmt.Errorf("invalid quest license display campaign")
		}
		if _, duplicate := display.products[product.CampaignID]; duplicate {
			return nil, fmt.Errorf("duplicate quest license display campaign %d", product.CampaignID)
		}
		if _, known := catalog.Offer(product.CampaignID); !known {
			return nil, fmt.Errorf("unknown quest license display campaign %d", product.CampaignID)
		}
		expected := catalog.QuestIDsForOffer(product.CampaignID)
		if len(product.Quests) == 0 || len(product.Quests) != len(expected) {
			return nil, fmt.Errorf("incomplete quest license display campaign %d", product.CampaignID)
		}
		totalRows += len(product.Quests)
		if totalRows > MaxQuestMappings {
			return nil, fmt.Errorf("too many quest license display rows")
		}
		canonical := cloneDisplayProduct(product)
		sort.Slice(canonical.Quests, func(i, j int) bool { return canonical.Quests[i].ID < canonical.Quests[j].ID })
		titleBytes, err := displayTextBytes(canonical.Title, 96, true)
		if err != nil {
			return nil, fmt.Errorf("quest license display title: %w", err)
		}
		// LPV2's fixed header/title length plus its starter-count extension.
		wireBytes := 42 + titleBytes
		for i, quest := range canonical.Quests {
			if quest.ID != expected[i] {
				return nil, fmt.Errorf("quest license display rows differ from campaign %d", product.CampaignID)
			}
			wireBytes += 8
			for _, field := range []struct {
				value    string
				limit    int
				required bool
			}{{quest.Name, 120, true}, {quest.Conditions, 512, false}, {quest.Publication, 512, false}} {
				size, err := displayTextBytes(field.value, field.limit, field.required)
				if err != nil {
					return nil, fmt.Errorf("quest license display quest %d: %w", quest.ID, err)
				}
				wireBytes += size
			}
		}
		for _, starter := range catalog.StartersForOffer(product.CampaignID) {
			wireBytes += 10 + len(starter.Key)
		}
		if wireBytes > 0x4000 {
			return nil, fmt.Errorf("quest license display campaign %d exceeds LPV2 capacity", product.CampaignID)
		}
		display.products[product.CampaignID] = canonical
		display.ids = append(display.ids, product.CampaignID)
	}
	for id, offer := range catalog.products {
		if _, present := display.products[id]; offer.Enabled && !present {
			return nil, fmt.Errorf("enabled quest license campaign %d lacks display text", id)
		}
	}
	sort.Slice(display.ids, func(i, j int) bool { return display.ids[i] < display.ids[j] })
	return display, nil
}

// Match the LPV serializers' strict CP949 byte/control policy without changing
// the source text. No translation, shortening, whitespace repair or item-name
// lookup occurs while validating a trusted display document.
func displayTextBytes(value string, limit int, required bool) (int, error) {
	if len(value) > limit*3 || required && strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("empty or oversized display text")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return 0, fmt.Errorf("display text contains control characters")
		}
	}
	encoded, _, err := transform.String(korean.EUCKR.NewEncoder(), value)
	if err != nil || len(encoded) > limit {
		return 0, fmt.Errorf("display text exceeds strict CP949 limit")
	}
	decoded, _, err := transform.String(korean.EUCKR.NewDecoder(), encoded)
	if err != nil || decoded != value {
		return 0, fmt.Errorf("display text does not roundtrip through CP949")
	}
	return len(encoded), nil
}

func cloneDisplayProduct(product DisplayProduct) DisplayProduct {
	product.Quests = append([]DisplayQuest(nil), product.Quests...)
	return product
}

func (d *DisplayCatalog) CatalogVersion() string {
	if d == nil {
		return ""
	}
	return d.version
}

func (d *DisplayCatalog) Offer(campaignID uint32) (DisplayProduct, bool) {
	if d == nil {
		return DisplayProduct{}, false
	}
	product, ok := d.products[campaignID]
	return cloneDisplayProduct(product), ok
}

func (d *DisplayCatalog) Products() []DisplayProduct {
	if d == nil {
		return nil
	}
	products := make([]DisplayProduct, 0, len(d.ids))
	for _, id := range d.ids {
		products = append(products, cloneDisplayProduct(d.products[id]))
	}
	return products
}
