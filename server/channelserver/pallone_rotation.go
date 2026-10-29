package channelserver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"time"

	cfg "erupe-ce/config"
	"go.uber.org/zap"
)

// Each round keeps its own ledger. A previous round is claimable while the
// current one is running; Next is persisted before publishing its calendar ID.
type PalloneSchedule struct {
	Current  PalloneEvent
	Next     PalloneEvent
	Previous *PalloneEvent
}

type PalloneRotationRepository interface {
	EnsurePalloneSchedule(time.Time, cfg.PalloneFestivalOptions, string, []PalloneRoute) (PalloneSchedule, error)
}

func (e PalloneEvent) Claimable(now time.Time) bool {
	return !now.Before(e.End) && now.Before(e.ClaimUntil)
}

func palloneInitialStart(now time.Time, o cfg.PalloneFestivalOptions) (time.Time, error) {
	if err := o.Validate(cfg.ZZ); err != nil {
		return time.Time{}, err
	}
	if o.EventID != 0 || o.ActiveDays <= 0 {
		return time.Time{}, ErrPalloneInvalid
	}
	anchor := rotationMondayNoon(now)
	if o.StartAt != "" {
		var err error
		anchor, err = time.Parse(time.RFC3339, o.StartAt)
		if err != nil {
			return time.Time{}, err
		}
	}
	cycle := time.Duration(o.ActiveDays) * 24 * time.Hour
	if !now.Before(anchor) {
		anchor = anchor.Add(now.Sub(anchor) / cycle * cycle)
	}
	return anchor.UTC(), nil
}

func palloneRotationEvent(id int64, start time.Time, o cfg.PalloneFestivalOptions) (PalloneEvent, error) {
	if id <= 0 || id > math.MaxInt32 {
		return PalloneEvent{}, fmt.Errorf("Pallone round ID exhausted")
	}
	e := PalloneEvent{ID: int32(id), Start: start,
		End:        start.Add(time.Duration(o.ActiveDays) * 24 * time.Hour),
		ClaimUntil: start.Add(time.Duration(o.ActiveDays+o.ClaimDays) * 24 * time.Hour)}
	if !validPalloneWindow(e) {
		return PalloneEvent{}, ErrPalloneInvalid
	}
	return e, nil
}

func (s *Server) automaticPalloneFestival() bool {
	return s.independentPalloneFestival() && s.erupeConfig.PalloneFestival.EventID == 0
}

func (s *Server) palloneSchedule(now time.Time) (PalloneSchedule, error) {
	var empty PalloneSchedule
	r, ok := s.palloneRepo.(PalloneRotationRepository)
	if !ok {
		return empty, fmt.Errorf("Pallone repository has no lifecycle support")
	}
	profile, err := loadPallonePublication(s.erupeConfig.BinPath)
	if err != nil || profile == nil {
		return empty, err
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return empty, err
	}
	routes := []PalloneRoute{{Key: profile.Key, Star: uint16(profile.Route.Star), QuestIDs: profile.Route.StageQuestIDs}}
	schedule, err := r.EnsurePalloneSchedule(now, s.erupeConfig.PalloneFestival, fmt.Sprintf("%x", sha256.Sum256(data)), routes)
	if err == nil && schedule.Previous != nil {
		if rewards, ok := s.palloneRepo.(PalloneRewardRepository); ok {
			if _, finalErr := rewards.FinalizePalloneEvent(schedule.Previous.ID, now); finalErr != nil {
				// An old settlement failure must not stop the new round or Tower.
				s.logger.Warn("Pallone previous-round settlement unavailable", zap.Error(finalErr))
			}
		}
	}
	return schedule, err
}

// Only calendar/claim consumers use this selection. Current-score packets have
// no native round selector and must never silently receive last round's score.
func (s *Server) palloneClaimEvent(now time.Time) (*PalloneEvent, error) {
	if s.automaticPalloneFestival() {
		v, err := s.palloneSchedule(now)
		return v.Previous, err
	}
	e, err := s.palloneEvent(now)
	if e != nil && !e.Claimable(now) {
		e = nil
	}
	return e, err
}

func (s *Server) rotatePalloneEvents() {
	if !s.independentPalloneFestival() {
		return
	}
	advance := func() {
		if err := s.recoverPalloneRunReports(TimeAdjusted()); err != nil {
			s.logger.Error("Pallone result recovery failed", zap.Error(err))
		}
		if s.automaticPalloneFestival() {
			if _, err := s.palloneSchedule(TimeAdjusted()); err != nil {
				s.logger.Error("Pallone rotation failed", zap.Error(err))
			}
		}
		if err := s.finalizePalloneRewards(TimeAdjusted()); err != nil {
			s.logger.Error("Pallone finalization failed", zap.Error(err))
		}
	}
	advance()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			advance()
		}
	}
}
