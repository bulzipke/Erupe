package channelserver

import (
	"time"

	"go.uber.org/zap"
)

// The client checks these course rights before requesting stamps at login.
const (
	hlStampRights = 1<<2 | 1<<26 | 1<<27 // Hunter Life, official cafe, renewing HL
	exStampRights = 1<<3 | 1<<26 | 1<<28 // Extra, official cafe, renewing EX
)

// stampPeriodStart takes the server's UTC+9 time and returns the most recent noon.
func stampPeriodStart(now time.Time) time.Time {
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start
}

func (s *Session) startDailyStamps() {
	if s.done == nil || s.server.stampRepo == nil {
		return
	}
	s.dailyStampsOnce.Do(func() {
		firstNoon := stampPeriodStart(TimeAdjusted()).AddDate(0, 0, 1)
		go s.runDailyStamps(firstNoon)
	})
}

// Login uses the native client request. The timer covers a session that stays online at noon.
func (s *Session) runDailyStamps(firstNoon time.Time) {
	timer := time.NewTimer(time.Until(firstNoon))
	defer timer.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-timer.C:
			if s.closed.Load() {
				return
			}
			periodStart := stampPeriodStart(TimeAdjusted())
			retry := !periodStart.Before(firstNoon) && !s.claimOnlineDailyStamps(periodStart)
			wait := time.Until(periodStart.AddDate(0, 0, 1))
			if retry {
				wait = min(wait, time.Minute)
			}
			timer.Reset(wait)
		}
	}
}

func (s *Session) claimOnlineDailyStamps(periodStart time.Time) bool {
	s.Lock()
	charID := s.charID
	s.Unlock()
	rights := s.dailyStampRights.Load()
	complete := true
	for _, stamp := range [...]struct {
		kind   string
		rights uint32
	}{{"hl", hlStampRights}, {"ex", exStampRights}} {
		if rights&stamp.rights == 0 {
			continue
		}
		if _, _, _, err := s.server.stampRepo.ClaimDaily(charID, stamp.kind, periodStart); err != nil {
			s.logger.Error("Failed to claim online daily stamp", zap.String("type", stamp.kind), zap.Error(err))
			complete = false
		}
	}
	return complete
}
