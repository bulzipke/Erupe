package channelserver

import "fmt"

// Operator-approved HR replacements. These are NOT round-40 original data.
var divaHRMilestoneRewards = buildDivaHRMilestoneRewards()

func buildDivaHRMilestoneRewards() []DivaRewardCatalogEntry {
	var rows []DivaRewardCatalogEntry
	for tier := 0; tier < 2; tier++ {
		low, high := uint16(2), uint16(99)
		if tier == 1 {
			low, high = 100, 999
		}
		for _, track := range []struct {
			kind    uint8
			points  []uint32
			items   []uint16
			amounts [2][]uint16
		}{
			{1, []uint32{100, 1000, 3000, 5000, 10000, 20000},
				[]uint16{0, 0x3694, 0, 0x1d2c, 0, 0x3694},
				[2][]uint16{{100, 1, 200, 3, 500, 2}, {200, 2, 400, 5, 1000, 4}}},
			{6, []uint32{1000, 3000, 5000, 10000, 20000},
				[]uint16{0, 0x0402, 0, 0x0402, 0},
				[2][]uint16{{100, 5, 200, 10, 500}, {200, 10, 400, 20, 1000}}},
		} {
			for i, points := range track.points {
				kind := uint8(7)
				if track.items[i] == 0 {
					kind = 26
				}
				rows = append(rows, DivaRewardCatalogEntry{
					Key:        fmt.Sprintf("custom-milestone-%d-hr%d-%d", track.kind, tier, points),
					RewardType: track.kind, Threshold: points, MinHR: low, MaxHR: high,
					ItemType: kind, ItemID: track.items[i], Quantity: track.amounts[tier][i],
					Basis: "operator-approved-2026-09-22-hr-milestones",
				})
			}
		}
	}
	return rows
}

func divaPrayerRewardCatalog() []DivaRewardCatalogEntry {
	rows := append([]DivaRewardCatalogEntry(nil), divaHistoricalPrayerRewards...)
	rows = append(rows, divaHistoricalSingleThresholdPrayerRewards...)
	for _, r := range divaHRMilestoneRewards {
		if r.RewardType == 1 {
			rows = append(rows, r)
		}
	}
	return rows
}

func divaRewardRankMatches(r DivaRewardCatalogEntry, hr, gr uint16) bool {
	if r.GR {
		return gr > 0
	}
	return gr == 0 && hr >= r.MinHR && hr <= r.MaxHR
}

// The tactics wire has only an HR/GR byte, not prayer's lower/upper HR bounds.
// Supply only this character's HR tier to that single HR tab. A GR viewer may
// inspect the upper HR tier, but cannot acquire it. IDs are not on this wire.
func divaHRInterceptionDisplay(hr, gr uint16) []DivaPrize {
	if gr > 0 {
		hr = 100
	}
	var prizes []DivaPrize
	for _, r := range divaHRMilestoneRewards {
		if r.RewardType == 6 && divaRewardRankMatches(r, hr, 0) {
			prizes = append(prizes, DivaPrize{Type: "personal", PointsReq: int(r.Threshold),
				ItemType: int(r.ItemType), ItemID: int(r.ItemID), Quantity: int(r.Quantity)})
		}
	}
	return prizes
}
