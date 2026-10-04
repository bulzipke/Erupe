package channelserver

import (
	"errors"
	"sync"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type mockWeaponUsageRepo struct {
	mu          sync.Mutex
	weaponTypes map[uint32]uint8
	calls       []uint32
	err         error
}

func (m *mockWeaponUsageRepo) RecordQuestDeparture(characterID uint32) (uint8, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, characterID)
	if m.err != nil {
		return 0, false, m.err
	}
	weaponType, ok := m.weaponTypes[characterID]
	return weaponType, ok, nil
}

func (m *mockWeaponUsageRepo) callCount(characterID uint32) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, calledID := range m.calls {
		if calledID == characterID {
			count++
		}
	}
	return count
}

func TestQuestStageRecordsEachHumanWeaponOnce(t *testing.T) {
	const questID = uint16(23303)
	const stageID = "sl2Qs200p0a1u0"
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{
		1: 0,
		2: 1,
		3: 12,
		4: 13,
	}}
	server.weaponUsageRepo = repo
	stage := NewStage(stageID)
	stage.maxPlayers = 8 // Unused companion slots must not create usage records.
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(questID, 0)
	server.stages.Store(stageID, stage)

	var sessions []*Session
	for charID := uint32(1); charID <= 4; charID++ {
		session := createMockSession(charID, server)
		if !doStageTransfer(session, charID, stageID) {
			t.Fatalf("quest-stage transfer failed for character %d", charID)
		}
		sessions = append(sessions, session)
	}

	if got := len(repo.calls); got != 4 {
		t.Fatalf("departure calls = %d, want one for each of 4 human sessions", got)
	}
	for i, session := range sessions {
		charID := uint32(i + 1)
		wantWeapon := repo.weaponTypes[charID]
		if gotWeapon, ok := session.questWeaponForResult(questID); !ok || gotWeapon != wantWeapon {
			t.Errorf("character %d captured weapon = (%d, %t), want (%d, true)", charID, gotWeapon, ok, wantWeapon)
		}
	}

	// A duplicate ENTER_STAGE packet for an already joined session is not a
	// second departure and therefore must not inflate usage.
	if !doStageTransfer(sessions[0], 99, stageID) {
		t.Fatal("repeat quest-stage transfer failed")
	}
	if got := repo.callCount(1); got != 1 {
		t.Fatalf("character 1 departure calls after repeat = %d, want 1", got)
	}
}

func TestNonQuestStageDoesNotRecordWeaponUsage(t *testing.T) {
	server := createMockServer()
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 5}}
	server.weaponUsageRepo = repo
	const stageID = "sl1Ns200p0a0u0"
	server.stages.Store(stageID, NewStage(stageID))

	if !doStageTransfer(createMockSession(1, server), 1, stageID) {
		t.Fatal("non-quest stage transfer failed")
	}
	if got := len(repo.calls); got != 0 {
		t.Fatalf("non-quest departure calls = %d, want 0", got)
	}
}

func TestNonZZQuestStageDoesNotTrustUnvalidatedStageName(t *testing.T) {
	server := createMockServer()
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 5}}
	server.weaponUsageRepo = repo
	const stageID = "sl1Qs200p0a0u0"
	server.stages.Store(stageID, NewStage(stageID))
	session := createMockSession(1, server)

	if !doStageTransfer(session, 1, stageID) {
		t.Fatal("non-ZZ quest-stage transfer failed")
	}
	if got := repo.callCount(1); got != 0 {
		t.Fatalf("unvalidated non-ZZ quest departure calls = %d, want 0", got)
	}
	if state := session.questWeaponState.Load(); state != 0 {
		t.Fatalf("non-ZZ departure retained unmatchable weapon state %#x", state)
	}
}

type firstCallBlockingWeaponUsageRepo struct {
	mu          sync.Mutex
	started     chan struct{}
	release     chan struct{}
	calls       []uint32
	weaponTypes map[uint32]uint8
}

