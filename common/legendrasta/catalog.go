// Package legendrasta defines the native mhfdat preset name IDs, shared by
// client roster validation and public dashboard display. Equipment variants
// of one character deliberately share one name ID.
package legendrasta

var names = map[uint8]string{
	1:  "플로라",
	2:  "기넬",
	3:  "나타샤",
	4:  "티아라",
	5:  "유웰",
	6:  "프라우",
	7:  "타이조",
	8:  "리아",
	9:  "키스",
	10: "칠카",
	11: "에드워드",
	12: "레일라",
	13: "이토 카이지",
	14: "코트를 입은 남자",
	15: "클로에",
	17: "룰러",
	18: "적의 세이버",
	19: "흑의 세이버",
	20: "흑의 라이더",
	21: "그레이엄",
}

func Name(id uint8) string { return names[id] }
