package questlicense

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func validDocument() Document {
	price := uint32(50)
	start := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	return Document{Version: "test-v1", Licenses: []Definition{{Key: "veil", QuestIDs: []int{40081, 40142}}, {Key: "kit", QuestIDs: []int{40142}}},
		Products: []Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"veil", "kit"}}}}
}

func TestCatalogCopiesCallerAndReturnedData(t *testing.T) {
	doc := validDocument()
	c, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Licenses[0].QuestIDs[0] = 1
	doc.Products[0].LicenseKeys[0] = "changed"
	*doc.Products[0].Price = 1
	offer, ok := c.Offer(123)
	if !ok || offer.Price != 50 || strings.Join(offer.LicenseKeys, ",") != "kit,veil" {
		t.Fatal(offer)
	}
	offer.LicenseKeys[0] = "changed"
	keys := c.KeysForQuest(40142)
	if strings.Join(keys, ",") != "kit,veil" {
		t.Fatal(keys)
	}
	keys[0] = "changed"
	got, _ := c.Offer(123)
	if got.LicenseKeys[0] != "kit" || c.KeysForQuest(40142)[0] != "kit" || len(c.KeysForQuest(40081)) != 1 {
		t.Fatal("catalog mutated")
	}
}

func TestCatalogRejectsUnsafeOrAmbiguousProducts(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Document)
	}{
		{"version", func(d *Document) { d.Version = "bad\nheader" }},
		{"duplicate key", func(d *Document) { d.Licenses[1].Key = "veil" }},
		{"invalid key", func(d *Document) { d.Licenses[0].Key = "../veil" }},
		{"empty quests", func(d *Document) { d.Licenses[0].QuestIDs = nil }},
		{"invalid quest", func(d *Document) { d.Licenses[0].QuestIDs[0] = 65536 }},
		{"duplicate quest", func(d *Document) { d.Licenses[0].QuestIDs = []int{1, 1} }},
		{"zero campaign", func(d *Document) { d.Products[0].CampaignID = 0 }},
		{"signed campaign overflow", func(d *Document) { d.Products[0].CampaignID = 1 << 31 }},
		{"duplicate campaign", func(d *Document) { d.Products = append(d.Products, d.Products[0]) }},
		{"unknown key", func(d *Document) { d.Products[0].LicenseKeys = []string{"unknown"} }},
		{"duplicate product key", func(d *Document) { d.Products[0].LicenseKeys = []string{"veil", "veil"} }},
		{"empty product key", func(d *Document) { d.Products[0].LicenseKeys = nil }},
		{"missing price", func(d *Document) { d.Products[0].Price = nil }},
		{"zero price", func(d *Document) { *d.Products[0].Price = 0 }},
		{"price overflow", func(d *Document) { *d.Products[0].Price = 65536 }},
		{"missing dates", func(d *Document) { d.Products[0].StartsAt = nil; d.Products[0].EndsAt = nil }},
		{"one date", func(d *Document) { d.Products[0].EndsAt = nil }},
		{"reversed date", func(d *Document) { d.Products[0].EndsAt = d.Products[0].StartsAt }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDocument()
			tt.change(&d)
			if _, err := New(d); err == nil {
				t.Fatal("accepted invalid product")
			}
		})
	}
}

func TestDisabledDraftDoesNotRequireInventedPricesOrDates(t *testing.T) {
	d := validDocument()
	d.Products[0].Enabled = false
	d.Products[0].Price, d.Products[0].StartsAt, d.Products[0].EndsAt = nil, nil, nil
	c, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	offer, _ := c.Offer(123)
	if offer.Enabled || offer.Price != 0 || !offer.StartsAt.IsZero() {
		t.Fatal(offer)
	}
}

