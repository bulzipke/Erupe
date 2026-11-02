package channelserver

import (
	"encoding/binary"
	"encoding/json"
	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"testing"
	"time"
)

// These fixed cases are observations from both original ZZ producer/selectors
// for the 207 HR Hunters IDs, rather than expectations derived from the server
// switch. Only 48 parents produced one of the 15 intrusion result IDs.
func nativeIntrusionTestOracle(t *testing.T) struct{ Parents []struct{ ID, Child uint16 } } {
	t.Helper()
	return struct{ Parents []struct{ ID, Child uint16 } }{Parents: []struct{ ID, Child uint16 }{
		{21007, 0},
		{21019, 0},
		{21020, 0},
		{21021, 0},
		{21022, 0},
		{21023, 0},
		{21028, 0},
		{21032, 0},
		{21036, 0},
		{21039, 0},
		{21041, 0},
		{21043, 0},
		{21044, 0},
		{21049, 0},
		{21054, 0},
		{21055, 0},
		{21056, 0},
		{21057, 0},
		{21062, 0},
		{21067, 0},
		{21069, 0},
		{21070, 0},
		{21071, 0},
		{21079, 26617},
		{21082, 26617},
		{21083, 26617},
		{21093, 26614},
		{21095, 26614},
		{21096, 26614},
		{21097, 26614},
		{21099, 26614},
		{21103, 0},
		{21105, 0},
		{21107, 0},
		{21108, 0},
		{21118, 0},
		{21127, 0},
		{21132, 26611},
		{21472, 26619},
		{21473, 0},
		{21474, 26616},
		{21475, 26613},
		{21476, 26622},
		{21477, 0},
		{21478, 0},
		{21479, 26616},
		{21480, 26619},
		{21481, 0},
		{21482, 0},
		{21483, 0},
		{21484, 0},
		{21486, 0},
		{21490, 26624},
		{21491, 0},
		{21492, 0},
		{21694, 0},
		{21695, 0},
		{21696, 0},
		{21697, 0},
		{21698, 0},
		{21699, 0},
		{21702, 0},
		{21759, 26617},
		{21767, 26617},
		{21771, 26617},
		{21780, 26614},
		{21782, 26614},
		{21783, 26614},
		{21785, 0},
		{21793, 26614},
		{21797, 26614},
		{21805, 26615},
		{21808, 0},
		{21809, 0},
		{21821, 0},
		{21832, 0},
		{21833, 0},
		{21835, 0},
		{21836, 0},
		{21837, 0},
		{21838, 0},
		{22003, 0},
		{22004, 0},
		{22005, 0},
		{22006, 0},
		{22007, 0},
		{22008, 0},
		{22009, 0},
		{22010, 0},
		{22011, 0},
		{22012, 0},
		{22013, 0},
		{22031, 0},
		{22032, 0},
		{22033, 0},
		{22034, 0},
		{22035, 0},
		{22036, 0},
		{22037, 0},
		{22038, 0},
		{22039, 0},
		{22040, 0},
		{22041, 0},
		{22042, 0},
		{22043, 0},
		{22044, 0},
		{22046, 0},
		{22047, 0},
		{22048, 0},
		{22049, 0},
		{22050, 0},
		{22051, 0},
		{22052, 0},
		{22053, 0},
		{22056, 0},
		{22057, 0},
		{22058, 0},
		{22059, 0},
		{22060, 0},
		{22061, 0},
		{22062, 0},
		{22063, 0},
		{22064, 0},
		{22066, 0},
		{22067, 0},
		{22068, 0},
		{22084, 0},
		{22085, 0},
		{22086, 0},
		{22087, 0},
		{22088, 0},
		{22089, 26622},
		{22090, 26622},
		{22108, 0},
		{22109, 0},
		{26013, 0},
		{26014, 0},
		{26105, 0},
		{26116, 0},
		{26152, 0},
		{26153, 0},
		{26164, 0},
		{26165, 0},
		{26168, 26620},
		{26215, 0},
		{26224, 26620},
		{26229, 26620},
		{26230, 26620},
		{26240, 0},
		{26241, 0},
		{26244, 0},
		{26253, 26620},
		{26255, 0},
		{26256, 26619},
		{26259, 0},
		{26262, 26619},
		{26265, 0},
		{26268, 26622},
		{26271, 0},
		{26276, 26622},
		{26298, 0},
		{26299, 26619},
		{26303, 26616},
		{26306, 26616},
		{26309, 0},
		{26312, 0},
		{26315, 0},
		{26323, 26616},
		{26326, 0},
		{26329, 0},
		{26332, 0},
		{26335, 0},
		{26338, 26619},
		{26347, 26616},
		{26348, 0},
		{26349, 0},
		{26352, 26616},
		{26353, 0},
		{26402, 26624},
		{26403, 26624},
		{26404, 26624},
		{26460, 0},
		{26545, 0},
		{26551, 0},
		{26588, 26619},
		{26589, 0},
		{26611, 0},
		{26612, 0},
		{26613, 0},
		{26614, 0},
		{26615, 0},
		{26616, 0},
		{26617, 0},
		{26618, 0},
		{26619, 0},
		{26620, 0},
		{26621, 0},
		{26622, 0},
		{26623, 0},
		{26624, 0},
		{26625, 0},
		{26675, 0},
		{26676, 0},
		{26686, 0},
		{26691, 0},
		{26692, 0},
		{26748, 26612},
	}}
}
func nativeIntrusionTestServer(t *testing.T, ids []int, start, end time.Time) *Server {
	t.Helper()
	rows := []any{}
	for _, q := range ids {
		rows = append(rows, map[string]any{"quest_id": q, "profiles": []string{"native"}, "schedule": "window"})
	}
	b, e := json.Marshal(map[string]any{"version": "isolated553", "profiles": []any{map[string]any{"id": "native", "worlds": []string{"events"}}}, "schedules": []any{map[string]any{"id": "window", "starts_at": start.Format(time.RFC3339Nano), "ends_at": end.Format(time.RFC3339Nano)}}, "quests": rows})
	if e != nil {
		t.Fatal(e)
	}
	c, e := questdelivery.Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.questDelivery = c
	srv.worldName = "events"
	srv.channelNumber = 1
	return srv
}
func nativeIntrusionTestRecord(q uint16) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint16(b, q)
	return b
}
func TestNativeIntrusionOriginalOracle(t *testing.T) {
	in := nativeIntrusionTestOracle(t)
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	s := createMockSession(1, srv)
	positive := 0
	for _, r := range in.Parents {
		p := questRunStagePayload(r.ID, 0x20)
		if got := nativeIntrusionResultForParent(r.ID); got != r.Child {
			t.Fatalf("original oracle %d got%d want%d", r.ID, got, r.Child)
		}
		want := r.Child
		switch r.ID {
		case 22037:
			want = 26611
		case 22040:
			want = 26623
		}
		if got := s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, p); got != want {
			t.Fatalf("original or approved key %d got%d want%d", r.ID, got, want)
		}
		if r.Child != 0 {
			positive++
		}
		for _, f := range []byte{0, 8, 0x28} {
			if got := s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, questRunStagePayload(r.ID, f)); got != 0 {
				t.Fatal("absent native flag or HC")
			}
		}
		for _, bad := range [][]byte{p[:143], append(append([]byte{}, p...), 0), nil} {
			if s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, bad) != 0 {
				t.Fatal("length")
			}
		}
		p[0x68] ^= 1
		if s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, p) != 0 {
			t.Fatal("mirror")
		}
	}
	if positive != 48 {
		t.Fatal("closed48 scope")
	}
	for _, q := range []uint16{21131, 21660, 50173, 0, 65535} {
		if s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, questRunStagePayload(q, 0x20)) != 0 {
			t.Fatal("unaudited/retired/event parent")
		}
	}
	if s.nativeIntrusionResultFromSetup("sl1Ns200p0a0u0", questRunStagePayload(21093, 0x20)) != 0 {
		t.Fatal("town")
	}
	srv.erupeConfig.RealClientMode = cfg.Z1
	if s.nativeIntrusionResultFromSetup(deliveryDepartureTestStage, questRunStagePayload(21093, 0x20)) != 0 {
		t.Fatal("nonZZ")
	}
}
func TestNativeIntrusionAdmissionReceipt(t *testing.T) {
	in := nativeIntrusionTestOracle(t)
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []int{}
	for _, r := range in.Parents {
		ids = append(ids, int(r.ID))
	}
	// The complete207 source roster already includes all15intrusion IDs.
	srv := nativeIntrusionTestServer(t, ids, at, at.Add(time.Hour))
	pairs := 0
	for _, r := range in.Parents {
		if r.Child == 0 {
			continue
		}
		s := createMockSession(1, srv)
		stage := NewStage(deliveryDepartureTestStage)
		stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(r.ID, 0x20)
		pinDeliveryDepartureParticipant(t, s, stage, at)
		receipt := s.questDeliveryReceipt.Load()
		if receipt.nativeIntrusionResultID != r.Child {
			t.Fatal("admission pin")
		}
		for q := uint16(26611); q <= 26625; q++ {
			if got := s.allowsQuestDeliveryRecord(nativeIntrusionTestRecord(q)); got != (q == r.Child) {
				t.Fatalf("exact child parent%d result%d", r.ID, q)
			}
			pairs++
		}
		if !s.allowsQuestDeliveryRecord(nativeIntrusionTestRecord(r.ID)) {
			t.Fatal("normal parent result")
		}
		// Real town transfer clears departure while preserving the immutable result receipt.
		town := NewStage("sl1Ns200p0a0u0")
		srv.stages.Store(town.id, town)
		if !doStageTransfer(s, 1, town.id) || s.questDeliveryDeparture.Load() != nil || s.questDeliveryReceipt.Load() != receipt || !s.allowsQuestDeliveryRecord(nativeIntrusionTestRecord(r.Child)) {
			t.Fatal("town receipt lifetime")
		}
	}
	if pairs != 720 {
		t.Fatal("48x15 exact child outcomes")
	}
	// Setup-first and admission-first both pin only once. Later metadata cannot grant an alias.
	for _, initialFlag := range []byte{0, 0x20} {
		s := createMockSession(1, srv)
		stage := NewStage(deliveryDepartureTestStage)
		pinDeliveryDepartureParticipant(t, s, stage, at)
		if s.questDeliveryReceipt.Load().questID != 0 {
			t.Fatal("pending")
		}
		p := &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(21093, initialFlag)}
		stage.Lock()
		ok, rejected := s.validateQuestDeliverySetupLocked(stage, p, 21093, true)
		stage.Unlock()
		if !ok || len(rejected) != 0 {
			t.Fatal("late original setup")
		}
		receipt := s.questDeliveryReceipt.Load()
		want := uint16(0)
		if initialFlag == 0x20 {
			want = 26614
		}
		if receipt.nativeIntrusionResultID != want {
			t.Fatal("late pin")
		}
		stage.rawBinaryData[stageBinaryKey{1, 3}] = p.RawDataPayload
		p.RawDataPayload = questRunStagePayload(21093, initialFlag^0x20)
		stage.Lock()
		ok, rejected = s.validateQuestDeliverySetupLocked(stage, p, 21093, true)
		stage.Unlock()
		if !ok || len(rejected) != 0 || s.questDeliveryReceipt.Load() != receipt {
			t.Fatal("same parent metadata repinned")
		}
		p.RawDataPayload = questRunStagePayload(26614, 0x20)
		stage.Lock()
		ok, rejected = s.validateQuestDeliverySetupLocked(stage, p, 26614, true)
		stage.Unlock()
		if ok || len(rejected) != 0 || s.questDeliveryReceipt.Load() != receipt || s.closed.Load() {
			t.Fatal("crossID setup must reject atomically without close")
		}
	}
}
func TestNativeIntrusionResultRetainsAccess(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []int{21093, 26614}
	mk := func() *Session {
		srv := nativeIntrusionTestServer(t, ids, at, at.Add(time.Hour))
		s := createMockSession(1, srv)
		st := NewStage(deliveryDepartureTestStage)
		st.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(21093, 0x20)
		pinDeliveryDepartureParticipant(t, s, st, at)
		return s
	}
	record := nativeIntrusionTestRecord(26614)
	for _, q := range []int{21093, 26614} {
		s := mk()
		s.server.questAccess = &QuestAccessGameplay{required: map[int]struct{}{q: {}}}
		if s.allowsQuestDeliveryRecord(record) {
			t.Fatal("code parent or child must retain exact rights")
		}
		s.questAccessSnapshot.Store(newQuestAccessSnapshot(s.userID, s.charID, time.Now(), map[int]questAccessGrant{q: {accountPermanent: true}}, nil))
		if s.allowsQuestDeliveryRecord(record) {
			t.Fatal("new right repaired old admission")
		}
	}
	s := mk()
	s.server.worldName = "other"
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("child world restriction bypass")
	}
	s = mk()
	old := s.questDeliveryReceipt.Load()
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: at.Add(time.Hour), questID: old.questID, nativeIntrusionResultID: old.nativeIntrusionResultID})
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("child schedule restriction bypass")
	}
	s = mk()
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: at, questID: 0, nativeIntrusionResultID: 26614})
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("pending admission bypass")
	}
	s = mk()
	s.questDeliveryReceipt.Store(nil)
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("missing receipt bypass")
	}
	// Even a fabricated intrusion field cannot weaken the unchanged Return predicate.
	s = mk()
	var rq uint16
	for q := 1; q <= 65535; q++ {
		if isReturnWorldQuest(q) {
			rq = uint16(q)
			break
		}
	}
	if rq == 0 {
		t.Fatal("Return fixture")
	}
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: at, questID: rq, nativeIntrusionResultID: 26614})
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("Return parent bypass")
	}
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{admittedAt: at, questID: 21093, nativeIntrusionResultID: rq})
	if s.allowsQuestDeliveryRecord(nativeIntrusionTestRecord(rq)) {
		t.Fatal("Return result bypass")
	}
	for _, p := range [][2]uint16{{21731, 21749}, {21749, 21731}, {21746, 21750}, {21750, 21746}} {
		if !sameQuestDeliveryResultID(p[0], p[1]) {
			t.Fatal("Tower alias changed")
		}
	}
	if sameQuestDeliveryResultID(21093, 26614) {
		t.Fatal("global crossID matcher broadened")
	}
}
