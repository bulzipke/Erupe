package channelserver

func (r *TowerRepository) GetGemNotice(charID uint32) (latest, seen int64, err error) {
	err = r.db.QueryRow(`SELECT COALESCE(MAX(id),0),COALESCE((SELECT last_read_id FROM tower_gem_notices WHERE character_id=$1),0) FROM tower_gem_history WHERE receiver_id=$1`, charID).Scan(&latest, &seen)
	return
}
func (r *TowerRepository) ReadGemNotice(charID uint32, through int64) error {
	if through <= 0 {
		return nil
	}
	_, err := r.db.Exec(`INSERT INTO tower_gem_notices(character_id,last_read_id) VALUES($1,$2) ON CONFLICT(character_id) DO UPDATE SET last_read_id=GREATEST(tower_gem_notices.last_read_id,EXCLUDED.last_read_id)`, charID, through)
	return err
}
