package channelserver

import "github.com/jmoiron/sqlx"

const (
	festaGoalSoulsPerParticipant = 3000
	festaPersonalGoalSouls       = 2000
	festaPersonalBookMultiplier  = 2
	festaFPAbove3500Rate         = 150
)

// Called under the round lifecycle lock. A single statement snapshots the
// previous round's contributors against current guild membership.
func initializeFestaGoals(tx *sqlx.Tx, eventID uint32) error {
	_, err := tx.Exec(`WITH previous_round AS (
		SELECT p.id FROM events p JOIN events active ON active.id=$1
		WHERE p.event_type='festa' AND p.start_time+interval '336 hours'<=active.start_time
		ORDER BY p.start_time DESC,p.id DESC LIMIT 1
	), activated AS (
		INSERT INTO festa_goal_rounds(event_id,previous_event_id,souls_per_participant,guild_join_cutoff,rebalanced_fp,
			personal_goal_souls,personal_book_multiplier,fp_above_3500_rate)
		SELECT id,(SELECT id FROM previous_round),$2,start_time+interval '168 hours',true,$3,$4,$5
		FROM events WHERE id=$1
		RETURNING event_id,previous_event_id
	), participants AS (
		SELECT DISTINCT a.event_id,s.guild_id,c.user_id
		FROM activated a JOIN events previous ON previous.id=a.previous_event_id
		JOIN festa_submissions s ON s.event_id=previous.id
		JOIN characters c ON c.id=s.character_id
		WHERE s.souls>0 AND s.timestamp>=previous.start_time+interval '168 hours'
			AND s.timestamp<previous.start_time+interval '336 hours'
			AND EXISTS(
				SELECT 1 FROM guild_characters member JOIN characters mc ON mc.id=member.character_id
				WHERE member.guild_id=s.guild_id AND mc.user_id=c.user_id
			)
	), sizes AS (
		INSERT INTO festa_goal_guild_sizes(event_id,guild_id,participants)
		SELECT event_id,guild_id,COUNT(*) FROM participants GROUP BY event_id,guild_id
		RETURNING event_id,guild_id
	)
	INSERT INTO festa_goal_guild_participants(event_id,guild_id,user_id)
	SELECT p.event_id,p.guild_id,p.user_id FROM participants p JOIN sizes USING(event_id,guild_id)`,
		eventID, festaGoalSoulsPerParticipant, festaPersonalGoalSouls, festaPersonalBookMultiplier, festaFPAbove3500Rate)
	return err
}

type festaGoalState struct {
	soulsPerParticipant uint32
	personalGoalSouls   uint32
	personalBookFactor  uint32
	guildID             uint32
	participants        uint32
}

func getFestaGoalState(q sqlx.Queryer, eventID, charID uint32) (festaGoalState, error) {
	var state festaGoalState
	err := q.QueryRowx(`SELECT COALESCE(r.souls_per_participant,0),
		COALESCE(r.personal_goal_souls,r.souls_per_participant,0),COALESCE(r.personal_book_multiplier,1),
		COALESCE(m.guild_id,0),COALESCE(g.participants,1)
		FROM (SELECT $1::integer AS event_id) e
		LEFT JOIN festa_goal_rounds r USING(event_id)
		LEFT JOIN guild_characters m ON m.character_id=$2
		LEFT JOIN festa_goal_guild_sizes g ON g.event_id=e.event_id AND g.guild_id=m.guild_id`,
		eventID, charID).Scan(&state.soulsPerParticipant, &state.personalGoalSouls, &state.personalBookFactor, &state.guildID, &state.participants)
	return state, err
}

type festaGuildEligibility struct {
	locked   bool
	eligible bool
	counted  bool
	userID   uint32
	ownSouls uint64
}

func getFestaGuildEligibility(q sqlx.Queryer, eventID, guildID, charID uint32) (festaGuildEligibility, error) {
	var result festaGuildEligibility
	err := q.QueryRowx(`SELECT r.guild_join_cutoff IS NOT NULL,
		COALESCE(m.joined_at<r.guild_join_cutoff,false) AND (claim.character_id IS NULL OR claim.character_id=c.id),
		p.user_id IS NOT NULL,c.user_id,
		COALESCE((SELECT SUM(souls) FROM festa_submissions WHERE event_id=r.event_id AND guild_id=$2 AND character_id=c.id),0)
		FROM festa_goal_rounds r CROSS JOIN characters c
		LEFT JOIN guild_characters m ON m.character_id=c.id AND m.guild_id=$2
		LEFT JOIN festa_goal_guild_participants p ON p.event_id=r.event_id AND p.guild_id=$2 AND p.user_id=c.user_id
		LEFT JOIN festa_goal_guild_claimants claim ON claim.event_id=r.event_id AND claim.user_id=c.user_id
		WHERE r.event_id=$1 AND c.id=$3`, eventID, guildID, charID).
		Scan(&result.locked, &result.eligible, &result.counted, &result.userID, &result.ownSouls)
	return result, err
}

func (e festaGuildEligibility) personalGoalMet(guildCut, participants uint32) bool {
	required := uint64(1)
	if e.locked && !e.counted {
		// Accounts outside the snapshot must contribute their share of the tier.
		required = max(1, (uint64(guildCut)+uint64(participants)-1)/uint64(participants))
	}
	return e.ownSouls >= required
}

// The source rows are ordered by the original threshold and ID. Equal
// thresholds within a tier stay together and reward IDs never change.
func applyFestaGoalCutoffs(prizes []Prize, prizeType string, state festaGoalState) {
	if state.soulsPerParticipant == 0 {
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
		target := min(uint64(max(state.participants, 1))*uint64(state.soulsPerParticipant), uint64(^uint32(0)))
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
		// 11381–11386 are the three grades of both Ancient Hunting book families.
		if prize.ItemID >= 11381 && prize.ItemID <= 11386 {
			prize.NumItem *= state.personalBookFactor
		}
		if prize.SoulsReq <= 1 {
			continue
		}
		progress := tiers[prize.Tier]
		if progress.position == 0 || prize.SoulsReq != progress.last {
			progress.position++
			progress.last = prize.SoulsReq
			tiers[prize.Tier] = progress
		}
		prize.SoulsReq = uint32((uint64(progress.position)*uint64(state.personalGoalSouls) +
			uint64(progress.total) - 1) / uint64(progress.total))
	}
}
