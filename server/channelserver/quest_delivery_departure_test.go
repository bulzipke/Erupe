package channelserver

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

const deliveryDepartureTestStage = "sl2Qs200p0a1u0"

func deliveryDepartureTestServer(t *testing.T, start, end time.Time) *Server {
	t.Helper()
	input := map[string]any{
		"version":   "departure-test",
		"profiles":  []any{map[string]any{"id": "events", "worlds": []string{"events"}}},
		"schedules": []any{map[string]any{"id": "window", "starts_at": start.Format(time.RFC3339Nano), "ends_at": end.Format(time.RFC3339Nano)}},
		"quests":    []any{map[string]any{"quest_id": 40221, "profiles": []string{"events"}, "schedule": "window"}},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := questdelivery.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.questDelivery = catalog
	srv.worldName, srv.channelNumber = "events", 1
	return srv
}

// Pin a deterministic admission before feeding the real late-setup handler.
// The window can be long past, so these boundary tests do not sleep or depend
// on the wall clock reaching an exact millisecond during CI.
func pinDeliveryDepartureParticipant(t *testing.T, s *Session, stage *Stage, at time.Time) {
	t.Helper()
	s.lifecycleMu.Lock()
	stage.Lock()
	if !s.admitQuestDeliveryLocked(stage, false, at) {
		stage.Unlock()
		s.lifecycleMu.Unlock()
		t.Fatal("test admission was denied")
	}
	stage.clients[s] = s.charID
	if stage.host == nil {
		stage.host = s
	}
	stage.Unlock()
	s.Lock()
	s.stage = stage
	s.Unlock()
	s.questWeaponGeneration = 1
	s.questWeaponPendingStage = stage.id
	s.questWeaponPendingGeneration = 1
	s.lifecycleMu.Unlock()
}

func TestQuestDeliveryDepartureAdmissionBoundaries(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(72 * time.Hour)
	for _, tc := range []struct {
		name  string
		at    time.Time
		world string
		want  bool
	}{
		{"before start", start.Add(-time.Nanosecond), "events", false},
		{"start included", start, "events", true},
		{"last instant", end.Add(-time.Nanosecond), "events", true},
		{"end excluded", end, "events", false},
		{"after end", end.Add(time.Hour), "events", false},
		{"wrong world", start, "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := deliveryDepartureTestServer(t, start, end)
			srv.worldName = tc.world
			stage := NewStage(deliveryDepartureTestStage)
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40221, 0)
			s := createMockSession(1, srv)
			s.lifecycleMu.Lock()
			stage.Lock()
			got := s.admitQuestDeliveryLocked(stage, false, tc.at)
			stage.Unlock()
			s.lifecycleMu.Unlock()
			if got != tc.want {
				t.Fatalf("admission = %t, want %t", got, tc.want)
			}
			if stamp := s.questDeliveryDeparture.Load(); tc.want {
				if stamp == nil || stamp.questID != 40221 || !stamp.admittedAt.Equal(tc.at) {
					t.Fatalf("accepted departure was not pinned: %#v", stamp)
				}
			} else if stamp != nil {
				t.Fatal("denied admission retained a departure")
			}
		})
	}
}

func TestQuestDeliveryDepartureKnownClosedSetupDeniedBeforeTransfer(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40221, 0)
	srv.stages.Store(stage.id, stage)
	s := createMockSession(1, srv)
	if doStageTransfer(s, 1, stage.id) {
		t.Fatal("expired quest transfer succeeded")
	}
	stage.RLock()
	_, joined := stage.clients[s]
	stage.RUnlock()
	if joined || s.stage != nil || s.questDeliveryDeparture.Load() != nil {
		t.Fatal("denied transfer changed membership or departure state")
	}
	if len(s.sendPackets) != 1 {
		t.Fatal("denied transfer did not send exactly one failure ACK")
	}
}

