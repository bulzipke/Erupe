package channelserver

import "math"

// The client must receive the modified snapshot before it can upload an older
// local RP balance. Apply automatic awards at load, with a durable receipt in
// the same savedata transaction; online hunters receive them on their next load.
func (s *Session) applyPalloneRPOnLoad(save *CharacterSaveData) error {
	if !s.server.independentPalloneFestival() {
		return nil
	}
	r, ok := s.server.palloneRepo.(PalloneRewardRepository)
	if !ok {
		return nil
	}
	if err := r.FinalizeEndedPalloneEvents(TimeAdjusted()); err != nil {
		return err
	}
	awards, err := r.PendingPalloneRP(s.charID)
	if err != nil || len(awards) == 0 {
		return err
	}
	amount := uint64(save.RP)
	for _, a := range awards {
		if a.Quantity <= 0 {
			return ErrPalloneInvalid
		}
		amount += uint64(a.Quantity)
		save.palloneRPEvents = append(save.palloneRPEvents, a.EventID)
	}
	limit := uint64(s.server.erupeConfig.GameplayOptions.MaximumRP)
	if limit == 0 {
		limit = math.MaxUint16
	}
	if amount > limit {
		amount = limit
	}
	save.RP = uint16(amount)
	return save.Save(s)
}
