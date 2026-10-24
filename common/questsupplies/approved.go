package questsupplies

const (
	ApprovedVersion  = "season-supplies-approved-20261024-v1"
	GWKey            = "gw_ticket"
	HunterDiceBagKey = "hunter_dice_bag"
)

// ApprovedSeasonalDocument encodes only the approved 2026-10-24 quantities and
// price. No real dates, season IDs or campaign IDs have been assigned. Adding a
// new approved annual window later does not reset any previous window's claim.
func ApprovedSeasonalDocument() Document {
	gw := Definition{Key: GWKey, Kind: FreeCharacterWindow,
		Delivery:   ItemStack{ItemType: StandardItemType, ItemID: 6831, Quantity: 4},
		QuestRules: PreserveOriginalQuestRules}
	for _, id := range []uint16{50070, 50071, 50072, 50073} {
		gw.Quests = append(gw.Quests, QuestRequirement{QuestID: id,
			EntryItem: ItemStack{ItemType: StandardItemType, ItemID: 6831, Quantity: 1}})
	}
	dice := Definition{Key: HunterDiceBagKey, Kind: PaidRepeatable,
		Delivery: ItemStack{ItemType: StandardItemType, ItemID: 3638, Quantity: 1}, PriceGoldG: 1,
		QuestRules: PreserveOriginalQuestRules}
	for _, id := range []uint16{51032, 51033, 51034, 51035, 55620, 55621, 55622, 55623, 55624} {
		dice.Quests = append(dice.Quests, QuestRequirement{QuestID: id,
			EntryItem: ItemStack{ItemType: StandardItemType, ItemID: 3638, Quantity: 1}})
	}
	return Document{Version: ApprovedVersion, Definitions: []Definition{gw, dice}}
}

// NewApprovedSeasonalDraft returns an inspectable immutable draft. It cannot
// prepare a live action, and has no window binding until dates/identity receive
// separate approval. Native claim/save validation and a durable adapter remain
// prerequisites for any future activation outside this package.
func NewApprovedSeasonalDraft() (*Catalog, error) {
	return New(ApprovedSeasonalDocument())
}
