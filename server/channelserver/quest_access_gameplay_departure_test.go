package channelserver

import (
	"context"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type admissionAccessLoaderFunc func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error)

func (f admissionAccessLoaderFunc) Load(ctx context.Context, userID, characterID uint32, ids []int) (*QuestAccessSnapshot, error) {
	return f(ctx, userID, characterID, ids)
}

func accessDepartureTestServer(t *testing.T, ids ...int) *Server {
	t.Helper()
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	var err error
	srv.questAccess, err = NewQuestAccessGameplay(admissionAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		t.Error("unexpected access database read during pinned admission/setup/result")
		return nil, fmt.Errorf("unexpected read")
	}), ids)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func accessDepartureTestSession(id uint32, srv *Server) *Session {
	s := createMockSession(id, srv)
	s.userID = 100 + id
	return s
}

func accessDepartureTestSnapshot(s *Session, observed time.Time, qid int, grant questAccessGrant) *QuestAccessSnapshot {
	return newQuestAccessSnapshot(s.userID, s.charID, observed, map[int]questAccessGrant{qid: grant}, nil)
}

func TestQuestAccessGameplayAdmissionWithoutPublicationManifest(t *testing.T) {
	at := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		grant   questAccessGrant
		invalid string
		want    bool
	}{
		{name: "account permanent", grant: questAccessGrant{accountPermanent: true}, want: true},
		{name: "legacy independent", grant: questAccessGrant{legacyExpiry: at.Add(time.Nanosecond)}, want: true},
		{name: "legacy expiry exclusive", grant: questAccessGrant{legacyExpiry: at}},
		{name: "no authority"},
		{name: "missing snapshot", invalid: "nil"},
		{name: "wrong account", grant: questAccessGrant{accountPermanent: true}, invalid: "user"},
		{name: "wrong character", grant: questAccessGrant{accountPermanent: true}, invalid: "char"},
		{name: "future observation", grant: questAccessGrant{accountPermanent: true}, invalid: "future"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := accessDepartureTestServer(t, 40142)
			s := accessDepartureTestSession(1, srv)
			snapshot := accessDepartureTestSnapshot(s, at.Add(-time.Second), 40142, tc.grant)
			switch tc.invalid {
			case "nil":
				snapshot = nil
			case "user":
				snapshot = newQuestAccessSnapshot(s.userID+1, s.charID, at.Add(-time.Second), snapshot.grants, nil)
			case "char":
				snapshot = newQuestAccessSnapshot(s.userID, s.charID+1, at.Add(-time.Second), snapshot.grants, nil)
			case "future":
				snapshot = accessDepartureTestSnapshot(s, at.Add(time.Nanosecond), 40142, tc.grant)
			}
			s.questAccessSnapshot.Store(snapshot)
			prior := &questDeliveryReceipt{questID: 23303, admittedAt: at.Add(-time.Hour)}
			s.questDeliveryReceipt.Store(prior)
			stage := NewStage(deliveryDepartureTestStage)
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40142, 0)
			s.lifecycleMu.Lock()
			stage.Lock()
			got := s.admitQuestDeliveryLocked(stage, false, at)
			stage.Unlock()
			s.lifecycleMu.Unlock()
			if got != tc.want {
				t.Fatalf("admission = %t, want %t", got, tc.want)
			}
			if got {
				departure, receipt := s.questDeliveryDeparture.Load(), s.questDeliveryReceipt.Load()
				if departure.accessSnapshot != snapshot || receipt.accessSnapshot != snapshot || departure.receipt != receipt {
					t.Fatal("admission did not pin the exact supplied authority")
				}
			} else if s.questDeliveryDeparture.Load() != nil || s.questDeliveryReceipt.Load() != prior {
				t.Fatal("rejection changed the earlier run receipt")
			}
		})
	}
}

