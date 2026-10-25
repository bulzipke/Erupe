package channelserver

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// CommitTowerDailyRewards commits the completion flags and their native
// pending items together. A retry can repeat this exact before/after pair;
// it cannot recreate a consumed item or change its advertised contents.
func (r *TowerRepository) CommitTowerDailyRewards(charID uint32, earthID int32, day time.Time, before, progress []byte) error {
	awards, err := towerDailyNewAwards(before, progress, day)
	if err != nil || charID == 0 || earthID < 0 || earthID > 65535 || len(awards) == 0 ||
		(r.roundID != nil && *r.roundID != earthID) {
		return errors.New("invalid Tower daily award")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = r.checkTowerRound(tx); err != nil {
		return err
	}
	var id uint32
	if err = tx.Get(&id, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID); err != nil {
		return err
	}
	var current []byte
	err = tx.Get(&current, `SELECT data FROM tower_daily_bins WHERE character_id=$1 FOR UPDATE`, charID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !bytes.Equal(current, before) && !bytes.Equal(current, progress) {
		return errors.New("Tower daily progress changed before award")
	}
	for _, award := range awards {
		_, err = tx.Exec(`INSERT INTO tower_daily_rewards
			(earth_id,character_id,day_start,mission_slot,item_slot,item_id,quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT(earth_id,character_id,day_start,mission_slot,item_slot) DO NOTHING`,
			earthID, charID, day.UTC(), award.MissionSlot, award.ItemSlot, award.Item.ItemID, award.Item.Quantity)
		if err != nil {
			return err
		}
		var item towerDailyItem
		if err = tx.QueryRow(`SELECT item_id,quantity FROM tower_daily_rewards
			WHERE earth_id=$1 AND character_id=$2 AND day_start=$3 AND mission_slot=$4 AND item_slot=$5`,
			earthID, charID, day.UTC(), award.MissionSlot, award.ItemSlot).Scan(&item.ItemID, &item.Quantity); err != nil {
			return err
		}
		if item != award.Item {
			return errors.New("Tower daily award retry differs")
		}
	}
	if _, err = tx.Exec(`INSERT INTO tower_daily_bins(character_id,data) VALUES($1,$2)
		ON CONFLICT(character_id) DO UPDATE SET data=EXCLUDED.data,updated_at=now()`, charID, progress); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TowerRepository) ListTowerDailyRewards(charID uint32, earthID int32, limit int) ([]TowerDailyRewardRow, error) {
	if charID == 0 || earthID < 0 || limit < 1 || limit > towerDailyRewardPageSize {
		return nil, errors.New("invalid Tower daily reward listing")
	}
	rows, err := r.db.Query(`SELECT id,item_id,quantity FROM tower_daily_rewards
		WHERE character_id=$1 AND earth_id=$2 AND claimed_at IS NULL ORDER BY id LIMIT $3`, charID, earthID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []TowerDailyRewardRow
	for rows.Next() {
		var row TowerDailyRewardRow
		if err = rows.Scan(&row.ID, &row.Item.ItemID, &row.Item.Quantity); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// The native receiver already inserted these items in its own inventory.
// Mark the offered rows consumed in the same transaction as that savedata.
func commitTowerDailyRewardReceipts(tx *sqlx.Tx, charID uint32, ids []int64) error {
	if len(ids) > towerDailyRewardPageSize {
		return errors.New("too many Tower daily receipts")
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return errors.New("invalid Tower daily receipt ID")
		}
		seen[id] = true
		var found int64
		err := tx.QueryRow(`UPDATE tower_daily_rewards SET claimed_at=COALESCE(claimed_at,now())
			WHERE id=$1 AND character_id=$2 RETURNING id`, id, charID).Scan(&found)
		if err != nil {
			return fmt.Errorf("Tower daily receipt %d: %w", id, err)
		}
	}
	return nil
}
