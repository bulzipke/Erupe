package channelserver

import "time"

// Freeze all guild ranks together, including empty results. The phase and
// publication guards are repeated here so repository callers cannot finalize
// a still-running prayer round. Guild deletion cannot reorder final ranks.
func (r *DivaRepository) ensureDivaGuildFinalRanks(eventID uint32, cutoff time.Time) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var start time.Time
	if err := tx.Get(&start, `SELECT start_time FROM events WHERE id=$1 AND event_type='diva' FOR UPDATE`, eventID); err != nil {
		return err
	}
	end := start.Add(time.Duration(divaPhaseDuration) * time.Second)
	if cutoff.Before(end) || TimeAdjusted().Before(end.Add(time.Duration(divaInterlude)*time.Second)) {
		return nil
	}
	result, err := tx.Exec(`INSERT INTO diva_guild_finalizations(event_id) VALUES($1) ON CONFLICT DO NOTHING`, eventID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	_, err = tx.Exec(`INSERT INTO diva_guild_final_ranks(event_id,guild_id,name,points,rank)
		SELECT $1,s.guild_id,COALESCE(g.name,''),s.points,RANK() OVER(ORDER BY s.points DESC)
		FROM (SELECT guild_id,SUM(quest_points+bonus_points)::bigint points
			FROM diva_song_records WHERE event_id=$1 AND guild_id IS NOT NULL
			AND submitted_at >= $2 AND submitted_at < $3 GROUP BY guild_id
			HAVING SUM(quest_points+bonus_points)>0) s LEFT JOIN guilds g ON g.id=s.guild_id`, eventID, start, end)
	if err != nil {
		return err
	}
	return tx.Commit()
}
