package mhfitem

// ItemRaritySuffix preserves the optional suffix attached to an item's rarity
// in the Ferias source. It is raw catalog data, not a gameplay-policy decision.
type ItemRaritySuffix uint8

const (
	ItemRaritySuffixNone ItemRaritySuffix = iota
	ItemRaritySuffixM
	ItemRaritySuffixX
)

const (
	itemCatalogPouchMaxMask uint16 = 0x00ff
	itemCatalogRarityMask   uint16 = 0x0700
	itemCatalogRarityShift         = 8
	itemCatalogSuffixMask   uint16 = 0x1800
	itemCatalogSuffixShift         = 11
	itemCatalogKnownMask    uint16 = 0x8000
)

// ItemMetadata contains the stable, language-independent facts copied from
// the Ferias item catalog. PouchMax is the in-quest pouch limit shown by
// Ferias; it is not a warehouse, mail-attachment, or shared-box stack limit.
type ItemMetadata struct {
	Rarity       uint8
	RaritySuffix ItemRaritySuffix
	PouchMax     uint8
}

// LookupItemMetadata returns the generated Ferias metadata for itemID. A false
// result means only that the source snapshot did not contain that ID; callers
// must not infer that the client considers the ID invalid.
func LookupItemMetadata(itemID uint16) (ItemMetadata, bool) {
	packed := generatedItemCatalog[itemID]
	if packed&itemCatalogKnownMask == 0 {
		return ItemMetadata{}, false
	}

	return ItemMetadata{
		Rarity:       uint8((packed & itemCatalogRarityMask) >> itemCatalogRarityShift),
		RaritySuffix: ItemRaritySuffix((packed & itemCatalogSuffixMask) >> itemCatalogSuffixShift),
		PouchMax:     uint8(packed & itemCatalogPouchMaxMask),
	}, true
}
