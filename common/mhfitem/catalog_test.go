package mhfitem

import "testing"

func TestLookupItemMetadata(t *testing.T) {
	tests := []struct {
		name  string
		id    uint16
		want  ItemMetadata
		known bool
	}{
		{
			name:  "ordinary rarity",
			id:    0x0001,
			want:  ItemMetadata{Rarity: 4, RaritySuffix: ItemRaritySuffixNone, PouchMax: 1},
			known: true,
		},
		{
			name:  "M suffix",
			id:    0x064C,
			want:  ItemMetadata{Rarity: 4, RaritySuffix: ItemRaritySuffixM, PouchMax: 99},
			known: true,
		},
		{
			name:  "X suffix",
			id:    0x2DB3,
			want:  ItemMetadata{Rarity: 5, RaritySuffix: ItemRaritySuffixX, PouchMax: 99},
			known: true,
		},
		{
			name:  "last Ferias item",
			id:    0x4097,
			want:  ItemMetadata{Rarity: 5, RaritySuffix: ItemRaritySuffixNone, PouchMax: 99},
			known: true,
		},
		{name: "empty slot sentinel", id: 0x0000},
		{name: "hole within source range", id: 0x001F},
		{name: "immediately above source maximum", id: 0x4098},
		{name: "wire sentinel", id: 0xFFFF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := LookupItemMetadata(tt.id)
			if known != tt.known {
				t.Fatalf("LookupItemMetadata(%04X) known = %t, want %t", tt.id, known, tt.known)
			}
			if got != tt.want {
				t.Errorf("LookupItemMetadata(%04X) = %+v, want %+v", tt.id, got, tt.want)
			}
		})
	}
}

func TestGeneratedItemCatalogInvariants(t *testing.T) {
	if ItemCatalogSourceSHA256 != "042F8528ACF1B25C1CA763984166CECF4EA05895180852C954311399CE837F8C" {
		t.Errorf("ItemCatalogSourceSHA256 = %q", ItemCatalogSourceSHA256)
	}
	if ItemCatalogEntryCount != 15713 {
		t.Errorf("ItemCatalogEntryCount = %d, want 15713", ItemCatalogEntryCount)
	}
	if ItemCatalogMaxID != 0x4097 {
		t.Errorf("ItemCatalogMaxID = %04X, want 4097", ItemCatalogMaxID)
	}

	knownCount := 0
	for id := 0; id <= int(^uint16(0)); id++ {
		metadata, known := LookupItemMetadata(uint16(id))
		if !known {
			continue
		}
		knownCount++
		if metadata.Rarity < 1 || metadata.Rarity > 7 {
			t.Errorf("item %04X has rarity %d", id, metadata.Rarity)
		}
		if metadata.PouchMax == 0 {
			t.Errorf("item %04X has zero PouchMax", id)
		}
		if metadata.RaritySuffix > ItemRaritySuffixX {
			t.Errorf("item %04X has unknown rarity suffix %d", id, metadata.RaritySuffix)
		}
	}
	if knownCount != ItemCatalogEntryCount {
		t.Errorf("known item count = %d, want %d", knownCount, ItemCatalogEntryCount)
	}
}
