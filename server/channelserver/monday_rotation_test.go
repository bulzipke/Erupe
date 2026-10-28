package channelserver

import (
	"testing"
	"time"

	cfg "erupe-ce/config"
)

func TestMondayNoonRotationBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 26, 12, 0, 0, 0, divaLocation)
	for _, tc := range []struct {
		at, want time.Time
	}{
		{start.Add(-time.Nanosecond), start.AddDate(0, 0, -7)},
		{start, start},
		{start.Add(48 * time.Hour).UTC(), start},
		{start.AddDate(0, 0, 7).Add(-time.Nanosecond), start},
		{start.AddDate(0, 0, 7), start.AddDate(0, 0, 7)},
	} {
		if got := rotationMondayNoon(tc.at); !got.Equal(tc.want) {
			t.Fatalf("at %v: got %v, want %v", tc.at, got, tc.want)
		}
		if got := divaNextNormalStart(DivaEvent{}, tc.at); !got.Equal(tc.want) {
			t.Fatalf("Diva bootstrap at %v: got %v, want %v", tc.at, got, tc.want)
		}
		got, err := towerInitialStart(tc.at, cfg.TowerRotationOptions{ActiveDays: 21, CycleDays: 21})
		if err != nil || !got.Equal(tc.want) {
			t.Fatalf("Tower bootstrap at %v: got %v, want %v: %v", tc.at, got, tc.want, err)
		}
	}
}

func TestDivaMondayRolloverWithoutRequests(t *testing.T) {
	r, _ := setupDivaRepo(t)
	s := createMockServer()
	s.divaRepo = r
	s.erupeConfig.DebugOptions.DivaOverride = -1
	if err := s.initializeDivaRotation(); err != nil {
		t.Fatal(err)
	}
	events, err := r.GetEvents()
	if err != nil || len(events) != 1 || events[0].StartTime != uint32(rotationMondayNoon(TimeAdjusted()).Unix()) {
		t.Fatalf("startup without an NPC request: %+v %v", events, err)
	}
	first := events[0]
	next, err := r.EnsureDivaEvent(first.phaseTime(5).Add(5*24*time.Hour), -1)
	if err != nil || next.ID == first.ID || next.StartTime != uint32(first.phaseTime(5).Unix()) {
		t.Fatalf("late worker drifted: %+v %v", next, err)
	}
}
