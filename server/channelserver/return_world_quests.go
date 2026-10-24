package channelserver

import (
	"context"
	"sort"
	"time"

	"go.uber.org/zap"
)

// Final G10/ZZ Return-world catalog from the surviving Japanese quest table:
// https://ferias.life.coocan.jp/quest/sr_gr.htm
// All 17 IDs were checked against the embedded IDs and titles of the ripped
// binaries. Do not mix in obsolete HR quests or ticket-consuming event copies.
//
// The guide-tour gift is NOT a distribution/event receipt. In the unmodified
// HD client, FUN_103ff140 starts the tour in world type 5, FUN_103ff270 marks
// savedata+0xce bit 0x1000 on completion, and FUN_105b0cd0 leads to
// FUN_105b2210 case 0x10. That case checks space for item 0x1358 (4952), adds
// 50 through FUN_10bab7c0, and sets savedata+0xce bit 0x2000 on success.
// The native savedata path already preserves both bits and the items. Adding a
// second server distribution here would double-grant the original tour gift.
var returnWorldQuestIDs = [...]int{
	23352, 23353, 23354, 23355, 23356, 23357, 23358, 23359, 23360,
	23361, 23362, 23363, 23364, 23365, 23366, 23374, 23375,
}

type returnWorldEligibilityRepo interface {
	ReturnWorldEligible(userID uint32, now time.Time) (bool, error)
}

func isReturnWorldQuest(questID int) bool {
	for _, id := range returnWorldQuestIDs {
		if questID == id {
			return true
		}
	}
	return false
}

func (s *Session) canUseReturnWorldQuests() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	snapshot, err := s.loadReturnQuestEligibility(ctx)
	cancel()
	if err != nil && s.logger != nil {
		s.logger.Warn("Failed to resolve Return-world quest eligibility", zap.Error(err))
	}
	return err == nil && s.allowsReturnQuestSnapshot(snapshot, returnWorldQuestIDs[0], time.Now())
}

// Inject the original catalog for a currently eligible account in any world.
// Known IDs manually registered in the DB follow the same restriction.
// Existing DB rows win; ordinary event quests and their settings stay intact.
func (s *Session) withReturnWorldQuests(quests []EventQuest) []EventQuest {
	return s.withReturnWorldQuestsAllowed(quests, s.canUseReturnWorldQuests())
}

func (s *Session) withReturnWorldQuestsAllowed(quests []EventQuest, allowed bool) []EventQuest {
	seen := make(map[int]bool, len(quests))
	result := make([]EventQuest, 0, len(quests)+len(returnWorldQuestIDs))
	var nextID uint32
	for _, quest := range quests {
		if quest.ID > nextID {
			nextID = quest.ID
		}
		if isReturnWorldQuest(quest.QuestID) && !allowed {
			continue
		}
		result = append(result, quest)
		seen[quest.QuestID] = true
	}
	if allowed {
		for _, id := range returnWorldQuestIDs {
			if seen[id] {
				continue
			}
			nextID++
			// Deliver using the existing G Event listing (type 28). This does
			// not alter the quest's native costs, reward tables or requirements.
			result = append(result, EventQuest{ID: nextID, QuestID: id, QuestType: 28, MaxPlayers: 4, Flags: -1})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].QuestID < result[j].QuestID })
	return result
}
