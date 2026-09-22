package channelserver

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	cfg "erupe-ce/config"
)

// Retained across return to town, and replaced only on a real new departure.
// All access is protected by lifecycleMu.
type divaBattleSongRun struct {
	Generation            uint64
	StartedAt             time.Time
	QuestID               uint16
	EventID, ActivationID uint32
	Key                   string
}

func (s *Session) captureDivaBattleSongDeparture(questID uint16, generation uint64) {
	mode := s.server.erupeConfig.DebugOptions.DivaOverride
	if questID == 0 || s.server.erupeConfig.RealClientMode != cfg.ZZ || mode == 0 || mode == 1 || mode == 3 ||
		s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
		return
	}
	repo, ok := s.server.divaRepo.(DivaBattleSongRepository)
	if !ok {
		return
	}
	s.lifecycleMu.Lock()
	run := s.divaBattleRun
	s.lifecycleMu.Unlock()
	if run.Generation != generation || run.StartedAt.IsZero() || run.Key != "" {
		return
	}
	event, err := s.divaEvent()
	if err != nil || !divaBattleSongPhase(event, run.StartedAt) {
		return
	}
	state, err := repo.GetDivaBattleSong(s.charID, event.ID)
	if err != nil || !divaBattleSongActive(state, run.StartedAt) {
		return
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return
	}
	key[6], key[8] = (key[6]&0x0f)|0x40, (key[8]&0x3f)|0x80
	id := hex.EncodeToString(key[:])
	run.Key = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	run.QuestID, run.EventID, run.ActivationID = questID, event.ID, state.ActivationID
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.questWeaponGeneration == generation && s.divaBattleRun.Generation == generation && s.divaBattleRun.Key == "" {
		s.divaBattleRun = run
	}
}
