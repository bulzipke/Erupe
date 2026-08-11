package channelserver

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"

	"go.uber.org/zap"
)

type mockRavienteCompletion struct {
	runID        int64
	kind         RavienteRunKind
	duration     time.Duration
	participants []RavienteRunParticipant
}

type mockRavienteRunRepo struct {
	nextID          int64
	questKinds      map[uint16]RavienteRunKind
	starts          []uint16
	participants    []RavienteRunParticipant
	completions     []mockRavienteCompletion
	aborts          []int64
	abortActive     int
	abortErr        error
	completeStarted chan struct{}
	completeRelease chan struct{}
}

func (m *mockRavienteRunRepo) AbortActive(_ string, _ time.Time, _ string) error {
	m.abortActive++
	return nil
}

func (m *mockRavienteRunRepo) Start(_ string, generation uint16, _ time.Time) (int64, error) {
	m.nextID++
	m.starts = append(m.starts, generation)
	return m.nextID, nil
}

func (m *mockRavienteRunRepo) AddParticipant(_ int64, _ RavienteRunKind, participant RavienteRunParticipant) error {
	m.participants = append(m.participants, participant)
	return nil
}

func (m *mockRavienteRunRepo) Complete(runID int64, kind RavienteRunKind, _ time.Time, duration time.Duration, participants []RavienteRunParticipant) error {
	if m.completeStarted != nil {
		select {
		case <-m.completeStarted:
		default:
			close(m.completeStarted)
		}
	}
	if m.completeRelease != nil {
		<-m.completeRelease
	}
	m.completions = append(m.completions, mockRavienteCompletion{
		runID:        runID,
		kind:         kind,
		duration:     duration,
		participants: append([]RavienteRunParticipant(nil), participants...),
	})
	return nil
}

func (m *mockRavienteRunRepo) Abort(runID int64, _ time.Time, _ string) error {
	if m.abortErr != nil {
		return m.abortErr
	}
	m.aborts = append(m.aborts, runID)
	return nil
}

func (m *mockRavienteRunRepo) ResolveQuestKind(questID uint16) (RavienteRunKind, bool, error) {
	kind, ok := m.questKinds[questID]
	return kind, ok, nil
}

func newTestRavienteTracker(repo *mockRavienteRunRepo) *RavienteRunTracker {
	tracker := NewRavienteRunTracker(repo, nil)
	_ = tracker.Initialize("test-channel", time.Unix(1, 0))
	return tracker
}

func TestServerStartBindFailureDoesNotAbortLiveRavienteRun(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("reserve listener: %v", err)
	}
	defer listener.Close()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	repo := &mockRavienteRunRepo{questKinds: make(map[uint16]RavienteRunKind)}
	server := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
	server.Port = port
	server.ravienteRunTracker = NewRavienteRunTracker(repo, nil)

	if err := server.Start(); err == nil {
		server.Shutdown()
		t.Fatal("Start succeeded despite reserved channel port")
	}
	if repo.abortActive != 0 {
		t.Fatalf("failed duplicate start aborted %d active runs, want 0", repo.abortActive)
	}
}

func TestRavienteRunTrackerKindsDedupeAndNormalIgnore(t *testing.T) {
	tests := []struct {
		name    string
		questID uint16
		kind    RavienteRunKind
	}{
		{name: "berserk", questID: 54751, kind: RavienteRunKindBerserk},
		{name: "extreme", questID: 55596, kind: RavienteRunKindExtreme},
		{name: "small", questID: 55796, kind: RavienteRunKindSmall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockRavienteRunRepo{questKinds: map[uint16]RavienteRunKind{
				tt.questID: tt.kind,
			}}
			tracker := newTestRavienteTracker(repo)
			started := time.Unix(100, 0)
			tracker.Start(7, started)
			tracker.ObserveQuestParticipant(tt.questID, 10, "Alice", started.Add(time.Second))
			tracker.ObserveQuestParticipant(tt.questID, 10, "Renamed Alice", started.Add(2*time.Second))
			tracker.ObserveQuestParticipant(12345, 11, "Ignored", started.Add(3*time.Second))
			tracker.Complete(7, started.Add(time.Minute))

			if len(repo.participants) != 1 || repo.participants[0].CharacterName != "Alice" {
				t.Fatalf("participant snapshots = %+v, want one first-name snapshot", repo.participants)
			}
			if len(repo.completions) != 1 || repo.completions[0].kind != tt.kind || len(repo.completions[0].participants) != 1 {
				t.Fatalf("completions = %+v", repo.completions)
			}
		})
	}
}

func TestRavienteRunTrackerAbortRequiresExpectedGeneration(t *testing.T) {
	repo := &mockRavienteRunRepo{questKinds: map[uint16]RavienteRunKind{54751: RavienteRunKindBerserk}}
	tracker := newTestRavienteTracker(repo)
	started := time.Unix(100, 0)
	tracker.Start(10, started)

	// A delayed teardown from generation 9 must not abort the newly-started 10.
	tracker.Abort(9, started.Add(time.Second), "semaphore_teardown")
	if len(repo.aborts) != 0 {
		t.Fatalf("stale generation aborted runs %v", repo.aborts)
	}
	tracker.ObserveQuestParticipant(54751, 1, "Alice", started.Add(2*time.Second))
	tracker.Complete(10, started.Add(time.Minute))
	if len(repo.completions) != 1 {
		t.Fatalf("new generation completion count = %d, want 1", len(repo.completions))
	}
}

