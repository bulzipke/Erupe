package channelserver

import (
	"errors"
	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"sort"
	"time"

	"go.uber.org/zap"
)

const divaBattleSongDuration = time.Hour

var errDivaBattleSongUnavailable = errors.New("diva battle song is unavailable")

// The native client subtracts Used from its earned milestone count. StartedAt
// is the activation START (not expiry); the client adds 3,600 seconds itself.
type DivaBattleSongState struct {
	Used         uint8  `db:"used_count"`
	ActivationID uint32 `db:"activation_id"`
	StartedAt    uint32 `db:"started_at"`
	Effects      []DivaBattleSongEffect
}

type DivaBattleSongEffect struct {
	ID   uint16 `db:"effect_id"`
	Used uint8  `db:"used_count"`
}

type DivaBattleSongRepository interface {
	GetDivaBattleSong(charID, eventID uint32) (DivaBattleSongState, error)
	UseDivaBattleSong(charID, eventID uint32) (DivaBattleSongState, error)
	ConsumeDivaBattleSongEffects(charID uint32, run divaBattleSongRun, effectIDs []uint16, now time.Time) error
}

func divaBattleSongPhase(event DivaEvent, now time.Time) bool {
	start := int64(event.StartTime)
	return event.ID != 0 && now.Unix() >= start+divaPhaseDuration+divaInterlude &&
		now.Unix() < start+divaPhaseDuration+divaWeekDuration
}

func divaBattleSongActive(state DivaBattleSongState, now time.Time) bool {
	return state.ActivationID != 0 && state.StartedAt != 0 &&
		now.Unix() >= int64(state.StartedAt) &&
		now.Unix() < int64(state.StartedAt)+int64(divaBattleSongDuration/time.Second)
}

// Keep exactly the table advertised by GET_UD_TOTAL_POINT_INFO. Do not turn the
// schedule's 0x19 field into free activations or lower the thresholds.
func divaBattleSongEarned(total int64) uint8 {
	var count uint8
	for i, threshold := range udMilestones {
		if i == 64 {
			break
		}
		if threshold != 0 && total >= 0 && uint64(total) >= threshold {
			count++
		}
	}
	return count
}

func divaBattleSongPayload(state DivaBattleSongState, effects []int) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint8(0)
	bf.WriteUint8(state.Used)
	bf.WriteUint32(state.ActivationID)
	if state.StartedAt == 0 {
		bf.WriteUint32(0xFFFFFFFF)
	} else {
		bf.WriteUint32(state.StartedAt)
	}
	// Native FUN_11535c80 reads four u16 effect IDs and u8 per-effect consumed
	// counts. These are NOT color/error/count triples. Effect descriptions and
	// levels come from Kiju info and daily winning colors, not this response.
	for i := 0; i < 4; i++ {
		var effect uint16
		var used uint8
		if i < len(effects) && effects[i] > 0 && effects[i] <= 25 {
			effect = uint16(effects[i])
		}
		if len(state.Effects) != 0 {
			effect = 0
			if i < len(state.Effects) {
				effect, used = state.Effects[i].ID, state.Effects[i].Used
			}
		}
		bf.WriteUint16(effect)
		bf.WriteUint8(used)
	}
	return bf.Data()
}

func divaBattleSongEffectIDs(ids []uint16) ([]uint16, bool) {
	if len(ids) == 0 || len(ids) > 4 {
		return nil, false
	}
	result := append([]uint16(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for i, id := range result {
		if id == 0 || id > 25 || (i != 0 && id == result[i-1]) {
			return nil, false
		}
	}
	return result, true
}

func consumeDivaBattleSongEffects(s *Session, p *mhfpacket.MsgMhfAddRewardSongCount) {
	reject := func() { doAckBufSucceed(s, p.AckHandle, []byte{1}) }
	ids, valid := divaBattleSongEffectIDs(p.Entries)
	if !valid || int(p.Count) != len(p.Entries) || int(p.ArraySizeBytes) != 2*len(p.Entries) ||
		s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 ||
		s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		reject()
		return
	}
	s.lifecycleMu.Lock()
	run := s.divaBattleRun
	s.lifecycleMu.Unlock()
	if run.Key == "" || run.ActivationID != p.PrayerID {
		reject()
		return
	}
	repo, ok := s.server.divaRepo.(DivaBattleSongRepository)
	if !ok {
		reject()
		return
	}
	if err := repo.ConsumeDivaBattleSongEffects(s.charID, run, ids, TimeAdjusted()); err != nil {
		if !errors.Is(err, errDivaBattleSongUnavailable) {
			s.logger.Warn("Failed to record diva battle song effects", zap.Error(err))
		}
		reject()
		return
	}
	doAckBufSucceed(s, p.AckHandle, []byte{0})
}

func getDivaBattleSong(s *Session, ack uint32) {
	state := DivaBattleSongState{}
	effects := defaultBeadTypes
	if s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		doAckBufFail(s, ack, nil)
		return
	}
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 ||
		mode == 0 || mode == 1 || mode == 3 {
		doAckBufSucceed(s, ack, divaBattleSongPayload(state, nil))
		return
	}
	repo, ok := s.server.divaRepo.(DivaBattleSongRepository)
	if !ok {
		doAckBufSucceed(s, ack, divaBattleSongPayload(state, nil))
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		doAckBufFail(s, ack, nil)
		return
	}
	if divaBattleSongPhase(event, TimeAdjusted()) {
		state, err = repo.GetDivaBattleSong(s.charID, event.ID)
		if err != nil {
			s.logger.Warn("Failed to load diva battle song", zap.Error(err))
			doAckBufFail(s, ack, nil)
			return
		}
	}
	if beads, err := s.server.divaRepo.GetBeads(); err == nil && len(beads) != 0 {
		effects = beads
	}
	doAckBufSucceed(s, ack, divaBattleSongPayload(state, effects))
}

func useDivaBattleSong(s *Session, ack uint32) {
	// USE is a one-byte BUFFER response (FUN_114fe0d0 + FUN_11536890).
	// A simple ACK leaves the native response buffer unset.
	reject := func() { doAckBufSucceed(s, ack, []byte{1}) }
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 || mode == 0 || mode == 1 || mode == 3 ||
		s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		reject()
		return
	}
	repo, ok := s.server.divaRepo.(DivaBattleSongRepository)
	if !ok {
		reject()
		return
	}
	event, err := s.divaEvent()
	now := TimeAdjusted()
	if err != nil || !divaBattleSongPhase(event, now) {
		reject()
		return
	}
	_, err = repo.UseDivaBattleSong(s.charID, event.ID)
	if err != nil {
		if !errors.Is(err, errDivaBattleSongUnavailable) {
			s.logger.Warn("Failed to activate diva battle song", zap.Error(err))
		}
		reject()
		return
	}
	doAckBufSucceed(s, ack, []byte{0})
}
