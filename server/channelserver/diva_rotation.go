package channelserver

import (
	"errors"
	"time"

	"go.uber.org/zap"
)

func (s *Server) initializeDivaRotation() error {
	if s.erupeConfig.DebugOptions.DivaOverride != -1 {
		return nil
	}
	repo, ok := s.divaRepo.(DivaEventLifecycleRepository)
	if !ok {
		return errors.New("Diva repository has no lifecycle support")
	}
	_, err := repo.EnsureDivaEvent(TimeAdjusted(), -1)
	return err
}

// Independent of NPC requests. Every channel may run this worker; the shared
// database advisory lock makes startup and rollover idempotent.
func (s *Server) rotateDivaEvents() {
	if s.erupeConfig.DebugOptions.DivaOverride != -1 {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.initializeDivaRotation(); err != nil {
				s.logger.Error("Diva rotation failed", zap.Error(err))
			}
		}
	}
}
