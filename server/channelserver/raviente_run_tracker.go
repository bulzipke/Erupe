package channelserver

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

func (s *Server) ravienteRunChannelKey() string {
	if s.Port != 0 {
		return fmt.Sprintf("port:%d", s.Port)
	}
	if s.GlobalID != "" {
		return "global:" + s.GlobalID
	}
	return fmt.Sprintf("server:%d", s.ID)
}

func (s *Server) recordRavienteRunStart(generation uint16) {
	if s.ravienteRunTracker != nil {
		s.ravienteRunTracker.Start(generation, time.Now())
	}
}

func (s *Server) recordRavienteRunCompletion(generation uint16) {
	if s.ravienteRunTracker != nil {
		s.ravienteRunTracker.Complete(generation, time.Now())
	}
}

func (s *Server) recordRavienteRunAbort(generation uint16, reason string) {
	if s.ravienteRunTracker != nil {
		s.ravienteRunTracker.Abort(generation, time.Now(), reason)
	}
}

func (s *Server) recordRavienteRunTeardown(generation uint16, completed bool) {
	if completed {
		s.recordRavienteRunCompletion(generation)
		return
	}
	s.recordRavienteRunAbort(generation, "semaphore_teardown")
}

func (s *Server) recordRavienteQuestParticipant(questID uint16, session *Session) {
	if s.ravienteRunTracker == nil || session == nil {
		return
	}
	s.ravienteRunTracker.ObserveQuestParticipant(questID, session.charID, session.Name, time.Now())
}

type ravienteQuestKindCacheEntry struct {
	kind RavienteRunKind
	ok   bool
}

type trackedRavienteRun struct {
	id           int64
	generation   uint16
	kind         RavienteRunKind
	startedAt    time.Time
	participants map[uint32]RavienteRunParticipant
	closed       bool
}

// RavienteRunTracker serializes the small number of lifecycle transitions for
// one channel.  It deliberately owns no Raviente/semaphore locks, so repository
// calls can never hold the gameplay-state locks while waiting for PostgreSQL.
type RavienteRunTracker struct {
	mu                     sync.Mutex
	repo                   RavienteRunRepo
	logger                 *zap.Logger
	channelKey             string
	active                 *trackedRavienteRun
	questKinds             map[uint16]ravienteQuestKindCacheEntry
	terminalGenerations    map[uint16]struct{}
	terminalGenerationFIFO []uint16
}

func NewRavienteRunTracker(repo RavienteRunRepo, logger *zap.Logger) *RavienteRunTracker {
	return &RavienteRunTracker{
		repo:                repo,
		logger:              logger,
		questKinds:          make(map[uint16]ravienteQuestKindCacheEntry),
		terminalGenerations: make(map[uint16]struct{}),
	}
}

// Initialize closes a run left active by a previous process.  Raviente's
// register arrays are memory-only, so a process restart cannot safely resume or
// call such a run completed.
func (t *RavienteRunTracker) Initialize(channelKey string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channelKey = channelKey
	t.active = nil
	t.terminalGenerations = make(map[uint16]struct{})
	t.terminalGenerationFIFO = nil
	return t.repo.AbortActive(channelKey, now, "server_restart")
}

func (t *RavienteRunTracker) Start(generation uint16, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, terminal := t.terminalGenerations[generation]; terminal {
		return
	}
	if t.active != nil {
		if t.active.generation == generation && !t.active.closed {
			return
		}
		oldGeneration := t.active.generation
		if err := t.repo.Abort(t.active.id, now, "superseded"); err != nil {
			t.logError("Failed to abort superseded Raviente run", err,
				zap.Int64("runID", t.active.id))
			t.active.closed = true
			return
		}
		t.markTerminalGeneration(oldGeneration)
		t.active = nil
		if oldGeneration == generation {
			// This was a retry for a terminal/closed generation, not a new
			// lifecycle.  Never recreate the just-tombstoned run.
			return
		}
	}

	id, err := t.repo.Start(t.channelKey, generation, now)
	if err != nil {
		t.logError("Failed to start Raviente run record", err,
			zap.Uint16("generation", generation))
		return
	}
	t.active = &trackedRavienteRun{
		id:           id,
		generation:   generation,
		kind:         RavienteRunKindUnknown,
		startedAt:    now,
		participants: make(map[uint32]RavienteRunParticipant),
	}
}