func TestQuestAccessGameplayLateSetupCannotUseLaterPurchase(t *testing.T) {
	at := time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC)
	for _, allowedAtEntry := range []bool{false, true} {
		t.Run(fmt.Sprintf("entry-authorized=%t", allowedAtEntry), func(t *testing.T) {
			srv := accessDepartureTestServer(t, 40142)
			srv.weaponUsageRepo = &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 4}}
			stage := NewStage(deliveryDepartureTestStage)
			srv.stages.Store(stage.id, stage)
			s := accessDepartureTestSession(1, srv)
			grant := questAccessGrant{}
			if allowedAtEntry {
				grant.legacyExpiry = at.Add(time.Second)
			}
			original := accessDepartureTestSnapshot(s, at.Add(-time.Second), 40142, grant)
			s.questAccessSnapshot.Store(original)
			pinDeliveryDepartureParticipant(t, s, stage, at)
			if s.allowsQuestDeliverySave() || s.allowsQuestDeliveryRecord(deliveryResultTestData(40142)) {
				t.Fatal("pending setup authorized a result")
			}
			// The current cache is deliberately the opposite of entry authority.
			s.questAccessSnapshot.Store(accessDepartureTestSnapshot(s, at.Add(time.Hour), 40142, questAccessGrant{accountPermanent: !allowedAtEntry}))
			handleMsgSysSetStageBinary(s, &mhfpacket.MsgSysSetStageBinary{
				StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(40142, 0),
			})
			if s.closed.Load() == allowedAtEntry {
				t.Fatal("late setup used current authority instead of admission authority")
			}
			if allowedAtEntry {
				receipt := s.questDeliveryReceipt.Load()
				if receipt.questID != 40142 || receipt.accessSnapshot != original || !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(40142)) {
					t.Fatal("valid legacy entry did not survive late setup/expiry")
				}
			}
		})
	}
}

func TestQuestAccessGameplayHostAndGuestHaveIndependentAuthority(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	srv := accessDepartureTestServer(t, 40142)
	repo := &mockWeaponUsageRepo{weaponTypes: map[uint32]uint8{1: 4, 2: 13}}
	srv.weaponUsageRepo = repo
	stage := NewStage(deliveryDepartureTestStage)
	srv.stages.Store(stage.id, stage)
	host, guest := accessDepartureTestSession(1, srv), accessDepartureTestSession(2, srv)
	host.questAccessSnapshot.Store(accessDepartureTestSnapshot(host, at.Add(-time.Second), 40142, questAccessGrant{accountPermanent: true}))
	guest.questAccessSnapshot.Store(accessDepartureTestSnapshot(guest, at.Add(-time.Second), 40142, questAccessGrant{}))
	pinDeliveryDepartureParticipant(t, host, stage, at)
	pinDeliveryDepartureParticipant(t, guest, stage, at)
	handleMsgSysSetStageBinary(host, &mhfpacket.MsgSysSetStageBinary{
		StageID: stage.id, BinaryType0: 1, BinaryType1: 3, RawDataPayload: questRunStagePayload(40142, 0),
	})
	if host.closed.Load() || !guest.closed.Load() || guest.stage != nil {
		t.Fatal("guest borrowed the host's account right or the valid host was removed")
	}
	if repo.callCount(1) != 1 || repo.callCount(2) != 0 {
		t.Fatal("rejected guest generated quest usage side effects")
	}
}

func TestQuestAccessGameplayResultUsesPinnedValidStart(t *testing.T) {
	at := time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC)
	srv := accessDepartureTestServer(t, 40142, 40143)
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40142, 0)
	srv.stages.Store(stage.id, stage)
	s := accessDepartureTestSession(1, srv)
	original := accessDepartureTestSnapshot(s, at.Add(-time.Second), 40142, questAccessGrant{legacyExpiry: at.Add(time.Second)})
	s.questAccessSnapshot.Store(original)
	pinDeliveryDepartureParticipant(t, s, stage, at)
	receipt := s.questDeliveryReceipt.Load()
	s.questAccessSnapshot.Store(nil)
	if !doStageTransfer(s, 1, "sl1Ns200p0a0u0") {
		t.Fatal("town transfer failed")
	}
	if s.questDeliveryReceipt.Load() != receipt || s.questDeliveryDeparture.Load() != nil {
		t.Fatal("town changed the immutable result receipt")
	}
	if !s.allowsQuestDeliverySave() || !s.allowsQuestDeliveryRecord(deliveryResultTestData(40142)) {
		t.Fatal("a valid start lost result authority after expiry/town/cache removal")
	}
	if s.allowsQuestDeliveryRecord(deliveryResultTestData(40143)) || s.allowsQuestDeliveryRecord(deliveryResultTestData(23303)) {
		t.Fatal("protected run authorized a different result ID")
	}
	bad := accessDepartureTestSnapshot(s, at.Add(-time.Second), 40142, questAccessGrant{})
	s.questDeliveryReceipt.Store(&questDeliveryReceipt{questID: 40142, admittedAt: at, accessSnapshot: bad})
	s.questAccessSnapshot.Store(accessDepartureTestSnapshot(s, at.Add(time.Hour), 40142, questAccessGrant{accountPermanent: true}))
	if s.allowsQuestDeliverySave() || s.allowsQuestDeliveryRecord(deliveryResultTestData(40142)) {
		t.Fatal("a later purchase retroactively authorized an invalid start")
	}
}

