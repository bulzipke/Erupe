package entranceserver

import (
	cfg "erupe-ce/config"
	"fmt"
	"go.uber.org/zap"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func testProfileServer(m *keyconfigMock) *Server {
	s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
	s.sessionRepo = m
	return s
}
func TestProfileLimits(t *testing.T) {
	var l profileLimits
	now := time.Now()
	var release []func()
	for i := 0; i < 32; i++ {
		r, ok := l.enter(fmt.Sprint(i/8), now)
		if !ok {
			t.Fatal(i)
		}
		release = append(release, r)
	}
	if _, ok := l.enter("extra", now); ok {
		t.Fatal("unbounded in-flight requests")
	}
	for _, r := range release {
		r()
	}
	for i := 0; i < 240-8; i++ {
		r, ok := l.enter("0", now)
		if !ok {
			t.Fatal(i)
		}
		r()
	}
	if _, ok := l.enter("0", now); ok {
		t.Fatal("IP request cap")
	}
	r, ok := l.enter("0", now.Add(time.Minute))
	if !ok {
		t.Fatal("window did not recover")
	}
	r()
	for i := 0; i < 24; i++ {
		if !l.allow("0", 10, 512<<10, now.Add(time.Minute)) {
			t.Fatal(i)
		}
	}
	if l.allow("0", 10, 1, now.Add(time.Minute)) {
		t.Fatal("account byte cap")
	}
	if !l.allow("0", 11, 1, now.Add(time.Minute)) {
		t.Fatal("account isolation")
	}
	for i := 0; i < 5000; i++ {
		r, ok := l.enter(fmt.Sprint("ip-", i), now)
		if ok {
			r()
		}
	}
	if len(l.ips) > 4096 {
		t.Fatal("unbounded identity map")
	}
}
func TestProfileRequestAndIPByteBudgets(t *testing.T) {
	now := time.Now()
	var l profileLimits
	r, ok := l.enter("host", now)
	if !ok {
		t.Fatal("admission")
	}
	r()
	for i := 0; i < profileAccountRequests; i++ {
		if !l.allow("host", 1, 0, now) {
			t.Fatal(i)
		}
	}
	if l.allow("host", 1, 0, now) {
		t.Fatal("account request quota bypassed")
	}
	if !l.allow("host", 1, 0, now.Add(time.Minute)) {
		t.Fatal("account window recovery")
	}
	var bytes profileLimits
	r, ok = bytes.enter("shared", now)
	if !ok {
		t.Fatal("admission")
	}
	r()
	for i := 0; i < 128; i++ {
		if !bytes.allow("shared", uint32(i+1), 512<<10, now) {
			t.Fatal(i)
		}
	}
	if bytes.allow("shared", 1000, 1, now) {
		t.Fatal("IP byte quota bypassed with multiple accounts")
	}
}

type countedProfileConn struct {
	net.Conn
	read atomic.Int32
}

func (c *countedProfileConn) Read(b []byte) (int, error) {
	n, e := c.Conn.Read(b)
	c.read.Add(int32(n))
	return n, e
}
func TestProfileRejectsBeforePayload(t *testing.T) {
	for _, kind := range []string{"bad-header-mac", "oversize", "old-protocol"} {
		t.Run(kind, func(t *testing.T) {
			m := &keyconfigMock{rows: make(map[[2]uint32][]byte), revs: make(map[[2]uint32]uint32)}
			s := testProfileServer(m)
			a, b := net.Pipe()
			defer b.Close()
			c := &countedProfileConn{Conn: a}
			done := make(chan struct{})
			go func() { defer close(done); s.handleEntranceServerConnection(c) }()
			frame := keyconfigRequest(123, 456, 1, 0, 0, make([]byte, 512<<10))
			frame = frame[:80]
			switch kind {
			case "bad-header-mac":
				frame[79] ^= 1
			case "oversize":
				frame[28] = 0x7f
			case "old-protocol":
				frame = []byte("ERUKCF01")
			}
			_ = b.SetDeadline(time.Now().Add(time.Second))
			_, _ = b.Write(frame)
			var tmp [1]byte
			_, _ = b.Read(tmp[:])
			_ = b.Close()
			<-done
			if c.read.Load() > 80 || m.saves != 0 {
				t.Fatal("read unauthenticated/oversize payload", c.read.Load())
			}
		})
	}
}
