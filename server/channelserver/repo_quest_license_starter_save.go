package channelserver

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
)

func validateQuestLicenseStarterSaveIDs(ids []uint32) error {
	if len(ids) > 512 {
		return fmt.Errorf("too many starter save receipts")
	}
	seen := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		if id < campaignCoinRewardBase || id > math.MaxInt32 || seen[id] {
			return fmt.Errorf("invalid or duplicate starter save receipt")
		}
		seen[id] = true
	}
	return nil
}

type questLicenseStarterSaveRow struct {
	ID       uint32
	ItemID   uint16
	Quantity uint16
	State    string
	Key      string
}

// validatedQuestLicenseStarterSaveRows requires the authenticated account and
// character locks first. It validates the complete batch without consuming it.
func validatedQuestLicenseStarterSaveRows(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32, ids []uint32) ([]questLicenseStarterSaveRow, error) {
	rows := make([]questLicenseStarterSaveRow, 0, len(ids))
	for _, id := range ids {
		var row questLicenseStarterSaveRow
		err := tx.QueryRowContext(ctx, `SELECT id,item_id,quantity,state,starter_key FROM quest_license_starters
			WHERE id=$1 AND user_id=$2 AND character_id=$3 FOR UPDATE`, id, userID, characterID).
			Scan(&row.ID, &row.ItemID, &row.Quantity, &row.State, &row.Key)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("unknown starter save receipt")
		}
		if err != nil {
			return nil, err
		}
		if row.State != "pending" && row.State != "claimed" {
			return nil, fmt.Errorf("starter suppression receipt is not collectible")
		}
		if row.ItemID == 0 || row.Quantity == 0 {
			return nil, fmt.Errorf("invalid starter save item snapshot")
		}
		if row.State == "pending" {
			var policy questLicenseStarterBinding
			if err := tx.GetContext(ctx, &policy, `SELECT starter_key,license_keys,item_id,quantity,legacy_campaign_ids,policy_hash
				FROM quest_license_starter_policies WHERE starter_key=$1`, row.Key); err != nil {
				return nil, err
			}
			if policy.ItemID != row.ItemID || policy.Quantity != row.Quantity {
				return nil, fmt.Errorf("starter save policy snapshot mismatch")
			}
			claimed, pending, err := legacyQuestLicenseStarterState(ctx, tx, userID, characterID, policy)
			if err != nil {
				return nil, err
			}
			if claimed || pending {
				return nil, fmt.Errorf("legacy starter delivery conflicts with new receipt")
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// PrepareSave validates a native claim for staging only. It never consumes a
// receipt or grants inventory. Already committed acknowledgements return no new
// offers. The future handler must stage these rows on that authenticated session
// before acknowledging the claim, then attach only that snapshot to client
// SAVEDATA. Server-generated/DB-derived saves must not carry staged receipts.
func (r *QuestLicenseStarterRepository) PrepareSave(ctx context.Context, userID, characterID uint32, ids []uint32) ([]CampaignReward, error) {
	if err := validateQuestLicenseStarterSaveIDs(ids); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("starter repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("starter save character not authenticated")
	}
	rows, err := validatedQuestLicenseStarterSaveRows(ctx, tx, userID, characterID, ids)
	if err != nil {
		return nil, err
	}
	items := make([]CampaignReward, 0, len(rows))
	for _, row := range rows {
		if row.State == "pending" {
			items = append(items, CampaignReward{ID: row.ID, ItemType: 7, ItemID: row.ItemID, Quantity: row.Quantity, Deadline: time.Unix(0, 0).UTC()})
		}
	}
	return items, nil
}

// commitQuestLicenseStarterReceipts must run inside the CLIENT savedata
// transaction, BEFORE its first character UPDATE. This preserves account ->
// character lock order and rolls receipt consumption back with any later save
// failure. An empty snapshot is a no-op for ordinary/server-generated saves.
//
// Legacy claim writers must use the same account lock before native activation:
// rechecking their rows here alone cannot serialize an unrelated legacy writer.
// This helper does not establish that arbitrary savedata contains these items.
func commitQuestLicenseStarterReceipts(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32, ids []uint32) error {
	if err := validateQuestLicenseStarterSaveIDs(ids); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if tx == nil {
		return fmt.Errorf("starter receipt commit requires save transaction")
	}
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("starter save character not authenticated")
	}
	rows, err := validatedQuestLicenseStarterSaveRows(ctx, tx, userID, characterID, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.State == "claimed" {
			continue
		}
		updated, err := tx.ExecContext(ctx, `UPDATE quest_license_starters SET state='claimed',claimed_at=COALESCE(claimed_at,now())
			WHERE id=$1 AND user_id=$2 AND character_id=$3 AND state='pending'`, row.ID, userID, characterID)
		if err != nil {
			return err
		}
		if n, err := updated.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("starter save receipt changed during commit")
		}
	}
	return nil
}
