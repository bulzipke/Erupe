package channelserver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// Bound one admitted run independently of the event deadline. This duration
// never delays settlement when the next event begins.
const palloneMaxRunDuration = 4 * time.Hour

func permanentPalloneRunError(err error) bool {
	return errors.Is(err, ErrPalloneInvalid) || errors.Is(err, ErrPalloneConflict) || errors.Is(err, ErrPalloneIneligible) || errors.Is(err, ErrPalloneGuild) || errors.Is(err, ErrPalloneScoreLimit)
}

type palloneDeparturePlan struct {
	Event  PalloneEvent
	Routes []PalloneRoute
}

type palloneRunState struct {
	Receipt  PalloneRunReceipt
	EndsAt   time.Time
	QuestIDs []uint16
	Stage    int
	StageID  string
	Failed   bool
}

type PalloneReadyRun struct {
	Receipt    PalloneRunReceipt
	Submission PalloneSubmission
}

func (s *Server) preparePalloneDeparture(now time.Time) *palloneDeparturePlan {
	if s.erupeConfig.RealClientMode != cfg.ZZ || s.palloneRepo == nil {
		return nil
	}
	e, err := s.palloneEvent(now)
	if err != nil || e == nil || !e.Active(now) {
		return nil
	}
	publication, err := s.palloneDeliveryRows(e.ID)
	if err != nil || len(publication) == 0 {
		return nil
	}
	routes, err := s.palloneRepo.GetPalloneRoutes(e.ID)
	if err != nil {
		return nil
	}
	return &palloneDeparturePlan{Event: *e, Routes: routes}
}

// Called only for an admitted quest participant while lifecycleMu is held.
// Town transfer preserves the snapshot: the native result is sent afterwards.
func (s *Session) capturePalloneStageLocked(qid uint16, stageID string, now time.Time, plan *palloneDeparturePlan, fresh bool) {
	if qid == 0 {
		return
	}
	run := s.palloneRun
	if run != nil && !run.Failed && now.Before(run.Receipt.StartedAt.Add(palloneMaxRunDuration)) {
		if !fresh && qid == run.QuestIDs[run.Stage] && stageID == run.StageID {
			return // Duplicate setup, not another completion.
		}
		if run.Stage+1 < len(run.QuestIDs) && qid == run.QuestIDs[run.Stage+1] {
			run.Stage++
			run.StageID = stageID
			return
		}
	}
	s.palloneRun = nil
	s.palloneReceipt = nil
	startedAt := now
	if !fresh && plan == s.palloneDeparture && !s.palloneDepartureStarted.IsZero() {
		startedAt = s.palloneDepartureStarted
	}
	if plan == nil || !plan.Event.Active(startedAt) || !now.Before(startedAt.Add(palloneMaxRunDuration)) {
		return
	}
	var selected *PalloneRoute
	for i := range plan.Routes {
		r := &plan.Routes[i]
		if len(r.QuestIDs) != 0 && r.QuestIDs[0] == qid {
			if selected != nil {
				return // Ambiguous first quest needs an explicit route selector.
			}
			selected = r
		}
	}
	if selected == nil {
		return
	}
	id := newTowerGuardianRunID()
	if id == "" {
		return
	}
	s.palloneRun = &palloneRunState{
		Receipt: PalloneRunReceipt{EventID: plan.Event.ID, CharID: s.charID, RouteKey: selected.Key,
			RunID: id, StartedAt: startedAt, FinalQuestID: selected.QuestIDs[len(selected.QuestIDs)-1]},
		EndsAt:   plan.Event.End,
		QuestIDs: append([]uint16(nil), selected.QuestIDs...), StageID: stageID,
	}
}

func (s *Session) capturePalloneStage(qid uint16, stageID string, now time.Time, plan *palloneDeparturePlan) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.capturePalloneStageLocked(qid, stageID, now, plan, false)
}

