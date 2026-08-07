package channelserver

import (
	"testing"
)

func TestQuestStatsRecordResult(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	repo := NewQuestStatsRepository(db)
	const questID = 60001
	if _, err := db.Exec("DELETE FROM quest_result_stats WHERE quest_id = $1", questID); err != nil {
		t.Fatalf("clear fixture: %v", err)
	}
	defer func() { _, _ = db.Exec("DELETE FROM quest_result_stats WHERE quest_id = $1", questID) }()

	read := func() (cleared, failed int64, name string) {
		t.Helper()
		if err := db.QueryRow(
			"SELECT cleared, failed, quest_name FROM quest_result_stats WHERE quest_id = $1", questID,
		).Scan(&cleared, &failed, &name); err != nil {
			t.Fatalf("read counters: %v", err)
		}
		return
	}

	if err := repo.RecordResult(questID, "천이종 리오레우스", true); err != nil {
		t.Fatalf("record clear: %v", err)
	}
	if cleared, failed, name := read(); cleared != 1 || failed != 0 || name != "천이종 리오레우스" {
		t.Fatalf("after first clear: cleared=%d failed=%d name=%q", cleared, failed, name)
	}

	// The two counters must accumulate independently.
	if err := repo.RecordResult(questID, "천이종 리오레우스", true); err != nil {
		t.Fatalf("record second clear: %v", err)
	}
	if err := repo.RecordResult(questID, "", false); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	cleared, failed, name := read()
	if cleared != 2 || failed != 1 {
		t.Fatalf("counters = cleared %d / failed %d, want 2 / 1", cleared, failed)
	}
	// The failure path has no title, and must not blank the one a clear stored.
	if name != "천이종 리오레우스" {
		t.Fatalf("quest_name = %q, want the title kept from the clear path", name)
	}
}

func TestQuestStatsFailureFirstKeepsLaterName(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	repo := NewQuestStatsRepository(db)
	const questID = 60002
	if _, err := db.Exec("DELETE FROM quest_result_stats WHERE quest_id = $1", questID); err != nil {
		t.Fatalf("clear fixture: %v", err)
	}
	defer func() { _, _ = db.Exec("DELETE FROM quest_result_stats WHERE quest_id = $1", questID) }()

	// A quest can be failed before anyone clears it, so the row is created
	// without a title; the first clear afterwards has to fill it in.
	if err := repo.RecordResult(questID, "", false); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := repo.RecordResult(questID, "극한 티가렉스", true); err != nil {
		t.Fatalf("record clear: %v", err)
	}

	var name string
	if err := db.QueryRow(
		"SELECT quest_name FROM quest_result_stats WHERE quest_id = $1", questID,
	).Scan(&name); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if name != "극한 티가렉스" {
		t.Fatalf("quest_name = %q, want the title filled in by the clear", name)
	}
}
