package channelserver

import (
	"fmt"
	"sort"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"go.uber.org/zap"
)

func pallonePresentType(t uint32) bool { return t >= 3201 && t <= 3204 || t >= 3301 && t <= 3303 }

func palloneRewardID(id uint32) bool { return id&0xc000 == 0xc000 }

func pallonePresentReply(s *Session, p *mhfpacket.MsgMhfPresentBox, eventID int32, rows []PalloneRewardOffer, value uint32, err error) {
	if err != nil {
		s.logger.Warn("Pallone reward request failed", zap.Error(err))
		if p.Unk1 == 1 {
			doAckBufFail(s, p.AckHandle, nil)
		} else {
			doAckSimpleFail(s, p.AckHandle, make([]byte, 4))
		}
		return
	}
	bf := byteframe.NewByteFrame()
	if p.Unk1 != 1 {
		bf.WriteUint32(value)
		doAckSimpleSucceed(s, p.AckHandle, bf.Data())
		return
	}
	bf.WriteInt32(eventID)
	bf.WriteUint32(0)
	bf.WriteUint32(0)
	bf.WriteUint32(uint32(len(rows)))
	for _, r := range rows {
		bf.WriteUint32(r.ID)
		bf.WriteUint32(r.PresentType)
		// The six unused retail present fields have no verified festival
		// semantics. Keep the native zero layout used by the common receiver.
		for i := 0; i < 6; i++ {
			bf.WriteInt32(0)
		}
		bf.WriteInt32(r.ItemType)
		bf.WriteUint32(uint32(r.ItemID))
		bf.WriteInt32(r.Quantity)
	}
	doAckBufSucceed(s, p.AckHandle, bf.Data())
}

func (s *Session) handlePallonePresentBox(p *mhfpacket.MsgMhfPresentBox) bool {
	selected := false
	if p.Unk1 == 1 || p.Unk1 == 3 {
		for _, typ := range p.Unk7 {
			selected = selected || pallonePresentType(typ)
		}
	}
	if p.Unk1 == 2 && s.server.independentPalloneFestival() {
		for _, id := range p.Unk7 {
			selected = selected || palloneRewardID(id)
		}
	}
	if !selected {
		return false
	}
	if p.Unk2 != uint32(len(p.Unk7)) || len(p.Unk7) == 0 || len(p.Unk7) > 256 {
		pallonePresentReply(s, p, 0, nil, 0, ErrPalloneInvalid)
		return true
	}
	r, ok := s.server.palloneRepo.(PalloneRewardRepository)
	if !ok || !s.server.independentPalloneFestival() {
		pallonePresentReply(s, p, 0, nil, 0, nil)
		return true
	}
	now := TimeAdjusted()
	if p.Unk1 == 2 {
		offers, err := r.PreparePalloneRewardClaims(s.charID, p.Unk7, now)
		if err == nil {
			err = s.stagePalloneRewardClaims(offers)
		}
		pallonePresentReply(s, p, 0, nil, 0, err)
		return true
	}
	current := p.Unk7[0] >= 3301
	for _, typ := range p.Unk7 {
		if !pallonePresentType(typ) || current != (typ >= 3301) {
			pallonePresentReply(s, p, 0, nil, 0, ErrPalloneInvalid)
			return true
		}
	}
	var e *PalloneEvent
	var err error
	if current {
		e, err = s.server.palloneEvent(now)
	} else {
		e, err = s.server.palloneClaimEvent(now)
	}
	if err != nil || e == nil {
		pallonePresentReply(s, p, 0, nil, 0, err)
		return true
	}
	if !current {
		if _, err = r.FinalizePalloneEvent(e.ID, now); err != nil {
			pallonePresentReply(s, p, e.ID, nil, 0, err)
			return true
		}
	}
	types := append([]uint32(nil), p.Unk7...)
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for i := 1; i < len(types); i++ {
		if types[i] == types[i-1] {
			pallonePresentReply(s, p, e.ID, nil, 0, ErrPalloneInvalid)
			return true
		}
	}
	key := fmt.Sprintf("%d:%v", e.ID, types)
	if p.Unk1 == 1 && p.Unk3 != 0 {
		s.Lock()
		snapshot, exists := s.palloneRewardPages[key]
		snapshot = append([]PalloneRewardOffer(nil), snapshot...)
		s.Unlock()
		if !exists {
			pallonePresentReply(s, p, e.ID, nil, 0, ErrPalloneInvalid)
			return true
		}
		pallonePresentReply(s, p, e.ID, palloneRewardPage(snapshot, p.Unk3), 0, nil)
		return true
	}
	offers, err := r.OfferPalloneRewards(e.ID, s.charID, current, p.Unk7, now)
	if err != nil {
		pallonePresentReply(s, p, e.ID, nil, 0, err)
		return true
	}
	s.Lock()
	visible := make([]PalloneRewardOffer, 0, len(offers))
	for _, offer := range offers {
		if _, pending := s.palloneRewardClaims[offer.ID]; !pending {
			visible = append(visible, offer)
		}
	}
	if s.palloneRewardPages == nil {
		s.palloneRewardPages = make(map[string][]PalloneRewardOffer)
	}
	// A receiving loop advances by 256 even after earlier receipts commit.
	// Preserve its page membership so deleting claimed rows cannot skip a page.
	if p.Unk1 == 1 {
		if len(s.palloneRewardPages) >= 8 {
			s.palloneRewardPages = make(map[string][]PalloneRewardOffer)
		}
		s.palloneRewardPages[key] = append([]PalloneRewardOffer(nil), visible...)
	}
	s.Unlock()
	pallonePresentReply(s, p, e.ID, palloneRewardPage(visible, 0), uint32(len(visible)), nil)
	return true
}

