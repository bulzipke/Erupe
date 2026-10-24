package channelserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type returnCandidateReader struct {
	UserRepo
	read func(context.Context, uint32) (returnQuestExpiries, error)
}

func (r returnCandidateReader) ReadReturnQuestExpiries(ctx context.Context, userID uint32) (returnQuestExpiries, error) {
	return r.read(ctx, userID)
}

type returnCandidateBooleanOnly struct{ UserRepo }

func (returnCandidateBooleanOnly) ReturnWorldEligible(uint32, time.Time) (bool, error) {
	return true, nil
}

func returnCandidateSession() *Session {
	s := createMockSession(11, createMockServer())
	s.userID = 77
	s.server.erupeConfig.RealClientMode = cfg.ZZ
	return s
}

func returnCandidateSnapshot(s *Session, observed time.Time, first, second returnQuestExpiry) *returnQuestEligibilitySnapshot {
	return &returnQuestEligibilitySnapshot{userID: s.userID, characterID: s.charID, observedAt: observed,
		expiries: returnQuestExpiries{userID: s.userID, returnExpiry: first, newcomerExpiry: second}}
}

func TestReturnQuestCandidateOriginalORAndStrictBoundary(t *testing.T) {
	at := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	null := returnQuestExpiry{}
	negative := returnQuestExpiry{kind: returnQuestExpiryNegativeInfinity}
	positive := returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity}
	finite := func(when time.Time) returnQuestExpiry {
		return returnQuestExpiry{kind: returnQuestExpiryFinite, at: when}
	}
	for _, tc := range []struct {
		name string
		a, b returnQuestExpiry
		want bool
	}{
		{"both null", null, null, false}, {"negative infinity", negative, null, false},
		{"expired return", finite(at.Add(-time.Microsecond)), null, false},
		{"exact return", finite(at), null, false}, {"exact newcomer", null, finite(at), false},
		{"future return", finite(at.Add(time.Microsecond)), null, true},
		{"future newcomer OR", finite(at.Add(-time.Hour)), finite(at.Add(time.Microsecond)), true},
		{"positive infinity return", positive, negative, true}, {"positive infinity newcomer", null, positive, true},
		{"invalid kind", returnQuestExpiry{kind: 99}, positive, false},
		{"bad finite precision", finite(at.Add(time.Nanosecond)), positive, false},
		{"nonfinite payload", returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity, at: at}, null, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := returnCandidateSession()
			proof := returnCandidateSnapshot(s, at.Add(-time.Second), tc.a, tc.b)
			if got := s.allowsReturnQuestSnapshot(proof, 23352, at); got != tc.want {
				t.Fatalf("allowed=%t want=%t", got, tc.want)
			}
			if !s.allowsReturnQuestSnapshot(nil, 23303, at) {
				t.Fatal("Return evidence constrained an ordinary quest")
			}
		})
	}
	for _, invalid := range []string{"nil", "zero", "account", "character", "raw account", "future", "unobserved", "legacy mode"} {
		t.Run(invalid, func(t *testing.T) {
			s := returnCandidateSession()
			proof := returnCandidateSnapshot(s, at, positive, null)
			switch invalid {
			case "nil":
				proof = nil
			case "zero":
				proof = &returnQuestEligibilitySnapshot{}
			case "account":
				proof.userID++
			case "character":
				proof.characterID++
			case "raw account":
				proof.expiries.userID++
			case "future":
				proof.observedAt = at.Add(time.Nanosecond)
			case "unobserved":
				proof.observedAt = time.Time{}
			case "legacy mode":
				s.server.erupeConfig.RealClientMode = cfg.G9
			}
			if s.allowsReturnQuestSnapshot(proof, 23352, at) {
				t.Fatal("invalid proof granted a Return quest")
			}
		})
	}
}

