package channelserver

import (
	"database/sql"

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

// GetTrialsWithMonopoly returns all festa trials with their computed monopoly color.
func (r *FestaRepository) GetTrialsWithMonopoly() ([]FestaTrial, error) {
	var trials []FestaTrial
	rows, err := r.db.Queryx(`SELECT ft.*,
		COALESCE(CASE
			WHEN COUNT(gc.id) FILTER (WHERE fr.team = 'blue' AND gc.trial_vote = ft.id) >
				 COUNT(gc.id) FILTER (WHERE fr.team = 'red' AND gc.trial_vote = ft.id)
			THEN CAST('blue' AS public.festival_color)
			WHEN COUNT(gc.id) FILTER (WHERE fr.team = 'red' AND gc.trial_vote = ft.id) >
				 COUNT(gc.id) FILTER (WHERE fr.team = 'blue' AND gc.trial_vote = ft.id)
			THEN CAST('red' AS public.festival_color)
		END, CAST('none' AS public.festival_color)) AS monopoly
		FROM public.festa_trials ft
		LEFT JOIN public.guild_characters gc ON ft.id = gc.trial_vote
		LEFT JOIN public.festa_registrations fr ON gc.guild_id = fr.guild_id
		AND fr.event_id=COALESCE($1,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)
		GROUP BY ft.id`, r.eventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var trial FestaTrial
		if err := rows.StructScan(&trial); err != nil {
			continue
		}
		trials = append(trials, trial)
	}
	return trials, nil
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
// All entries are inserted; callers should pre-filter zero values.
func (r *FestaRepository) SubmitSouls(charID, guildID uint32, souls []uint16) error {
	return r.writeFesta(1<<2, func(tx *sqlx.Tx) error {
		for i, s := range souls {
			if _, err := tx.Exec(`INSERT INTO festa_submissions(character_id,guild_id,trial_type,souls,timestamp,event_id)
				VALUES($1,$2,$3,$4,now(),COALESCE($5,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0))`, charID, guildID, i, s, r.eventID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClaimPrize records that a character has claimed a festa prize.
func (r *FestaRepository) ClaimPrize(prizeID uint32, charID uint32) error {
	phases := uint(1<<2 | 1<<4)
	if prizeID == 0 {
		phases = 1 << 4
	}
	return r.writeFesta(phases, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(1178940483,$1::integer)`, charID); err != nil {
			return err
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
	var prizes []Prize
	rows, err := r.db.Queryx(
		`SELECT id, tier, souls_req, item_id, num_item, (SELECT count(*) FROM festa_prizes_accepted fpa WHERE fp.id = fpa.prize_id AND fpa.character_id = $1
		AND fpa.event_id=COALESCE($3,(SELECT event_id FROM festa_event_lifecycle WHERE singleton),0)) AS claimed FROM festa_prizes fp WHERE type=$2`,
		charID, prizeType, r.eventID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var prize Prize
		if err := rows.StructScan(&prize); err != nil {
			continue
		}
		prizes = append(prizes, prize)
	}
	return prizes, nil
}

// ensure sql import is used
var _ = sql.ErrNoRows
