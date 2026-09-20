package channelserver

import (
	"fmt"
	"sort"
)

// The native client applies rewards locally before its claim/save sequence.
// Staging does not grant anything or mark any receipt consumed.
func (s *Session) stageDivaRewardClaims(offers []DivaRewardOffer) error {
	s.Lock()
	defer s.Unlock()
	if len(offers) > 32 {
		return fmt.Errorf("too many pending diva rewards")
	}
	seen := make(map[uint32]struct{}, len(offers))
	newCount := len(s.divaRewardClaims)
	for _, offer := range offers {
		if offer.ID == 0 || offer.Quantity == 0 ||
			(offer.ItemType != 7 && offer.ItemType != 26) ||
			(offer.ItemType == 7 && offer.ItemID == 0) {
			return fmt.Errorf("invalid pending diva reward %d", offer.ID)
		}
		if _, exists := seen[offer.ID]; exists {
			return fmt.Errorf("duplicate pending diva reward %d", offer.ID)
		}
		seen[offer.ID] = struct{}{}
		if pending, exists := s.divaRewardClaims[offer.ID]; exists {
			if pending != offer {
				return fmt.Errorf("conflicting pending diva reward %d", offer.ID)
			}
		} else {
			newCount++
		}
	}
	if newCount > 32 {
		return fmt.Errorf("too many pending diva rewards")
	}
	if len(offers) == 0 {
		return nil
	}
	if s.divaRewardClaims == nil {
		s.divaRewardClaims = make(map[uint32]DivaRewardOffer, len(offers))
	}
	for _, offer := range offers {
		s.divaRewardClaims[offer.ID] = offer
	}
	return nil
}

func (s *Session) pendingDivaRewardClaims(itemType uint8) []uint32 {
	s.Lock()
	defer s.Unlock()
	var ids []uint32
	for id, offer := range s.divaRewardClaims {
		if offer.ItemType == itemType {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Clear only the committed snapshot, not subsequently staged rewards or the
// other save type's pending receipts.
func (s *Session) completeDivaRewardClaims(itemType uint8, ids []uint32) {
	s.Lock()
	defer s.Unlock()
	for _, id := range ids {
		if offer, exists := s.divaRewardClaims[id]; exists && offer.ItemType == itemType {
			delete(s.divaRewardClaims, id)
		}
	}
}

func (s *Session) hasPendingDivaRewardClaims() bool {
	s.Lock()
	defer s.Unlock()
	return len(s.divaRewardClaims) != 0
}
