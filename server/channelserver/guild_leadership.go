package channelserver

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type inactiveGuildLeaderRotator interface {
	RotateInactiveGuildLeaders(context.Context, time.Time) (bool, []GuildLeadershipChange, error)
}

// Every channel can run this worker. The durable weekly cursor and advisory
// lock serialize the actual changes across channels and processes. Busy/error
// retries run in one minute; otherwise sleep until Monday 00:00 UTC+9.
func (s *Server) rotateInactiveGuildLeaders() {
	repo, ok := s.guildRepo.(inactiveGuildLeaderRotator)
	if !ok || s.db == nil || s.erupeConfig.DebugOptions.ProxyPort != 0 {
		return
	}
	var processedWeek time.Time
	for {
		select {
		case <-s.done:
			return
		default:
		}
		now := TimeAdjusted()
		week := guildLeadershipWeekStart(now)
		if week.After(processedWeek) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			complete, changes, err := repo.RotateInactiveGuildLeaders(ctx, now)
			cancel()
			if err != nil {
				s.logger.Warn("Failed to rotate inactive guild leaders", zap.Error(err))
			} else if complete {
				processedWeek = week
				for _, change := range changes {
					s.logger.Info("Automatically transferred inactive guild leadership",
						zap.Uint32("guildID", change.GuildID), zap.Uint32("formerLeaderID", change.FormerLeaderID),
						zap.Uint32("newLeaderID", change.NewLeaderID), zap.Time("week", week))
				}
			}
		}
		delay := time.Until(week.AddDate(0, 0, 7))
		if processedWeek.Before(week) && delay > time.Minute {
			delay = time.Minute
		}
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-s.done:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
