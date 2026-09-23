package channelserver

// towerFloorRewards reproduces the twelve-page, 500-floor investigation
// reward screen supplied by the server owner. It is a fixed historical table,
// not a claim that all original Tower seasons used these rewards.
//
// The item IDs are mapped to Ferias item.js. The screen uses Japanese names;
// the Korean client displays the corresponding local item names.
var towerFloorRewards = []WeeklySeibatuRankingReward{
	{1, 0, 0, 7201, 0x2B96, 1},   // 焦げた秘文書
	{2, 0, 0, 7201, 0x2BA5, 1},   // 再覚之古書
	{3, 0, 0, 7201, 0x2A1A, 3},   // 高貴な黒布
	{4, 0, 0, 7201, 0x2B9B, 1},   // 太古の砕金
	{5, 0, 0, 7201, 0x2B9C, 1},   // 天技之古書
	{6, 0, 0, 7201, 0x2C75, 1},   // 古猟の儀書・緋
	{7, 0, 0, 7201, 0x2C78, 1},   // 古猟の術書・緋
	{8, 0, 0, 7201, 0x2B97, 10},  // 勇ましき宝玉
	{9, 0, 0, 7201, 0x2B98, 10},  // 閃きの宝玉
	{10, 0, 0, 7201, 0x2B99, 10}, // 加護の宝玉
	{11, 0, 0, 7201, 0x2C01, 1},
	{12, 0, 0, 7201, 0x2C75, 1},
	{13, 0, 0, 7201, 0x2C78, 1},
	{14, 0, 0, 7201, 0x2C75, 1},
	{15, 0, 0, 7201, 0x2C78, 1},
	{16, 0, 0, 7201, 0x2C75, 1},
	{17, 0, 0, 7201, 0x2C78, 1},
	{18, 0, 0, 7201, 0x2C75, 1},
	{19, 0, 0, 7201, 0x2C78, 1},
	{20, 0, 0, 7201, 0x2A1A, 3},
	{21, 0, 0, 7201, 0x2C01, 1},
	{22, 0, 0, 7201, 0x2C75, 1},
	{23, 0, 0, 7201, 0x2C78, 1},
	{24, 0, 0, 7201, 0x2C75, 1},
	{25, 0, 0, 7201, 0x2C16, 4}, // いにしえの超鉄鋼
	{26, 0, 0, 7201, 0x2C78, 1},
	{27, 0, 0, 7201, 0x2C75, 1},
	{28, 0, 0, 7201, 0x2C78, 1},
	{29, 0, 0, 7201, 0x2C75, 1},
	{30, 0, 0, 7201, 0x2BC9, 3}, // 清雅な白布
	{31, 0, 0, 7201, 0x2C01, 1},
	{32, 0, 0, 7201, 0x2B97, 10},
	{33, 0, 0, 7201, 0x2B98, 10},
	{34, 0, 0, 7201, 0x2B99, 10},
	{35, 0, 0, 7201, 0x2C16, 4},
	{36, 0, 0, 7201, 0x2C75, 1},
	{39, 0, 0, 7201, 0x2B9C, 1},
	{40, 0, 0, 7201, 0x2A3D, 3}, // 桜星鉄 (앵성철)
	{41, 0, 0, 7201, 0x2C01, 1},
	{43, 0, 0, 7201, 0x2C78, 1},
	{45, 0, 0, 7201, 0x2C16, 4},
	{47, 0, 0, 7201, 0x2C78, 1},
	{50, 0, 0, 7201, 0x2C5C, 1}, // 白銀の秘文書
	{51, 0, 0, 7201, 0x2C01, 1},
	{53, 0, 0, 7201, 0x2C75, 2},
	{57, 0, 0, 7201, 0x2C78, 1},
	{60, 0, 0, 7201, 0x2C16, 4},
	{61, 0, 0, 7201, 0x2C01, 1},
	{63, 0, 0, 7201, 0x2C75, 2},
	{67, 0, 0, 7201, 0x2C78, 1},
	{70, 0, 0, 7201, 0x2C16, 4},
	{71, 0, 0, 7201, 0x2C01, 1},
	{73, 0, 0, 7201, 0x2C75, 2},
	{77, 0, 0, 7201, 0x2C78, 1},
	{79, 0, 0, 7201, 0x2B9C, 1},
	{80, 0, 0, 7201, 0x2C16, 6},
	{81, 0, 0, 7201, 0x2C01, 1},
	{83, 0, 0, 7201, 0x2C75, 2},
	{87, 0, 0, 7201, 0x2C78, 1},
	{90, 0, 0, 7201, 0x2C16, 6},
	{91, 0, 0, 7201, 0x2C01, 1},
	{93, 0, 0, 7201, 0x2C75, 2},
	{95, 0, 0, 7201, 0x2A1A, 3},
	{97, 0, 0, 7201, 0x2C78, 1},
	{100, 0, 0, 7201, 0x2C16, 6},
	{101, 0, 0, 7201, 0x2C01, 1},
	{103, 0, 0, 7201, 0x2C75, 2},
	{107, 0, 0, 7201, 0x2C78, 1},
	{110, 0, 0, 7201, 0x2C16, 6},
	{113, 0, 0, 7201, 0x2C75, 2},
	{115, 0, 0, 7201, 0x2B9C, 1},
	{117, 0, 0, 7201, 0x2C78, 1},
	{120, 0, 0, 7201, 0x2C16, 12},
	{123, 0, 0, 7201, 0x2C75, 2},
	{127, 0, 0, 7201, 0x2C78, 1},
	{130, 0, 0, 7201, 0x2C16, 12},
	{132, 0, 0, 7201, 0x2C75, 2},
	{134, 0, 0, 7201, 0x2C78, 1},
	{136, 0, 0, 7201, 0x2C75, 2},
	{138, 0, 0, 7201, 0x2C78, 1},
	{140, 0, 0, 7201, 0x2C16, 12},
	{142, 0, 0, 7201, 0x2C76, 1}, // 古猟の儀書・蒼
	{144, 0, 0, 7201, 0x2C79, 1}, // 古猟の術書・蒼
	{146, 0, 0, 7201, 0x2C76, 1},
	{148, 0, 0, 7201, 0x2C79, 1},
	{150, 0, 0, 7201, 0x2B9C, 1},
	{155, 0, 0, 7201, 0x2C76, 1},
	{160, 0, 0, 7201, 0x2BC9, 3},
	{165, 0, 0, 7201, 0x2C79, 1},
	{170, 0, 0, 7201, 0x2B97, 10},
	{175, 0, 0, 7201, 0x2C76, 1},
	{180, 0, 0, 7201, 0x2B98, 10},
	{185, 0, 0, 7201, 0x2C79, 1},
	{190, 0, 0, 7201, 0x2B99, 10},
	{195, 0, 0, 7201, 0x2C76, 1},
	{200, 0, 0, 7201, 0x2B97, 15},
	{210, 0, 0, 7201, 0x2B98, 15},
	{220, 0, 0, 7201, 0x2C76, 1},
	{235, 0, 0, 7201, 0x2C79, 2},
	{250, 0, 0, 7201, 0x2B99, 15},
	{265, 0, 0, 7201, 0x2B97, 20},
	{280, 0, 0, 7201, 0x2C76, 1},
	{300, 0, 0, 7201, 0x2B98, 20},
	{315, 0, 0, 7201, 0x2C76, 2},
	{330, 0, 0, 7201, 0x2C79, 1},
	{350, 0, 0, 7201, 0x2B99, 20},
	{365, 0, 0, 7201, 0x2C76, 2},
	{380, 0, 0, 7201, 0x2C79, 1},
	{400, 0, 0, 7201, 0x2B97, 25},
	{415, 0, 0, 7201, 0x2C76, 2},
	{430, 0, 0, 7201, 0x2C79, 1},
	{450, 0, 0, 7201, 0x2B98, 25},
	{465, 0, 0, 7201, 0x2C76, 1},
	{480, 0, 0, 7201, 0x2C79, 2},
	{500, 0, 0, 7201, 0x2B99, 25},
}

