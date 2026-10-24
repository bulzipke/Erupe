package questlicense

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
)

func testDisplayDocument(catalog *Catalog) DisplayDocument {
	doc := DisplayDocument{CatalogVersion: catalog.Version()}
	for _, id := range []uint32{123, 124} {
		if _, ok := catalog.Offer(id); !ok {
			continue
		}
		product := DisplayProduct{CampaignID: id, Title: "Permanent license"}
		for _, quest := range catalog.QuestIDsForOffer(id) {
			product.Quests = append(product.Quests, DisplayQuest{ID: quest, Name: "Quest", Conditions: "Original equipment and rank", Publication: "Independent publication"})
		}
		doc.Products = append(doc.Products, product)
	}
	return doc
}

func TestDisplayCatalogCanonicalDetachedAndConcurrent(t *testing.T) {
	doc := validDocument()
	doc.Products = append(doc.Products, Product{CampaignID: 124, LicenseKeys: []string{"kit"}})
	catalog, err := New(doc)
	if err != nil {
		t.Fatal(err)
	}
	input := testDisplayDocument(catalog)
	slices.Reverse(input.Products)
	slices.Reverse(input.Products[1].Quests)
	display, err := NewDisplay(input, catalog)
	if err != nil {
		t.Fatal(err)
	}
	input.Products[1].Quests[0].Name = "changed"
	product, ok := display.Offer(123)
	if !ok || len(product.Quests) != 2 || product.Quests[0].ID != 40081 || product.Quests[0].Name != "Quest" {
		t.Fatal("display is not canonical and detached", product)
	}
	product.Quests[0].Name = "changed"
	products := display.Products()
	if len(products) != 2 || products[0].CampaignID != 123 || products[1].CampaignID != 124 {
		t.Fatal("display products are not sorted", products)
	}
	products[0].Quests[0].Name = "changed"
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 50; j++ {
				local, _ := display.Offer(123)
				local.Quests[0].Name = "local"
				all := display.Products()
				all[0].Quests[0].Name = "local"
			}
		}()
	}
	workers.Wait()
	fresh, _ := display.Offer(123)
	if fresh.Quests[0].Name != "Quest" || display.CatalogVersion() != catalog.Version() {
		t.Fatal("accessor mutation reached display storage")
	}
	var absent *DisplayCatalog
	if absent.CatalogVersion() != "" || absent.Products() != nil {
		t.Fatal("nil display has metadata")
	}
	if p, ok := absent.Offer(123); ok || p.CampaignID != 0 {
		t.Fatal("nil display returned a product")
	}
	if p, ok := display.Offer(999); ok || p.CampaignID != 0 {
		t.Fatal("unknown display returned a product")
	}
}