func TestRavienteRunTrackerTerminalBeforeDelayedStartCreatesNoPhantomRun(t *testing.T) {
	for _, terminal := range []struct {
		name string
		call func(*RavienteRunTracker, uint16, time.Time)
	}{
		{name: "abort", call: func(tracker *RavienteRunTracker, generation uint16, now time.Time) {
			tracker.Abort(generation, now, "semaphore_teardown")
		}},
		{name: "complete", call: func(tracker *RavienteRunTracker, generation uint16, now time.Time) {
			tracker.Complete(generation, now)
		}},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			repo := &mockRavienteRunRepo{questKinds: make(map[uint16]RavienteRunKind)}
			tracker := newTestRavienteTracker(repo)
			now := time.Unix(100, 0)
			terminal.call(tracker, 33, now)
			tracker.Start(33, now.Add(time.Millisecond))
			if len(repo.starts) != 0 {
				t.Fatalf("late start created phantom runs %v", repo.starts)
			}
		})
	}
}

func TestRavienteRunTrackerClosedGenerationRetryDoesNotRestart(t *testing.T) {
	repo := &mockRavienteRunRepo{questKinds: make(map[uint16]RavienteRunKind)}
	tracker := newTestRavienteTracker(repo)
	now := time.Unix(100, 0)
	tracker.Start(8, now)
	repo.abortErr = errors.New("temporary database error")
	tracker.Abort(8, now.Add(time.Second), "semaphore_teardown")
	repo.abortErr = nil

	tracker.Start(8, now.Add(2*time.Second))
	if len(repo.starts) != 1 {
		t.Fatalf("closed generation was restarted: starts=%v", repo.starts)
	}
}

func TestRavienteResetUsesKilledTimeAsCompletion(t *testing.T) {
	server := createMockServerWithRaviente()
	repo := &mockRavienteRunRepo{questKinds: map[uint16]RavienteRunKind{54751: RavienteRunKindBerserk}}
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.raviente.id = 12
	server.ravienteRunTracker.Start(12, time.Now().Add(-time.Minute))
	session := createMockSession(1, server)
	session.Name = "Alice"
	server.recordRavienteQuestParticipant(54751, session)
	server.raviente.register[1] = 100
	server.raviente.register[2] = 1

	server.resetRaviente()

	if len(repo.completions) != 1 {
		t.Fatalf("killed-time teardown completions = %d, want 1", len(repo.completions))
	}
	if len(repo.aborts) != 0 {
		t.Fatalf("killed-time teardown aborted runs %v", repo.aborts)
	}
}

func TestRavienteLifecycleSerializesOldCompletionBeforeNewStart(t *testing.T) {
	repo := &mockRavienteRunRepo{
		questKinds:      map[uint16]RavienteRunKind{54751: RavienteRunKindBerserk},
		completeStarted: make(chan struct{}),
		completeRelease: make(chan struct{}),
	}
	server := createMockServerWithRaviente()
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.raviente.id = 1
	server.ravienteRunTracker.Start(1, time.Now().Add(-time.Minute))
	oldParticipant := createMockSession(1, server)
	oldParticipant.Name = "Old"
	server.recordRavienteQuestParticipant(54751, oldParticipant)
	server.raviente.register[1] = 100
	server.raviente.register[2] = 200

	resetDone := make(chan struct{})
	go func() {
		server.resetRaviente()
		close(resetDone)
	}()
	select {
	case <-repo.completeStarted:
	case <-time.After(time.Second):
		t.Fatal("old generation completion did not reach repository")
	}

	// reset released gameplay locks but intentionally retains lifecycleMu while
	// PostgreSQL completion is in flight.  Prepare a canonical room for G+1.
	addRaviSemaphore(server)
	server.raviente.Lock()
	server.raviente.register[3] = 300
	server.raviente.Unlock()
	newSession := createMockSession(2, server)
	newStartDone := make(chan struct{})
	go func() {
		handleMsgSysOperateRegister(newSession, &mhfpacket.MsgSysOperateRegister{
			AckHandle:      3,
			SemaphoreID:    raviRegisterGeneral,
			RawDataPayload: raviRegisterPayload(13, 1, 300),
		})
		close(newStartDone)
	}()
	select {
	case <-newStartDone:
		t.Fatal("new generation started before old completion committed")
	case <-time.After(25 * time.Millisecond):
	}

	close(repo.completeRelease)
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("old generation reset did not finish")
	}
	select {
	case <-newStartDone:
	case <-time.After(time.Second):
		t.Fatal("new generation did not start after old completion")
	}
	if len(repo.completions) != 1 || len(repo.starts) != 2 || repo.starts[1] != 2 {
		t.Fatalf("ordered lifecycle completions=%d starts=%v", len(repo.completions), repo.starts)
	}
}