func palloneRewardPage(rows []PalloneRewardOffer, offset uint32) []PalloneRewardOffer {
	if uint64(offset) >= uint64(len(rows)) {
		return nil
	}
	end := uint64(offset) + 256
	if end > uint64(len(rows)) {
		end = uint64(len(rows))
	}
	return rows[int(offset):int(end)]
}

func (s *Session) stagePalloneRewardClaims(offers []PalloneRewardOffer) error {
	s.Lock()
	defer s.Unlock()
	if len(s.palloneRewardClaims)+len(offers) > 8192 {
		return ErrPalloneInvalid
	}
	for _, o := range offers {
		if o.Claimed {
			continue
		}
		if o.CharID != s.charID || !palloneRewardID(o.ID) || (o.ItemType != palloneItem && o.ItemType != palloneGP) {
			return ErrPalloneInvalid
		}
		if previous, ok := s.palloneRewardClaims[o.ID]; ok && previous != o {
			return ErrPalloneConflict
		}
	}
	if s.palloneRewardClaims == nil {
		s.palloneRewardClaims = make(map[uint32]PalloneRewardOffer)
	}
	for _, o := range offers {
		if !o.Claimed {
			s.palloneRewardClaims[o.ID] = o
		}
	}
	return nil
}

func (s *Session) pendingPalloneRewardClaims(itemType int32) []uint32 {
	s.Lock()
	defer s.Unlock()
	var ids []uint32
	for id, o := range s.palloneRewardClaims {
		if o.ItemType == itemType {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *Session) completePalloneRewardClaims(itemType int32, ids []uint32) {
	s.Lock()
	defer s.Unlock()
	for _, id := range ids {
		if o, ok := s.palloneRewardClaims[id]; ok && o.ItemType == itemType {
			delete(s.palloneRewardClaims, id)
		}
	}
}

func (s *Session) handlePalloneRewardTable(p *mhfpacket.MsgMhfGetWeeklySeibatuRankingReward) bool {
	if p.Unk1 != 5 || !s.server.independentPalloneFestival() {
		return false
	}
	var kind string
	gpOnly := false
	itemsOnly := false
	star := -1
	switch {
	case p.Unk2 == 240031:
		kind = "reach"
	case p.Unk2 == 240041:
		kind = "break"
		itemsOnly = true
	case p.Unk2 == 240042:
		kind = "break"
		gpOnly = true
	case p.Unk2 == 240051:
		kind = "conquer"
		itemsOnly = true
	case p.Unk2 == 240052:
		kind = "conquer"
		gpOnly = true
	case p.Unk2 == 240001:
		kind = "personal-total"
	case p.Unk2 == 240002:
		kind = "guild-total"
	case p.Unk2 >= 243400 && p.Unk2 <= 243410:
		kind = "personal-route"
		star = int(p.Unk2 - 243400)
	case p.Unk2 >= 243500 && p.Unk2 <= 243510:
		kind = "guild-route"
		star = int(p.Unk2 - 243500)
	default:
		return false
	}
	current := kind == "reach" || kind == "break" || kind == "conquer"
	now := TimeAdjusted()
	var e *PalloneEvent
	var err error
	if current {
		e, err = s.server.palloneEvent(now)
	} else {
		e, err = s.server.palloneClaimEvent(now)
	}
	if err != nil {
		doAckBufFail(s, p.AckHandle, nil)
		return true
	}
	if e == nil && !current {
		// Preview remains available in the first round, before any prior award
		// exists. This does not create ranking entitlements before settlement.
		e, err = s.server.palloneEvent(now)
		if err != nil {
			doAckBufFail(s, p.AckHandle, nil)
			return true
		}
	}
	if e == nil {
		doAckPalloneEmpty(s, p.AckHandle)
		return true
	}
	r, ok := s.server.palloneRepo.(PalloneRewardRepository)
	if !ok {
		doAckPalloneEmpty(s, p.AckHandle)
		return true
	}
	c, err := r.PalloneRewardCatalog(e.ID)
	if err != nil {
		doAckBufFail(s, p.AckHandle, nil)
		return true
	}
	var rules []PalloneRewardRule
	for _, rule := range c.Rules {
		if rule.Kind != kind || gpOnly && rule.ItemType != palloneGP || itemsOnly && rule.ItemType != palloneItem || star >= 0 && rule.Star >= 0 && star != rule.Star {
			continue
		}
		rules = append(rules, rule)
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if current {
			return rules[i].Threshold < rules[j].Threshold
		}
		return rules[i].MinRank < rules[j].MinRank
	})
	bf := byteframe.NewByteFrame()
	bf.WriteInt32(e.ID)
	bf.WriteUint32(0)
	bf.WriteUint32(0)
	bf.WriteUint32(uint32(len(rules)))
	for _, rule := range rules {
		a, b := rule.Threshold, int32(0)
		if !current {
			a = rule.MinRank
			b = rule.MaxRank
		}
		bf.WriteInt32(a)
		bf.WriteInt32(b)
		bf.WriteUint32(0)
		bf.WriteInt32(rule.ItemType)
		bf.WriteUint32(uint32(rule.ItemID))
		bf.WriteInt32(rule.Quantity)
	}
	doAckBufSucceed(s, p.AckHandle, bf.Data())
	return true
}

func (s *Server) finalizePalloneRewards(now time.Time) error {
	r, ok := s.palloneRepo.(PalloneRewardRepository)
	if !ok {
		return nil
	}
	return r.FinalizeEndedPalloneEvents(now)
}
