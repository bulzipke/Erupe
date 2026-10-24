package channelserver

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type QuestLicenseRuntimeSnapshot struct {
	CampaignIDs              []uint32
	ProtectedQuestIDs        []int
	PremiumCategoryAvailable bool
}

type QuestLicenseRuntimeRepository struct{ db *sqlx.DB }

func NewQuestLicenseRuntimeRepository(db *sqlx.DB) *QuestLicenseRuntimeRepository {
	return &QuestLicenseRuntimeRepository{db: db}
}

// The protected quest roster is frozen before accepting clients. Editing live
// Type9 IDs while this feature is running requires a deliberate server restart.
func (r *QuestLicenseRuntimeRepository) Load(ctx context.Context) (QuestLicenseRuntimeSnapshot, error) {
	var result QuestLicenseRuntimeSnapshot
	if r == nil || r.db == nil {
		return result, fmt.Errorf("quest license runtime repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.SelectContext(ctx, &result.CampaignIDs, `SELECT id FROM campaigns ORDER BY id`); err != nil {
		return result, err
	}
	if err = tx.SelectContext(ctx, &result.ProtectedQuestIDs, `SELECT DISTINCT quest_id FROM event_quests WHERE quest_type=9 ORDER BY quest_id LIMIT $1`, maxQuestAccessTargets+1); err != nil {
		return result, err
	}
	if len(result.ProtectedQuestIDs) > maxQuestAccessTargets {
		return result, fmt.Errorf("quest license runtime exceeds protected quest capacity")
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM campaign_categories WHERE id=6 AND type=1)`).Scan(&result.PremiumCategoryAvailable); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
