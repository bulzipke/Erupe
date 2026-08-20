package channelserver

import (
	"testing"
	"time"
)

func addAutoSupportRaviSemaphore(s *Server) {
	s.semaphore = map[string]*Semaphore{
		"hs_l0u3": {name: "hs_l0u3", clients: make(map[*Session]uint32)},
	}
}

func TestRaviSupportInterval(t *testing.T) {
	for _, test := range []struct {
		seconds int
		want    time.Duration
	}{
		{seconds: -1, want: 0},
		{seconds: 0, want: 0},
		{seconds: 30, want: 30 * time.Second},
	} {
		if got := raviSupportInterval(test.seconds); got != test.want {
			t.Errorf("raviSupportInterval(%d) = %s, want %s", test.seconds, got, test.want)
		}
	}
}

func TestExecuteRaviResurrectionSupport(t *testing.T) {
	s := createMockServer()
	s.raviente.state[28] = 1

	if s.executeRaviResurrectionSupport() {
		t.Fatal("resurrection support executed without a Raviente room")
	}
	if s.raviente.state[28] != 1 {
		t.Fatalf("resurrection request changed without a room: %d", s.raviente.state[28])
	}

	addAutoSupportRaviSemaphore(s)
	if !s.executeRaviResurrectionSupport() {
		t.Fatal("pending resurrection support was not executed")
	}
	if s.raviente.state[28] != 0 {
		t.Fatalf("resurrection request = %d, want 0", s.raviente.state[28])
	}
	if s.executeRaviResurrectionSupport() {
		t.Fatal("resurrection support executed without a pending request")
	}
}

func TestExecuteRaviSedationSupport(t *testing.T) {
	s := createMockServer()
	s.raviente.state[0] = 10
	s.raviente.state[1] = 20
	s.raviente.state[2] = 30
	s.raviente.state[3] = 40
	s.raviente.state[4] = 50
	s.raviente.support[1] = 999

	if s.executeRaviSedationSupport() {
		t.Fatal("sedation support executed without a Raviente room")
	}
	if s.raviente.support[1] != 999 {
		t.Fatalf("sedation support changed without a room: %d", s.raviente.support[1])
	}

	addAutoSupportRaviSemaphore(s)
	if !s.executeRaviSedationSupport() {
		t.Fatal("sedation support did not update the fulfilled marker")
	}
	if s.raviente.support[1] != 150 {
		t.Fatalf("sedation support = %d, want 150", s.raviente.support[1])
	}
	if s.executeRaviSedationSupport() {
		t.Fatal("unchanged sedation support reported another update")
	}

	s.requestRaviSedationSupport()
	if s.raviente.support[1] != 151 {
		t.Fatalf("sedation request = %d, want 151", s.raviente.support[1])
	}
	if !s.executeRaviSedationSupport() || s.raviente.support[1] != 150 {
		t.Fatalf("requested sedation was not fulfilled: %d", s.raviente.support[1])
	}
}
