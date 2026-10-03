package channelserver

import "go.uber.org/zap"

// These three flags were sold only during Diva Defense's Battle Song chapter.
// A DB row is not permission to make a historical event-only product permanent.
func roadShopInterceptionFlag(itemID uint32) bool {
	return itemID >= 13895 && itemID <= 13897
}

func filterRoadShopItems(s *Session, items []ShopItem) []ShopItem {
	hasFlags := false
	for _, item := range items {
		if roadShopInterceptionFlag(item.ItemID) {
			hasFlags = true
			break
		}
	}
	if !hasFlags {
		return items
	}
	active := false
	if s.server.erupeConfig.DebugOptions.DivaOverride != 0 && s.server.divaRepo != nil {
		event, err := s.divaEvent()
		if err != nil {
			s.logger.Warn("Failed to resolve Road interception shop period", zap.Error(err))
		} else {
			active = divaBattleSongPhase(event, TimeAdjusted())
		}
	}
	filtered := make([]ShopItem, 0, len(items))
	for _, item := range items {
		if active || !roadShopInterceptionFlag(item.ItemID) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
