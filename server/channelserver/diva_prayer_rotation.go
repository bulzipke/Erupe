package channelserver

// DivaPrayerRotation is an operator-defined continuation, not a reconstruction
// of the round-40 reward table. Its key versions both the schedule and receipts:
// changing the start, interval or items requires an explicit migration decision.
type DivaPrayerRotation struct {
	Key             string
	Start, Interval uint32
	Items           []DivaRewardCatalogEntry
}

func divaPrayerRotation() DivaPrayerRotation {
	return DivaPrayerRotation{
		Key: "gr-crystals-v1", Start: 103000, Interval: 1000,
		Items: []DivaRewardCatalogEntry{
			{RewardType: 1, GR: true, ItemType: 7, ItemID: 0x2338, Quantity: 5, Basis: "custom-shared-reference-2026-09-22"},
			{RewardType: 1, GR: true, ItemType: 7, ItemID: 0x2339, Quantity: 5, Basis: "custom-shared-reference-2026-09-22"},
		},
	}
}

// The first reward is earned at Start itself. Scores beyond the client wire's
// uint32 range cannot create unrepresentable thresholds or wrapped receipts.
func divaPrayerRotationDueCount(points int64, gr uint16) uint32 {
	if gr == 0 {
		return 0
	}
	return divaPrayerRotation().dueCount(points)
}

// Index is zero-based and stable for one rotation version. The catalog key is
// independent of query order, claim batches and character reconnects.
func divaPrayerRotationReward(index uint32) DivaRewardCatalogEntry {
	return divaPrayerRotation().reward(index)
}

// Only this display catalog carries the repeat-tail flags. The ordinary
// catalog remains a finite list, so its normal eligibility path cannot grant
// either display row as an additional once-per-round reward.
func divaPrayerNormaRewardPayload() []byte {
	rows := divaPrayerRewardCatalog()
	// Native repeat-boundary lookup compares only HR/GR, not HR bounds. With
	// low-HR rows before high-HR rows on tied scores, its first HR tail marker
	// must remain zero or the high-HR 20,000-point milestone moves into the
	// repeat page. This changes display grouping only, never eligibility.
	for _, hr := range []uint16{2, 100} {
		rotation := divaHRPrayerRotation(hr)
		for index := range rotation.Items {
			row := rotation.reward(uint32(index))
			row.NormaRepeat = hr != 2 || index != 0
			rows = append(rows, row)
		}
	}
	for index := range divaPrayerRotation().Items {
		row := divaPrayerRotationReward(uint32(index))
		row.NormaRepeat = true
		rows = append(rows, row)
	}
	return divaNormaRewardPayload(rows)
}