func TestParseRejectsUnknownTrailingAndOversizeDocuments(t *testing.T) {
	for _, data := range []string{`{"version":"v1","licenses":[],"products":[],"typo":true}`, `{"version":"v1"} {}`, `null`, strings.Repeat(" ", MaxDocumentBytes+1)} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("accepted invalid document")
		}
	}
	for _, data := range []string{`{"version":"v1","version":"v2"}`, `{"Version":"v1"}`, `{"version":"v1","ver\u017fion":"v2"}`, `{"version":"v1","products":[{"campaign_id":1,"license_keys":[],"licen\u017fe_keys":[]}]}`, `{"version":"v1","products":[{"campaign_id":1,"enabled":false,"enabled":true}]}`, strings.Repeat("[", 66) + strings.Repeat("]", 66)} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("accepted ambiguous or deeply nested policy")
		}
	}
	if _, err := Parse([]byte(`{"version":"disabled-v1","licenses":[],"products":[]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogBoundsAndConcurrentDetachedReads(t *testing.T) {
	d := validDocument()
	d.Products[0].LicenseKeys = make([]string, MaxProductKeys+1)
	if _, err := New(d); err == nil {
		t.Fatal("unbounded key list")
	}
	d = validDocument()
	d.Licenses = make([]Definition, MaxDefinitions+1)
	if _, err := New(d); err == nil {
		t.Fatal("unbounded definitions")
	}
	d = validDocument()
	d.Licenses[0].QuestIDs = make([]int, MaxQuestMappings+1)
	if _, err := New(d); err == nil {
		t.Fatal("unbounded mappings")
	}
	c, err := New(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				o, _ := c.Offer(123)
				o.LicenseKeys[0] = fmt.Sprint(j)
				k := c.KeysForQuest(40142)
				k[0] = "local"
			}
		}()
	}
	wg.Wait()
	if c.Version() != "test-v1" || c.KeysForQuest(40142)[0] != "kit" {
		t.Fatal("shared mutable catalog")
	}
}

func TestOfferDigestCanonicalAndDetachedQuestIDs(t *testing.T) {
	doc := validDocument()
	doc.Products = append(doc.Products, Product{CampaignID: 124, LicenseKeys: []string{"kit"}})
	c, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	want, ok := c.OfferDigest(123)
	if !ok || want == [32]byte{} || !slices.Equal(c.QuestIDsForOffer(123), []int{40081, 40142}) {
		t.Fatal("missing offer snapshot or non-distinct quest IDs")
	}
	reordered := validDocument()
	reordered.Licenses = []Definition{{Key: "kit", QuestIDs: []int{40142}}, {Key: "veil", QuestIDs: []int{40142, 40081}}}
	reordered.Products[0].LicenseKeys = []string{"kit", "veil"}
	reordered.Products = []Product{{CampaignID: 124, LicenseKeys: []string{"kit"}}, reordered.Products[0]}
	start := reordered.Products[1].StartsAt.In(time.FixedZone("same-instant", 9*60*60))
	end := reordered.Products[1].EndsAt.In(time.FixedZone("same-instant", 9*60*60))
	reordered.Products[1].StartsAt, reordered.Products[1].EndsAt = &start, &end
	other, err := New(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := other.OfferDigest(123); !ok || got != want {
		t.Fatal("input order or timestamp offset changed the offer digest")
	}
	if got, _ := other.OfferDigest(124); got == want {
		t.Fatal("independent offers share a snapshot")
	}
	// Mutating caller data and either returned snapshot must not affect reads.
	doc.Licenses[0].QuestIDs[0] = 1
	doc.Products[0].LicenseKeys[0] = "changed"
	*doc.Products[0].Price = 1
	ids := c.QuestIDsForOffer(123)
	ids[0] = 1
	mutated := want
	mutated[0] ^= 0xff
	if got, ok := c.OfferDigest(123); !ok || got != want || got == mutated ||
		!slices.Equal(c.QuestIDsForOffer(123), []int{40081, 40142}) {
		t.Fatal("offer snapshot aliases caller or returned storage")
	}
	for _, catalog := range []*Catalog{nil, {}, c} {
		if got, ok := catalog.OfferDigest(999); ok || got != [32]byte{} || catalog.QuestIDsForOffer(999) != nil {
			t.Fatal("unknown offer returned a snapshot")
		}
	}
}

func TestOfferDigestBindsPolicyAndEachKeysFullMapping(t *testing.T) {
	c, err := New(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := c.OfferDigest(123)
	for _, test := range []struct {
		name   string
		change func(*Document)
	}{
		{"version", func(d *Document) { d.Version = "test-v2" }},
		{"product ID", func(d *Document) { d.Products[0].CampaignID = 124 }},
		{"enabled", func(d *Document) { d.Products[0].Enabled = false }},
		{"price", func(d *Document) { *d.Products[0].Price = 51 }},
		{"start", func(d *Document) { v := d.Products[0].StartsAt.Add(time.Nanosecond); d.Products[0].StartsAt = &v }},
		{"end", func(d *Document) { v := d.Products[0].EndsAt.Add(time.Nanosecond); d.Products[0].EndsAt = &v }},
		{"keys with same quest union", func(d *Document) { d.Products[0].LicenseKeys = []string{"veil"} }},
		{"quest mapping under same key", func(d *Document) { d.Licenses[0].QuestIDs = []int{40081, 40143} }},
		{"per-key mapping with same quest union", func(d *Document) { d.Licenses[1].QuestIDs = []int{40081} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := validDocument()
			test.change(&doc)
			other, err := New(doc)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := other.OfferDigest(doc.Products[0].CampaignID); !ok || got == want {
				t.Fatal("changed offer reused an existing quote digest")
			}
		})
	}
	// A right not in this product is outside its grant contract.
	doc := validDocument()
	doc.Licenses = append(doc.Licenses, Definition{Key: "unrelated", QuestIDs: []int{65535}})
	other, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := other.OfferDigest(123); got != want {
		t.Fatal("unrelated definition changed the offer snapshot")
	}
	doc.Products[0].Enabled = false
	doc.Products[0].Price, doc.Products[0].StartsAt, doc.Products[0].EndsAt = nil, nil, nil
	disabled, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := disabled.OfferDigest(123); !ok || got == [32]byte{} ||
		!slices.Equal(disabled.QuestIDsForOffer(123), []int{40081, 40142}) {
		t.Fatal("disabled draft lost its immutable grant description")
	}
}

func TestOfferSnapshotsConcurrentDetachedReads(t *testing.T) {
	c, err := New(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := c.OfferDigest(123)
	errors := make(chan error, 16)
	var workers sync.WaitGroup
	for i := 0; i < cap(errors); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				got, ok := c.OfferDigest(123)
				ids := c.QuestIDsForOffer(123)
				if !ok || got != want || !slices.Equal(ids, []int{40081, 40142}) {
					errors <- fmt.Errorf("concurrent read observed a changed offer snapshot")
					return
				}
				got[0] ^= 0xff
				ids[0] = j
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestCatalogDefinitionsCompleteCanonicalAndDetached(t *testing.T) {
	doc := validDocument()
	doc.Licenses = append(doc.Licenses, Definition{Key: "retired_sale", QuestIDs: []int{65535, 40008}})
	catalog, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	definitions := catalog.Definitions()
	if len(definitions) != 3 || definitions[0].Key != "kit" || definitions[1].Key != "retired_sale" ||
		!slices.Equal(definitions[1].QuestIDs, []int{40008, 65535}) {
		t.Fatal("definitions omit unsold rights or are not canonical", definitions)
	}
	definitions[1].Key = "mutated"
	definitions[1].QuestIDs[0] = 1
	doc.Licenses[2].QuestIDs[0] = 2
	fresh := catalog.Definitions()
	if fresh[1].Key != "retired_sale" || !slices.Equal(fresh[1].QuestIDs, []int{40008, 65535}) {
		t.Fatal("definition contract aliases caller storage", fresh)
	}
	var absent *Catalog
	if absent.Definitions() != nil {
		t.Fatal("nil catalog has definitions")
	}
}

func starterDocument() Document {
	doc := validDocument()
	doc.Starters = []StarterDefinition{
		{Key: "veil_base", LicenseKeys: []string{"veil", "kit"}, ItemID: 1859, Quantity: 1, LegacyCampaignIDs: []uint32{492821212, 17}},
		{Key: "kit_support", LicenseKeys: []string{"kit"}, ItemID: 42, Quantity: 2},
	}
	doc.Products[0].StarterKeys = []string{"veil_base", "kit_support"}
	return doc
}

func TestStarterPoliciesCanonicalDetachedAndRetainedAfterSale(t *testing.T) {
	doc := starterDocument()
	doc.Products[0].Enabled = false
	doc.Licenses = append(doc.Licenses, Definition{Key: "retired_right", QuestIDs: []int{65535}})
	doc.Starters = append(doc.Starters, StarterDefinition{Key: "retired_starter", LicenseKeys: []string{"retired_right"}, ItemID: 11, Quantity: 1})
	catalog, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := catalog.OfferDigest(123)
	starters := catalog.Starters()
	if len(starters) != 3 || starters[0].Key != "kit_support" || starters[1].Key != "retired_starter" || starters[2].Key != "veil_base" {
		t.Fatal("complete lifetime registry is not sorted", starters)
	}
	attached := catalog.StartersForOffer(123)
	if len(attached) != 2 || attached[0].Key != "kit_support" || attached[1].Key != "veil_base" ||
		!slices.Equal(attached[1].LicenseKeys, []string{"kit", "veil"}) ||
		!slices.Equal(attached[1].LegacyCampaignIDs, []uint32{17, 492821212}) {
		t.Fatal("disabled product lost its canonical starter metadata", attached)
	}
	// Both source buffers and every accessor result are independently owned.
	doc.Starters[0].LicenseKeys[0] = "mutated"
	doc.Starters[0].LegacyCampaignIDs[0] = 1
	doc.Starters[0].ItemID = 1
	doc.Products[0].StarterKeys[0] = "mutated"
	starters[2].LicenseKeys[0] = "mutated"
	starters[2].LegacyCampaignIDs[0] = 1
	attached[1].LicenseKeys[0] = "mutated"
	attached[1].LegacyCampaignIDs[0] = 1
	starter, ok := catalog.Starter("veil_base")
	if !ok || starter.ItemID != 1859 || starter.Quantity != 1 ||
		!slices.Equal(starter.LicenseKeys, []string{"kit", "veil"}) ||
		!slices.Equal(starter.LegacyCampaignIDs, []uint32{17, 492821212}) {
		t.Fatal("starter aliases source or returned buffers", starter)
	}
	starter.LicenseKeys[0] = "mutated"
	starter.LegacyCampaignIDs[0] = 1
	offer, _ := catalog.Offer(123)
	if !slices.Equal(offer.StarterKeys, []string{"kit_support", "veil_base"}) {
		t.Fatal("offer starter references changed", offer)
	}
	offer.StarterKeys[0] = "mutated"
	fresh, _ := catalog.Starter("veil_base")
	freshOffer, _ := catalog.Offer(123)
	if fresh.LicenseKeys[0] != "kit" || fresh.LegacyCampaignIDs[0] != 17 || freshOffer.StarterKeys[0] != "kit_support" {
		t.Fatal("returned starter metadata aliases catalog storage")
	}
	if got, _ := catalog.OfferDigest(123); got != want {
		t.Fatal("caller mutation changed the bound starter policy")
	}
	var absent *Catalog
	if absent.Starters() != nil || absent.StartersForOffer(123) != nil || catalog.StartersForOffer(999) != nil {
		t.Fatal("unknown catalog/product returned starter policies")
	}
	if got, ok := absent.Starter("veil_base"); ok || got.Key != "" {
		t.Fatal("nil catalog returned a policy")
	}
	if got, ok := catalog.Starter("missing"); ok || got.Key != "" {
		t.Fatal("unknown key returned a policy")
	}
}

func TestStarterQualifyingRightsAreAlternatives(t *testing.T) {
	for _, key := range []string{"veil", "kit"} {
		doc := starterDocument()
		doc.Starters = doc.Starters[:1]
		doc.Products[0].StarterKeys = []string{"veil_base"}
		doc.Products[0].LicenseKeys = []string{key}
		if _, err := New(doc); err != nil {
			t.Fatalf("one equivalent qualifying right %q must suffice: %v", key, err)
		}
	}
	doc := starterDocument()
	doc.Licenses = append(doc.Licenses, Definition{Key: "other", QuestIDs: []int{1}})
	doc.Products[0].LicenseKeys = []string{"other"}
	if _, err := New(doc); err == nil {
		t.Fatal("product grants none of its starter's qualifying rights")
	}
}

func TestOfferDigestBindsStarterBenefitAndLegacyBasis(t *testing.T) {
	catalog, err := New(starterDocument())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := catalog.OfferDigest(123)
	for _, tt := range []struct {
		name   string
		change func(*Document)
	}{
		{"item", func(d *Document) { d.Starters[0].ItemID++ }},
		{"quantity", func(d *Document) { d.Starters[0].Quantity++ }},
		{"qualifying alternatives", func(d *Document) { d.Starters[0].LicenseKeys = []string{"veil"} }},
		{"legacy replaced", func(d *Document) { d.Starters[0].LegacyCampaignIDs[0]++ }},
		{"legacy appended", func(d *Document) { d.Starters[0].LegacyCampaignIDs = append(d.Starters[0].LegacyCampaignIDs, 18) }},
		{"legacy removed", func(d *Document) { d.Starters[0].LegacyCampaignIDs = nil }},
		{"starter identity", func(d *Document) { d.Starters[0].Key = "veil_base_v2"; d.Products[0].StarterKeys[0] = "veil_base_v2" }},
		{"starter removed", func(d *Document) {
			d.Starters = d.Starters[1:]
			d.Products[0].StarterKeys = []string{"kit_support"}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := starterDocument()
			tt.change(&doc)
			other, err := New(doc)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := other.OfferDigest(123); got == want {
				t.Fatal("changed starter policy reused the prior quote digest")
			}
		})
	}
	reordered := starterDocument()
	slices.Reverse(reordered.Starters)
	slices.Reverse(reordered.Starters[1].LicenseKeys)
	slices.Reverse(reordered.Starters[1].LegacyCampaignIDs)
	slices.Reverse(reordered.Products[0].StarterKeys)
	other, err := New(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := other.OfferDigest(123); got != want {
		t.Fatal("starter input order changed a canonical digest")
	}
	doc := starterDocument()
	doc.Licenses = append(doc.Licenses, Definition{Key: "unrelated", QuestIDs: []int{2}})
	doc.Starters = append(doc.Starters, StarterDefinition{Key: "unrelated_starter", LicenseKeys: []string{"unrelated"}, ItemID: 3, Quantity: 1})
	other, err = New(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := other.OfferDigest(123); got != want {
		t.Fatal("unrelated policy changed this product's quote digest")
	}
}

func TestCatalogRejectsUnsafeStarterPolicy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Document)
	}{
		{"invalid key", func(d *Document) { d.Starters[0].Key = "../veil" }},
		{"duplicate key", func(d *Document) { d.Starters[1].Key = d.Starters[0].Key }},
		{"empty qualifying rights", func(d *Document) { d.Starters[0].LicenseKeys = nil }},
		{"unknown qualifying right", func(d *Document) { d.Starters[0].LicenseKeys = []string{"missing"} }},
		{"duplicate qualifying right", func(d *Document) { d.Starters[0].LicenseKeys = []string{"veil", "veil"} }},
		{"too many qualifying rights", func(d *Document) { d.Starters[0].LicenseKeys = make([]string, MaxProductKeys+1) }},
		{"zero item", func(d *Document) { d.Starters[0].ItemID = 0 }},
		{"zero quantity", func(d *Document) { d.Starters[0].Quantity = 0 }},
		{"zero legacy campaign", func(d *Document) { d.Starters[0].LegacyCampaignIDs[0] = 0 }},
		{"legacy signed overflow", func(d *Document) { d.Starters[0].LegacyCampaignIDs[0] = 1 << 31 }},
		{"duplicate legacy campaign", func(d *Document) { d.Starters[0].LegacyCampaignIDs = []uint32{1, 1} }},
		{"too many legacy campaigns", func(d *Document) { d.Starters[0].LegacyCampaignIDs = make([]uint32, MaxStarterLegacyCampaigns+1) }},
		{"too many policies", func(d *Document) { d.Starters = make([]StarterDefinition, MaxStarterDefinitions+1) }},
		{"unknown product starter", func(d *Document) { d.Products[0].StarterKeys = []string{"missing"} }},
		{"duplicate product starter", func(d *Document) { d.Products[0].StarterKeys = []string{"veil_base", "veil_base"} }},
		{"omitted qualifying starter", func(d *Document) { d.Products[0].StarterKeys = []string{"veil_base"} }},
		{"omitted all qualifying starters", func(d *Document) { d.Products[0].StarterKeys = nil }},
		{"too many product starters", func(d *Document) { d.Products[0].StarterKeys = make([]string, MaxProductStarters+1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := starterDocument()
			tt.change(&doc)
			if _, err := New(doc); err == nil {
				t.Fatal("accepted unsafe starter policy")
			}
		})
	}
}

func TestParseStarterPolicyIsStrictAndPreservesMetadata(t *testing.T) {
	encoded, err := json.Marshal(starterDocument())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if starter, ok := parsed.Starter("veil_base"); !ok || starter.ItemID != 1859 || len(starter.LegacyCampaignIDs) != 2 {
		t.Fatal("typed JSON lost starter metadata", starter)
	}
	for _, tt := range []struct{ name, from, to string }{
		{"unknown policy field", `"item_id":1859`, `"item_id":1859,"typo":true`},
		{"duplicate policy field", `"item_id":1859`, `"item_id":1859,"item_id":1`},
		{"noncanonical field", `"item_id":1859`, `"Item_ID":1859`},
		{"unicode field alias", `"key":"veil_base","license_keys":`, `"key":"veil_base","licen\u017fe_keys":`},
		{"item overflow", `"item_id":1859`, `"item_id":65536`},
		{"quantity overflow", `"quantity":1`, `"quantity":65536`},
		{"negative legacy campaign", `492821212`, `-1`},
		{"unknown product field", `"starter_keys":`, `"starterz":`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := strings.Replace(string(encoded), tt.from, tt.to, 1)
			if changed == string(encoded) {
				t.Fatal("test did not mutate a policy field")
			}
			if _, err := Parse([]byte(changed)); err == nil {
				t.Fatal("accepted ambiguous or invalid starter JSON")
			}
		})
	}
}

func TestStarterPoliciesConcurrentDetachedReads(t *testing.T) {
	catalog, err := New(starterDocument())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := catalog.OfferDigest(123)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				starter, _ := catalog.Starter("veil_base")
				starter.LicenseKeys[0] = "local"
				starter.LegacyCampaignIDs[0] = uint32(j)
				all := catalog.Starters()
				all[1].LicenseKeys[0] = "local"
				attached := catalog.StartersForOffer(123)
				attached[1].LegacyCampaignIDs[0] = uint32(j)
				offer, _ := catalog.Offer(123)
				offer.StarterKeys[0] = "local"
			}
		}()
	}
	workers.Wait()
	starter, _ := catalog.Starter("veil_base")
	if got, _ := catalog.OfferDigest(123); got != want || starter.LicenseKeys[0] != "kit" || starter.LegacyCampaignIDs[0] != 17 {
		t.Fatal("concurrent returned metadata changed catalog storage")
	}
}