func TestReturnQuestCandidateTimestampInputPrecision(t *testing.T) {
	base := time.Date(2026, 10, 24, 23, 59, 59, 0, time.FixedZone("original-offset", 9*60*60))
	for _, tc := range []struct{ nanos, micros int }{
		{0, 0}, {1, 0}, {499, 0}, {500, 0}, {501, 1}, {1000, 1}, {1499, 1},
		{1500, 2}, {2500, 2}, {3500, 4}, {999999499, 999999}, {999999500, 1000000}, {999999999, 1000000},
	} {
		t.Run(fmt.Sprint(tc.nanos), func(t *testing.T) {
			at := base.Add(time.Duration(tc.nanos))
			got := returnQuestPGInputTime(at)
			want := base.Add(time.Duration(tc.micros) * time.Microsecond)
			if !got.Equal(want) || got.Location() != base.Location() {
				t.Fatalf("got=%s want=%s", got, want)
			}
		})
	}
	s := returnCandidateSession()
	expiry := base.Add(time.Second)
	proof := returnCandidateSnapshot(s, base.Add(-time.Second), returnQuestExpiry{kind: returnQuestExpiryFinite, at: expiry}, returnQuestExpiry{})
	if s.allowsReturnQuestSnapshot(proof, 23352, expiry.Add(-100*time.Nanosecond)) {
		t.Fatal("direct Go before bypassed PG fractional rounding")
	}
	if !s.allowsReturnQuestSnapshot(proof, 23352, expiry.Add(-501*time.Nanosecond)) {
		t.Fatal("last SQL-representable instant was excluded")
	}
}

func TestReturnQuestCandidateReaderRequestAndLockScope(t *testing.T) {
	s := returnCandidateSession()
	s.server.userRepo = returnCandidateBooleanOnly{}
	if proof, err := s.loadReturnQuestEligibility(context.Background()); proof != nil || err == nil {
		t.Fatal("legacy bool was used as raw evidence fallback")
	}
	calls := 0
	s.server.userRepo = returnCandidateReader{read: func(_ context.Context, uid uint32) (returnQuestExpiries, error) {
		calls++
		if uid != 77 {
			t.Fatal("reader received a character ID or foreign account")
		}
		if !s.lifecycleMu.TryLock() {
			t.Fatal("reader held lifecycleMu")
		}
		s.lifecycleMu.Unlock()
		if !s.questDeliveryListMu.TryLock() {
			t.Fatal("reader held listing mutex")
		}
		s.questDeliveryListMu.Unlock()
		return returnQuestExpiries{userID: uid, returnExpiry: returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity}}, nil
	}}
	before := time.Now()
	first, err := s.loadReturnQuestEligibility(context.Background())
	if err != nil || first.observedAt.Before(before) || !s.allowsReturnQuestSnapshot(first, 23352, time.Now()) {
		t.Fatal("original values were not bound after the read", err)
	}
	second, err := s.loadReturnQuestEligibility(context.Background())
	if err != nil || calls != 2 || first == second {
		t.Fatal("request-local reads became a Session cache")
	}
	s.server.userRepo = returnCandidateReader{read: func(context.Context, uint32) (returnQuestExpiries, error) {
		return returnQuestExpiries{userID: 78}, nil
	}}
	if got, err := s.loadReturnQuestEligibility(context.Background()); got != nil || err == nil {
		t.Fatal("foreign account returned authority")
	}
	s.server.userRepo = returnCandidateReader{read: func(context.Context, uint32) (returnQuestExpiries, error) {
		return returnQuestExpiries{}, errors.New("reader failure")
	}}
	if got, err := s.loadReturnQuestEligibility(context.Background()); got != nil || err == nil {
		t.Fatal("reader failure returned authority")
	}
	if !s.allowsReturnQuestSnapshot(nil, 23303, time.Now()) || s.closed.Load() {
		t.Fatal("technical failure constrained ordinary quests or login")
	}
}

