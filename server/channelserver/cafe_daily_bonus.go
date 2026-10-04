package channelserver

import (
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"
)

const (
	cafeCourseID               = 30 // Net cafe course; accrues cafe_time.
	cafeBonusItemNetcafePoints = 17 // cafebonus.item_type for N points.
	cafeBonusCheckInterval     = 30 * time.Second
)

// cafeDayNext is the next daily reset of accumulated cafe time: midnight in
// Korea, the same calendar as the daily gacha coins.
func cafeDayNext(now time.Time) time.Time {
	return dailyCoinMidnight(now).AddDate(0, 0, 1)
}

// cafeSessionSeconds is the session time not yet added to cafe_time.
func (s *Session) cafeSessionSeconds(now time.Time) int64 {
	start := s.cafeTimeStart.Load()
	if start == 0 {
		start = s.sessionStart
	}
	return max(0, now.Unix()-start)
}

// syncCafeDay starts a new cafe day once the stored reset time has passed:
// accumulated time and accepted bonuses are cleared, and this session's
// uncommitted time restarts at midnight. A reset further away than the next
// midnight is a weekly date from before the daily cycle and is pulled in.
func (s *Session) syncCafeDay(now time.Time) error {
	next := cafeDayNext(now)
	reset, err := s.server.charRepo.ReadTime(s.charID, "cafe_reset", time.Time{})
	if err == nil && !reset.IsZero() && now.Before(reset) && !reset.After(next) {
		return nil
	}
	if err := s.server.charRepo.ResetCafeTime(s.charID, next); err != nil {
		return err
	}
	if err := s.server.cafeRepo.ResetAccepted(s.charID); err != nil {
		return err
	}
	dayStart := dailyCoinMidnight(now).Unix()
	for {
		start := s.cafeTimeStart.Load()
		if start >= dayStart || s.cafeTimeStart.CompareAndSwap(start, dayStart) {
			return nil
		}
	}
}

// commitCafeTime moves this session's uncommitted time into cafe_time.
func (s *Session) commitCafeTime(now time.Time) {
	if err := s.syncCafeDay(now); err != nil {
		s.logger.Error("Failed to reset cafe day", zap.Error(err))
	}
	elapsed := s.cafeSessionSeconds(now)
	if _, err := s.server.charRepo.AdjustInt(s.charID, "cafe_time", int(elapsed)); err != nil {
		s.logger.Error("Failed to update cafe time", zap.Error(err))
		return
	}
	s.cafeTimeStart.Store(now.Unix())
}

// settleCafeBonus grants every N point bonus reached today and returns the
// chat notice.
func (s *Session) settleCafeBonus(now time.Time) []string {
	if err := s.syncCafeDay(now); err != nil {
		s.logger.Error("Failed to reset cafe day", zap.Error(err))
		return nil
	}
	cafeTime, err := readCharacterInt(s, "cafe_time")
	if err != nil {
		s.logger.Error("Failed to read cafe time", zap.Error(err))
		return nil
	}
	bonuses, err := s.server.cafeRepo.GetBonuses(s.charID)
	if err != nil {
		s.logger.Error("Failed to read cafe bonuses", zap.Error(err))
		return nil
	}
	sort.SliceStable(bonuses, func(i, j int) bool { return bonuses[i].TimeReq < bonuses[j].TimeReq })

	sessionSec := s.cafeSessionSeconds(now)
	elapsed := int64(cafeTime) + sessionSec
	day := dailyCoinMidnight(now).Format("2006-01-02")
	maxPoints := s.server.erupeConfig.GameplayOptions.MaximumNP
	var messages []string
	capped := false
	for _, bonus := range bonuses {
		if bonus.Claimed || bonus.ItemType != cafeBonusItemNetcafePoints || elapsed < int64(bonus.TimeReq) {
			continue
		}
		claim, err := s.server.cafeRepo.ClaimBonus(s.charID, bonus.ID, sessionSec, maxPoints)
		if err != nil {
			s.logger.Error("Failed to claim cafe bonus", zap.Uint32("bonusID", bonus.ID), zap.Error(err))
			break
		}
		if claim.Capped {
			capped = true
			break
		}
		if !claim.Claimed {
			continue
		}
		s.logger.Info("Cafe bonus granted", zap.Uint32("charID", s.charID), zap.Uint32("bonusID", bonus.ID),
			zap.Uint32("timeReq", bonus.TimeReq), zap.Int("points", claim.Granted))
		messages = append(messages, fmt.Sprintf("%d분 접속으로 PC방 포인트를 %d 획득했습니다.", bonus.TimeReq/60, claim.Granted))
		if claim.Granted < int(bonus.Quantity) {
			capped = true
			break
		}
	}
	if !capped {
		s.cafeCapNoticeDay = ""
	} else if s.cafeCapNoticeDay != day {
		// Once per day: a capped bonus is retried every check.
		s.cafeCapNoticeDay = day
		messages = append(messages, "PC방 포인트가 상한치입니다. 더 이상 획득할 수 없습니다.")
	}
	return messages
}

// startCafeBonus runs the automatic N point bonus for this session. Like the
// daily coins it starts after stage entry, so the launcher does not count.
func (s *Session) startCafeBonus() {
	if s.server == nil || s.server.charRepo == nil || s.server.cafeRepo == nil || s.server.erupeConfig == nil ||
		s.charID == 0 || s.done == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	if s.closed.Load() || s.cafeBonusDone != nil {
		return
	}
	s.cafeBonusDone = make(chan struct{})
	go s.runCafeBonus(s.cafeBonusDone)
}

func (s *Session) waitCafeBonus() {
	s.Lock()
	done := s.cafeBonusDone
	s.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Session) runCafeBonus(done chan struct{}) {
	defer close(done)
	// Allow initial stage rendering before the chat notification.
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-timer.C:
			if s.cafeCourse.Load() && !s.closed.Load() {
				messages := s.settleCafeBonus(time.Now())
				if len(messages) > 0 && !s.closed.Load() {
					sendServerChatMessages(s, messages)
				}
			}
			timer.Reset(cafeBonusCheckInterval)
		}
	}
}
