package channelserver

import (
	"testing"

	cfg "erupe-ce/config"
)

func TestExplicitCollabEventOverridesLegacyFlags(t *testing.T) {
	s := &Session{server: &Server{
		collabEvent: collabHiganjima,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent:     true,
			EnableHiganjimaEvent: true,
			EnableNierEvent:      true,
		}},
	}}

	if s.allowsCollabQuest(collabKaiji) {
		t.Error("Kaiji quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest(collabHiganjima) {
		t.Error("Higanjima quest should be visible in its explicit world")
	}
	if s.allowsCollabQuest(collabNier) {
		t.Error("NieR quest should be hidden by explicit Higanjima world")
	}
	if !s.allowsCollabQuest("") {
		t.Error("Unscoped event quest should remain visible")
	}
}

func TestNoneCollabEventHidesScopedQuestsAndTuneValues(t *testing.T) {
	s := &Session{server: &Server{
		collabEvent: collabNone,
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent:     true,
			EnableHiganjimaEvent: true,
			EnableNierEvent:      true,
		}},
	}}

	if s.allowsCollabQuest(collabKaiji) || s.allowsCollabQuest(collabHiganjima) || s.allowsCollabQuest(collabNier) {
		t.Error("none world should hide every scoped collaboration quest")
	}
	if got := s.appendCollabTuneValues(nil); len(got) != 0 {
		t.Errorf("none world produced %d collaboration tune values, want 0", len(got))
	}
}

func TestLegacyCollabFlagsRemainSupported(t *testing.T) {
	s := &Session{server: &Server{
		erupeConfig: &cfg.Config{GameplayOptions: cfg.GameplayOptions{
			EnableKaijiEvent: true,
			EnableNierEvent:  true,
		}},
	}}

	if !s.allowsCollabQuest(collabKaiji) || s.allowsCollabQuest(collabHiganjima) || !s.allowsCollabQuest(collabNier) {
		t.Error("legacy flags should determine visibility when no per-world mode is configured")
	}
	got := s.appendCollabTuneValues(nil)
	if len(got) != 2 || got[0].ID != 1106 || got[1].ID != 1153 {
		t.Errorf("legacy tune values = %#v, want Kaiji and NieR", got)
	}
}

func TestRandomCollabRotationKeepsEventUntilLastSessionLeaves(t *testing.T) {
	choices := []string{collabKaiji, collabNier}
	next := 0
	rotation := newCollabRotation(func() string {
		choice := choices[next]
		next++
		return choice
	})

	first, started := rotation.acquire()
	if first != collabKaiji || !started {
		t.Fatalf("first acquire = (%q, %t), want (%q, true)", first, started, collabKaiji)
	}
	second, started := rotation.acquire()
	if second != collabKaiji || started {
		t.Fatalf("second acquire = (%q, %t), want (%q, false)", second, started, collabKaiji)
	}

	if _, ended := rotation.release(); ended {
		t.Fatal("rotation ended while one session was still active")
	}
	if event, sessions := rotation.snapshot(); event != collabKaiji || sessions != 1 {
		t.Fatalf("rotation after first release = (%q, %d), want (%q, 1)", event, sessions, collabKaiji)
	}
	if event, ended := rotation.release(); event != collabKaiji || !ended {
		t.Fatalf("last release = (%q, %t), want (%q, true)", event, ended, collabKaiji)
	}
	if event, sessions := rotation.snapshot(); event != "" || sessions != 0 {
		t.Fatalf("empty rotation = (%q, %d), want (empty, 0)", event, sessions)
	}

	nextEvent, started := rotation.acquire()
	if nextEvent != collabNier || !started {
		t.Fatalf("next 0 -> 1 acquire = (%q, %t), want (%q, true)", nextEvent, started, collabNier)
	}
}

func TestRandomCollabRotationIsSharedAcrossWorldChannels(t *testing.T) {
	rotation := newCollabRotation(func() string { return collabHiganjima })
	first := &Session{server: &Server{collabEvent: collabRandom, collabRotation: rotation}}
	second := &Session{server: &Server{collabEvent: collabRandom, collabRotation: rotation}}

	first.acquireCollabEvent()
	second.acquireCollabEvent()
	if first.effectiveCollabEvent() != collabHiganjima || second.effectiveCollabEvent() != collabHiganjima {
		t.Fatalf("sessions received different events: first=%q second=%q", first.effectiveCollabEvent(), second.effectiveCollabEvent())
	}
	if !first.allowsCollabQuest(collabHiganjima) || first.allowsCollabQuest(collabKaiji) {
		t.Fatal("random session quest filtering did not match the selected event")
	}

	first.releaseCollabEvent()
	if event, sessions := rotation.snapshot(); event != collabHiganjima || sessions != 1 {
		t.Fatalf("shared rotation ended too early: event=%q sessions=%d", event, sessions)
	}
	second.releaseCollabEvent()
	if event, sessions := rotation.snapshot(); event != "" || sessions != 0 {
		t.Fatalf("shared rotation did not reset: event=%q sessions=%d", event, sessions)
	}
}
