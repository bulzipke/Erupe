// Package mhfquest contains small pieces of quest metadata shared by the
// channel server and read-only dashboard API.
package mhfquest

// KoreanNames is the manual Korean title override table keyed by the quest ID
// embedded in the quest binary. Add entries here when a translated title is
// preferred over the Japanese title stored in bin/quests.
var KoreanNames = map[uint16]string{
	23618: "≪천이★1 퀘스트≫ 극익을 갖춘 극룡",
	53187: "≪이벤트 퀘스트≫ 사냥 지원! 출발 전 사전 준비",
}

// KoreanName returns a manually localized quest title.
func KoreanName(questID uint16) (string, bool) {
	name, ok := KoreanNames[questID]
	return name, ok && name != ""
}
