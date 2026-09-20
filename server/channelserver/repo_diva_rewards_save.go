package channelserver

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// commitDivaRewardReceipts runs in the transaction persisting the client's
// matching item/GP state. It never grants or increments anything. Already
// committed receipts are accepted, preserving the original timestamp.
func commitDivaRewardReceipts(tx *sqlx.Tx, charID uint32, ids []uint32, itemType uint8) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 32 || (itemType != 7 && itemType != 26) {
		return fmt.Errorf("invalid diva reward receipt batch")
	}
	keys := make([]int64, len(ids))
	seen := make(map[uint32]struct{}, len(ids))
	for i, id := range ids {
		if id == 0 {
			return fmt.Errorf("invalid diva reward receipt ID")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate diva reward receipt ID %d", id)
		}
		seen[id] = struct{}{}
		keys[i] = int64(id)
	}
	var receipts []struct {
		ID       uint32 `db:"id"`
		CharID   uint32 `db:"char_id"`
		ItemType uint8  `db:"item_type"`
	}
	if err := tx.Select(&receipts, `SELECT id, char_id, item_type
		FROM diva_reward_receipts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, pq.Array(keys)); err != nil {
		return fmt.Errorf("lock diva reward receipts: %w", err)
	}
	if len(receipts) != len(ids) {
		return fmt.Errorf("missing diva reward receipt")
	}
	for _, receipt := range receipts {
		if receipt.CharID != charID || receipt.ItemType != itemType {
			return fmt.Errorf("diva reward receipt ownership or item type mismatch")
		}
	}
	result, err := tx.Exec(`UPDATE diva_reward_receipts
		SET claimed_at = COALESCE(claimed_at, NOW())
		WHERE id = ANY($1) AND char_id = $2 AND item_type = $3`, pq.Array(keys), charID, itemType)
	if err != nil {
		return fmt.Errorf("commit diva reward receipts: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return fmt.Errorf("incomplete diva reward receipt update")
	}
	return nil
}

// Store the client's absolute GP balance and confirm only its GP receipts in
// the same transaction. Item receipts remain pending for the savedata path.
func (r *CharacterRepository) UpdateGCPAndPactWithDivaRewards(charID, gcp, pactID uint32, ids []uint32) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin diva GP save: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	result, err := tx.Exec("UPDATE characters SET gcp=$1, pact_id=$2 WHERE id=$3", gcp, pactID, charID)
	if err != nil {
		return fmt.Errorf("save diva GP balance: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrCharacterNotFound
	}
	if err := commitDivaRewardReceipts(tx, charID, ids, 26); err != nil {
		return err
	}
	return tx.Commit()
}
