package channelserver

import "fmt"

// DivaRewardCatalogEntry describes an original or approved custom reward, not an offered
// entitlement. Opaque claim IDs are allocated separately in the database.
type DivaRewardCatalogEntry struct {
	Key              string
	RewardType       uint8
	Threshold        uint32 // Participation days for type 0, points for 1/6, areas for 7.
	Lower, Upper     uint32 // Personal/guild ranking range for types 2/3.
	GR               bool
	MinHR, MaxHR     uint16 // Native saved HR, not the displayed ZZ rank.
	ItemType         uint8
	ItemID, Quantity uint16
	Basis            string // Distinguishes original, historical and approved custom tables.
	NormaRepeat      bool   // Display-only repeat-tail marker; never creates an entitlement.
}

// Operator-approved replacement tables, deliberately separate from round 40.
var divaCustomSongRewards = buildDivaCustomSongRewards()

func buildDivaCustomSongRewards() []DivaRewardCatalogEntry {
	var rows []DivaRewardCatalogEntry
	for tier := 0; tier < 2; tier++ {
		low, high := uint16(2), uint16(99)
		quantities := []uint16{1, 100, 3, 1, 200, 5, 3}
		if tier == 1 {
			low, high = 100, 999
			quantities = []uint16{2, 200, 5, 2, 400, 10, 5}
		}
		for i, id := range []uint16{0x3694, 0, 0x1d2c, 0x3694, 0, 0x1d2c, 0x3694} {
			kind := uint8(7)
			if id == 0 {
				kind = 26
			}
			rows = append(rows, DivaRewardCatalogEntry{Key: fmt.Sprintf("custom-daily-hr%d-%d", tier, i+1),
				RewardType: 0, Threshold: uint32(i + 1), MinHR: low, MaxHR: high,
				ItemType: kind, ItemID: id, Quantity: quantities[i], Basis: "operator-approved-2026-09-22"})
		}
	}
	for i, bounds := range [][2]uint32{{1, 100}, {101, 500}} {
		quantities := [2]uint16{50, 5}
		if i == 1 {
			quantities = [2]uint16{25, 2}
		}
		for j, id := range []uint16{0x0402, 0x069b} {
			rows = append(rows, DivaRewardCatalogEntry{Key: fmt.Sprintf("custom-guild-%d-%04x", bounds[1], id),
				RewardType: 3, Lower: bounds[0], Upper: bounds[1], ItemType: 7, ItemID: id, Quantity: quantities[j], Basis: "operator-approved-2026-09-22"})
		}
	}
	return rows
}

func divaSongRewardCatalog() []DivaRewardCatalogEntry {
	rows := append([]DivaRewardCatalogEntry(nil), diva40SongRewards...)
	return append(rows, divaCustomSongRewards...)
}

// A bundle is selected once at the first acquisition query, before the client
// receives opaque IDs. Its unclaimed rows survive promotion and reconnects.
func divaRewardGroup(r DivaRewardCatalogEntry) (group, variant string) {
	if r.RewardType == 0 && r.Threshold > 0 {
		group = fmt.Sprintf("daily-%d", r.Threshold)
		variant = "gr"
		if !r.GR {
			variant = fmt.Sprintf("hr-%d-%d", r.MinHR, r.MaxHR)
		}
	} else if r.RewardType == 3 {
		group, variant = "guild-rank", fmt.Sprintf("rank-%d-%d", r.Lower, r.Upper)
	} else if (r.RewardType == 1 || r.RewardType == 6) && r.Threshold > 0 && (r.GR || r.MinHR > 0) {
		group, variant = fmt.Sprintf("milestone-%d", r.Threshold), "gr"
		if !r.GR {
			variant = fmt.Sprintf("hr-%d-%d", r.MinHR, r.MaxHR)
		}
	}
	return
}

type DivaRewardOffer struct {
	ID         uint32 `db:"id"`
	EventID    uint32 `db:"event_id"`
	RewardType uint8  `db:"reward_type"`
	CatalogKey string `db:"catalog_key"`
	ItemType   uint8  `db:"item_type"`
	ItemID     uint16 `db:"item_id"`
	Quantity   uint16 `db:"quantity"`
}

