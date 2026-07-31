package channelserver

import (
	"math/rand/v2"
	"sync"

	"go.uber.org/zap"
)

const (
	collabNone      = "none"
	collabRandom    = "random"
	collabKaiji     = "kaiji"
	collabHiganjima = "higanjima"
	collabNier      = "nier"
)

var collabEvents = []string{collabKaiji, collabHiganjima, collabNier}

var collabTuneValues = []struct {
	event  string
	tuneID uint16
}{
	{event: collabKaiji, tuneID: 1106},
	{event: collabHiganjima, tuneID: 1144},
	{event: collabNier, tuneID: 1153},
}

// CollabRotation keeps one randomly selected collaboration active while at
// least one authenticated player is connected to any channel in the world.
// The next 0 -> 1 transition selects a new event.
type CollabRotation struct {
	mu             sync.Mutex
	activeEvent    string
	activeSessions int
	choose         func() string
}

// NewCollabRotation creates a world-scoped random collaboration rotation.
func NewCollabRotation() *CollabRotation {
	return newCollabRotation(func() string {
		return collabEvents[rand.IntN(len(collabEvents))]
	})
}

func newCollabRotation(choose func() string) *CollabRotation {
	return &CollabRotation{choose: choose}
}

func (r *CollabRotation) acquire() (event string, started bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeSessions == 0 {
		r.activeEvent = r.choose()
		started = true
	}
	r.activeSessions++
	return r.activeEvent, started
}

func (r *CollabRotation) release() (event string, ended bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeSessions == 0 {
		return "", false
	}
	event = r.activeEvent
	r.activeSessions--
	if r.activeSessions == 0 {
		r.activeEvent = ""
		ended = true
	}
	return event, ended
}

func (r *CollabRotation) snapshot() (event string, sessions int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeEvent, r.activeSessions
}

func (s *Session) acquireCollabEvent() {
	if s.server == nil || s.server.collabEvent != collabRandom || s.server.collabRotation == nil {
		return
	}

	s.Lock()
	if s.collabRotationAcquired {
		s.Unlock()
		return
	}
	event, started := s.server.collabRotation.acquire()
	s.collabEvent = event
	s.collabRotationAcquired = true
	s.Unlock()

	if started && s.logger != nil {
		s.logger.Info("Random collaboration rotation started", zap.String("collabEvent", event))
	}
}

func (s *Session) releaseCollabEvent() {
	if s.server == nil || s.server.collabRotation == nil {
		return
	}

	s.Lock()
	if !s.collabRotationAcquired {
		s.Unlock()
		return
	}
	s.collabRotationAcquired = false
	s.collabEvent = ""
	s.Unlock()

	event, ended := s.server.collabRotation.release()
	if ended && s.logger != nil {
		s.logger.Info("Random collaboration rotation ended", zap.String("collabEvent", event))
	}
}

func (s *Session) effectiveCollabEvent() string {
	if s.server == nil {
		return collabNone
	}
	if s.server.collabEvent != collabRandom {
		return s.server.collabEvent
	}

	s.Lock()
	event := s.collabEvent
	s.Unlock()
	if event == "" {
		return collabNone
	}
	return event
}

// enabledCollabEvents returns the collaboration content enabled for this
// player. An explicit or random world value takes precedence over legacy
// global flags so incompatible Rasta Bar NPC layouts are never enabled
// together.
func (s *Session) enabledCollabEvents() map[string]bool {
	event := s.effectiveCollabEvent()
	if event != "" {
		if event == collabNone {
			return map[string]bool{}
		}
		return map[string]bool{event: true}
	}

	options := s.server.erupeConfig.GameplayOptions
	return map[string]bool{
		collabKaiji:     options.EnableKaijiEvent,
		collabHiganjima: options.EnableHiganjimaEvent,
		collabNier:      options.EnableNierEvent,
	}
}

// allowsCollabQuest reports whether an event quest is visible to this player.
// Empty scopes are ordinary event quests and remain available everywhere.
func (s *Session) allowsCollabQuest(scope string) bool {
	if scope == "" {
		return true
	}
	return s.enabledCollabEvents()[scope]
}

func (s *Session) appendCollabTuneValues(values []tuneValue) []tuneValue {
	enabled := s.enabledCollabEvents()
	for _, value := range collabTuneValues {
		if enabled[value.event] {
			values = append(values, tuneValue{ID: value.tuneID, Value: 1})
		}
	}
	return values
}