func TestReturnQuestCandidatePinnedLateSetupAndResult(t *testing.T) {
	at := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC) // already expired in real time
	s := returnCandidateSession()
	proof := returnCandidateSnapshot(s, at.Add(-time.Second), returnQuestExpiry{}, returnQuestExpiry{kind: returnQuestExpiryFinite, at: at.Add(time.Microsecond)})
	stage := NewStage(deliveryDepartureTestStage)
	s.server.stages.Store(stage.id, stage)
	s.lifecycleMu.Lock()
	stage.Lock()
	if !s.admitQuestDeliveryWithReturnLocked(stage, false, at, proof) {
		t.Fatal("eligible unknown setup was denied")
	}
	stage.clients[s] = s.charID
	stage.host = s
	stage.Unlock()
	s.lifecycleMu.Unlock()
	s.Lock()
	s.stage = stage
	s.Unlock()
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(23352, 0)})
	receipt := s.questDeliveryReceipt.Load()
	if receipt == nil || receipt.questID != 23352 || receipt.returnEligibility != proof || !receipt.admittedAt.Equal(at) || s.closed.Load() {
		t.Fatal("late setup lost pre-expiry admission")
	}
	validatedSetup := append([]byte(nil), stage.rawBinaryData[stageBinaryKey{1, 3}]...)
	handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: []byte{1, 2, 3}})
	if s.closed.Load() || s.questDeliveryReceipt.Load() != receipt || !bytes.Equal(stage.rawBinaryData[stageBinaryKey{1, 3}], validatedSetup) {
		t.Fatal("malformed update changed a validated Return run without publication/code manifests")
	}
	if !s.allowsQuestDeliverySave() {
		t.Fatal("post-expiry SAVE lost pinned authority")
	}
	record := make([]byte, questIDOffset+2)
	binary.LittleEndian.PutUint16(record[questIDOffset:], 23352)
	if !s.allowsQuestDeliveryRecord(record) {
		t.Fatal("post-expiry result lost pinned authority")
	}
	binary.LittleEndian.PutUint16(record[questIDOffset:], 23353)
	if s.allowsQuestDeliveryRecord(record) {
		t.Fatal("another Return quest borrowed the receipt")
	}
	s.lifecycleMu.Lock()
	stage.Lock()
	if !s.admitQuestDeliveryWithReturnLocked(stage, true, time.Now(), nil) {
		t.Fatal("same live run needed a later read")
	}
	stage.Unlock()
	s.lifecycleMu.Unlock()
	if s.questDeliveryReceipt.Load() != receipt {
		t.Fatal("duplicate packet replaced the original receipt")
	}
	other := NewStage("sl2Qs201p0a1u0")
	other.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(23352, 0)
	if s.admitQuestDeliveryWithReturnLocked(other, false, time.Now(), nil) {
		t.Fatal("nil duplicate sentinel authorized another run")
	}
}

func TestReturnQuestCandidateLateDenialDoesNotCloseOrMutate(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			s := returnCandidateSession()
			at := time.Now()
			var proof *returnQuestEligibilitySnapshot
			if !unavailable {
				proof = returnCandidateSnapshot(s, at.Add(-time.Second), returnQuestExpiry{}, returnQuestExpiry{})
			}
			stage := NewStage(deliveryDepartureTestStage)
			s.server.stages.Store(stage.id, stage)
			if !s.admitQuestDeliveryWithReturnLocked(stage, false, at, proof) {
				t.Fatal("ordinary unknown setup was blocked")
			}
			stage.clients[s] = s.charID
			stage.host = s
			s.stage = stage
			before := s.questDeliveryDeparture.Load()
			handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(23352, 0)})
			if s.closed.Load() || s.questDeliveryDeparture.Load() != before || len(stage.clients) != 1 || len(stage.rawBinaryData) != 0 {
				t.Fatal("Return setup denial closed or partially mutated the stage")
			}
			record := make([]byte, questIDOffset+2)
			binary.LittleEndian.PutUint16(record[questIDOffset:], 23352)
			if s.allowsQuestDeliveryRecord(record) {
				t.Fatal("denied Return setup authorized a result")
			}
			handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(23303, 0)})
			if s.closed.Load() || !bytes.Equal(stage.rawBinaryData[stageBinaryKey{1, 3}], questRunStagePayload(23303, 0)) {
				t.Fatal("Return failure changed ordinary setup behavior")
			}
		})
	}
}

func TestReturnQuestCandidateActualStagePreflight(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		quest                    uint16
		allowed, readError, want bool
	}{
		{"eligible Return", 23352, true, false, true}, {"ineligible Return", 23352, false, false, false},
		{"Return reader error", 23352, false, true, false}, {"ordinary reader error", 23303, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := returnCandidateSession()
			s.server.worldType = 0
			stage := NewStage(deliveryDepartureTestStage)
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(tc.quest, 0)
			s.server.stages.Store(stage.id, stage)
			calls := 0
			s.server.userRepo = returnCandidateReader{read: func(_ context.Context, uid uint32) (returnQuestExpiries, error) {
				calls++
				if !s.lifecycleMu.TryLock() {
					t.Fatal("DB read under lifecycle lock")
				}
				s.lifecycleMu.Unlock()
				if !stage.TryLock() {
					t.Fatal("DB read under Stage lock")
				}
				stage.Unlock()
				if tc.readError {
					return returnQuestExpiries{}, errors.New("offline")
				}
				values := returnQuestExpiries{userID: uid}
				if tc.allowed {
					values.newcomerExpiry.kind = returnQuestExpiryPositiveInfinity
				}
				return values, nil
			}}
			if got := doStageTransfer(s, 1, stage.id); got != tc.want {
				t.Fatalf("got=%t want=%t", got, tc.want)
			}
			if calls != 1 || s.closed.Load() {
				t.Fatal("unexpected lookup count or connection close")
			}
			if tc.want && isReturnWorldQuest(int(tc.quest)) && (s.questDeliveryReceipt.Load() == nil || s.questDeliveryReceipt.Load().questID != tc.quest) {
				t.Fatal("handler did not bind departure")
			}
			if tc.want && !isReturnWorldQuest(int(tc.quest)) && tc.readError && (s.questDeliveryDeparture.Load() != nil || !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(tc.quest))) {
				t.Fatal("ordinary legacy run changed after optional Return reader error")
			}
		})
	}
}

