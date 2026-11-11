package channelserver

import (
	"context"
	"erupe-ce/common/clientintegrity"
	"go.uber.org/zap"
)

func (s *Session) attachClientIntegrity(tid, cid uint32, token string) bool {
	if !s.server.erupeConfig.ClientIntegrity.Enabled {
		return true
	}
	disconnect := func() {
		s.logger.Warn("Disconnecting channel: client integrity failed", zap.Uint32("charID", cid))
		s.markClosed()
		s.closeConnection()
	}
	m := s.server.integrity
	if m == nil || s.server.erupeConfig.DebugOptions.DisableTokenCheck {
		disconnect()
		return false
	}
	s.integrityMu.Lock()
	already := s.integrityRelease != nil
	s.integrityMu.Unlock()
	if already {
		disconnect()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.Timeout())
	defer cancel()
	release, err := m.Attach(ctx, clientintegrity.SessionKey(tid, cid, token), disconnect)
	if err != nil {
		disconnect()
		return false
	}
	s.integrityMu.Lock()
	if s.closed.Load() {
		s.integrityMu.Unlock()
		release()
		return false
	}
	s.integrityRelease = release
	s.integrityMu.Unlock()
	return true
}
