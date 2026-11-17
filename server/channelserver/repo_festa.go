package channelserver

import (
	"database/sql"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"
)

// FestaRepository centralizes all database access for festa-related tables
// (events, festa_registrations, festa_submissions, festa_prizes, festa_prizes_accepted, festa_trials, guild_characters).
type FestaRepository struct {
	db      *sqlx.DB
	eventID *uint32
}

// NewFestaRepository creates a new FestaRepository.
func NewFestaRepository(db *sqlx.DB) *FestaRepository {
	return &FestaRepository{db: db}
}

// FestaEvent represents a festa event row.
type FestaEvent struct {
	ID         uint32 `db:"id"`
	StartTime  uint32 `db:"start_time"`
	Continuous bool   `db:"festa_continuous"`
}

// FestaGuildRanking holds a guild's ranking result for a trial or daily window.
type FestaGuildRanking struct {
	GuildID   uint32
	GuildName string
	Team      FestivalColor
	Souls     uint32
}

// CleanupAll removes all festa state: events, registrations, submissions, accepted prizes, and trial votes.
func (r *FestaRepository) CleanupAll() error {
	for _, q := range []string{
		"DELETE FROM events WHERE event_type='festa'",
		"DELETE FROM festa_registrations",
		"DELETE FROM festa_submissions",
		"DELETE FROM festa_prizes_accepted",
		"UPDATE guild_characters SET trial_vote=NULL",
	} {
		if _, err := r.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// InsertEvent creates a new festa event with the given start time.
func (r *FestaRepository) InsertEvent(startTime uint32) error {
	_, err := r.db.Exec(
		"INSERT INTO events (event_type, start_time) VALUES ('festa', to_timestamp($1)::timestamp without time zone)",
		startTime,
	)
	return err
}

// GetFestaEvents returns all festa events (id and start_time as epoch).
func (r *FestaRepository) GetFestaEvents() ([]FestaEvent, error) {
	var events []FestaEvent
	rows, err := r.db.Queryx("SELECT id, (EXTRACT(epoch FROM start_time)::bigint) as start_time,festa_continuous FROM events WHERE event_type='festa' ORDER BY start_time,id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e FestaEvent
		if err := rows.StructScan(&e); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// GetTeamSouls returns the total souls for a given team color ("blue" or "red").
func (r *FestaRepository) GetTeamSouls(team string) (uint32, error) {
	var souls uint32
	err := r.db.QueryRow(
		`SELECT COALESCE(SUM(fs.souls), 0) FROM festa_registrations fr
		LEFT JOIN festa_submissions fs ON fr.guild_id=fs.guild_id AND fr.event_id=fs.event_id
		WHERE fr.team=$1 AND fr.event_id=COALESCE($2,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)`,
		team, r.eventID,
	).Scan(&souls)
	return souls, err
}

// GetTrialsWithMonopoly applies the round's ballots during a monopoly window.
func (r *FestaRepository) GetTrialsWithMonopoly(monopoly bool) ([]FestaTrial, error) {
	var trials []FestaTrial
	err := r.db.Select(&trials, `SELECT id, objective, goal_id, times_req, locale_req, reward, 'none'::festival_color AS monopoly,
		CASE WHEN objective=5 THEN 2 ELSE 0 END AS unk
		FROM festa_trials WHERE objective BETWEEN 1 AND 4 OR
			(objective=5 AND $1::integer IS NOT NULL AND goal_id IN(53226,63303))
		ORDER BY id`, r.eventID)
	if err != nil {
		return nil, err
	}
	if r.eventID == nil || !monopoly {
		return trials, nil
	}
	var votes []festaMonopolyVote
	err = r.db.Select(&votes, `SELECT trial_id, team
		FROM festa_trial_votes JOIN festa_registrations USING(event_id,guild_id)
		WHERE event_id=$1 GROUP BY team,trial_id ORDER BY COUNT(*) DESC,trial_id`, r.eventID)
	if err != nil {
		return nil, err
	}
	var first FestivalColor
	err = r.db.Get(&first, `WITH previous_round AS (
		SELECT id FROM events WHERE event_type='festa'
			AND start_time<(SELECT start_time FROM events WHERE id=$1)
		ORDER BY start_time DESC,id DESC LIMIT 1
	), scores AS (
		SELECT COALESCE(SUM(s.souls) FILTER (WHERE r.team='red'),0) AS red,
		       COALESCE(SUM(s.souls) FILTER (WHERE r.team='blue'),0) AS blue
		FROM previous_round p JOIN festa_registrations r ON r.event_id=p.id
		JOIN festa_submissions s USING(event_id,guild_id)
	)
	SELECT CASE WHEN red>blue THEN 'red' ELSE 'blue' END FROM scores`, r.eventID)
	if err != nil {
		return nil, err
	}
	picks := festaMonopolies(votes, first)
	for i := range trials {
		if team, ok := picks[trials[i].ID]; ok {
			trials[i].Monopoly = team
		}
	}
	return trials, nil
}

// festaTeamTotalsAt reads a single historical tally. Submissions are never
// moved between rounds, and the timestamp boundary keeps a window stable.
func (r *FestaRepository) festaTeamTotalsAt(cutoff time.Time) (blue, red uint32, err error) {
	err = r.db.QueryRow(`SELECT
		COALESCE(SUM(s.souls) FILTER (WHERE r.team='blue'),0),
		COALESCE(SUM(s.souls) FILTER (WHERE r.team='red'),0)
		FROM festa_submissions s JOIN festa_registrations r USING(event_id,guild_id)
		WHERE s.event_id=$1 AND s.timestamp<$2`, r.eventID, cutoff).Scan(&blue, &red)
	return
}

func (r *FestaRepository) festaGuildStanding(guildID uint32) (souls, participants, rank uint32, team FestivalColor, err error) {
	err = r.db.QueryRow(`WITH guild_scores AS (
		SELECT guild_id, team, SUM(souls) AS souls,
			COUNT(DISTINCT character_id) FILTER (WHERE souls>0) AS participants
		FROM festa_registrations JOIN festa_submissions USING(event_id,guild_id)
		WHERE event_id=$1 GROUP BY guild_id,team HAVING SUM(souls)>0
	), standings AS (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY team ORDER BY souls DESC,guild_id) AS rank FROM guild_scores
	)
	SELECT souls, participants, rank, team FROM standings WHERE guild_id=$2`, r.eventID, guildID).
		Scan(&souls, &participants, &rank, &team)
	return
}

// GetTopGuildForTrial returns the top-scoring guild for a given trial type.
// Returns sql.ErrNoRows if no submissions exist.
func (r *FestaRepository) GetTopGuildForTrial(trialType uint16) (FestaGuildRanking, error) {
	var ranking FestaGuildRanking
	var temp uint32
	ranking.Team = FestivalColorNone
	err := r.db.QueryRow(`
		SELECT fs.guild_id, g.name, fr.team, SUM(fs.souls) as _
		FROM festa_submissions fs
		LEFT JOIN festa_registrations fr ON fs.guild_id = fr.guild_id AND fs.event_id=fr.event_id
		LEFT JOIN guilds g ON fs.guild_id = g.id
		WHERE fs.trial_type = $1 AND fs.event_id=COALESCE($2,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)
		GROUP BY fs.guild_id, g.name, fr.team
		ORDER BY _ DESC LIMIT 1
	`, trialType, r.eventID).Scan(&ranking.GuildID, &ranking.GuildName, &ranking.Team, &temp)
	return ranking, err
}

// GetTopGuildInWindow returns the top-scoring guild within a time window (epoch seconds).
// Returns sql.ErrNoRows if no submissions exist.
func (r *FestaRepository) GetTopGuildInWindow(start, end uint32) (FestaGuildRanking, error) {
	var ranking FestaGuildRanking
	var temp uint32
	ranking.Team = FestivalColorNone
	err := r.db.QueryRow(`
		SELECT fs.guild_id, g.name, fr.team, SUM(fs.souls) as _
		FROM festa_submissions fs
		LEFT JOIN festa_registrations fr ON fs.guild_id = fr.guild_id AND fs.event_id=fr.event_id
		LEFT JOIN guilds g ON fs.guild_id = g.id
		WHERE EXTRACT(EPOCH FROM fs.timestamp)::int > $1 AND EXTRACT(EPOCH FROM fs.timestamp)::int < $2
		AND fs.event_id=COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)
		GROUP BY fs.guild_id, g.name, fr.team
		ORDER BY _ DESC LIMIT 1
	`, start, end, r.eventID).Scan(&ranking.GuildID, &ranking.GuildName, &ranking.Team, &temp)
	return ranking, err
}

// GetCharSouls returns the total souls submitted by a character.
func (r *FestaRepository) GetCharSouls(charID uint32) (uint32, error) {
	var souls uint32
	err := r.db.QueryRow(
		`SELECT COALESCE((SELECT SUM(souls) FROM festa_submissions WHERE character_id=$1
		AND event_id=COALESCE($2,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)),0)`,
		charID, r.eventID,
	).Scan(&souls)
	return souls, err
}

func (r *FestaRepository) festaPersonalRewardBonus(charID, maxFP uint32) (uint32, error) {
	var souls, above3500Rate uint32
	var rebalanced bool
	err := r.db.QueryRow(`SELECT
		COALESCE((SELECT SUM(souls) FROM festa_submissions WHERE event_id=$1 AND character_id=$2),0),
		COALESCE(g.rebalanced_fp,false),COALESCE(g.fp_above_3500_rate,0)
		FROM (SELECT $1::integer AS event_id) e LEFT JOIN festa_goal_rounds g USING(event_id)`,
		r.eventID, charID).Scan(&souls, &rebalanced, &above3500Rate)
	if err != nil {
		return 0, err
	}
	return festaPersonalFPBonus(souls, maxFP, rebalanced, above3500Rate), nil
}

// Main reward affiliation comes from the first positive submission to a
// registered guild in the round's gathering window. Membership may change
// afterwards, but preview and claim continue to use this same history.
func (r *FestaRepository) festaMainPrizeGuild(q sqlx.Queryer, charID uint32) (uint32, error) {
	var guildID uint32
	err := q.QueryRowx(`SELECT s.guild_id FROM festa_submissions s JOIN events e ON e.id=s.event_id
		WHERE s.event_id=$1 AND s.character_id=$2 AND s.souls>0
		AND s.timestamp>=e.start_time+interval '168 hours' AND s.timestamp<e.start_time+interval '336 hours'
		AND EXISTS(SELECT 1 FROM festa_registrations r WHERE r.event_id=s.event_id AND r.guild_id=s.guild_id)
		ORDER BY s.timestamp,s.guild_id LIMIT 1`, r.eventID, charID).Scan(&guildID)
	return guildID, err
}

// HasClaimedMainPrize checks if a character has claimed the main festa prize (prize_id=0).
func (r *FestaRepository) HasClaimedMainPrize(charID uint32) bool {
	var exists uint32
	err := r.db.QueryRow(`SELECT prize_id FROM festa_prizes_accepted WHERE prize_id=0 AND character_id=$1
		AND event_id=COALESCE($2,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0) LIMIT 1`, charID, r.eventID).Scan(&exists)
	return err == nil
}

// VoteTrial sets a character's trial vote.
func (r *FestaRepository) VoteTrial(charID uint32, trialID uint32) error {
	return r.writeFesta(1<<1, func(tx *sqlx.Tx) error {
		if r.eventID != nil {
			result, err := tx.Exec(`INSERT INTO festa_trial_votes(event_id,guild_id,trial_id)
				SELECT fr.event_id, fr.guild_id, ft.id
				FROM festa_registrations fr JOIN events e ON e.id=fr.event_id
				JOIN guild_characters gc ON gc.guild_id=fr.guild_id AND gc.character_id=$2
				JOIN guilds g ON g.id=fr.guild_id AND g.leader_id=$2
				JOIN festa_trials ft ON ft.id=$3 AND ft.objective BETWEEN 1 AND 4
				WHERE fr.event_id=$1 AND clock_timestamp() < e.start_time + interval '165 hours'
				ON CONFLICT(event_id,guild_id) DO UPDATE SET trial_id=EXCLUDED.trial_id`, r.eventID, charID, trialID)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err == nil && affected == 0 {
				return sql.ErrNoRows
			}
			return err
		}
		_, err := tx.Exec(`UPDATE guild_characters SET trial_vote=$1 WHERE character_id=$2`, trialID, charID)
		return err
	})
}

// RegisterGuild registers a guild for a festa team.
func (r *FestaRepository) RegisterGuild(guildID uint32, team string) error {
	return r.writeFesta(1<<1, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(1178940482,$1::integer)`, guildID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO festa_registrations(guild_id,team,event_id)
			SELECT $1,$2,COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)
			WHERE NOT EXISTS(SELECT 1 FROM festa_registrations WHERE guild_id=$1
			AND event_id=COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0))`, guildID, team, r.eventID)
		return err
	})
}

// SubmitSouls records soul submissions for a character within a transaction.
// Zero entries are skipped without changing the native category numbers.
func (r *FestaRepository) SubmitSouls(charID, guildID uint32, souls []uint16) error {
	return r.writeFesta(1<<2, func(tx *sqlx.Tx) error {
		if r.eventID != nil {
			var memberID uint32
			if err := tx.QueryRow(`SELECT m.character_id FROM guild_characters m
				JOIN festa_registrations r ON r.guild_id=m.guild_id
				WHERE r.event_id=$1 AND r.guild_id=$2 AND m.character_id=$3 FOR SHARE OF m`,
				r.eventID, guildID, charID).Scan(&memberID); err != nil {
				return err
			}
		}
		var submittedAt time.Time
		if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&submittedAt); err != nil {
			return err
		}
		for i, s := range souls {
			if s == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO festa_submissions(character_id,guild_id,trial_type,souls,timestamp,event_id)
				VALUES($1,$2,$3,$4,$5,COALESCE($6,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0))`, charID, guildID, i, s, submittedAt, r.eventID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClaimPrize records that a character has claimed a festa prize.
func (r *FestaRepository) ClaimPrize(prizeID uint32, charID uint32) error {
	return r.claimFestaPrize(prizeID, charID, "")
}

func (r *FestaRepository) ClaimGoalPrize(prizeID, charID uint32, prizeType string) error {
	if prizeID == 0 || (prizeType != "personal" && prizeType != "guild") {
		return sql.ErrNoRows
	}
	return r.claimFestaPrize(prizeID, charID, prizeType)
}

func (r *FestaRepository) claimFestaPrize(prizeID, charID uint32, goalType string) error {
	phases := uint(1<<2 | 1<<4)
	if prizeID == 0 {
		phases = 1 << 4
	}
	return r.writeFesta(phases, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(1178940483,$1::integer)`, charID); err != nil {
			return err
		}
		if prizeID == 0 && r.eventID != nil {
			if _, err := r.festaMainPrizeGuild(tx, charID); err != nil {
				return err
			}
		}
		if goalType != "" && r.eventID != nil {
			if err := r.checkFestaGoal(tx, prizeID, charID, goalType); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO festa_prizes_accepted(prize_id,character_id,event_id)
			SELECT $1,$2,COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)
			WHERE NOT EXISTS(SELECT 1 FROM festa_prizes_accepted WHERE prize_id=$1 AND character_id=$2
			AND event_id=COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0))`, prizeID, charID, r.eventID)
		return err
	})
}

// ListPrizes returns festa prizes of the given type with a claimed flag for the character.
func (r *FestaRepository) ListPrizes(charID uint32, prizeType string) ([]Prize, error) {
	var state festaGoalState
	if r.eventID != nil {
		var err error
		state, err = getFestaGoalState(r.db, *r.eventID, charID)
		if err != nil {
			return nil, err
		}
	}
	prizes, err := r.listFestaPrizes(r.db, charID, prizeType, state)
	if err != nil || prizeType != "guild" || state.soulsPerParticipant == 0 {
		return prizes, err
	}
	eligibility, err := getFestaGuildEligibility(r.db, *r.eventID, state.guildID, charID)
	if err != nil || !eligibility.locked {
		return prizes, err
	}
	if !eligibility.eligible {
		return nil, nil
	}
	if !eligibility.counted {
		// The native list has one shared soul threshold and no field for a
		// personal minimum. Send each tier once that minimum has been met.
		prizes = slices.DeleteFunc(prizes, func(prize Prize) bool {
			return !eligibility.personalGoalMet(prize.SoulsReq, state.participants)
		})
	}
	return prizes, nil
}

func (r *FestaRepository) listFestaPrizes(q sqlx.Queryer, charID uint32, prizeType string, state festaGoalState) ([]Prize, error) {
	var prizes []Prize
	err := sqlx.Select(q, &prizes,
		`SELECT id, tier, souls_req, item_id, num_item, (SELECT count(*) FROM festa_prizes_accepted fpa WHERE fp.id = fpa.prize_id AND fpa.character_id = $1
		AND fpa.event_id=COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)) AS claimed
		FROM festa_prizes fp WHERE type=$2 ORDER BY souls_req,id`,
		charID, prizeType, r.eventID,
	)
	if err == nil {
		applyFestaGoalCutoffs(prizes, prizeType, state)
	}
	return prizes, err
}

func (r *FestaRepository) checkFestaGoal(tx *sqlx.Tx, prizeID, charID uint32, prizeType string) error {
	if prizeType == "guild" {
		var memberID uint32
		if err := tx.QueryRow(`SELECT character_id FROM guild_characters WHERE character_id=$1 FOR SHARE`, charID).Scan(&memberID); err != nil {
			return err
		}
	}
	state, err := getFestaGoalState(tx, *r.eventID, charID)
	if err != nil || state.soulsPerParticipant == 0 {
		return err
	}
	prizes, err := r.listFestaPrizes(tx, charID, prizeType, state)
	if err != nil {
		return err
	}
	for _, prize := range prizes {
		if prize.ID != prizeID {
			continue
		}
		if prizeType == "guild" {
			return r.checkFestaGuildGoal(tx, charID, state, prize.SoulsReq)
		}
		var personalSouls uint64
		err = tx.QueryRow(`SELECT COALESCE(SUM(souls),0) FROM festa_submissions
			WHERE event_id=$1 AND character_id=$2`, r.eventID, charID).Scan(&personalSouls)
		if err != nil {
			return err
		}
		if personalSouls > 0 && personalSouls >= uint64(prize.SoulsReq) {
			return nil
		}
		break
	}
	return sql.ErrNoRows
}

func (r *FestaRepository) checkFestaGuildGoal(tx *sqlx.Tx, charID uint32, state festaGoalState, guildCut uint32) error {
	eligibility, err := getFestaGuildEligibility(tx, *r.eventID, state.guildID, charID)
	if err != nil {
		return err
	}
	if (eligibility.locked && !eligibility.eligible) || !eligibility.personalGoalMet(guildCut, state.participants) {
		return sql.ErrNoRows
	}
	var guildSouls uint64
	err = tx.QueryRow(`SELECT COALESCE(SUM(s.souls),0) FROM festa_registrations f
		JOIN festa_submissions s USING(event_id,guild_id) WHERE f.event_id=$1 AND f.guild_id=$2`,
		r.eventID, state.guildID).Scan(&guildSouls)
	if err != nil {
		return err
	}
	if guildSouls < uint64(guildCut) {
		return sql.ErrNoRows
	}
	if !eligibility.locked {
		return nil
	}
	var receivingCharacter uint32
	// The unique account row settles concurrent claims from two characters.
	return tx.QueryRow(`INSERT INTO festa_goal_guild_claimants(event_id,user_id,character_id) VALUES($1,$2,$3)
		ON CONFLICT(event_id,user_id) DO UPDATE SET character_id=EXCLUDED.character_id
		WHERE festa_goal_guild_claimants.character_id=EXCLUDED.character_id RETURNING character_id`,
		r.eventID, eligibility.userID, charID).Scan(&receivingCharacter)
}
