package channelserver

import (
	"context"
	"time"

	"erupe-ce/common/clientintegrity"
	"erupe-ce/common/savejournal"

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
	release, err := m.AttachWithEnv(ctx, clientintegrity.SessionKey(tid, cid, token), disconnect,
		func(e clientintegrity.Env) { s.noteClientEnvironment(cid, e) })
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

const (
	clientEnvSeen     = 1 << 31
	clientEnvDebugger = 1 << 8
)

// noteClientEnvironment records what the client reported about its own PC
// with an integrity proof: known memory editors and a debugger. The first
// report of a session is recorded even when clean, so a clean session differs
// from one whose client never reported; after that only changes are. Each
// goes to the server log and into the next save journal record.
//
// This is evidence for later review. Nothing is blocked or disconnected here.
func (s *Session) noteClientEnvironment(cid uint32, e clientintegrity.Env) {
	if e.Looks == 0 {
		return // the client did not look, so it saw nothing either way
	}
	state := uint32(clientEnvSeen) | uint32(e.Tools)
	if e.Debugger {
		state |= clientEnvDebugger
	}
	if s.clientEnv.Swap(state) == state {
		return
	}
	fields := []zap.Field{zap.Uint32("charID", cid), zap.String("tools", e.ToolsString()),
		zap.Bool("debugger", e.Debugger), zap.Uint8("looks", e.Looks), zap.Duration("span", e.Span)}
	if e.Tools != 0 || e.Debugger {
		s.logger.Warn("Client reported a memory tool or debugger", fields...)
	} else {
		s.logger.Info("Client environment report", fields...)
	}
	s.noteSaveActivity(savejournal.Event{Kind: "env", Tools: e.ToolsString(), Debugger: e.Debugger,
		Span: int64(e.Span / time.Second)})
}
