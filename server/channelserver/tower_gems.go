package channelserver

// towerGemTypes maps the five CSV slots to the active client msx type IDs.
// Type 5 is unused; type 8 is the candlestick. Colors occupy the high byte.
var towerGemTypes = [...]uint16{1, 2, 3, 4, 8}

func towerGemID(index int) uint16 {
	return uint16(index/5)<<8 | towerGemTypes[index%5]
}

func towerGemIndex(id int32) (int, bool) {
	if id < 0 || id>>8 >= 6 {
		return 0, false
	}
	for slot, kind := range towerGemTypes {
		if uint16(id&0xff) == kind {
			return int(id>>8)*5 + slot, true
		}
	}
	return 0, false
}