func TestDisplayCatalogRejectsIncompleteOrUnsafeContracts(t *testing.T) {
	catalog, err := New(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		edit func(*DisplayDocument)
	}{
		{"version", func(d *DisplayDocument) { d.CatalogVersion += "-different" }},
		{"missing enabled product", func(d *DisplayDocument) { d.Products = nil }},
		{"zero campaign", func(d *DisplayDocument) { d.Products[0].CampaignID = 0 }},
		{"unknown campaign", func(d *DisplayDocument) { d.Products[0].CampaignID = 124 }},
		{"duplicate campaign", func(d *DisplayDocument) { d.Products = append(d.Products, d.Products[0]) }},
		{"missing row", func(d *DisplayDocument) { d.Products[0].Quests = d.Products[0].Quests[:1] }},
		{"duplicate row", func(d *DisplayDocument) { d.Products[0].Quests[1].ID = d.Products[0].Quests[0].ID }},
		{"wrong row", func(d *DisplayDocument) { d.Products[0].Quests[0].ID = 65535 }},
		{"blank title", func(d *DisplayDocument) { d.Products[0].Title = " " }},
		{"long title", func(d *DisplayDocument) { d.Products[0].Title = strings.Repeat("T", 97) }},
		{"blank name", func(d *DisplayDocument) { d.Products[0].Quests[0].Name = "" }},
		{"long name", func(d *DisplayDocument) { d.Products[0].Quests[0].Name = strings.Repeat("N", 121) }},
		{"long conditions", func(d *DisplayDocument) { d.Products[0].Quests[0].Conditions = strings.Repeat("C", 513) }},
		{"long publication", func(d *DisplayDocument) { d.Products[0].Quests[0].Publication = strings.Repeat("P", 513) }},
		{"control", func(d *DisplayDocument) { d.Products[0].Quests[0].Name = "a\nb" }},
		{"format control", func(d *DisplayDocument) { d.Products[0].Quests[0].Name = "a\u200bb" }},
		{"unrepresentable", func(d *DisplayDocument) { d.Products[0].Title = "\U0001f600" }},
		{"CP949 bytes", func(d *DisplayDocument) { d.Products[0].Title = strings.Repeat("\uac00", 49) }},
		{"product limit", func(d *DisplayDocument) { d.Products = make([]DisplayProduct, MaxDefinitions+1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := testDisplayDocument(catalog)
			tt.edit(&doc)
			if _, err := NewDisplay(doc, catalog); err == nil {
				t.Fatal("accepted unsafe display contract")
			}
		})
	}
	if _, err := NewDisplay(DisplayDocument{}, nil); err == nil {
		t.Fatal("accepted display without a catalog")
	}
	doc := testDisplayDocument(catalog)
	doc.Products[0].Title = strings.Repeat("\uac00", 48)
	if _, err := NewDisplay(doc, catalog); err != nil {
		t.Fatal("exact CP949 title byte bound rejected", err)
	}
	disabled := validDocument()
	disabled.Products[0].Enabled = false
	draft, _ := New(disabled)
	if _, err := NewDisplay(DisplayDocument{CatalogVersion: draft.Version()}, draft); err != nil {
		t.Fatal("disabled proposal requires premature source text", err)
	}
}

func TestParseDisplayStrictJSON(t *testing.T) {
	catalog, _ := New(validDocument())
	data, err := json.Marshal(testDisplayDocument(catalog))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDisplay(data, catalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ from, to string }{
		{`"catalog_version":`, `"catalog_ver\u017fion":`},
		{`"title":`, `"Title":`},
		{`"name":"Quest"`, `"name":"Quest","name":"other"`},
		{`"id":40081`, `"id":40081,"item_id":1`},
	} {
		changed := strings.Replace(string(data), tt.from, tt.to, 1)
		if changed == string(data) {
			t.Fatal("test did not alter input")
		}
		if _, err := ParseDisplay([]byte(changed), catalog); err == nil {
			t.Fatal("accepted ambiguous display JSON")
		}
	}
	for _, bad := range [][]byte{append(append([]byte(nil), data...), []byte(` {}`)...), []byte("null"), []byte(strings.Repeat(" ", MaxDocumentBytes+1))} {
		if _, err := ParseDisplay(bad, catalog); err == nil {
			t.Fatal("accepted trailing, null or oversized display")
		}
	}
}

func TestDisplayCatalogChecksCompleteLPV2Capacity(t *testing.T) {
	ids := make([]int, 16)
	for i := range ids {
		ids[i] = i + 1
	}
	catalog, err := New(Document{Version: "capacity", Licenses: []Definition{{Key: "right", QuestIDs: ids}},
		Products: []Product{{CampaignID: 123, LicenseKeys: []string{"right"}}}})
	if err != nil {
		t.Fatal(err)
	}
	doc := DisplayDocument{CatalogVersion: catalog.Version(), Products: []DisplayProduct{{CampaignID: 123, Title: "T"}}}
	for _, id := range ids {
		doc.Products[0].Quests = append(doc.Products[0].Quests, DisplayQuest{ID: id, Name: "Q", Conditions: strings.Repeat("C", 512), Publication: strings.Repeat("P", 512)})
	}
	doc.Products[0].Quests[15].Publication = strings.Repeat("P", 325)
	if _, err := NewDisplay(doc, catalog); err != nil {
		t.Fatal("exact 16 KiB LPV2 rejected", err)
	}
	doc.Products[0].Quests[15].Publication += "P"
	if _, err := NewDisplay(doc, catalog); err == nil {
		t.Fatal("accepted a 16 KiB + 1 display contract")
	}
}