func raviRegisterPayload(op, destination uint8, value uint32) []byte {
	data := make([]byte, 7)
	data[0] = op
	data[1] = destination
	binary.LittleEndian.PutUint32(data[2:6], value)
	return data
}

func TestOperateRegisterTracksNaturalRavienteStartAndCompletion(t *testing.T) {
	server := createMockServerWithRaviente()
	addRaviSemaphore(server)
	repo := &mockRavienteRunRepo{questKinds: map[uint16]RavienteRunKind{54751: RavienteRunKindBerserk}}
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.raviente.id = 23
	session := createMockSession(1, server)
	session.Name = "Alice"

	handleMsgSysOperateRegister(session, &mhfpacket.MsgSysOperateRegister{
		AckHandle:      1,
		SemaphoreID:    raviRegisterGeneral,
		RawDataPayload: raviRegisterPayload(13, 1, 100),
	})
	server.recordRavienteQuestParticipant(54751, session)
	handleMsgSysOperateRegister(session, &mhfpacket.MsgSysOperateRegister{
		AckHandle:      2,
		SemaphoreID:    raviRegisterGeneral,
		RawDataPayload: raviRegisterPayload(13, 2, 200),
	})

	if len(repo.starts) != 1 || repo.starts[0] != 23 {
		t.Fatalf("starts = %v, want generation 23", repo.starts)
	}
	if len(repo.completions) != 1 || repo.completions[0].kind != RavienteRunKindBerserk {
		t.Fatalf("completions = %+v", repo.completions)
	}
}

func TestOperateRegisterIgnoresKilledTimeBeforeStart(t *testing.T) {
	server := createMockServerWithRaviente()
	addRaviSemaphore(server)
	repo := &mockRavienteRunRepo{questKinds: make(map[uint16]RavienteRunKind)}
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.raviente.id = 44
	session := createMockSession(1, server)

	handleMsgSysOperateRegister(session, &mhfpacket.MsgSysOperateRegister{
		AckHandle:      1,
		SemaphoreID:    raviRegisterGeneral,
		RawDataPayload: raviRegisterPayload(13, 2, 200),
	})
	handleMsgSysOperateRegister(session, &mhfpacket.MsgSysOperateRegister{
		AckHandle:      2,
		SemaphoreID:    raviRegisterGeneral,
		RawDataPayload: raviRegisterPayload(13, 1, 100),
	})

	if len(repo.starts) != 1 || repo.starts[0] != 44 {
		t.Fatalf("post-stale start = %v, want generation 44", repo.starts)
	}
	if server.raviente.register[2] != 0 {
		t.Fatalf("pre-start killed time survived as register[2]=%d", server.raviente.register[2])
	}
	if len(repo.completions) != 0 {
		t.Fatalf("pre-start killed time created completions %+v", repo.completions)
	}
	server.resetRaviente()
	if len(repo.completions) != 0 {
		t.Fatalf("teardown after stale killed time created completions %+v", repo.completions)
	}
	if len(repo.aborts) != 1 {
		t.Fatalf("teardown aborts = %v, want the active generation aborted", repo.aborts)
	}
}

func TestOperateRegisterIgnoresLifecycleUpdatesWithoutCanonicalSemaphore(t *testing.T) {
	server := createMockServerWithRaviente()
	repo := &mockRavienteRunRepo{questKinds: make(map[uint16]RavienteRunKind)}
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.raviente.id = 55
	session := createMockSession(1, server)

	for i, destination := range []uint8{1, 2} {
		handleMsgSysOperateRegister(session, &mhfpacket.MsgSysOperateRegister{
			AckHandle:      uint32(i + 1),
			SemaphoreID:    raviRegisterGeneral,
			RawDataPayload: raviRegisterPayload(13, destination, 100),
		})
	}

	if server.raviente.register[1] != 0 || server.raviente.register[2] != 0 {
		t.Fatalf("orphan lifecycle packet mutated registers to [%d,%d]",
			server.raviente.register[1], server.raviente.register[2])
	}
	if len(repo.starts) != 0 || len(repo.completions) != 0 {
		t.Fatalf("orphan lifecycle packet created starts=%v completions=%v", repo.starts, repo.completions)
	}
}

func TestQuestStageEntryUsesHostBinaryForRavienteParticipant(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := &mockRavienteRunRepo{questKinds: map[uint16]RavienteRunKind{54751: RavienteRunKindBerserk}}
	server.ravienteRunTracker = newTestRavienteTracker(repo)
	server.ravienteRunTracker.Start(1, time.Now())

	const stageID = "sl2Qs200p0a1u0"
	stage := NewStage(stageID)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(54751, 0)
	server.stages.Store(stageID, stage)
	guest := createMockSession(42, server)
	guest.Name = "Guest"

	if !doStageTransfer(guest, 1, stageID) {
		t.Fatal("quest stage transfer failed")
	}
	if len(repo.participants) != 1 || repo.participants[0].CharacterID != 42 {
		t.Fatalf("participants = %+v, want guest 42", repo.participants)
	}
}
