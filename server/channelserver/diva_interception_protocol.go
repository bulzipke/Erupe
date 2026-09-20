package channelserver

import (
	"sort"

	"erupe-ce/common/byteframe"
)

// HD 114fe8b0/11536900: GET_UD_TACTICS_POINT accepts 0x204 bytes:
// status u8, total u32, count u8, then count quest IDs (u16).
// HD 114fe9c0/11536a30: ADD_UD_TACTICS_POINT accepts 0x200 bytes:
// status u8, count u8, then the same quest IDs. These are buffered ACK payloads.
// 103a25c0 scans exactly 255 entries to suppress the first-clear quest bonus;
// 10430d40 also uses them for quest-completion display. They are NOT guild areas.
const divaTacticsCompletedQuestCapacity = 255

func divaTacticsPointPayload(total uint32, quests []uint16) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint32(total)
	writeDivaTacticsCompletedQuests(bf, quests)
	return bf.Data()
}

func divaTacticsAddPayload(quests []uint16) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	writeDivaTacticsCompletedQuests(bf, quests)
	return bf.Data()
}

// Neither native parser clears the previous list before copying count entries.
// Always replace all 255 slots, padding unused slots with quest ID zero, so
// a new round does not inherit the previous round's first-clear suppression.
// The caller must supply the complete current list, even for an ignored report.
func writeDivaTacticsCompletedQuests(bf *byteframe.ByteFrame, quests []uint16) {
	ordered := append([]uint16(nil), quests...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	bf.WriteUint8(divaTacticsCompletedQuestCapacity)
	count := 0
	var previous uint16
	for _, quest := range ordered {
		if quest == 0 || quest == previous {
			continue
		}
		if count == divaTacticsCompletedQuestCapacity {
			break
		}
		bf.WriteUint16(quest)
		previous = quest
		count++
	}
	for count < divaTacticsCompletedQuestCapacity {
		bf.WriteUint16(0)
		count++
	}
}
