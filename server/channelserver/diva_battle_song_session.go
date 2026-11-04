package channelserver

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

// Retained across return to town, and replaced only on a real new departure.
// All access is protected by lifecycleMu.
type divaBattleSongRun struct {
	Generation            uint64
	StartedAt             time.Time
	QuestID               uint16
	EventID, ActivationID uint32
	Key                   string
	Beads                 *HuntDivaBeads
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
	var levels map[int]int
	if len(state.Effects) > 0 {
		slots, levelErr := s.divaBeadsForEvent(event)
		if levelErr == nil {
			var colors []byte
			colors, levelErr = s.server.divaRepo.GetDivaWinningColors(event.ID, divaNoon(s.divaSongStart(event)), run.StartedAt)
			if levelErr == nil {
				levels = DivaBeadLevels(slots, colors)
			}
		}
		if levelErr != nil {
			s.logger.Warn("Failed to capture Diva bead levels for hunt record", zap.Error(levelErr))
		}
	}
	run.Beads = divaBattleSongRecordBeads(event.ID, state, levels)
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.questWeaponGeneration == generation && s.divaBattleRun.Generation == generation && s.divaBattleRun.Key == "" {
		s.divaBattleRun = run
	}
}

func divaBattleSongRecordBeads(eventID uint32, state DivaBattleSongState, levels map[int]int) *HuntDivaBeads {
	if eventID == 0 || len(state.Effects) == 0 || len(state.Effects) > 4 {
		return nil
	}
	result := &HuntDivaBeads{Round: eventID}
	seen := make(map[uint16]bool)
	for _, effect := range state.Effects {
		name := strings.TrimSuffix(DivaBeadNameKO(int(effect.ID)), "의 기주")
		if name == "" || seen[effect.ID] {
			return nil
		}
		seen[effect.ID] = true
		result.Beads = append(result.Beads, name)
		if level, known := levels[int(effect.ID)]; known {
			result.Levels = append(result.Levels, level)
		}
	}
	if len(result.Levels) != len(result.Beads) {
		result.Levels = nil
	}
	return result
}

func (s *Session) divaBeadsForHuntRecord(questID uint16) *HuntDivaBeads {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	run := s.divaBattleRun
	if run.Key == "" || run.QuestID != questID || run.Generation != s.questWeaponGeneration || run.Beads == nil {
		return nil
	}
	// Return a copy so later result processing cannot mutate the departure.
	return &HuntDivaBeads{Round: run.Beads.Round, Beads: append([]string(nil), run.Beads.Beads...),
		Levels: append([]int(nil), run.Beads.Levels...)}
}
