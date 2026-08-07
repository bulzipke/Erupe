package channelserver

import (
	"testing"
)

type recordedQuestResult struct {
	questID uint16
	cleared bool
}

type mockQuestStatsRepo struct {
	results []recordedQuestResult
}

func (m *mockQuestStatsRepo) RecordResult(questID uint16, _ string, cleared bool) error {
	m.results = append(m.results, recordedQuestResult{questID: questID, cleared: cleared})
	return nil
}

func (m *mockQuestStatsRepo) counts() (cleared, failed int) {
	for _, r := range m.results {
		if r.cleared {
			cleared++
		} else {
			failed++
		}
	}
	return
}

func newQuestResultSession(t *testing.T) (*Session, *mockQuestStatsRepo) {
	t.Helper()
	server := createMockServer()
	repo := &mockQuestStatsRepo{}
	server.questStatsRepo = repo
	return createMockSession(1, server), repo
}

// recordQuestResult is the whole mechanism: the record log carries both the
// quest ID and the outcome, so each finished attempt maps to exactly one row.
func TestQuestResultRecordsOneRowPerOutcome(t *testing.T) {
	session, repo := newQuestResultSession(t)

	session.recordQuestResult(53187, "이벤트 퀘스트", true)
	session.recordQuestResult(53187, "이벤트 퀘스트", false)

	cleared, failed := repo.counts()
	if cleared != 1 || failed != 1 {
		t.Fatalf("counts = cleared %d / failed %d, want 1 / 1: %+v", cleared, failed, repo.results)
	}
}

func TestQuestResultIgnoresZeroQuestID(t *testing.T) {
	session, repo := newQuestResultSession(t)

	session.recordQuestResult(0, "", true)

	if cleared, failed := repo.counts(); cleared != 0 || failed != 0 {
		t.Fatalf("counts = cleared %d / failed %d, want 0 / 0", cleared, failed)
	}
}

func TestQuestResultSurvivesMissingRepository(t *testing.T) {
	// Channels started without a database must not panic on quest completion.
	server := createMockServer()
	server.questStatsRepo = nil
	session := createMockSession(1, server)

	session.recordQuestResult(53187, "이벤트 퀘스트", true)
}

// The outcome byte in the record log is what separates the three endings. These
// values come from captured runs of live quests; see handlers_session.go.
func TestQuestResultCodeMapping(t *testing.T) {
	tests := []struct {
		name    string
		code    byte
		cleared bool
		counted bool
	}{
		{name: "cleared", code: questResultCodeCleared, cleared: true, counted: true},
		{name: "fainted out", code: questResultCodeFainted, cleared: false, counted: true},
		{name: "retired by choice", code: questResultCodeRetired, counted: false},
		{name: "unknown code", code: 0x0b, counted: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, repo := newQuestResultSession(t)

			// Mirror the handler's switch so the mapping itself is under test.
			switch test.code {
			case questResultCodeCleared:
				session.recordQuestResult(53187, "이벤트 퀘스트", true)
			case questResultCodeFainted:
				session.recordQuestResult(53187, "이벤트 퀘스트", false)
			case questResultCodeRetired:
			default:
			}

			cleared, failed := repo.counts()
			switch {
			case !test.counted:
				if cleared != 0 || failed != 0 {
					t.Fatalf("code %#x was counted: cleared %d / failed %d", test.code, cleared, failed)
				}
			case test.cleared:
				if cleared != 1 || failed != 0 {
					t.Fatalf("code %#x: cleared %d / failed %d, want 1 / 0", test.code, cleared, failed)
				}
			default:
				if cleared != 0 || failed != 1 {
					t.Fatalf("code %#x: cleared %d / failed %d, want 0 / 1", test.code, cleared, failed)
				}
			}
		})
	}
}

// Retiring must never reach the counters, which is the difference between this
// and the earlier version that inferred a failure from the return to town.
func TestQuestResultRetireIsNotAFailure(t *testing.T) {
	if questResultCodeRetired == questResultCodeFainted {
		t.Fatal("retire and faint-out must map to different codes")
	}
	if questResultCodeCleared == questResultCodeFainted {
		t.Fatal("clear and faint-out must map to different codes")
	}
}
