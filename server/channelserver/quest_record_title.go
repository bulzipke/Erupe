package channelserver

import (
	"fmt"

	"erupe-ce/common/mhfquest"
)

// questTitleForRecord resolves the title attached to the quest ID reported in
// MSG_SYS_RECORD_LOG. Localized JSON quests prefer their Korean title. Retail
// .bin quests contain a single title, so that exact server-sent title is kept.
func questTitleForRecord(s *Session, questID uint16) string {
	if questID == 0 {
		return "퀘스트 번호 없음"
	}
	if title, ok := mhfquest.ResolveTitle(s.server.erupeConfig.BinPath, questID, "ko"); ok {
		return title
	}

	return fmt.Sprintf("퀘스트 #%d", questID)
}
