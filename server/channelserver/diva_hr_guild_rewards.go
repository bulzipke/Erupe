package channelserver

import "fmt"

// Operator-approved HR replacement, not a reconstruction of round 40.
// Every tier shares one finite table: 55 guild tickets and 1,500 GP per round.
// Acquisition and promotion protection belong to the repository, not this
// display catalog. GR viewers can preview this table but cannot obtain new HR
// entitlements; already offered HR receipts remain valid after promotion.
func divaHRGuildRewards() []DivaRewardCatalogEntry {
	values := [][4]uint16{
		{2, 7, 1026, 5}, {3, 7, 1026, 10}, {5, 26, 0, 100},
		{6, 7, 1026, 10}, {8, 26, 0, 200}, {10, 7, 1026, 10},
		{20, 26, 0, 300}, {22, 7, 1026, 20}, {24, 26, 0, 400},
		{26, 26, 0, 500},
	}
	rows := make([]DivaRewardCatalogEntry, 0, len(values))
	for _, v := range values {
		rows = append(rows, DivaRewardCatalogEntry{
			Key:        fmt.Sprintf("custom-tactics-guild-hr-%d", v[0]),
			RewardType: 7, Threshold: uint32(v[0]), MinHR: 2, MaxHR: 999,
			ItemType: uint8(v[1]), ItemID: v[2], Quantity: v[3],
			Basis: "operator-approved-2026-09-23-hr-guild",
		})
	}
	return rows
}

func divaHRGuildDisplay(hr, gr uint16) []DivaPrize {
	if gr > 0 {
		hr = 2
	}
	var prizes []DivaPrize
	for _, row := range divaHRGuildRewards() {
		if divaRewardRankMatches(row, hr, 0) {
			prizes = append(prizes, DivaPrize{Type: "guild", PointsReq: int(row.Threshold),
				ItemType: int(row.ItemType), ItemID: int(row.ItemID), Quantity: int(row.Quantity)})
		}
	}
	return prizes
}
