package channelserver

import (
	"time"

	"go.uber.org/zap"
)

func (s *Server) festaEvent(now time.Time) (FestaEvent, error) {
	if s.festaRepo == nil {
		return FestaEvent{}, nil
	}
	if r, ok := s.festaRepo.(FestaEventLifecycleRepository); ok && s.erupeConfig.DebugOptions.FestaOverride == -1 {
		return r.EnsureFestaEvent(now)
	}
	events, err := s.festaRepo.GetFestaEvents()
	if err != nil || len(events) == 0 {
		return FestaEvent{}, err
	}
	return events[len(events)-1], nil
}

func (s *Server) initializeFestaRotation() error {
	if s.erupeConfig.DebugOptions.FestaOverride != -1 {
		return nil
	}
	_, err := s.festaEvent(TimeAdjusted())
	return err
}

func (s *Server) rotateFestaEvents() {
	if s.erupeConfig.DebugOptions.FestaOverride != -1 {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.initializeFestaRotation(); err != nil {
				s.logger.Error("Festa rotation failed", zap.Error(err))
			}
		}
	}
}

func (s *Session) festaContext() (FestaEvent, FestaRepo, error) {
	e, err := s.server.festaEvent(TimeAdjusted())
	r := s.server.festaRepo
	if scoped, ok := r.(FestaEventLifecycleRepository); ok && s.server.erupeConfig.DebugOptions.FestaOverride == -1 {
		r = scoped.ForFestaEvent(e.ID)
	}
	return e, r, err
}

func (s *Session) festaPrizeRepo(kind string) (FestaRepo, error) {
	e, repo, err := s.festaContext()
	if err != nil {
		return repo, err
	}
	if e.Continuous {
		id := s.festaPersonalOfferEvent.Load()
		if kind == "guild" {
			id = s.festaGuildOfferEvent.Load()
		}
		if id != e.ID {
			return repo, errFestaRoundChanged
		}
	}
	return repo, nil
}
