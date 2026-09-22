package channelserver

import (
	"errors"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

const divaTacticsFollowerChangeCooldown = 24 * time.Hour

var (
	errDivaTacticsFollowerUnavailable = errors.New("diva tactics follower unavailable")
	errDivaTacticsFollowerLocked      = errors.New("diva tactics follower is locked for 24 hours")
	errDivaTacticsFollowerFunds       = errors.New("insufficient guild contribution points")
)

// Indices into the client's mhfsqd.bin tables, not item or NPC IDs.
type DivaTacticsFollowerChoice struct {
	NameIndex uint16 `db:"name_index"`
	Voice     uint16 `db:"voice"`
	Weapon    uint16 `db:"weapon"`
	Strength  uint16 `db:"strength"`
}

type DivaTacticsFollowerState struct {
	DivaTacticsFollowerChoice
	// Earliest settings-change time. A nonzero past value still enables NPCs.
	AvailableAt uint32 `db:"available_at"`
}

type DivaTacticsFollowerRepository interface {
	GetDivaTacticsFollower(charID, eventID uint32, override int) (DivaTacticsFollowerState, error)
	SetDivaTacticsFollower(charID, eventID uint32, choice DivaTacticsFollowerChoice, override int) (DivaTacticsFollowerState, error)
}

func divaTacticsFollowerCost(choice DivaTacticsFollowerChoice) (uint32, bool) {
	if choice.NameIndex >= 40 || choice.Voice >= 4 || choice.Weapon >= 2 || choice.Strength >= 3 {
		return 0, false
	}
	// Both native weapon choices cost 300 GP; strength adds 500/1,000/2,000.
	return 300 + [...]uint32{500, 1000, 2000}[choice.Strength], true
}

func divaTacticsFollowerPayload(state DivaTacticsFollowerState) []byte {
	bf := byteframe.NewByteFrame()
	bf.WriteUint16(state.NameIndex)
	bf.WriteUint16(state.Voice)
	bf.WriteUint16(state.Weapon)
	bf.WriteUint16(state.Strength)
	bf.WriteUint32(state.AvailableAt)
	return bf.Data()
}

func expectedDivaTacticsFollowerError(err error) bool {
	return errors.Is(err, errDivaTacticsFollowerUnavailable) || errors.Is(err, errDivaTacticsFollowerLocked) || errors.Is(err, errDivaTacticsFollowerFunds)
}

func getDivaTacticsFollower(s *Session, ack uint32) {
	empty := func() { doAckBufSucceed(s, ack, divaTacticsFollowerPayload(DivaTacticsFollowerState{})) }
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 || (mode != -1 && mode != 2) {
		empty()
		return
	}
	if s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		doAckBufFail(s, ack, nil)
		return
	}
	repo, ok := s.server.divaRepo.(DivaTacticsFollowerRepository)
	if !ok {
		empty()
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		doAckBufFail(s, ack, nil)
		return
	}
	if !divaBattleSongPhase(event, TimeAdjusted()) {
		empty()
		return
	}
	state, err := repo.GetDivaTacticsFollower(s.charID, event.ID, mode)
	if expectedDivaTacticsFollowerError(err) {
		empty()
		return
	}
	if err != nil {
		s.logger.Warn("Failed to load diva tactics follower", zap.Error(err))
		doAckBufFail(s, ack, nil)
		return
	}
	doAckBufSucceed(s, ack, divaTacticsFollowerPayload(state))
}

func setDivaTacticsFollower(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
	reject := func() { doAckSimpleFail(s, p.AckHandle, make([]byte, 4)) }
	choice := DivaTacticsFollowerChoice{NameIndex: p.NameIndex, Voice: p.Voice, Weapon: p.Weapon, Strength: p.Strength}
	_, valid := divaTacticsFollowerCost(choice)
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if !valid || s.server.erupeConfig.RealClientMode != cfg.ZZ || s.charID == 0 || (mode != -1 && mode != 2) ||
		s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		reject()
		return
	}
	repo, ok := s.server.divaRepo.(DivaTacticsFollowerRepository)
	if !ok {
		reject()
		return
	}
	event, err := s.divaEvent()
	if err != nil || !divaBattleSongPhase(event, TimeAdjusted()) {
		reject()
		return
	}
	if _, err = repo.SetDivaTacticsFollower(s.charID, event.ID, choice, mode); err != nil {
		if !expectedDivaTacticsFollowerError(err) {
			s.logger.Warn("Failed to hire diva tactics follower", zap.Error(err))
		}
		reject()
		return
	}
	// Native SET completion expects a simple ACK. It deducts the same cost from
	// local GP and then saves the absolute balance; a duplicate success is unsafe.
	doAckSimpleSucceed(s, p.AckHandle, make([]byte, 4))
}
