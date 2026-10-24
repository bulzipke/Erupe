package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"
)

// ErrQuestLicenseStarterStaleSave means the client or DB-derived blob predates
// an inventory-changing starter commit. Retrying it with a newly read epoch
// would defeat this guard; recovery needs a fresh client-visible savedata load.
var ErrQuestLicenseStarterStaleSave = errors.New("savedata predates committed starter inventory")

// QuestLicenseStarterEpochBaseline is server-owned metadata. It is never read
// from the client's wire packet. A client save uses its Session's last loaded
// baseline; a DB-derived save uses the baseline loaded atomically with its blob.
type QuestLicenseStarterEpochBaseline struct {
	UserID uint32
	Epoch  int64
}

// QuestLicenseStarterEpochSnapshot reads inventory and generation together in
// one PostgreSQL statement, including the same active-character authorization.
type QuestLicenseStarterEpochSnapshot struct {
	ID       uint32
	Savedata []byte
	IsNew    bool
	Name     string
	Hash     []byte
	Epoch    int64
}

func validQuestLicenseStarterEpochIdentity(userID, characterID uint32) bool {
	return userID > 0 && userID <= math.MaxInt32 && characterID > 0 && characterID <= math.MaxInt32
}

func (r *CharacterRepository) LoadSaveDataWithStarterEpoch(ctx context.Context, userID, characterID uint32) (QuestLicenseStarterEpochSnapshot, error) {
	var result QuestLicenseStarterEpochSnapshot
	if r == nil || r.db == nil || ctx == nil || !validQuestLicenseStarterEpochIdentity(userID, characterID) {
		return result, fmt.Errorf("starter epoch savedata identity or repository unavailable")
	}
	err := r.db.QueryRowContext(ctx, `SELECT c.id,c.savedata,c.is_new_character,c.name,c.savedata_hash,c.starter_inventory_epoch
		FROM characters c JOIN users u ON u.id=c.user_id
		WHERE c.id=$1 AND c.user_id=$2 AND NOT c.deleted`, characterID, userID).
		Scan(&result.ID, &result.Savedata, &result.IsNew, &result.Name, &result.Hash, &result.Epoch)
	if err != nil {
		return QuestLicenseStarterEpochSnapshot{}, err
	}
	return result, nil
}

// LoadBackupsWithStarterEpoch deliberately excludes historical backups with an
// unknown generation. Authorization, current generation and backup selection
// share one read-only snapshot. A later write still checks the same baseline.
func (r *CharacterRepository) LoadBackupsWithStarterEpoch(ctx context.Context, userID, characterID uint32, expected int64) ([]SavedataBackup, error) {
	if r == nil || r.db == nil || ctx == nil || !validQuestLicenseStarterEpochIdentity(userID, characterID) || expected < 0 {
		return nil, fmt.Errorf("starter epoch backup identity or repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT c.starter_inventory_epoch FROM characters c JOIN users u ON u.id=c.user_id
		WHERE c.id=$1 AND c.user_id=$2 AND NOT c.deleted`, characterID, userID).Scan(&current); err != nil {
		return nil, err
	}
	if current != expected {
		return nil, ErrQuestLicenseStarterStaleSave
	}
	rows, err := tx.QueryContext(ctx, `SELECT slot,savedata,saved_at FROM savedata_backups
		WHERE char_id=$1 AND starter_inventory_epoch=$2 ORDER BY saved_at DESC,slot DESC`, characterID, expected)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]SavedataBackup, 0)
	for rows.Next() {
		var backup SavedataBackup
		if err := rows.Scan(&backup.Slot, &backup.Data, &backup.SavedAt); err != nil {
			return nil, err
		}
		result = append(result, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

// commitQuestLicenseStarterReceiptsWithEpoch replaces the unguarded receipt
// helper only in the opt-in save path. Call it before the first character write,
// and commit its transaction together with the exact client/DB-derived blob.
// The returned generation is provisional until that outer COMMIT succeeds.
// Empty receipt batches still validate the generation and account ownership.
func commitQuestLicenseStarterReceiptsWithEpoch(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32, expected int64, ids []uint32) (int64, error) {
	if ctx == nil || tx == nil || !validQuestLicenseStarterEpochIdentity(userID, characterID) || expected < 0 {
		return 0, fmt.Errorf("invalid starter epoch save transaction or identity")
	}
	if err := validateQuestLicenseStarterSaveIDs(ids); err != nil {
		return 0, err
	}
	var locked uint32
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		return 0, err
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT starter_inventory_epoch FROM characters
		WHERE id=$1 AND user_id=$2 AND NOT deleted FOR UPDATE`, characterID, userID).Scan(&current); err != nil {
		return 0, err
	}
	if current != expected {
		return 0, ErrQuestLicenseStarterStaleSave
	}
	if len(ids) == 0 {
		return current, nil
	}
	rows, err := validatedQuestLicenseStarterSaveRows(ctx, tx, userID, characterID, ids)
	if err != nil {
		return 0, err
	}
	advance := false
	for _, row := range rows {
		advance = advance || row.State == "pending"
	}
	if advance && current == math.MaxInt64 {
		return 0, fmt.Errorf("starter inventory generation exhausted")
	}
	// This helper revalidates its complete batch and preserves all legacy
	// conflict rules. The account lock makes the prior pending observation stable.
	if err := commitQuestLicenseStarterReceipts(ctx, tx, userID, characterID, ids); err != nil {
		return 0, err
	}
	if !advance {
		return current, nil
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `UPDATE characters SET starter_inventory_epoch=starter_inventory_epoch+1
		WHERE id=$1 AND user_id=$2 AND NOT deleted AND starter_inventory_epoch=$3
		RETURNING starter_inventory_epoch`, characterID, userID, expected).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}
