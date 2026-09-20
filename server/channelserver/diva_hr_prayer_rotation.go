package channelserver

import (
	"fmt"
	"math"
)

// This is an inferred operating continuation of the approved HR milestones,
// not a recovered official round-40 table. Both HR tiers share one sequence.
func divaHRPrayerRotation(hr uint16) DivaPrayerRotation {
	low, high, multiplier := uint16(2), uint16(99), uint16(1)
	if hr >= 100 && hr <= 999 {
		low, high, multiplier = 100, 999, 2
	} else if hr < 2 || hr > 99 {
		return DivaPrayerRotation{}
	}
	return DivaPrayerRotation{
		Key: "hr-prayer-v1", Start: 21000, Interval: 1000,
		Items: []DivaRewardCatalogEntry{
			{RewardType: 1, MinHR: low, MaxHR: high, ItemType: 7, ItemID: 0x3694, Quantity: multiplier,
				Basis: "custom-inferred-hr-prayer-2026-09-22"},
			{RewardType: 1, MinHR: low, MaxHR: high, ItemType: 26, Quantity: 100 * multiplier,
				Basis: "custom-inferred-hr-prayer-2026-09-22"},
		},
	}
}

func divaPrayerRotationForRank(hr, gr uint16) DivaPrayerRotation {
	if gr > 0 {
		return divaPrayerRotation()
	}
	return divaHRPrayerRotation(hr)
}

func (r DivaPrayerRotation) track() string {
	if len(r.Items) == 0 {
		return ""
	}
	if r.Items[0].GR {
		return "gr"
	}
	return "hr"
}

func (r DivaPrayerRotation) dueCount(points int64) uint32 {
	if r.Key == "" || r.Interval == 0 || len(r.Items) == 0 || points < int64(r.Start) {
		return 0
	}
	if points > math.MaxUint32 {
		points = math.MaxUint32
	}
	return uint32((points-int64(r.Start))/int64(r.Interval) + 1)
}

func (r DivaPrayerRotation) reward(index uint32) DivaRewardCatalogEntry {
	if index >= r.dueCount(math.MaxUint32) {
		return DivaRewardCatalogEntry{}
	}
	row := r.Items[index%uint32(len(r.Items))]
	row.Key = fmt.Sprintf("rotation-%s-%d", r.Key, index)
	row.Threshold = uint32(uint64(r.Start) + uint64(index)*uint64(r.Interval))
	return row
}
