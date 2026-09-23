package channelserver

import (
	"encoding/binary"
	"erupe-ce/common/stringsupport"
	cfg "erupe-ce/config"
)

// Keep configured legacy event rows, but load G10's two-floor quest files for
// Tower only. Road and explicit arena categories retain their own files.
func towerMilestoneListing(eq EventQuest, mode cfg.Mode) EventQuest {
	if mode == cfg.ZZ && eq.QuestType == 55 {
		switch eq.QuestID {
		case towerQuestGuardian1:
			eq.QuestID = towerQuestMilestone1
		case towerQuestGuardian2:
			eq.QuestID = towerQuestMilestone2
		}
	}
	return eq
}

// The shared quest cache remains byte-for-byte unchanged. Append the Tower
// title and redirect just the first string pointer in this listing's copy.
func towerMilestoneTitle(data []byte, id int) []byte {
	zone := 0
	if id == towerQuestMilestone1 {
		zone = 1
	}
	if id == towerQuestMilestone2 {
		zone = 2
	}
	if zone == 0 || len(data) < questStringPointerOff+4 {
		return data
	}
	table := uint64(binary.LittleEndian.Uint32(data[questStringPointerOff:]))
	if table < questBodyLenZZ || table+4 > uint64(len(data)) || len(data) > 65000 {
		return data
	}
	title := "≪천랑 퀘스트≫\n긴급조사의뢰·1구역"
	if zone == 2 {
		title = "≪천랑 퀘스트≫\n긴급조사의뢰·2구역"
	}
	out := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(out[table:], uint32(len(out)))
	out = append(out, stringsupport.UTF8ToSJIS(title)...)
	return append(out, 0)
}
