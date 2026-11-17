package channelserver

import "github.com/jmoiron/sqlx"

const festaGoalSoulsPerParticipant = 500

// Called under the round lifecycle lock. A single statement snapshots the
// previous round's contributors against current guild membership.
func initializeFestaGoals(tx *sqlx.Tx, eventID uint32) error {
	_, err := tx.Exec(`WITH previous_round AS (
		SELECT p.id FROM events p JOIN events active ON active.id=$1
		WHERE p.event_type='festa' AND p.start_time+interval '336 hours'<=active.start_time
		ORDER BY p.start_time DESC,p.id DESC LIMIT 1
	), activated AS (
		INSERT INTO festa_goal_rounds(event_id,previous_event_id)
		VALUES($1,(SELECT id FROM previous_round))
		RETURNING event_id,previous_event_id
	)
	INSERT INTO festa_goal_guild_sizes(event_id,guild_id,participants)
	SELECT a.event_id,s.guild_id,COUNT(DISTINCT c.user_id)
	FROM activated a JOIN events previous ON previous.id=a.previous_event_id
	JOIN festa_submissions s ON s.event_id=previous.id
	JOIN characters c ON c.id=s.character_id
	WHERE s.souls>0 AND s.timestamp>=previous.start_time+interval '168 hours'
		AND s.timestamp<previous.start_time+interval '336 hours'
		AND EXISTS(
			SELECT 1 FROM guild_characters member JOIN characters mc ON mc.id=member.character_id
			WHERE member.guild_id=s.guild_id AND mc.user_id=c.user_id
		)
	GROUP BY a.event_id,s.guild_id`, eventID)
	return err
}

type festaGoalState struct {
	scaled       bool
	guildID      uint32
	participants uint32
}

func getFestaGoalState(q sqlx.Queryer, eventID, charID uint32) (festaGoalState, error) {
	var state festaGoalState
	err := q.QueryRowx(`SELECT r.event_id IS NOT NULL,COALESCE(m.guild_id,0),COALESCE(g.participants,1)
		FROM (SELECT $1::integer AS event_id) e
		LEFT JOIN festa_goal_rounds r USING(event_id)
		LEFT JOIN guild_characters m ON m.character_id=$2
		LEFT JOIN festa_goal_guild_sizes g ON g.event_id=e.event_id AND g.guild_id=m.guild_id`,
		eventID, charID).Scan(&state.scaled, &state.guildID, &state.participants)
	return state, err
}

// The source rows are ordered by the original threshold and ID. Equal
// thresholds within a tier stay together and reward IDs never change.
func applyFestaGoalCutoffs(prizes []Prize, prizeType string, state festaGoalState) {
	if !state.scaled {
		for i := range prizes {
			prizes[i].SoulsReq = festaSoulGoal(prizes[i].SoulsReq)
		}
		return
	}
	if prizeType == "guild" {
		originalMax := make(map[uint32]uint32)
		for _, prize := range prizes {
			originalMax[prize.Tier] = prize.SoulsReq
		}
		target := min(uint64(max(state.participants, 1))*festaGoalSoulsPerParticipant, uint64(^uint32(0)))
		for i := range prizes {
			prize := &prizes[i]
			if denominator := uint64(originalMax[prize.Tier]); denominator != 0 {
				prize.SoulsReq = uint32((uint64(prize.SoulsReq)*target + denominator - 1) / denominator)
			}
		}
		return
	}

	type tierProgress struct {
		last, total, position uint32
	}
	tiers := make(map[uint32]tierProgress)
	for _, prize := range prizes {
		progress := tiers[prize.Tier]
		if prize.SoulsReq > 1 && prize.SoulsReq != progress.last {
			progress.last = prize.SoulsReq
			progress.total++
			tiers[prize.Tier] = progress
		}
	}
	for i := range prizes {
		prize := &prizes[i]
		if prize.SoulsReq <= 1 {
			continue
		}
		progress := tiers[prize.Tier]
		if progress.position == 0 || prize.SoulsReq != progress.last {
			progress.position++
			progress.last = prize.SoulsReq
			tiers[prize.Tier] = progress
		}
		prize.SoulsReq = uint32((uint64(progress.position)*festaGoalSoulsPerParticipant +
			uint64(progress.total) - 1) / uint64(progress.total))
	}
}
