package channelserver

import "time"

// Guild ranking uses the sum of member contributions, frozen to the guild
// recorded for each submission. Old rows without a historical guild are not
// retroactively assigned to a character's present membership.
func (r *DivaRepository) GetDivaGuildRanking(eventID, charID uint32, cutoff time.Time) ([]DivaRank, error) {
	var ranks []DivaRank
	if err := r.ensureDivaGuildFinalRanks(eventID, cutoff); err != nil {
		return nil, err
	}
	err := r.db.Select(&ranks, `WITH own_guild AS (
		SELECT guild_id FROM guild_characters WHERE character_id=$2
	), scores AS (
		SELECT r.guild_id,SUM(r.quest_points+r.bonus_points) points FROM diva_song_records r
		JOIN events e ON e.id=r.event_id
		WHERE r.event_id=$1 AND r.guild_id IS NOT NULL AND r.submitted_at<$3
		AND r.submitted_at>=e.start_time AND r.submitted_at<e.start_time+INTERVAL '601200 seconds'
		GROUP BY r.guild_id HAVING SUM(r.quest_points+r.bonus_points)>0
	), ranked AS (
		SELECT s.guild_id,COALESCE(g.name,'') name,s.points,
			RANK() OVER (ORDER BY s.points DESC) rank,
			ROW_NUMBER() OVER (ORDER BY s.points DESC,s.guild_id) position
		FROM scores s JOIN guilds g ON g.id=s.guild_id
		WHERE NOT EXISTS(SELECT 1 FROM diva_guild_finalizations WHERE event_id=$1)
		UNION ALL
		SELECT guild_id,name,points,rank,ROW_NUMBER() OVER(ORDER BY points DESC,guild_id)
		FROM diva_guild_final_ranks WHERE event_id=$1
	), result AS (
		SELECT guild_id,name,points,rank,EXISTS(SELECT 1 FROM own_guild o WHERE o.guild_id=r.guild_id) is_own
		FROM ranked r WHERE position<=100 OR guild_id IN (SELECT guild_id FROM own_guild)
		UNION ALL
		SELECT g.id,COALESCE(g.name,''),0,0,TRUE FROM guilds g JOIN own_guild o ON o.guild_id=g.id
		WHERE NOT EXISTS(SELECT 1 FROM ranked r WHERE r.guild_id=g.id)
	) SELECT guild_id,name,points,rank,is_own FROM result ORDER BY points DESC,guild_id`, eventID, charID, cutoff)
	return ranks, err
}