func (r *firstCallBlockingWeaponUsageRepo) RecordQuestDeparture(characterID uint32) (uint8, bool, error) {
	r.mu.Lock()
	r.calls = append(r.calls, characterID)
	first := len(r.calls) == 1
	weaponType, ok := r.weaponTypes[characterID]
	r.mu.Unlock()
	if first {
		close(r.started)
		<-r.release
	}
	return weaponType, ok, nil
}

func TestLateSetupArmsEveryParticipantBeforeDatabaseWork(t *testing.T) {
	const questID = uint16(23303)
	const stageID = "sl2Qs200p0a1u0"
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := &firstCallBlockingWeaponUsageRepo{
		started: make(chan struct{}), release: make(chan struct{}),
		weaponTypes: map[uint32]uint8{1: 4, 2: 13},
	}
	server.weaponUsageRepo = repo
	stage := NewStage(stageID)
	server.stages.Store(stageID, stage)
	first := createMockSession(1, server)
	second := createMockSession(2, server)
	if !doStageTransfer(first, 1, stageID) || !doStageTransfer(second, 2, stageID) {
		t.Fatal("quest-stage transfer failed")
	}

	done := make(chan struct{})
	go func() {
		handleMsgSysSetStageBinary(first, &mhfpacket.MsgSysSetStageBinary{
			StageID: stageID, BinaryType0: 1, BinaryType1: 3,
			RawDataPayload: questRunStagePayload(questID, 0),
		})
		close(done)
	}()
	<-repo.started

	for charID, session := range map[uint32]*Session{1: first, 2: second} {
		if state := session.questWeaponState.Load(); state != uint32(questID)<<8 {
			t.Errorf("character %d state while first query blocks = %#x, want armed marker %#x", charID, state, uint32(questID)<<8)
		}
	}
	close(repo.release)
	<-done
	for charID, session := range map[uint32]*Session{1: first, 2: second} {
		if weaponType, ok := session.questWeaponForResult(questID); !ok || weaponType != repo.weaponTypes[charID] {
			t.Errorf("character %d captured weapon = (%d, %t), want (%d, true)", charID, weaponType, ok, repo.weaponTypes[charID])
		}
	}
}

func TestQuestStageWaitsForValidatedSetupAndRecordsOnce(t *testing.T) {
	const questID = uint16(23303)
	const stageID = "sl2Qs200p0a1u0"
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 6}}
	server.weaponUsageRepo = repo
	stage := NewStage(stageID)
	server.stages.Store(stageID, stage)
	session := createMockSession(1, server)

	if !doStageTransfer(session, 1, stageID) {
		t.Fatal("quest-stage transfer failed")
	}
	if got := len(repo.calls); got != 0 {
		t.Fatalf("unvalidated quest-stage calls = %d, want 0", got)
	}

	// An arbitrary Qs stage or malformed setup is not sufficient evidence of
	// a departure and must not make the dashboard counter client-spoofable.
	handleMsgSysSetStageBinary(session, &mhfpacket.MsgSysSetStageBinary{
		StageID:        stageID,
		BinaryType0:    1,
		BinaryType1:    3,
		RawDataPayload: []byte{1, 2, 3},
	})
	if got := len(repo.calls); got != 0 {
		t.Fatalf("malformed quest-setup calls = %d, want 0", got)
	}

	validSetup := &mhfpacket.MsgSysSetStageBinary{
		StageID:        stageID,
		BinaryType0:    1,
		BinaryType1:    3,
		RawDataPayload: questRunStagePayload(questID, 0),
	}
	handleMsgSysSetStageBinary(session, validSetup)
	handleMsgSysSetStageBinary(session, validSetup)
	if got := repo.callCount(1); got != 1 {
		t.Fatalf("validated late quest-setup calls = %d, want 1", got)
	}
	if weaponType, ok := session.questWeaponForResult(questID); !ok || weaponType != 6 {
		t.Fatalf("late setup captured weapon = (%d, %t), want (6, true)", weaponType, ok)
	}
}

