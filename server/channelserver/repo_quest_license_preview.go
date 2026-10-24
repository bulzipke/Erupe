package channelserver

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"
)

type QuestLicensePreviewRepository struct{ db *sqlx.DB }

func NewQuestLicensePreviewRepository(db *sqlx.DB) *QuestLicensePreviewRepository {
	return &QuestLicensePreviewRepository{db: db}
}

// PreviewOwnership reads an advisory ownership snapshot. It performs no writes,
// grants or locks that could authorize a debit. The purchase repository repeats
// both character authentication and quest-coverage checks under its users lock.
// The boolean distinguishes an authenticated nonowner from an absent/deleted or
// foreign character. All keys must already have permanent registered mappings.
func (r *QuestLicensePreviewRepository) PreviewOwnership(ctx context.Context, userID, characterID uint32, licenseKeys []string) (QuestLicenseOwnership, bool, error) {
	if userID == 0 || userID > math.MaxInt32 || characterID == 0 || characterID > math.MaxInt32 {
		return QuestLicenseOwnershipNone, false, nil
	}
	keys, _, err := canonicalQuestLicenseKeys(licenseKeys)
	if err != nil {
		return QuestLicenseOwnershipNone, false, err
	}
	if r == nil || r.db == nil {
		return QuestLicenseOwnershipNone, false, fmt.Errorf("quest license preview repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return QuestLicenseOwnershipNone, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var authenticated bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM characters c JOIN users u ON u.id=c.user_id
		WHERE u.id=$1 AND c.id=$2 AND NOT c.deleted)`, userID, characterID).Scan(&authenticated)
	if err != nil || !authenticated {
		return QuestLicenseOwnershipNone, false, err
	}
	// This shared single-statement helper needs an account lock for a purchase,
	// but a repeatable-read, read-only snapshot is sufficient for advisory UI.
	coverage, err := loadQuestLicensePurchaseCoverage(ctx, tx, userID, keys)
	if err != nil {
		return QuestLicenseOwnershipNone, false, err
	}
	if coverage.registeredKeys != len(keys) || coverage.targetQuests <= 0 {
		return QuestLicenseOwnershipNone, false, fmt.Errorf("quest license preview definitions incomplete")
	}
	if coverage.coveredQuests == coverage.targetQuests {
		return QuestLicenseOwnershipOwned, true, nil
	}
	if coverage.coveredQuests > 0 {
		return QuestLicenseOwnershipPartial, true, nil
	}
	return QuestLicenseOwnershipNone, true, nil
}
