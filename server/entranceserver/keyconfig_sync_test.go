package entranceserver

import (
	"context"
	"database/sql"
	"encoding/binary"
	"erupe-ce/common/keyconfigsync"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type keyconfigMock struct {
	mu    sync.Mutex
	rows  map[[2]uint32][]byte
	revs  map[[2]uint32]uint32
	saves int
}

func (*keyconfigMock) GetServerIDForCharacter(uint32) (uint16, error) { return 0, nil }
func (*keyconfigMock) KeyconfigSession(_ context.Context, tid, cid uint32, _ time.Time, _ time.Duration) (keyconfigSession, error) {
	if tid == 123 && (cid == 456 || cid == 457) {
		return keyconfigSession{10, "0123456789abcdef"}, nil
	}
	if tid == 124 && cid == 458 {
		return keyconfigSession{11, "0123456789abcdef"}, nil
	}
	return keyconfigSession{}, sql.ErrNoRows
}
func (m *keyconfigMock) LoadKeyconfig(_ context.Context, uid, slot uint32) (uint32, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := [2]uint32{uid, slot}
	if m.revs[key] == 0 {
		return 0, nil, sql.ErrNoRows
	}
	return m.revs[key], append([]byte(nil), m.rows[key]...), nil
}
func (m *keyconfigMock) SaveKeyconfig(_ context.Context, uid, slot, rev uint32, data []byte) (uint32, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := [2]uint32{uid, slot}
	if m.revs[key] != rev {
		return 0, false, nil
	}
	m.revs[key]++
	m.rows[key] = append([]byte(nil), data...)
	m.saves++
	return m.revs[key], true, nil
}
func keyconfigRequest(tid, cid, op, slot, rev uint32, data []byte) []byte {
	b := make([]byte, 112+len(data))
	copy(b, keyconfigsync.RequestMagic)
	for i, v := range []uint32{tid, cid, op, slot, rev, uint32(len(data))} {
		binary.BigEndian.PutUint32(b[8+4*i:], v)
	}
	for i := 32; i < 48; i++ {
		b[i] = byte(i)
	}
	copy(b[48:], keyconfigsync.Sign(b[:48], "0123456789abcdef"))
	copy(b[80:], data)
	copy(b[len(b)-32:], keyconfigsync.Sign(b[:len(b)-32], "0123456789abcdef"))
	return b
}
func keyconfigExchange(t *testing.T, s *Server, b []byte) ([]byte, error) {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { defer close(done); s.handleEntranceServerConnection(server) }()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	// Write concurrently: an invalid header may be rejected before its body is read.
	go func() { _, _ = client.Write(b) }()
	h := make([]byte, 40)
	_, err := io.ReadFull(client, h)
	if err == nil {
		n := binary.BigEndian.Uint32(h[16:])
		if n > keyconfigsync.MaxBytes {
			t.Fatal("oversized response")
		}
		tail := make([]byte, 32+int(n))
		_, err = io.ReadFull(client, tail)
		h = append(h, tail...)
	}
	_ = client.Close()
	<-done
	return h, err
}
func TestKeyconfigAccountExchange(t *testing.T) {
	m := &keyconfigMock{rows: make(map[[2]uint32][]byte), revs: make(map[[2]uint32]uint32)}
	s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
	s.sessionRepo = m
	data, err := os.ReadFile("../../common/keyconfigsync/testdata/keyconfig.cfg")
	if err != nil {
		t.Fatal(err)
	}
	check := func(b []byte, want, rev uint32, payload []byte) {
		t.Helper()
		r, err := keyconfigExchange(t, s, b)
		if err != nil {
			t.Fatal(err)
		}
		if string(r[:8]) != keyconfigsync.ResponseMagic || binary.BigEndian.Uint32(r[8:]) != want || binary.BigEndian.Uint32(r[12:]) != rev || string(r[24:40]) != string(b[32:48]) || string(r[40:len(r)-32]) != string(payload) {
			t.Fatal("wrong response", r)
		}
		if string(r[len(r)-32:]) != string(keyconfigsync.Sign(r[:len(r)-32], "0123456789abcdef")) {
			t.Fatal("response MAC")
		}
	}
	check(keyconfigRequest(123, 456, 0, 0, 0, nil), keyconfigsync.Absent, 0, nil)
	first := keyconfigRequest(123, 456, 1, 0, 0, data)
	check(first, keyconfigsync.OK, 1, nil)
	check(first, keyconfigsync.Conflict, 0, nil)                                  // Replay cannot overwrite.
	check(keyconfigRequest(123, 457, 0, 0, 0, nil), keyconfigsync.OK, 1, data)    // Same account, other character.
	check(keyconfigRequest(124, 458, 0, 0, 0, nil), keyconfigsync.Absent, 0, nil) // Other account.
	check(keyconfigRequest(123, 456, 0, 1, 0, nil), keyconfigsync.Absent, 0, nil) // Independent file.
	check(keyconfigRequest(123, 456, 1, 0, 1, data), keyconfigsync.OK, 2, nil)
	for _, b := range [][]byte{keyconfigRequest(123, 458, 0, 0, 0, nil), keyconfigRequest(123, 456, 1, 0, 2, []byte("truncated")), keyconfigRequest(123, 456, 0, 3, 0, nil)} {
		if _, err := keyconfigExchange(t, s, b); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	bad := keyconfigRequest(123, 456, 1, 0, 2, data)
	bad[len(bad)-1] ^= 1
	if _, err := keyconfigExchange(t, s, bad); err == nil {
		t.Fatal("bad MAC accepted")
	}
	if m.saves != 2 {
		t.Fatal("unauthorized mutation", m.saves)
	}
	ini := []byte("[VIDEO]\r\nFPS_60=1\r\n[OPTION]\r\nFOV=60\r\n")
	check(keyconfigRequest(123, 456, 1, 2, 0, ini), keyconfigsync.OK, 1, nil)
	check(keyconfigRequest(123, 457, 0, 2, 0, nil), keyconfigsync.OK, 1, ini)
	check(keyconfigRequest(124, 458, 0, 2, 0, nil), keyconfigsync.Absent, 0, nil)
}