func TestQuestWeaponDepartureRepositoryErrorLeavesNoSnapshot(t *testing.T) {
	server := createMockServer()
	server.weaponUsageRepo = &mockWeaponUsageRepo{err: errors.New("database unavailable")}
	session := createMockSession(1, server)
	session.questWeaponGeneration = 1
	session.recordQuestWeaponDeparture(12345, 1)
	if state := session.questWeaponState.Load(); state != 0 {
		t.Fatalf("weapon state after repository error = %#x, want 0", state)
	}
}

type blockingWeaponUsageRepo struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingWeaponUsageRepo) RecordQuestDeparture(uint32) (uint8, bool, error) {
	close(r.started)
	<-r.release
	return 4, true, nil
}

func TestSlowOlderDepartureCannotOverwriteNewQuestSnapshot(t *testing.T) {
	server := createMockServer()
	repo := &blockingWeaponUsageRepo{started: make(chan struct{}), release: make(chan struct{})}
	server.weaponUsageRepo = repo
	session := createMockSession(1, server)
	session.questWeaponGeneration = 1
	done := make(chan struct{})
	go func() {
		session.recordQuestWeaponDeparture(100, 1)
		close(done)
	}()
	<-repo.started

	// This is the generation change performed by the next first Qs entry.
	session.lifecycleMu.Lock()
	session.questWeaponGeneration = 2
	session.questWeaponState.Store(uint32(200)<<8 | uint32(13+1))
	session.lifecycleMu.Unlock()
	close(repo.release)
	<-done

	if weaponType, ok := session.questWeaponForResult(200); !ok || weaponType != 13 {
		t.Fatalf("new quest snapshot = (%d, %t), want (13, true)", weaponType, ok)
	}
	if _, ok := session.questWeaponForResult(100); ok {
		t.Fatal("slow older departure overwrote the newer quest snapshot")
	}
}

func TestResultDuringWeaponLookupPreventsLateSnapshot(t *testing.T) {
	const questID = uint16(100)
	server := createMockServer()
	repo := &blockingWeaponUsageRepo{started: make(chan struct{}), release: make(chan struct{})}
	server.weaponUsageRepo = repo
	session := createMockSession(1, server)
	session.questWeaponGeneration = 1
	done := make(chan struct{})
	go func() {
		session.recordQuestWeaponDeparture(questID, 1)
		close(done)
	}()
	<-repo.started

	if state := session.questWeaponState.Load(); state != uint32(questID)<<8 {
		t.Fatalf("in-flight weapon state = %#x, want quest marker %#x", state, uint32(questID)<<8)
	}
	session.clearQuestWeaponForResult(questID)
	close(repo.release)
	<-done

	if state := session.questWeaponState.Load(); state != 0 {
		t.Fatalf("late weapon lookup restored consumed snapshot %#x", state)
	}
}

func TestQuestWeaponSnapshotBoundsAndMatchingClear(t *testing.T) {
	for _, weaponType := range []uint8{0, 13} {
		t.Run(string(rune('0'+weaponType)), func(t *testing.T) {
			const questID = uint16(12345)
			session := createMockSession(1, createMockServer())
			session.questWeaponState.Store(uint32(questID)<<8 | uint32(weaponType+1))

			if got, ok := session.questWeaponForResult(questID); !ok || got != weaponType {
				t.Fatalf("captured weapon = (%d, %t), want (%d, true)", got, ok, weaponType)
			}
			session.endQuestRun()
			if _, ok := session.questWeaponForResult(questID); !ok {
				t.Fatal("town transition cleared the weapon before the result log")
			}
			session.clearQuestWeaponForResult(questID + 1)
			if _, ok := session.questWeaponForResult(questID); !ok {
				t.Fatal("mismatched delayed result cleared the current weapon")
			}
			session.clearQuestWeaponForResult(questID)
			if state := session.questWeaponState.Load(); state != 0 {
				t.Fatalf("matching result left weapon state %#x", state)
			}
		})
	}
}
