package channelserver

import (
	"testing"
	"time"
)

func TestStampPeriodStart(t *testing.T) {
	korea := time.FixedZone("KST", 9*60*60)
	noon := time.Date(2026, 12, 1, 12, 0, 0, 0, korea)
	for _, tc := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before noon", noon.Add(-time.Nanosecond), noon.AddDate(0, 0, -1)},
		{"exactly noon", noon, noon},
		{"just after noon", noon.Add(time.Nanosecond), noon},
		{"after midnight", noon.Add(13 * time.Hour), noon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stampPeriodStart(tc.now); !got.Equal(tc.want) {
				t.Fatalf("stampPeriodStart(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestOnlineDailyStampsUseCourseRights(t *testing.T) {
	server := createMockServer()
	session := createMockSession(42, server)
	period := stampPeriodStart(TimeAdjusted())
	var claimed string
	server.stampRepo = &mockStampRepoForItems{
		claimDaily: func(id uint32, kind string, start time.Time) (uint16, uint16, bool, error) {
			if id != 42 || !start.Equal(period) {
				t.Fatalf("unexpected claim for %d at %v", id, start)
			}
			claimed += kind
			return 1, 0, true, nil
		},
	}
	for _, tc := range []struct {
		rights uint32
		want   string
	}{
		{1 << 1, ""},
		{1 << 2, "hl"},
		{1 << 3, "ex"},
		{1 << 26, "hlex"},
		{1 << 27, "hl"},
		{1 << 28, "ex"},
	} {
		claimed = ""
		session.dailyStampRights.Store(tc.rights)
		if !session.claimOnlineDailyStamps(period) || claimed != tc.want {
			t.Errorf("claims for rights %#x = %q, want %q", tc.rights, claimed, tc.want)
		}
	}
}

func TestOnlineDailyStampsClaimWhenTimerFires(t *testing.T) {
	server := createMockServer()
	session := createMockSession(42, server)
	session.done = make(chan struct{})
	session.dailyStampRights.Store(1 << 3)
	claimed := make(chan time.Time, 1)
	server.stampRepo = &mockStampRepoForItems{
		claimDaily: func(_ uint32, _ string, start time.Time) (uint16, uint16, bool, error) {
			claimed <- start
			return 1, 0, true, nil
		},
	}
	firstNoon := stampPeriodStart(TimeAdjusted())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		session.runDailyStamps(firstNoon)
	}()
	t.Cleanup(func() {
		close(session.done)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("stamp worker did not stop on disconnect")
		}
	})
	select {
	case got := <-claimed:
		if !got.Equal(stampPeriodStart(TimeAdjusted())) && !got.Equal(firstNoon) {
			t.Fatalf("online claim used %v, want the current noon", got)
		}
	case <-time.After(time.Second):
		t.Fatal("stamp worker did not claim at the period boundary")
	}
}
