package channelserver

import (
	"time"

	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

const roadShopPurpleMedal uint32 = 0x25C1

// The exchange week turns over at noon KST. Keep the existing Monday
// midnight keys so changing the boundary preserves this week's purchases.
func roadShopWeekKey(at time.Time) time.Time {
	return rotationMondayNoon(at).Add(-12 * time.Hour)
}

// These three flags were sold only during Diva Defense's Battle Song chapter.
// A DB row is not permission to make a historical event-only product permanent.
func roadShopInterceptionFlag(itemID uint32) bool {
	return itemID >= 13895 && itemID <= 13897
}

func roadShopMedalQuest(questID int) bool {
	switch questID {
	case 55519, // Zenith Slaying Report: Rathalos.
		55577, 55578, 55579, 55580, 55586, 55587, 55589, 55590, 55591, 55592, 55633, 55634, 55635, 55636:
		return true
	default:
		return false
	}
}

func (s *Session) roadShopMedalAvailable(now time.Time) bool {
	if s.server.eventRepo == nil {
		return false
	}
	quests, err := s.server.eventRepo.GetEventQuests()
	if err != nil {
		s.logger.Warn("Failed to check Road medal quest availability", zap.Error(err))
		return false
	}
	for _, eq := range quests {
		if !roadShopMedalQuest(eq.QuestID) || !s.allowsEventQuestInWorld(eq) {
			continue
		}
		if s.server.erupeConfig.QuestDelivery.Enabled && s.server.erupeConfig.RealClientMode == cfg.ZZ &&
			eq.ListHidden && isOrdinaryEventQuestType(eq.QuestType) {
			continue
		}
		managed, allowed := s.questDeliveryDecision(eq.QuestID, now)
		if allowed || (!managed && legacyQuestActiveAt(eq, now)) {
			return true
		}
	}
	return false
}

func filterRoadShopItems(s *Session, items []ShopItem) []ShopItem {
	var hasFlags, hasMedal bool
	for _, item := range items {
		hasFlags = hasFlags || roadShopInterceptionFlag(item.ItemID)
		hasMedal = hasMedal || item.ItemID == roadShopPurpleMedal
	}
	if !hasFlags && !hasMedal {
		return items
	}
	now := TimeAdjusted()
	medalAvailable := hasMedal && s.roadShopMedalAvailable(now)
	flagsAvailable := false
	if hasFlags && s.server.erupeConfig.DebugOptions.DivaOverride != 0 && s.server.divaRepo != nil {
		event, err := s.divaEvent()
		if err != nil {
			s.logger.Warn("Failed to resolve Road interception shop period", zap.Error(err))
		} else {
			flagsAvailable = divaBattleSongPhase(event, now)
		}
	}
	filtered := make([]ShopItem, 0, len(items))
	for _, item := range items {
		if (!medalAvailable && item.ItemID == roadShopPurpleMedal) ||
			(!flagsAvailable && roadShopInterceptionFlag(item.ItemID)) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}
