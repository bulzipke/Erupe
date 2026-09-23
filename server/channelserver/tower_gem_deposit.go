package channelserver

import (
	"errors"
	"erupe-ce/common/stringsupport"
)

// DepositTowerGem ties each merged display row to the committed Tower run.
// Lost ACKs may repeat a row, but cannot grant that row a second time.
func (r *TowerRepository) DepositTowerGem(charID uint32, runID string, slot int32, gem, quantity int32) error {
	index, valid := towerGemIndex(gem)
	if !valid || slot < 0 || slot >= 24 || quantity < 1 || quantity > 10 || charID == 0 || len(runID) != 32 {
		return errors.New("invalid tower gem deposit")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tower_settlements WHERE character_id=$1 AND run_id=$2)`, charID, runID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("uncommitted tower run")
	}
	result, err := tx.Exec(`INSERT INTO tower_gem_deposits(character_id,run_id,slot,gem,quantity) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(character_id,run_id,slot) DO NOTHING`, charID, runID, slot, gem, quantity)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var oldGem, oldQty int32
		if err = tx.QueryRow(`SELECT gem,quantity FROM tower_gem_deposits WHERE character_id=$1 AND run_id=$2 AND slot=$3`, charID, runID, slot).Scan(&oldGem, &oldQty); err != nil {
			return err
		}
		if oldGem != gem || oldQty != quantity {
			return errors.New("tower gem retry differs")
		}
		return tx.Commit()
	}
	var csv string
	if err = tx.QueryRow(`SELECT COALESCE(gems,$1) FROM tower WHERE char_id=$2 FOR UPDATE`, EmptyTowerCSV(30), charID).Scan(&csv); err != nil {
		return err
	}
	values := stringsupport.CSVElems(csv)
	if len(values) != 30 || values[index] < 0 || values[index] > 10 {
		return errors.New("invalid ancient treasure inventory")
	}
	csv = stringsupport.CSVSetIndex(csv, index, min(10, values[index]+int(quantity)))
	result, err = tx.Exec(`UPDATE tower SET gems=$1 WHERE char_id=$2`, csv, charID)
	if err != nil {
		return err
	}
	if n, err = result.RowsAffected(); err != nil || n != 1 {
		return errors.New("expected one tower gem row")
	}
	return tx.Commit()
}
