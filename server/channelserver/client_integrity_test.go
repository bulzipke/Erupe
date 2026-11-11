package channelserver

import (
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"net"
	"testing"
	"time"
)

func TestClientIntegrityMissingRuntimeClosesActualChannel(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	s := &Session{server: &Server{erupeConfig: &cfg.Config{ClientIntegrity: cfg.ClientIntegrity{Enabled: true}}}, rawConn: a, done: make(chan struct{}), logger: zap.NewNop()}
	if s.attachClientIntegrity(123, 456, "0123456789abcdef") {
		t.Fatal("missing verifier accepted")
	}
	if !s.closed.Load() {
		t.Fatal("session not closed")
	}
	b.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := b.Read(make([]byte, 1)); e == nil {
		t.Fatal("channel socket still open")
	}
}
func TestClientIntegritySessionCleanupIsIdempotent(t *testing.T) {
	calls := 0
	s := &Session{done: make(chan struct{}), integrityRelease: func() { calls++ }}
	s.markClosed()
	s.markClosed()
	if calls != 1 {
		t.Fatalf("released %d times", calls)
	}
}