func TestReturnQuestCandidateActualFileRequest(t *testing.T) {
	for _, tc := range []struct {
		name, filename     string
		allowed, readError bool
		want               uint8
	}{
		{"eligible Return", "23352d0", true, false, 0}, {"ineligible Return", "23352d0", false, false, 1},
		{"Return reader error", "23352d0", false, true, 1}, {"ordinary reader error", "23303d0", false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := returnCandidateSession()
			s.server.worldType = 0
			s.server.erupeConfig.BinPath = t.TempDir()
			qdir := filepath.Join(s.server.erupeConfig.BinPath, "quests")
			if err := os.MkdirAll(qdir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(qdir, tc.filename+".bin"), []byte{1, 2, 3, 4}, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.server.userRepo = returnCandidateReader{read: func(_ context.Context, uid uint32) (returnQuestExpiries, error) {
				calls++
				if tc.readError {
					return returnQuestExpiries{}, errors.New("offline")
				}
				v := returnQuestExpiries{userID: uid}
				if tc.allowed {
					v.returnExpiry.kind = returnQuestExpiryPositiveInfinity
				}
				return v, nil
			}}
			handleMsgSysGetFile(s, &mhfpacket.MsgSysGetFile{AckHandle: 42, Filename: tc.filename})
			if got := parseAckFromChannel(t, s); got != tc.want {
				t.Fatalf("ACK=%d want=%d", got, tc.want)
			}
			wantReads := 1
			if !returnQuestFilename(tc.filename) {
				wantReads = 0
			}
			if calls != wantReads || s.closed.Load() {
				t.Fatal("ordinary file caused Return read or connection close")
			}
		})
	}
	// Both requested and season-resolved IDs must be checked, including one
	// protected side paired with an ordinary/nonstandard filename.
	s := returnCandidateSession()
	at := time.Now()
	if s.returnQuestFileAllowed(nil, "23352d0", "23303d0", at) || s.returnQuestFileAllowed(nil, "custom", "23352n2", at) {
		t.Fatal("requested/resolved protection was bypassed")
	}
	expiry := at.Truncate(time.Microsecond).Add(time.Second)
	proof := returnCandidateSnapshot(s, at.Add(-time.Second), returnQuestExpiry{kind: returnQuestExpiryFinite, at: expiry}, returnQuestExpiry{})
	if !s.returnQuestFileAllowed(proof, "23352d0", "23352n2", expiry.Add(-time.Microsecond)) || s.returnQuestFileAllowed(proof, "23352d0", "23352n2", expiry) {
		t.Fatal("reply-time reevaluation reused a past boolean decision")
	}
}

func TestReturnQuestCandidateActualListingPages(t *testing.T) {
	s := returnCandidateSession()
	s.server.worldType = 0
	s.server.questCache = NewQuestCache(3600)
	s.server.questCache.Put(23303, "", make([]byte, 600))
	for _, id := range returnWorldQuestIDs {
		s.server.questCache.Put(id, "", make([]byte, 600))
	}
	s.server.eventRepo = &mockEventRepo{eventQuests: []EventQuest{{ID: 1, QuestID: 23303, QuestType: 28, MaxPlayers: 4, Flags: -1}}}
	calls := 0
	fail := false
	s.server.userRepo = returnCandidateReader{read: func(_ context.Context, uid uint32) (returnQuestExpiries, error) {
		calls++
		if !s.questDeliveryListMu.TryLock() {
			t.Fatal("listing reader held Session listing mutex")
		}
		s.questDeliveryListMu.Unlock()
		if fail {
			return returnQuestExpiries{}, errors.New("offline")
		}
		return returnQuestExpiries{userID: uid, returnExpiry: returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity}}, nil
	}}
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 1, Offset: 0})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("eligible common listing failed")
	}
	if calls != 1 || len(s.questDeliveryList.entries) != 18 {
		t.Fatal("common listing did not include original17 in ordinary world")
	}
	first := s.questDeliveryList
	fail = true
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 2, Offset: 1})
	_ = readAck(t, s)
	if calls != 1 || s.questDeliveryList != first {
		t.Fatal("continuation reread eligibility or changed its frozen pages")
	}
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 3, Offset: 0})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("Return source failure rejected ordinary listing")
	}
	if calls != 2 || len(s.questDeliveryList.entries) != 1 || s.closed.Load() {
		t.Fatal("new enumeration retained old Return rights or closed login")
	}
}