// The item/quantity of the original pre-casual guardian reward was not
// recovered. This single ancient document is a server-specific substitute.
// Eligibility is one guardian kill before the zone's casual-mode unlock,
// once per event, not four successive guardian-kill milestones.
var towerAdvanceRewards = []WeeklySeibatuRankingReward{
	{1, 0, 0, 7201, 0x2CC7, 1},
}

// Guild investigation rewards have no surviving complete item/quantity table.
// These are conservative server-specific substitutes, NOT original data.
var towerGuildRewards = []TenrouiraiReward{
	{Index: 1, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{5, 5, 5}},
	{Index: 2, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{8, 8, 8}},
	{Index: 3, Item: []uint16{0x2B97, 0x2B98, 0x2B99, 0x2B9C}, Quantity: []uint8{10, 10, 10, 1}},
	{Index: 4, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{12, 12, 12}},
	{Index: 5, Item: []uint16{0x2B97, 0x2B98, 0x2B99, 0x2B9C}, Quantity: []uint8{15, 15, 15, 1}},
	{Index: 6, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{18, 18, 18}},
	{Index: 7, Item: []uint16{0x2B97, 0x2B98, 0x2B99, 0x2B9C}, Quantity: []uint8{20, 20, 20, 1}},
	{Index: 8, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{24, 24, 24}},
	{Index: 9, Item: []uint16{0x2B97, 0x2B98, 0x2B99, 0x2B9C}, Quantity: []uint8{28, 28, 28, 1}},
	{Index: 10, Item: []uint16{0x2B97, 0x2B98, 0x2B99}, Quantity: []uint8{32, 32, 32}},
	{Index: 11, Item: []uint16{0x2B97, 0x2B98, 0x2B99, 0x2B9C}, Quantity: []uint8{40, 40, 40, 2}},
}
