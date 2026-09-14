package channelserver

import (
	"math/rand/v2"
	"sort"
	"sync"

	"go.uber.org/zap"
)

const (
	collabNone       = "none"
	collabRandom     = "random"
	collabKaiji      = "kaiji"
	collabHiganjima  = "higanjima"
	collabNier       = "nier"
	collabEvangelion = "evangelion"
	collabPSO2       = "pso2"
)

var collabEvents = []string{collabKaiji, collabHiganjima, collabNier, collabEvangelion, collabPSO2}

var collabTuneValues = []struct {
	event  string
	tuneID uint16
}{
	{event: collabKaiji, tuneID: 1106},
	{event: collabHiganjima, tuneID: 1144},
	{event: collabNier, tuneID: 1153},
	// Custom client contract; not a verified original Evangelion NPC flag.
	{event: collabEvangelion, tuneID: 1156},
	// Agreed PSO2 client flag; client-side behavior is implemented separately.
	{event: collabPSO2, tuneID: 1130},
}

// builtInCollabQuests are delivered with the matching collaboration event even
// when the quest is not registered in event_quests. A database row with the
// same quest ID overrides the built-in entry, which keeps custom scheduling
// and metadata possible without producing duplicates.
var builtInCollabQuests = []struct {
	event      string
	questID    int
	maxPlayers uint8
}{
	{event: collabEvangelion, questID: 40211, maxPlayers: 4},
	{event: collabEvangelion, questID: 40212, maxPlayers: 4},
	{event: collabEvangelion, questID: 40213, maxPlayers: 4},
	{event: collabEvangelion, questID: 40214, maxPlayers: 4},
	{event: collabKaiji, questID: 40215, maxPlayers: 1},
	{event: collabHiganjima, questID: 40217, maxPlayers: 4},
	{event: collabNier, questID: 40221, maxPlayers: 4},
	{event: collabNier, questID: 40223, maxPlayers: 4},
	{event: collabNier, questID: 40224, maxPlayers: 4},
	{event: collabNier, questID: 40225, maxPlayers: 4},
	{event: collabNier, questID: 40226, maxPlayers: 4},
	{event: collabNier, questID: 40227, maxPlayers: 4},
	{event: collabPSO2, questID: 40239, maxPlayers: 4},
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
		collabKaiji:      options.EnableKaijiEvent,
		collabHiganjima:  options.EnableHiganjimaEvent,
		collabNier:       options.EnableNierEvent,
		collabEvangelion: options.EnableEvangelionEvent,
		collabPSO2:       options.EnablePSO2Event,
	}
}

// allowsCollabQuestWorld limits collaboration quests and tune flags to open
// worlds only. The world's random rotation lifecycle remains independent.
func (s *Session) allowsCollabQuestWorld() bool {
	if s.server == nil {
		return false
	}
	switch s.server.worldType {
	case 1: // Open (자유).
		return true
	default:
		return false
	}
}

// allowsCollabQuest applies both world-type and active-event restrictions.
// Known built-in IDs remain collaboration quests even if a DB override has
// an empty scope. Other unscoped quests remain ordinary event quests.
func (s *Session) allowsCollabQuest(quest EventQuest) bool {
	scope := quest.CollabScope
	if scope == "" {
		for _, builtIn := range builtInCollabQuests {
			if quest.QuestID == builtIn.questID {
				scope = builtIn.event
				break
			}
		}
	}
	if scope == "" {
		return true
	}
	return s.allowsCollabQuestWorld() && s.enabledCollabEvents()[scope]
}

// appendBuiltInCollabQuests adds collaboration quests that belong to the
// currently selected event. Synthetic list IDs follow the largest
// database ID so they remain unique and look like ordinary event quest rows
// to the client.
func (s *Session) appendBuiltInCollabQuests(quests []EventQuest) []EventQuest {
	if !s.allowsCollabQuestWorld() {
		return quests
	}
	enabled := s.enabledCollabEvents()
	seenQuestIDs := make(map[int]struct{}, len(quests))
	var nextID uint32
	for _, quest := range quests {
		seenQuestIDs[quest.QuestID] = struct{}{}
		if quest.ID > nextID {
			nextID = quest.ID
		}
	}

	for _, builtIn := range builtInCollabQuests {
		if !enabled[builtIn.event] {
			continue
		}
		if _, exists := seenQuestIDs[builtIn.questID]; exists {
			continue
		}

		nextID++
		quest := EventQuest{
			ID:          nextID,
			MaxPlayers:  builtIn.maxPlayers,
			QuestType:   18,
			QuestID:     builtIn.questID,
			Mark:        1,
			Flags:       -1,
			CollabScope: builtIn.event,
		}
		quests = append(quests, quest)
		seenQuestIDs[quest.QuestID] = struct{}{}
	}

	sort.SliceStable(quests, func(i, j int) bool {
		return quests[i].QuestID < quests[j].QuestID
	})
	return quests
}

func (s *Session) appendCollabTuneValues(values []tuneValue) []tuneValue {
	if !s.allowsCollabQuestWorld() {
		return values
	}
	enabled := s.enabledCollabEvents()
	for _, value := range collabTuneValues {
		if enabled[value.event] {
			values = append(values, tuneValue{ID: value.tuneID, Value: 1})
		}
	}
	return values
}
