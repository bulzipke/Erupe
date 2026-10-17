package channelserver

import (
	"math/rand/v2"
	"sync"

	"go.uber.org/zap"
)

// Ignored by the stock tune switch, consumed by the matching Vorbis common hook.
const mezFesMinigameTuneID = uint16(0x7ffe)

// MezFesRotation is shared by every channel of ONE MezFes entrance world.
// Independent draws may select the same game on consecutive empty-world cycles.
type MezFesRotation struct {
	mu       sync.Mutex
	game     uint16
	sessions int
	choose   func() uint16
}

func NewMezFesRotation() *MezFesRotation {
	return &MezFesRotation{choose: func() uint16 { return uint16(2 + rand.IntN(2)*2) }}
}

func (r *MezFesRotation) acquire() (uint16, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	started := r.sessions == 0
	if started {
		r.game = r.choose()
	}
	r.sessions++
	return r.game, started
}

func (r *MezFesRotation) release() (uint16, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == 0 {
		return 0, false
	}
	game := r.game
	r.sessions--
	if r.sessions == 0 {
		r.game = 0
		return game, true
	}
	return game, false
}

func (s *Session) acquireMezFesMinigame() {
	if s.server == nil || s.server.worldType != 6 || s.server.mezFesRotation == nil || !s.server.erupeConfig.GameplayOptions.MezFesRandomMinigame {
		return
	}
	s.Lock()
	if s.mezFesRotationAcquired || s.closed.Load() {
		s.Unlock()
		return
	}
	game, started := s.server.mezFesRotation.acquire()
	s.mezFesMinigame, s.mezFesRotationAcquired = game, true
	s.Unlock()
	if started && s.logger != nil {
		s.logger.Info("Random MezFes group game selected", zap.Uint16("game", game))
	}
}

func (s *Session) releaseMezFesMinigame() {
	if s.server == nil || s.server.mezFesRotation == nil {
		return
	}
	s.Lock()
	if !s.mezFesRotationAcquired {
		s.Unlock()
		return
	}
	s.mezFesRotationAcquired = false
	s.mezFesMinigame = 0
	s.Unlock()
	game, ended := s.server.mezFesRotation.release()
	if ended && s.logger != nil {
		s.logger.Info("Random MezFes world emptied", zap.Uint16("game", game))
	}
}

func (s *Session) effectiveMezFesMinigame() uint16 {
	s.Lock()
	game := s.mezFesMinigame
	s.Unlock()
	if game == 2 || game == 4 {
		return game
	}
	if s.server.erupeConfig.GameplayOptions.MezFesSwitchMinigame {
		return 2
	}
	return 4
}
