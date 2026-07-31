package channelserver

import (
	"errors"
	"testing"
	"time"
)

type mockGuildMissionRepo struct {
	snapshot       GuildMissionSnapshot
	snapshotErr    error
	startedCharID  uint32
	startedDef     GuildMissionDefinition
	startedAt      time.Time
	startResult    GuildMissionRun
	startErr       error
	progressCharID uint32
	progressID     uint32
	progressCount  uint32
	progressAt     time.Time
	progressResult GuildMissionProgressResult
	progressErr    error
	cancelCharID   uint32
	cancelID       uint32
	cancelAt       time.Time
	cancelErr      error
}

func (m *mockGuildMissionRepo) GetSnapshot(_ uint32, _ time.Time) (GuildMissionSnapshot, error) {
	return m.snapshot, m.snapshotErr
}

func (m *mockGuildMissionRepo) Start(charID uint32, def GuildMissionDefinition, now time.Time) (GuildMissionRun, error) {
	m.startedCharID = charID
	m.startedDef = def
	m.startedAt = now
	return m.startResult, m.startErr
}

func (m *mockGuildMissionRepo) AddProgress(charID, missionID, requested uint32, now time.Time) (GuildMissionProgressResult, error) {
	m.progressCharID = charID
	m.progressID = missionID
	m.progressCount = requested
	m.progressAt = now
	return m.progressResult, m.progressErr
}

func (m *mockGuildMissionRepo) Cancel(charID, missionID uint32, now time.Time) error {
	m.cancelCharID = charID
	m.cancelID = missionID
	m.cancelAt = now
	return m.cancelErr
}

func TestGuildMissionServiceRejectsUnknownMission(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	svc := NewGuildMissionService(repo, nil)

	if _, err := svc.Start(10, 999999); !errors.Is(err, ErrGuildMissionUnknown) {
		t.Fatalf("Start error = %v, want ErrGuildMissionUnknown", err)
	}
	if _, err := svc.AddProgress(10, 999999, 1); !errors.Is(err, ErrGuildMissionUnknown) {
		t.Fatalf("AddProgress error = %v, want ErrGuildMissionUnknown", err)
	}
	if err := svc.Cancel(10, 999999); !errors.Is(err, ErrGuildMissionUnknown) {
		t.Fatalf("Cancel error = %v, want ErrGuildMissionUnknown", err)
	}
	if repo.startedCharID != 0 || repo.progressCharID != 0 || repo.cancelCharID != 0 {
		t.Fatal("repository must not be called for an unknown mission ID")
	}
}

func TestGuildMissionServicePassesValidatedDefinitionAndClock(t *testing.T) {
	repo := &mockGuildMissionRepo{}
	svc := NewGuildMissionService(repo, nil)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if _, err := svc.Start(77, 431201); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if repo.startedCharID != 77 {
		t.Fatalf("started char ID = %d, want 77", repo.startedCharID)
	}
	if repo.startedDef.ID != 431201 || repo.startedDef.Quantity != 35 {
		t.Fatalf("unexpected definition: %+v", repo.startedDef)
	}
	if !repo.startedAt.Equal(now) {
		t.Fatalf("started time = %v, want %v", repo.startedAt, now)
	}

	if _, err := svc.AddProgress(77, 431201, 500); err != nil {
		t.Fatalf("AddProgress returned error: %v", err)
	}
	if repo.progressID != 431201 || repo.progressCount != 500 || !repo.progressAt.Equal(now) {
		t.Fatalf("unexpected progress call: id=%d count=%d at=%v", repo.progressID, repo.progressCount, repo.progressAt)
	}

	if err := svc.Cancel(77, 431201); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if repo.cancelID != 431201 || !repo.cancelAt.Equal(now) {
		t.Fatalf("unexpected cancel call: id=%d at=%v", repo.cancelID, repo.cancelAt)
	}
}