func (s *Session) savePalloneRunReport(receipt PalloneRunReceipt, v *PalloneSubmission, qualified bool, now time.Time) error {
	r, ok := s.server.palloneRepo.(PalloneRunRepository)
	if !ok || s.server.palloneService == nil {
		return ErrPalloneInvalid
	}
	ready, err := r.SavePalloneRunReport(receipt, v, qualified, now)
	if err != nil || ready == nil {
		return err
	}
	_, err = s.server.palloneService.Submit(s.charID, ready.Receipt, ready.Submission, now)
	if err == nil {
		s.lifecycleMu.Lock()
		if s.palloneRun != nil && s.palloneRun.Receipt.RunID == ready.Receipt.RunID {
			copy := ready.Receipt
			s.palloneReceipt = &copy
		}
		s.lifecycleMu.Unlock()
	}
	if permanentPalloneRunError(err) {
		if markErr := r.RejectPalloneRunReport(s.charID, ready.Receipt.RunID, err.Error(), now); markErr != nil {
			return markErr
		}
	}
	return err
}

func (s *Session) submitPalloneRunScore(pkt *mhfpacket.MsgMhfCaravanMyScore, now time.Time) error {
	s.lifecycleMu.Lock()
	run := s.palloneRun
	if run == nil || run.Failed || run.Stage != len(run.QuestIDs)-1 || pkt.Unk1 != 3 || pkt.Unk2 != run.Receipt.RouteKey ||
		!now.Before(run.Receipt.StartedAt.Add(palloneMaxRunDuration)) {
		s.lifecycleMu.Unlock()
		return ErrPalloneInvalid
	}
	receipt := run.Receipt
	closed := !now.Before(run.EndsAt)
	s.lifecycleMu.Unlock()
	v := PalloneSubmission{BaseScore: pkt.Unk3, PremiumBonus: pkt.Unk5, ItemBonus: pkt.Unk6, GuildID: pkt.Unk4}
	if !validPalloneSubmission(v) {
		return ErrPalloneInvalid
	}
	if closed {
		return nil
	}
	err := s.savePalloneRunReport(receipt, &v, false, now)
	if errors.Is(err, ErrPalloneIneligible) {
		return nil
	}
	return err
}

func (s *Session) recordPalloneRunResult(data []byte, now time.Time) error {
	if s.server.erupeConfig.RealClientMode != cfg.ZZ || len(data) < killLogHeaderSize+killLogMonsterCount {
		return nil
	}
	qid := binary.LittleEndian.Uint16(data[questIDOffset:])
	s.lifecycleMu.Lock()
	run := s.palloneRun
	if run == nil || run.Failed || qid != run.QuestIDs[run.Stage] {
		s.lifecycleMu.Unlock()
		return nil
	}
	if data[questResultCodeOffset] != questResultCodeCleared {
		run.Failed = true
		s.palloneReceipt = nil
		s.lifecycleMu.Unlock()
		return nil
	}
	if run.Stage != len(run.QuestIDs)-1 {
		s.lifecycleMu.Unlock()
		return nil
	}
	if !now.Before(run.EndsAt) {
		// A late festival clear must still reach the normal quest-result handler.
		// Its pinned round is closed; never move this run into the new round.
		s.lifecycleMu.Unlock()
		return nil
	}
	frames := binary.LittleEndian.Uint32(data[questElapsedFramesOffset:])
	if frames == 0 || frames > maxRankedQuestDurationFrames || now.Before(run.Receipt.StartedAt) ||
		!now.Before(run.Receipt.StartedAt.Add(palloneMaxRunDuration)) {
		s.lifecycleMu.Unlock()
		return fmt.Errorf("invalid Pallone completion time")
	}
	if run.Receipt.CompletedAt.IsZero() {
		run.Receipt.CompletedAt = now
	}
	receipt := run.Receipt
	s.lifecycleMu.Unlock()
	// Qualifying and receiving points are two durable, order-independent
	// reports. A score request can never qualify its own run.
	err := s.savePalloneRunReport(receipt, nil, true, now)
	if errors.Is(err, ErrPalloneIneligible) {
		return nil
	}
	return err
}

func (s *Server) recoverPalloneRunReports(now time.Time) error {
	r, ok := s.palloneRepo.(PalloneRunRepository)
	if !ok || s.palloneService == nil {
		return nil
	}
	ready, err := r.ReadyPalloneRuns(now)
	if err != nil {
		return err
	}
	for _, v := range ready {
		if _, err = s.palloneService.Submit(v.Receipt.CharID, v.Receipt, v.Submission, now); err != nil {
			if permanentPalloneRunError(err) {
				if markErr := r.RejectPalloneRunReport(v.Receipt.CharID, v.Receipt.RunID, err.Error(), now); markErr != nil {
					return markErr
				}
				continue
			}
			return err
		}
	}
	return nil
}
