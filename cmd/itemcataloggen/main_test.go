package main

import (
	"bytes"
	"strings"
	"testing"
)

const validCatalogFixture = `"use strict";

var setItem = function (){return {

"0001":["translated name",4,100,1,1,"0001","translated description"]

,"064C":["raw M suffix","4M",0,99,1,"064C",""]

,"2DB3":["raw X suffix","5X","10G",255,2001,"2DB3","description"]

};};
`

func TestParseCatalog(t *testing.T) {
	records, err := parseCatalog([]byte(validCatalogFixture))
	if err != nil {
		t.Fatalf("parseCatalog() error = %v", err)
	}
	want := []catalogRecord{
		{ID: 0x0001, Rarity: 4, RaritySuffix: raritySuffixNone, PouchMax: 1},
		{ID: 0x064C, Rarity: 4, RaritySuffix: raritySuffixM, PouchMax: 99},
		{ID: 0x2DB3, Rarity: 5, RaritySuffix: raritySuffixX, PouchMax: 255},
	}
	if len(records) != len(want) {
		t.Fatalf("parseCatalog() returned %d records, want %d", len(records), len(want))
	}
	for i := range want {
		if records[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, records[i], want[i])
		}
	}
}

func TestParseCatalogRejectsMalformedData(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "duplicate ID",
			source: strings.Replace(validCatalogFixture,
				`,"2DB3":["raw X suffix","5X","10G",255,2001,"2DB3","description"]`,
				`,"064C":["raw X suffix","5X","10G",255,2001,"2DB3","description"]`, 1),
			wantErr: "duplicate item ID",
		},
		{
			name:    "unknown rarity suffix",
			source:  strings.Replace(validCatalogFixture, `"4M"`, `"4Q"`, 1),
			wantErr: `invalid rarity "4Q"`,
		},
		{
			name:    "zero pouch maximum",
			source:  strings.Replace(validCatalogFixture, `4,100,1,1`, `4,100,0,1`, 1),
			wantErr: "pouch maximum 0 is outside",
		},
		{
			name:    "wrong field count",
			source:  strings.Replace(validCatalogFixture, `,"translated description"`, ``, 1),
			wantErr: "got 6 fields, want 7",
		},
		{
			name:    "missing object end",
			source:  strings.Replace(validCatalogFixture, `};};`, ``, 1),
			wantErr: "object end was not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCatalog([]byte(tt.source))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseCatalog() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseCatalogDoesNotInterpretUncachedFields(t *testing.T) {
	source := strings.Replace(
		validCatalogFixture,
		`["translated name",4,100,1,1,"0001","translated description"]`,
		`[{"mutable":"name"},4,null,1,["mutable","rank"],false,{"mutable":"description"}]`,
		1,
	)
	records, err := parseCatalog([]byte(source))
	if err != nil {
		t.Fatalf("parseCatalog() interpreted an uncached field: %v", err)
	}
	if got := records[0]; got != (catalogRecord{ID: 0x0001, Rarity: 4, PouchMax: 1}) {
		t.Fatalf("first record = %+v", got)
	}
}

func TestRenderCatalogIsDeterministicAndOmitsTranslatedText(t *testing.T) {
	records, err := parseCatalog([]byte(validCatalogFixture))
	if err != nil {
		t.Fatal(err)
	}
	const digest = "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	first, err := renderCatalog(records, digest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderCatalog(records, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("renderCatalog() output is not deterministic")
	}
	for _, omitted := range []string{"translated name", "translated description", "raw M suffix", "raw X suffix"} {
		if bytes.Contains(first, []byte(omitted)) {
			t.Errorf("generated catalog unexpectedly contains %q", omitted)
		}
	}
	spaceNormalized := strings.Join(strings.Fields(string(first)), " ")
	for _, included := range []string{
		`ItemCatalogEntryCount = 3`,
		`ItemCatalogMaxID uint16 = 0x2DB3`,
		`0x0001: 0x8401`,
		`0x064C: 0x8C63`,
		`0x2DB3: 0x95FF`,
	} {
		if !strings.Contains(spaceNormalized, included) {
			t.Errorf("generated catalog does not contain %q", included)
		}
	}
}
