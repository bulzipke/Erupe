package channelserver

// Historical additions, not a round-40 reconstruction. The 2016 blog tables
// list one threshold for each item's entire quantity, unlike cumulative ranges
// that cannot identify individual payouts. Their GR scope is inferred from the
// surrounding catalog and items; retain that distinction in the provenance.
// Each row remains a once-per-event milestone, not a repeating reward.
var divaHistoricalSingleThresholdPrayerRewards = []DivaRewardCatalogEntry{
	{Key: "historical-2016-norma-gr-24500-070a", RewardType: 1, Threshold: 24500, GR: true, ItemType: 7, ItemID: 0x070a, Quantity: 10, Basis: "historical-2016-single-threshold-gr-inferred"},
	{Key: "historical-2016-norma-gr-29500-070b", RewardType: 1, Threshold: 29500, GR: true, ItemType: 7, ItemID: 0x070b, Quantity: 10, Basis: "historical-2016-single-threshold-gr-inferred"},
	{Key: "historical-2016-norma-gr-99000-2337", RewardType: 1, Threshold: 99000, GR: true, ItemType: 7, ItemID: 0x2337, Quantity: 10, Basis: "historical-2016-single-threshold-gr-inferred"},
	{Key: "historical-2016-norma-gr-100500-2338", RewardType: 1, Threshold: 100500, GR: true, ItemType: 7, ItemID: 0x2338, Quantity: 5, Basis: "historical-2016-single-threshold-gr-inferred"},
	{Key: "historical-2016-norma-gr-102000-2339", RewardType: 1, Threshold: 102000, GR: true, ItemType: 7, ItemID: 0x2339, Quantity: 5, Basis: "historical-2016-single-threshold-gr-inferred"},
}
