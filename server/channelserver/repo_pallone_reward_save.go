package channelserver

import "fmt"

func (r *CharacterRepository) UpdateGCPAndPactWithClientRewards(charID, gcp, pactID uint32, divaIDs, palloneIDs []uint32) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`UPDATE characters SET gcp=$1,pact_id=$2 WHERE id=$3`, gcp, pactID, charID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrCharacterNotFound
	}
	if err = commitDivaRewardReceipts(tx, charID, divaIDs, 26); err != nil {
		return err
	}
	if err = commitPalloneRewardReceipts(tx, charID, palloneIDs, palloneGP); err != nil {
		return fmt.Errorf("save Pallone GP receipts: %w", err)
	}
	return tx.Commit()
}
