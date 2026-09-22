package channelserver

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

// Kept after returning to town; replaced only by the next real departure.
// All access is guarded by lifecycleMu, alongside questWeaponGeneration.
type divaInterceptionRun struct {
	Generation       uint64
	StartedAt        time.Time
	QuestID          uint16
	EventID, GuildID uint32
	Key              string
}

func divaInterceptionWindow(event DivaEvent) (time.Time, time.Time) {
	start := time.Unix(int64(event.StartTime)+divaPhaseDuration+divaInterlude, 0)
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+divaWeekDuration, 0)
	return start, end
}

// Called only after validated quest setup and actual stage entry. Do not derive
// a reporting packet's event from whichever round happens to be current later.
func (s *Session) captureDivaInterceptionDeparture(questID uint16, generation uint64) {
	if s.server == nil || s.server.erupeConfig.RealClientMode != cfg.ZZ ||
		s.server.erupeConfig.DebugOptions.DivaOverride == 0 ||
		s.server.erupeConfig.DebugOptions.DivaOverride == 1 ||
		s.server.erupeConfig.DebugOptions.DivaOverride == 3 ||
		questID < udTacticsQuestMin || questID > udTacticsQuestMax || s.server.guildRepo == nil {
		return
	}
	repo, ok := s.server.divaRepo.(DivaInterceptionRepository)
	if !ok {
		return
	}
	s.lifecycleMu.Lock()
	run := s.divaTacticsRun
	s.lifecycleMu.Unlock()
	if run.Generation != generation || run.StartedAt.IsZero() || run.Key != "" {
		return
	}
	event, err := s.divaEvent()
	if err != nil {
		s.logger.Warn("Failed to bind Diva quest event", zap.Error(err))
		return
	}
	start, end := divaInterceptionWindow(event)
	if event.ID == 0 || run.StartedAt.Before(start) || !run.StartedAt.Before(end) {
		return
	}
	progress, err := repo.GetDivaInterceptionProgress(s.charID, event.ID)
	if err != nil {
		s.logger.Warn("Failed to check Diva interception round", zap.Error(err))
		return
	}
	if !progress.Enabled {
		activation, ok := s.server.divaRepo.(DivaMapActivationRepository)
		if !ok || s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour != nil {
			return
		}
		activatedAt, enabled, err := activation.GetDivaMapActivation(event.ID)
		if err != nil {
			s.logger.Warn("Failed to check Diva map activation", zap.Error(err))
			return
		}
		if !enabled || run.StartedAt.Before(activatedAt) {
			s.logger.Info("Legacy Diva interception round retained without new map eligibility", zap.Uint32("eventID", event.ID))
			return
		}
	}
	guildID, reason, err := resolveGuildMemberAccess(s, 0)
	if err != nil || reason != "" || guildID == 0 {
		return
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		s.logger.Warn("Failed to identify Diva quest run", zap.Error(err))
		return
	}
	key[6] = (key[6] & 0x0f) | 0x40
	key[8] = (key[8] & 0x3f) | 0x80
	id := hex.EncodeToString(key[:])
	run.QuestID, run.EventID, run.GuildID = questID, event.ID, guildID
	run.Key = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	if maps, ok := s.server.divaRepo.(DivaMapRepository); ok && s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour == nil {
		if _, err := maps.BindDivaMapDeparture(s.charID, guildID, event.ID, questID, run.Key, run.StartedAt, TimeAdjusted()); err != nil {
			// Do not move an unbound report into whatever map is current later.
			// Personal points still use the independently validated departure.
			s.logger.Warn("Failed to bind Diva map departure", zap.Error(err))
			if !progress.Enabled {
				return
			}
		}
	} else if !progress.Enabled {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.questWeaponGeneration == generation && s.divaTacticsRun.Generation == generation && s.divaTacticsRun.Key == "" {
		s.divaTacticsRun = run
	}
}

func (s *Session) divaInterceptionRunSnapshot() divaInterceptionRun {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.divaTacticsRun
}
