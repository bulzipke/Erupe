package channelserver

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

type gameplayAccessLoaderFunc func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error)

func (f gameplayAccessLoaderFunc) Load(ctx context.Context, userID, characterID uint32, questIDs []int) (*QuestAccessSnapshot, error) {
	return f(ctx, userID, characterID, questIDs)
}

func gameplayAccessTestSession(t *testing.T, loader QuestAccessGameplayLoader, questIDs ...int) *Session {
	t.Helper()
	gameplay, err := NewQuestAccessGameplay(loader, questIDs)
	if err != nil {
		t.Fatal(err)
	}
	srv := createMockServer()
	srv.questAccess = gameplay
	s := createMockSession(11, srv)
	s.userID = 7
	return s
}

func gameplayAccessAwait(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func gameplayAccessAwaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for quest access refresh")
		return nil
	}
}

func TestQuestAccessGameplayConstructorBoundsAndDetachedRoster(t *testing.T) {
	loader := gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		t.Fatal("constructor performed a load")
		return nil, nil
	})
	if got, err := NewQuestAccessGameplay(nil, []int{40142}); got != nil || err == nil {
		t.Fatal("nil loader was accepted")
	}
	for _, id := range []int{-1, 0, math.MaxUint16 + 1} {
		if got, err := NewQuestAccessGameplay(loader, []int{id}); got != nil || err == nil {
			t.Fatalf("invalid target %d was accepted", id)
		}
	}
	bounded := make([]int, maxQuestAccessTargets)
	for i := range bounded {
		bounded[i] = 40142
	}
	if got, err := NewQuestAccessGameplay(loader, bounded); err != nil || !slices.Equal(got.questIDs, []int{40142}) {
		t.Fatal("maximum bounded input was not normalized", got, err)
	}
	if got, err := NewQuestAccessGameplay(loader, append(bounded, 40142)); got != nil || err == nil {
		t.Fatal("oversized input was accepted after duplicate compaction")
	}
	empty, err := NewQuestAccessGameplay(loader, nil)
	if err != nil || len(empty.questIDs) != 0 || empty.requires(40142) {
		t.Fatal("empty roster was not preserved", empty, err)
	}
	input := []int{40143, 40142, 40143}
	gameplay, err := NewQuestAccessGameplay(loader, input)
	if err != nil || !slices.Equal(gameplay.questIDs, []int{40142, 40143}) {
		t.Fatal("targets were not sorted and compacted", gameplay, err)
	}
	if !slices.Equal(input, []int{40143, 40142, 40143}) {
		t.Fatal("constructor sorted caller-owned input")
	}
	input[0], input[1], input[2] = 40218, 40218, 40218
	if !gameplay.requires(40142) || !gameplay.requires(40143) || gameplay.requires(40218) || !slices.Equal(gameplay.questIDs, []int{40142, 40143}) {
		t.Fatal("caller mutation changed the protected roster")
	}
	var disabled *QuestAccessGameplay
	if disabled.requires(40142) {
		t.Fatal("nil gameplay reported a protected quest")
	}
}

func TestQuestAccessGameplayRefreshUsesDetachedTargetsOutsideGameplayLocks(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	stage := NewStage("sl2Qs200p0a1u0")
	var s *Session
	var published *QuestAccessSnapshot
	calls := 0
	loader := gameplayAccessLoaderFunc(func(_ context.Context, userID, characterID uint32, questIDs []int) (*QuestAccessSnapshot, error) {
		calls++
		if userID != 7 || characterID != 11 || !slices.Equal(questIDs, []int{40142, 40143}) {
			return nil, errors.New("loader received an unauthenticated identity or changed targets")
		}
		if !s.lifecycleMu.TryLock() {
			return nil, errors.New("loader ran with lifecycleMu held")
		}
		s.lifecycleMu.Unlock()
		if !s.Mutex.TryLock() {
			return nil, errors.New("loader ran with Session mutex held")
		}
		s.Mutex.Unlock()
		if !s.server.Mutex.TryLock() {
			return nil, errors.New("loader ran with Server mutex held")
		}
		s.server.Mutex.Unlock()
		if !stage.TryLock() {
			return nil, errors.New("loader ran with Stage lock held")
		}
		stage.Unlock()
		questIDs[0] = 40218
		published = newQuestAccessSnapshot(userID, characterID, now, map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
		return published, nil
	})
	s = gameplayAccessTestSession(t, loader, 40143, 40142)
	s.stage = stage
	s.server.stages.Store(stage.id, stage)
	for i := 0; i < 2; i++ {
		if err := s.refreshQuestAccess(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s.questAccessSnapshot.Load() != published {
			t.Fatal("refresh did not publish the exact immutable snapshot")
		}
	}
	if calls != 2 || !slices.Equal(s.server.questAccess.questIDs, []int{40142, 40143}) || s.server.questAccess.requires(40218) {
		t.Fatal("loader mutation changed the frozen protected roster")
	}
}

func TestQuestAccessGameplayInvalidRefreshInvalidatesOnlyCurrentCache(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		snapshot *QuestAccessSnapshot
		err      error
	}{
		{name: "nil snapshot"},
		{name: "wrong user", snapshot: newQuestAccessSnapshot(8, 11, now, nil, nil)},
		{name: "wrong character", snapshot: newQuestAccessSnapshot(7, 12, now, nil, nil)},
		{name: "zero observation", snapshot: newQuestAccessSnapshot(7, 11, time.Time{}, nil, nil)},
		{name: "loader error", err: errors.New("authority unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
				return tc.snapshot, tc.err
			}), 40142)
			s.questAccessSnapshot.Store(newQuestAccessSnapshot(7, 11, now.Add(-time.Minute), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil))
			if err := s.refreshQuestAccess(context.Background()); err == nil || s.questAccessSnapshot.Load() != nil {
				t.Fatal("invalid refresh retained stale authority", err)
			}
		})
	}
}