func TestReturnQuestCandidateDuplicateRaceAndLatePending(t *testing.T) {
	s := returnCandidateSession()
	at := time.Now()
	proof := returnCandidateSnapshot(s, at.Add(-time.Second), returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity}, returnQuestExpiry{})
	old := NewStage(deliveryDepartureTestStage)
	old.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(23352, 0)
	if !s.admitQuestDeliveryWithReturnLocked(old, false, at, proof) {
		t.Fatal("initial Return entry failed")
	}
	departure := s.questDeliveryDeparture.Load()
	s.server.userRepo = returnCandidateReader{read: func(context.Context, uint32) (returnQuestExpiries, error) {
		t.Fatal("duplicate reread account")
		return returnQuestExpiries{}, nil
	}}
	prepared, err := s.prepareReturnQuestAdmission(context.Background(), old.id)
	if err != nil || prepared != nil {
		t.Fatal("duplicate preflight did not return reuse sentinel")
	}
	replaced := NewStage(old.id)
	replaced.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(23352, 0)
	if s.admitQuestDeliveryWithReturnLocked(replaced, true, time.Now(), prepared) || s.questDeliveryDeparture.Load() != departure {
		t.Fatal("same name/different Stage borrowed Return admission")
	}
	pending := NewStage(old.id)
	if !s.admitQuestDeliveryWithReturnLocked(pending, false, time.Now(), prepared) {
		t.Fatal("unknown ordinary stage was constrained")
	}
	pending.clients[s] = s.charID
	if s.returnQuestSetupAllowedLocked(pending, 23352, true) {
		t.Fatal("nil preflight sentinel authorized later Return setup")
	}
	if s.closed.Load() {
		t.Fatal("duplicate race closed a connection")
	}
}

func TestReturnQuestCandidateType9OrderAndIndependentFailure(t *testing.T) {
	s := returnCandidateSession()
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(23352, 0)
	s.server.stages.Store(stage.id, stage)
	codeReads, returnReads := 0, 0
	var codeObserved, returnObserved time.Time
	var err error
	s.server.questAccess, err = NewQuestAccessGameplay(gameplayAccessLoaderFunc(func(_ context.Context, uid, cid uint32, _ []int) (*QuestAccessSnapshot, error) {
		codeReads++
		codeObserved = time.Now()
		return newQuestAccessSnapshot(uid, cid, codeObserved, map[int]questAccessGrant{23352: {accountPermanent: true}}, nil), nil
	}), []int{23352})
	if err != nil {
		t.Fatal(err)
	}
	s.server.userRepo = returnCandidateReader{read: func(_ context.Context, uid uint32) (returnQuestExpiries, error) {
		returnReads++
		returnObserved = time.Now()
		if codeReads != 1 {
			t.Fatal("Return observation moved before Type9 load")
		}
		return returnQuestExpiries{userID: uid, newcomerExpiry: returnQuestExpiry{kind: returnQuestExpiryPositiveInfinity}}, nil
	}}
	if !doStageTransfer(s, 1, stage.id) {
		t.Fatal("independent rights did not jointly authorize entry")
	}
	receipt := s.questDeliveryReceipt.Load()
	if codeReads != 1 || returnReads != 1 || receipt.admittedAt.Before(codeObserved) || receipt.admittedAt.Before(returnObserved) || receipt.returnEligibility.observedAt.Before(returnObserved) {
		t.Fatal("actual admission/observation ordering changed")
	}
	bad := returnCandidateSession()
	badStage := NewStage(deliveryDepartureTestStage)
	bad.server.stages.Store(badStage.id, badStage)
	bad.server.questAccess, err = NewQuestAccessGameplay(gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		return nil, errors.New("code unavailable")
	}), []int{40142})
	if err != nil {
		t.Fatal(err)
	}
	bad.server.userRepo = returnCandidateReader{read: func(context.Context, uint32) (returnQuestExpiries, error) {
		t.Fatal("Return read ran after an already failed Type9 preflight")
		return returnQuestExpiries{}, nil
	}}
	if doStageTransfer(bad, 2, badStage.id) || bad.closed.Load() {
		t.Fatal("failed code preflight changed established failure behavior")
	}
}
