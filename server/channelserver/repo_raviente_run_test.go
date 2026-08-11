package channelserver

import (
	"testing"
	"time"
)

func TestRavienteRunRepositoryPersistsWholeRunSnapshot(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewRavienteRunRepository(db)
	if _, err := db.Exec(`
		INSERT INTO event_quests (quest_type, quest_id)
		VALUES ($1, 54751), ($2, 55596), ($3, 55796), (1, 12345)
	`, QuestTypeBerserkRaviente, QuestTypeExtremeRaviente, QuestTypeSmallBerserkRavi); err != nil {
		t.Fatalf("insert event quests: %v", err)
	}

	for questID, want := range map[uint16]RavienteRunKind{
		54751: RavienteRunKindBerserk,
		55596: RavienteRunKindExtreme,
		55796: RavienteRunKindSmall,
	} {
		got, ok, err := repo.ResolveQuestKind(questID)
		if err != nil || !ok || got != want {
			t.Fatalf("ResolveQuestKind(%d) = (%q, %t, %v), want %q", questID, got, ok, err, want)
		}
	}
	if _, ok, err := repo.ResolveQuestKind(12345); err != nil || ok {
		t.Fatalf("normal quest resolution = (ok=%t, err=%v), want ignored", ok, err)
	}

	started := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	runID, err := repo.Start("port:53314", 7, started)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	alice := RavienteRunParticipant{CharacterID: 10, CharacterName: "Alice", FirstSeenAt: started.Add(time.Second)}
	if err := repo.AddParticipant(runID, RavienteRunKindBerserk, alice); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	// The completion snapshot re-upserts Alice and adds Bob atomically.
	bob := RavienteRunParticipant{CharacterID: 20, CharacterName: "Bob", FirstSeenAt: started.Add(2 * time.Second)}
	if err := repo.Complete(runID, RavienteRunKindBerserk, started.Add(time.Minute), time.Minute, []RavienteRunParticipant{alice, bob}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Repeated completion must be harmless and must not alter the terminal row.
	if err := repo.Complete(runID, RavienteRunKindBerserk, started.Add(2*time.Minute), 2*time.Minute, []RavienteRunParticipant{alice, bob}); err != nil {
		t.Fatalf("idempotent Complete: %v", err)
	}

	var status, kind string
	var durationMS int64
	if err := db.QueryRow(`
		SELECT status, event_kind, duration_ms
		FROM raviente_runs WHERE id = $1
	`, runID).Scan(&status, &kind, &durationMS); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if status != "completed" || kind != "berserk" || durationMS != 60_000 {
		t.Fatalf("run = status %q kind %q duration %d", status, kind, durationMS)
	}

	var participantCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM raviente_run_participants WHERE run_id = $1
	`, runID).Scan(&participantCount); err != nil {
		t.Fatalf("query participants: %v", err)
	}
	if participantCount != 2 {
		t.Fatalf("participant count = %d, want 2", participantCount)
	}
}

func TestRavienteRunRepositoryRestartAbortsOnlyActiveRows(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewRavienteRunRepository(db)
	started := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	runID, err := repo.Start("port:53314", 7, started)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := repo.AbortActive("port:53314", started.Add(time.Minute), "server_restart"); err != nil {
		t.Fatalf("AbortActive: %v", err)
	}
	var status, reason string
	if err := db.QueryRow(`SELECT status, end_reason FROM raviente_runs WHERE id = $1`, runID).Scan(&status, &reason); err != nil {
		t.Fatalf("query aborted run: %v", err)
	}
	if status != "aborted" || reason != "server_restart" {
		t.Fatalf("restart run = status %q reason %q", status, reason)
	}
}
