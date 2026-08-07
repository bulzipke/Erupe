package channelserver

import (
	"github.com/jmoiron/sqlx"
)

// QuestStatsRepository records how often each quest is cleared or left
// uncleared. Counts are server-wide rather than per character, since the
// dashboard only ranks the quests themselves.
type QuestStatsRepository struct {
	db *sqlx.DB
}

// NewQuestStatsRepository creates a new QuestStatsRepository.
func NewQuestStatsRepository(db *sqlx.DB) *QuestStatsRepository {
	return &QuestStatsRepository{db: db}
}

// RecordResult adds one clear or one failure for a quest.
//
// questName is only written when it is known and the stored name is still
// empty: the clear path resolves a title from the quest file, while the failure
// path has nothing but the ID, and an unnamed failure must not blank a title
// an earlier clear already recorded.
func (r *QuestStatsRepository) RecordResult(questID uint16, questName string, cleared bool) error {
	column := "failed"
	if cleared {
		column = "cleared"
	}
	_, err := r.db.Exec(`
		INSERT INTO quest_result_stats (quest_id, quest_name, `+column+`, updated_at)
		VALUES ($1, $2, 1, now())
		ON CONFLICT (quest_id) DO UPDATE SET
			`+column+` = quest_result_stats.`+column+` + 1,
			quest_name = CASE
				WHEN quest_result_stats.quest_name = '' THEN EXCLUDED.quest_name
				ELSE quest_result_stats.quest_name
			END,
			updated_at = now()
	`, questID, questName)
	return err
}