// Both pages of the round-40 GR daily and personal ranking screens were read.
// No HR daily or guild ranking rewards are invented from the GR/personal table.
var diva40SongRewards = []DivaRewardCatalogEntry{
	{Key: "daily-1-3694", RewardType: 0, Threshold: 1, GR: true, ItemType: 7, ItemID: 0x3694, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-2-30c3", RewardType: 0, Threshold: 2, GR: true, ItemType: 7, ItemID: 0x30c3, Quantity: 10, Basis: "round40-direct"},
	{Key: "daily-3-2499", RewardType: 0, Threshold: 3, GR: true, ItemType: 7, ItemID: 0x2499, Quantity: 3, Basis: "round40-direct"},
	{Key: "daily-4-3694", RewardType: 0, Threshold: 4, GR: true, ItemType: 7, ItemID: 0x3694, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-5-261b", RewardType: 0, Threshold: 5, GR: true, ItemType: 7, ItemID: 0x261b, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-5-31e9", RewardType: 0, Threshold: 5, GR: true, ItemType: 7, ItemID: 0x31e9, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-5-2638", RewardType: 0, Threshold: 5, GR: true, ItemType: 7, ItemID: 0x2638, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-5-31f2", RewardType: 0, Threshold: 5, GR: true, ItemType: 7, ItemID: 0x31f2, Quantity: 5, Basis: "round40-direct"},
	{Key: "daily-5-2e04", RewardType: 0, Threshold: 5, GR: true, ItemType: 7, ItemID: 0x2e04, Quantity: 10, Basis: "round40-direct"},
	{Key: "daily-6-1ff3", RewardType: 0, Threshold: 6, GR: true, ItemType: 7, ItemID: 0x1ff3, Quantity: 1, Basis: "round40-direct"},
	{Key: "daily-7-3694", RewardType: 0, Threshold: 7, GR: true, ItemType: 7, ItemID: 0x3694, Quantity: 10, Basis: "round40-direct"},
	{Key: "daily-7-1d2e", RewardType: 0, Threshold: 7, GR: true, ItemType: 7, ItemID: 0x1d2e, Quantity: 25, Basis: "round40-direct"},
	{Key: "rank-100-gp", RewardType: 2, Lower: 1, Upper: 100, ItemType: 26, Quantity: 12000, Basis: "round40-direct"},
	{Key: "rank-100-22e7", RewardType: 2, Lower: 1, Upper: 100, ItemType: 7, ItemID: 0x22e7, Quantity: 50, Basis: "round40-direct"},
	{Key: "rank-100-2cb0", RewardType: 2, Lower: 1, Upper: 100, ItemType: 7, ItemID: 0x2cb0, Quantity: 30, Basis: "round40-direct"},
	{Key: "rank-100-3649", RewardType: 2, Lower: 1, Upper: 100, ItemType: 7, ItemID: 0x3649, Quantity: 30, Basis: "round40-direct"},
	{Key: "rank-1000-gp", RewardType: 2, Lower: 101, Upper: 1000, ItemType: 26, Quantity: 6000, Basis: "round40-direct"},
	{Key: "rank-1000-22e7", RewardType: 2, Lower: 101, Upper: 1000, ItemType: 7, ItemID: 0x22e7, Quantity: 10, Basis: "round40-direct"},
	{Key: "rank-1000-2cb0", RewardType: 2, Lower: 101, Upper: 1000, ItemType: 7, ItemID: 0x2cb0, Quantity: 20, Basis: "round40-direct"},
	{Key: "rank-1000-3649", RewardType: 2, Lower: 101, Upper: 1000, ItemType: 7, ItemID: 0x3649, Quantity: 20, Basis: "round40-direct"},
	{Key: "rank-10000-gp", RewardType: 2, Lower: 1001, Upper: 10000, ItemType: 26, Quantity: 3000, Basis: "round40-direct"},
	{Key: "rank-10000-22e7", RewardType: 2, Lower: 1001, Upper: 10000, ItemType: 7, ItemID: 0x22e7, Quantity: 5, Basis: "round40-direct"},
	{Key: "rank-10000-2cb0", RewardType: 2, Lower: 1001, Upper: 10000, ItemType: 7, ItemID: 0x2cb0, Quantity: 10, Basis: "round40-direct"},
	{Key: "rank-10000-3649", RewardType: 2, Lower: 1001, Upper: 10000, ItemType: 7, ItemID: 0x3649, Quantity: 10, Basis: "round40-direct"},
}

// Historical fallback, not a claimed round-40 reconstruction. Every field was
// read from the GR1+ 20,000-point screen dated 2017-05-24 (page 13/26).
// See docs/diva-historical-prayer-rewards.md. Do not substitute cumulative totals.
var divaHistoricalPrayerRewards = []DivaRewardCatalogEntry{
	{Key: "norma-gr-20000-gp", RewardType: 1, Threshold: 20000, GR: true, ItemType: 26, Quantity: 5100, Basis: "2017-05-24-screen-direct"},
	{Key: "norma-gr-20000-2c15", RewardType: 1, Threshold: 20000, GR: true, ItemType: 7, ItemID: 0x2c15, Quantity: 10, Basis: "2017-05-24-screen-direct"},
	{Key: "norma-gr-20000-2c75", RewardType: 1, Threshold: 20000, GR: true, ItemType: 7, ItemID: 0x2c75, Quantity: 5, Basis: "2017-05-24-screen-direct"},
	{Key: "norma-gr-20000-2c78", RewardType: 1, Threshold: 20000, GR: true, ItemType: 7, ItemID: 0x2c78, Quantity: 8, Basis: "2017-05-24-screen-direct"},
	{Key: "norma-gr-20000-22e7", RewardType: 1, Threshold: 20000, GR: true, ItemType: 7, ItemID: 0x22e7, Quantity: 10, Basis: "2017-05-24-screen-direct"},
	{Key: "norma-gr-20000-2caf", RewardType: 1, Threshold: 20000, GR: true, ItemType: 7, ItemID: 0x2caf, Quantity: 5, Basis: "2017-05-24-screen-direct"},
}
