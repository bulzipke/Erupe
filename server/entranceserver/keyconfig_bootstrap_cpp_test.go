package entranceserver

import (
	"context"
	"errors"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

type bootstrapMock struct {
	*keyconfigMock
	mode     string
	attempts atomic.Int32
	winner   []byte
}

func (m *bootstrapMock) LoadKeyconfig(ctx context.Context, uid, slot uint32) (uint32, []byte, error) {
	if m.mode == "review-alt-offline" && slot == 2 {
		return 0, nil, errors.New("INI temporarily unavailable")
	}
	return m.keyconfigMock.LoadKeyconfig(ctx, uid, slot)
}

func (m *bootstrapMock) SaveKeyconfig(ctx context.Context, uid, slot, rev uint32, data []byte) (uint32, bool, error) {
	m.attempts.Add(1)
	if m.mode == "review-queue" {
		time.Sleep(500 * time.Millisecond)
	}
	if m.mode == "reject" || (m.mode == "reject-edit" && rev > 0) {
		return 0, false, errors.New("bootstrap storage unavailable")
	}
	if m.mode == "race" || (m.mode == "edit-race" && rev > 0) {
		// A second PC inserts after the first PC's authenticated absent reply.
		if _, ok, err := m.keyconfigMock.SaveKeyconfig(ctx, uid, slot, rev, m.winner); err != nil || !ok {
			return 0, false, errors.New("winner insertion failed")
		}
		return 0, false, nil
	}
	return m.keyconfigMock.SaveKeyconfig(ctx, uid, slot, rev, data)
}

func TestKeyconfigExistingProfileBootstrap(t *testing.T) {
	path := os.Getenv("KEYCONFIG_SYNC_CLIENT_TEST")
	if path == "" {
		t.Skip("compiled Windows client fixture not supplied")
	}
	winner, err := os.ReadFile("../../common/keyconfigsync/testdata/keyconfig.cfg")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"seed", "seed-alt", "missing", "invalid", "oversize", "reject", "race",
		"local-edit", "remote-edit", "both-edit", "cross-account", "new-character", "edit-race", "local-ini", "no-state", "reject-edit", "process-edit", "bad-edit",
		"review-alt-ini", "review-known-owner", "review-invalid-ini", "review-finish", "review-start-drain", "review-queue", "review-alt-offline"} {
		t.Run(mode, func(t *testing.T) {
			m := &bootstrapMock{keyconfigMock: &keyconfigMock{rows: make(map[[2]uint32][]byte), revs: make(map[[2]uint32]uint32)}, mode: mode, winner: winner}
			s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
			s.sessionRepo = m
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					go s.handleEntranceServerConnection(conn)
				}
			}()
			out, err := exec.Command(path, listener.Addr().String(), mode).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			t.Log(string(out))
			var saves, attempts int
			switch mode {
			case "seed", "seed-alt":
				saves, attempts = 3, 3
			case "race":
				saves, attempts = 1, 1
			case "reject":
				attempts = 1
			case "local-edit", "remote-edit", "both-edit", "new-character", "no-state", "edit-race", "process-edit":
				saves, attempts = 2, 2
			case "cross-account", "local-ini":
				saves, attempts = 3, 3
			case "reject-edit":
				saves, attempts = 1, 2
			case "bad-edit":
				saves, attempts = 1, 1
			case "review-alt-ini":
				saves, attempts = 5, 5
			case "review-known-owner", "review-invalid-ini":
				saves, attempts = 1, 1
			case "review-finish", "review-start-drain":
				saves, attempts = 3, 3
			case "review-queue":
				saves, attempts = 6, 6
			case "review-alt-offline":
				saves, attempts = 2, 2
			}
			m.mu.Lock()
			got := m.saves
			m.mu.Unlock()
			if got != saves || int(m.attempts.Load()) != attempts {
				t.Fatalf("unexpected writes: saved %d want %d, attempted %d want %d", got, saves, m.attempts.Load(), attempts)
			}
		})
	}
}