func TestQuestAccessGameplayDegradedAccountReadPreservesLegacy(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	warning := errors.New("account source unavailable")
	snapshot := newQuestAccessSnapshot(7, 11, now, map[int]questAccessGrant{
		40142: {legacyExpiry: expiry},
		40143: {},
	}, warning)
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		return snapshot, nil
	}), 40142, 40143)
	if err := s.refreshQuestAccess(context.Background()); err != nil || s.questAccessSnapshot.Load() != snapshot {
		t.Fatal("degraded legacy snapshot was not published", err)
	}
	if snapshot.AccountReadError() == nil || !s.allowsQuestAccess(40142, expiry.Add(-time.Nanosecond)) || s.allowsQuestAccess(40142, expiry) || s.allowsQuestAccess(40143, now) {
		t.Fatal("degraded account branch bypassed or discarded independent legacy authority")
	}
}

func TestQuestAccessGameplayOlderCompletionCannotReplaceNewerSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	older := newQuestAccessSnapshot(7, 11, now, map[int]questAccessGrant{40142: {}}, nil)
	newer := newQuestAccessSnapshot(7, 11, now.Add(time.Second), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var calls atomic.Int32
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return older, nil
		}
		return newer, nil
	}), 40142)
	done := make(chan error, 1)
	go func() { done <- s.refreshQuestAccess(context.Background()) }()
	gameplayAccessAwait(t, started, "older authority read")
	if err := s.refreshQuestAccess(context.Background()); err != nil || s.questAccessSnapshot.Load() != newer {
		t.Fatal("newer authority read was not published", err)
	}
	close(release)
	if err := gameplayAccessAwaitError(t, done); err != nil || s.questAccessSnapshot.Load() != newer {
		t.Fatal("slow older completion replaced newer authority", err)
	}
}

func TestQuestAccessGameplayFailedOlderReadCannotEraseNewerSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	previous := newQuestAccessSnapshot(7, 11, now, nil, nil)
	newer := newQuestAccessSnapshot(7, 11, now.Add(time.Second), map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	failed := errors.New("older authority read failed")
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var calls atomic.Int32
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return nil, failed
		}
		return newer, nil
	}), 40142)
	s.questAccessSnapshot.Store(previous)
	done := make(chan error, 1)
	go func() { done <- s.refreshQuestAccess(context.Background()) }()
	gameplayAccessAwait(t, started, "older failing authority read")
	if err := s.refreshQuestAccess(context.Background()); err != nil || s.questAccessSnapshot.Load() != newer {
		t.Fatal("newer authority read was not published", err)
	}
	close(release)
	if err := gameplayAccessAwaitError(t, done); !errors.Is(err, failed) || s.questAccessSnapshot.Load() != newer {
		t.Fatal("failed older read erased newer authority", err)
	}
}

func TestQuestAccessGameplayAdmissionPreflightDuplicateSentinelAndFreshSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	snapshot := newQuestAccessSnapshot(7, 11, now, map[int]questAccessGrant{40142: {accountPermanent: true}}, nil)
	calls := 0
	s := gameplayAccessTestSession(t, gameplayAccessLoaderFunc(func(context.Context, uint32, uint32, []int) (*QuestAccessSnapshot, error) {
		calls++
		return snapshot, nil
	}), 40142)
	stage := NewStage("sl2Qs200p0a1u0")
	s.questDeliveryDeparture.Store(&questDeliveryDeparture{stage: stage, admittedAt: now, questID: 40142})
	if prepared, err := s.prepareQuestAccessAdmission(context.Background(), stage.id); err != nil || prepared != nil || calls != 0 {
		t.Fatal("duplicate admission refreshed authority instead of returning its sentinel", prepared, err, calls)
	}
	if prepared, err := s.prepareQuestAccessAdmission(context.Background(), "sl1Ns200p0a0u0"); err != nil || prepared != nil || calls != 0 {
		t.Fatal("town admission performed an authority read", prepared, err, calls)
	}
	if prepared, err := s.prepareQuestAccessAdmission(context.Background(), "sl2Qs201p0a1u0"); err != nil || prepared != snapshot || s.questAccessSnapshot.Load() != prepared || calls != 1 {
		t.Fatal("fresh admission did not return its exact published snapshot", prepared, err, calls)
	}
}

func TestQuestAccessGameplayDisabledRefreshAndPreflightDoNotLoad(t *testing.T) {
	var nilSession *Session
	sessions := []*Session{nilSession, {}, createMockSession(11, createMockServer())}
	for _, s := range sessions {
		if err := s.refreshQuestAccess(context.Background()); err != nil {
			t.Fatal("disabled refresh returned an error", err)
		}
		if prepared, err := s.prepareQuestAccessAdmission(context.Background(), "sl2Qs200p0a1u0"); prepared != nil || err != nil {
			t.Fatal("disabled preflight returned authority", prepared, err)
		}
	}
}