func TestQuestAccessGameplayPublicationAndAuthorityAreBothRequired(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		world string
		at    time.Time
		owned bool
		want  bool
	}{
		{"both", "events", start, true, true},
		{"wrong world", "other", start, true, false},
		{"expired publication", "events", start.Add(time.Hour), true, false},
		{"no authority", "events", start, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := deliveryDepartureTestServer(t, start, start.Add(time.Hour))
			srv.worldName = tc.world
			var err error
			srv.questAccess, err = NewQuestAccessGameplay(admissionAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
				return nil, fmt.Errorf("unexpected read")
			}), []int{40221})
			if err != nil {
				t.Fatal(err)
			}
			s := accessDepartureTestSession(1, srv)
			s.questAccessSnapshot.Store(accessDepartureTestSnapshot(s, start.Add(-time.Second), 40221, questAccessGrant{accountPermanent: tc.owned}))
			stage := NewStage(deliveryDepartureTestStage)
			stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40221, 0)
			s.lifecycleMu.Lock()
			stage.Lock()
			got := s.admitQuestDeliveryLocked(stage, false, tc.at)
			stage.Unlock()
			s.lifecycleMu.Unlock()
			if got != tc.want {
				t.Fatalf("admission=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestQuestAccessGameplayDuplicatePreservesPinnedEntryAndPreflightRaceFailsClosed(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := accessDepartureTestServer(t, 40142)
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40142, 0)
	srv.stages.Store(stage.id, stage)
	s := accessDepartureTestSession(1, srv)
	s.questAccessSnapshot.Store(accessDepartureTestSnapshot(s, at.Add(-time.Second), 40142, questAccessGrant{legacyExpiry: at.Add(time.Second)}))
	pinDeliveryDepartureParticipant(t, s, stage, at)
	departure, receipt := s.questDeliveryDeparture.Load(), s.questDeliveryReceipt.Load()
	s.questAccessSnapshot.Store(nil)
	if !doStageTransfer(s, 1, stage.id) || s.questDeliveryDeparture.Load() != departure || s.questDeliveryReceipt.Load() != receipt {
		t.Fatal("duplicate transfer reread authority or replaced an admitted run")
	}
	prepared, err := s.prepareQuestAccessAdmission(context.Background(), stage.id)
	if err != nil || prepared != nil {
		t.Fatal("duplicate preflight did not return its reuse sentinel", prepared, err)
	}
	replacement := NewStage(stage.id)
	replacement.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40142, 0)
	s.lifecycleMu.Lock()
	replacement.Lock()
	allowed := s.admitQuestDeliveryLocked(replacement, false, time.Now(), prepared)
	replacement.Unlock()
	s.lifecycleMu.Unlock()
	if allowed || s.questDeliveryDeparture.Load() != departure {
		t.Fatal("duplicate preflight authorized a different/new stage")
	}
}

func TestQuestAccessGameplayTransferLoadsOutsideAllGameplayLocks(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	stage := NewStage(deliveryDepartureTestStage)
	stage.rawBinaryData[stageBinaryKey{1, 3}] = questRunStagePayload(40142, 0)
	srv.stages.Store(stage.id, stage)
	s := accessDepartureTestSession(1, srv)
	calls := 0
	loader := admissionAccessLoaderFunc(func(_ context.Context, user, char uint32, _ []int) (*QuestAccessSnapshot, error) {
		calls++
		if !s.lifecycleMu.TryLock() {
			t.Error("load held lifecycleMu")
		} else {
			s.lifecycleMu.Unlock()
		}
		if !s.TryLock() {
			t.Error("load held Session mutex")
		} else {
			s.Unlock()
		}
		if !srv.TryLock() {
			t.Error("load held Server mutex")
		} else {
			srv.Unlock()
		}
		if !stage.TryLock() {
			t.Error("load held Stage lock")
		} else {
			stage.Unlock()
		}
		return newQuestAccessSnapshot(user, char, time.Now().Add(-time.Second), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil), nil
	})
	var err error
	srv.questAccess, err = NewQuestAccessGameplay(loader, []int{40142})
	if err != nil {
		t.Fatal(err)
	}
	if !doStageTransfer(s, 1, stage.id) || calls != 1 {
		t.Fatalf("transfer failed or loaded %d times", calls)
	}
	if receipt := s.questDeliveryReceipt.Load(); receipt == nil || receipt.accessSnapshot != s.questAccessSnapshot.Load() {
		t.Fatal("transfer did not pin its prepared snapshot")
	}
}