func TestQuestDeliveryDepartureLateSetupUsesAdmissionTimeForEveryHunter(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(72 * time.Hour)
	srv := deliveryDepartureTestServer(t, start, end)
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 4, 2: 13}}
	srv.weaponUsageRepo = repo
	stage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(stage.id, stage)
	host, guest := createMockSession(1, srv), createMockSession(2, srv)
	pinDeliveryDepartureParticipant(t, host, stage, end.Add(-time.Nanosecond))
	pinDeliveryDepartureParticipant(t, guest, stage, end)
	handleMsgSysSetStageBinary(host, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3,
		RawDataPayload: questRunStagePayload(40221, 0),
	})
	if host.closed.Load() || host.questDeliveryDeparture.Load().questID != 40221 {
		t.Fatal("late setup interrupted a hunter admitted before expiry")
	}
	if !guest.closed.Load() || guest.stage != nil || guest.questDeliveryDeparture.Load() != nil {
		t.Fatal("hunter admitted at expiry was not disconnected and removed")
	}
	stage.RLock()
	_, hostJoined := stage.clients[host]
	_, guestJoined := stage.clients[guest]
	stage.RUnlock()
	if !hostJoined || guestJoined {
		t.Fatal("late setup retained the wrong party membership")
	}
	if repo.callCount(1) != 1 || repo.callCount(2) != 0 {
		t.Fatalf("departure usage counted rejected hunter: host=%d guest=%d", repo.callCount(1), repo.callCount(2))
	}
}

func TestQuestDeliveryDepartureDuplicateAndSameSetupContinueAfterExpiry(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	srv := deliveryDepartureTestServer(t, start, end)
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40221, 0)
	srv.stages.Store(stage.id, stage)
	host := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, host, stage, start)
	if !doStageTransfer(host, 1, stage.id) {
		t.Fatal("duplicate transfer interrupted an existing run after expiry")
	}
	payload := questRunStagePayload(40221, 0)
	handleMsgSysSetStageBinary(host, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: payload,
	})
	if host.closed.Load() || host.questDeliveryDeparture.Load().questID != 40221 {
		t.Fatal("same quest setup interrupted an existing run")
	}
	guest := createMockSession(2, srv)
	if doStageTransfer(guest, 2, stage.id) {
		t.Fatal("new guest entered a closed quest using the host's earlier departure")
	}
	handleMsgSysSetStageBinary(host, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3,
		RawDataPayload: questRunStagePayload(23303, 0),
	})
	stage.RLock()
	got := append([]byte(nil), stage.rawBinaryData[stageBinaryKey{1, 3}]...)
	stage.RUnlock()
	if !bytes.Equal(got, payload) || host.questDeliveryDeparture.Load().questID != 40221 || uint16(host.questRunState.Load()) != 40221 {
		t.Fatal("running quest setup changed to a different ID")
	}
}

func TestQuestDeliveryDepartureMalformedLateSetupClosesPendingOnly(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	stage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(stage.id, stage)
	s := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, s, stage, start)
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: []byte{1, 2, 3},
	})
	if !s.closed.Load() || s.stage != nil || s.questDeliveryDeparture.Load() != nil {
		t.Fatal("malformed setup left a pending quest active")
	}
	stage.RLock()
	_, stored := stage.rawBinaryData[stageBinaryKey{1, 3}]
	clients := len(stage.clients)
	stage.RUnlock()
	if stored || clients != 0 || s.questWeaponState.Load() != 0 {
		t.Fatal("malformed setup was stored or recorded as a departure")
	}
}

func TestQuestDeliveryDepartureRejectionCannotCloseNewTransfer(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
	stage := NewStage(deliveryDepartureTestStage)
	s := createMockSession(1, srv)
	pinDeliveryDepartureParticipant(t, s, stage, start)
	old := s.questDeliveryDeparture.Load()
	newStage := NewStage("sl1Ns200p0a0u0")
	s.lifecycleMu.Lock()
	s.questDeliveryDeparture.Store(nil)
	s.Lock()
	s.stage = newStage
	s.Unlock()
	s.lifecycleMu.Unlock()
	rejectQuestDeliveryDepartures(stage, []questDeliveryDepartureRejection{{s, old}})
	if s.closed.Load() || s.stage != newStage {
		t.Fatal("late rejection closed a later transfer")
	}
}

func TestQuestDeliveryDepartureFeatureOffPreservesMalformedSetup(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	stage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(stage.id, stage)
	s := createMockSession(1, srv)
	if !doStageTransfer(s, 1, stage.id) {
		t.Fatal("legacy quest-stage transfer failed")
	}
	payload := []byte{1, 2, 3}
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: payload,
	})
	stage.RLock()
	got := append([]byte(nil), stage.rawBinaryData[stageBinaryKey{1, 3}]...)
	stage.RUnlock()
	if s.closed.Load() || !bytes.Equal(got, payload) || s.questDeliveryDeparture.Load() != nil {
		t.Fatal("disabled delivery changed legacy stage behavior")
	}
}