// ObserveQuestParticipant records only a hunter who has actually entered a
// quest whose event_quests.quest_type is 40, 50, or 51 while this run is active.
func (t *RavienteRunTracker) ObserveQuestParticipant(questID uint16, charID uint32, name string, now time.Time) {
	if charID == 0 || strings.TrimSpace(name) == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil || t.active.closed {
		return
	}

	entry, cached := t.questKinds[questID]
	if !cached {
		kind, ok, err := t.repo.ResolveQuestKind(questID)
		if err != nil {
			t.logError("Failed to resolve Raviente quest type", err,
				zap.Uint16("questID", questID))
			return
		}
		entry = ravienteQuestKindCacheEntry{kind: kind, ok: ok}
		t.questKinds[questID] = entry
	}
	if !entry.ok {
		return
	}
	if t.active.kind != RavienteRunKindUnknown && t.active.kind != entry.kind {
		if t.logger != nil {
			t.logger.Warn("Ignored conflicting Raviente quest type during active run",
				zap.Int64("runID", t.active.id),
				zap.String("runKind", string(t.active.kind)),
				zap.String("questKind", string(entry.kind)),
				zap.Uint16("questID", questID))
		}
		return
	}
	if _, exists := t.active.participants[charID]; exists {
		return
	}

	participant := RavienteRunParticipant{
		CharacterID:   charID,
		CharacterName: name,
		FirstSeenAt:   now,
	}
	t.active.kind = entry.kind
	t.active.participants[charID] = participant
	if err := t.repo.AddParticipant(t.active.id, entry.kind, participant); err != nil {
		// Keep the in-memory snapshot.  Complete re-upserts every participant in
		// the same transaction as the terminal state transition.
		t.logError("Failed to persist Raviente participant", err,
			zap.Int64("runID", t.active.id), zap.Uint32("charID", charID))
	}
}

func (t *RavienteRunTracker) Complete(generation uint16, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.markTerminalGeneration(generation)
		return
	}
	if t.active.generation != generation {
		t.markTerminalGeneration(generation)
		return
	}
	if t.active.closed {
		return
	}
	if t.active.kind == RavienteRunKindUnknown || len(t.active.participants) == 0 {
		if err := t.repo.Abort(t.active.id, now, "completed_without_participants"); err != nil {
			t.logError("Failed to abort empty Raviente completion", err,
				zap.Int64("runID", t.active.id))
			t.active.closed = true
			return
		}
		t.markTerminalGeneration(generation)
		t.active = nil
		return
	}

	participants := make([]RavienteRunParticipant, 0, len(t.active.participants))
	for _, participant := range t.active.participants {
		participants = append(participants, participant)
	}
	sort.Slice(participants, func(i, j int) bool {
		return participants[i].CharacterID < participants[j].CharacterID
	})
	duration := now.Sub(t.active.startedAt)
	if duration <= 0 {
		duration = time.Millisecond
	}
	if err := t.repo.Complete(t.active.id, t.active.kind, now, duration, participants); err != nil {
		t.logError("Failed to complete Raviente run record", err,
			zap.Int64("runID", t.active.id))
		return
	}
	t.markTerminalGeneration(generation)
	t.active = nil
}

func (t *RavienteRunTracker) Abort(generation uint16, now time.Time, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.markTerminalGeneration(generation)
		return
	}
	if t.active.generation != generation {
		t.markTerminalGeneration(generation)
		return
	}
	if err := t.repo.Abort(t.active.id, now, reason); err != nil {
		t.logError("Failed to abort Raviente run record", err,
			zap.Int64("runID", t.active.id), zap.String("reason", reason))
		t.active.closed = true
		return
	}
	t.markTerminalGeneration(generation)
	t.active = nil
}

const ravienteTerminalGenerationHistory = 64

func (t *RavienteRunTracker) markTerminalGeneration(generation uint16) {
	if _, exists := t.terminalGenerations[generation]; exists {
		return
	}
	t.terminalGenerations[generation] = struct{}{}
	t.terminalGenerationFIFO = append(t.terminalGenerationFIFO, generation)
	if len(t.terminalGenerationFIFO) <= ravienteTerminalGenerationHistory {
		return
	}
	oldest := t.terminalGenerationFIFO[0]
	t.terminalGenerationFIFO = t.terminalGenerationFIFO[1:]
	delete(t.terminalGenerations, oldest)
}

func (t *RavienteRunTracker) logError(message string, err error, fields ...zap.Field) {
	if t.logger == nil {
		return
	}
	fields = append([]zap.Field{zap.Error(err)}, fields...)
	t.logger.Error(message, fields...)
}
