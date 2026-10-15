package channelserver

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/server/dashboarddelivery"
	"go.uber.org/zap"
)

// Mocks and older embedders keep the basic SessionRepo interface; the concrete
// PostgreSQL repository adds durable per-connection tracking.
func (s *Session) bindDashboardCoinSession() error {
	repo, ok := s.server.sessionRepo.(*SessionRepository)
	if !ok {
		return s.server.sessionRepo.BindSession(s.token, s.server.ID, s.charID)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
	if err := repo.BindTrackedSession(s.token, s.server.ID, s.charID, id); err != nil {
		return err
	}
	s.dashboardCoinSessionID = id
	return nil
}

func (s *Session) finishDashboardCoinSession(saved bool) error {
	if s.dashboardCoinSessionID == "" {
		return s.server.sessionRepo.ClearSession(s.token)
	}
	repo, ok := s.server.sessionRepo.(*SessionRepository)
	if !ok {
		return s.server.sessionRepo.ClearSession(s.token)
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = repo.FinishTrackedSession(ctx, s.dashboardCoinSessionID, s.userID, s.charID, s.token, saved)
		cancel()
		if err == nil {
			return nil
		}
	}
	return err
}

func (s *Session) startDashboardCoinNotices() {
	if s.server == nil || s.server.db == nil || s.userID == 0 || s.charID == 0 || s.done == nil {
		return
	}
	s.Lock()
	if s.closed.Load() || s.dashboardNoticesDone != nil {
		s.Unlock()
		return
	}
	s.dashboardNoticesDone = make(chan struct{})
	done := s.dashboardNoticesDone
	s.Unlock()
	go func() {
		defer close(done)
		// As with daily coins, wait for the first stage and chat UI to render.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-timer.C:
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				more, err := dashboarddelivery.SendNextNotice(ctx, s.server.db, s.userID, s.charID,
					func(message string) error { return s.sendDashboardCoinNotice(ctx, message) })
				cancel()
				if err != nil {
					if s.closed.Load() {
						return
					}
					s.logger.Warn("Dashboard coin login notice retained for retry", zap.Error(err), zap.Uint32("charID", s.charID))
					timer.Reset(5 * time.Second)
				} else if !more {
					return
				} else {
					timer.Reset(time.Millisecond)
				}
			}
		}
	}()
}

func (s *Session) waitDashboardCoinNotices() {
	s.Lock()
	done := s.dashboardNoticesDone
	s.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Session) sendDashboardCoinNotice(ctx context.Context, message string) error {
	if s.closed.Load() {
		return fmt.Errorf("session closed before coin notice")
	}
	bf := byteframe.NewByteFrame()
	pkt := makeServerChatMessage(message)
	bf.WriteUint16(uint16(pkt.Opcode()))
	if err := pkt.Build(bf, s.clientContext); err != nil {
		return err
	}
	sent := make(chan error, 1)
	select {
	case <-s.done:
		return fmt.Errorf("session closed before coin notice")
	case <-ctx.Done():
		return ctx.Err()
	case s.sendPackets <- packet{data: bf.Data(), nonBlocking: true, sent: sent, sendContext: ctx}:
	}
	select {
	case err := <-sent:
		return err
	case <-s.done:
		return fmt.Errorf("session closed during coin notice")
	case <-ctx.Done():
		return ctx.Err()
	}
}
