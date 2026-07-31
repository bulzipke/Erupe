package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

const guildMissionRunColumns = `
	id, guild_id, mission_id, target_type, target_id, required_count,
	skip_tickets, progress_per_exchange, cancel_ticket_cost, is_gr,
	reward_type, reward_level, progress, state,
	set_by, completed_by, cancelled_by, set_at, target_expires_at,
	completed_at, effect_expires_at, cancelled_at, updated_at`

// GuildMissionRepository persists guild-wide target state.
type GuildMissionRepository struct {
	db *sqlx.DB
}

// NewGuildMissionRepository creates a guild mission repository.
func NewGuildMissionRepository(db *sqlx.DB) *GuildMissionRepository {
	return &GuildMissionRepository{db: db}
}

// lockGuildMissionMember resolves an actual guild_characters membership and
// locks both the guild and membership rows. Pending applications never appear
// in guild_characters and therefore cannot mutate mission state.
func lockGuildMissionMember(tx *sqlx.Tx, charID uint32) (uint32, error) {
	var guildID uint32
	if err := tx.Get(&guildID,
		`SELECT guild_id FROM guild_characters WHERE character_id = $1`,
		charID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrGuildMissionNotMember
		}
		return 0, err
	}

	var lockedGuildID uint32
	if err := tx.Get(&lockedGuildID,
		`SELECT id FROM guilds WHERE id = $1 FOR UPDATE`,
		guildID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrGuildMissionNotMember
		}
		return 0, err
	}

	var lockedCharID uint32
	if err := tx.Get(&lockedCharID, `
		SELECT character_id
		FROM guild_characters
		WHERE guild_id = $1 AND character_id = $2
		FOR UPDATE
	`, lockedGuildID, charID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrGuildMissionNotMember
		}
		return 0, err
	}
	return lockedGuildID, nil
}

func expireGuildMissionTarget(tx *sqlx.Tx, guildID uint32, now time.Time) error {
	_, err := tx.Exec(`
		UPDATE guild_mission_runs
		SET state = $3, updated_at = $2
		WHERE guild_id = $1
		  AND state = $4
		  AND target_expires_at <= $2
	`, guildID, now, GuildMissionRunExpired, GuildMissionRunActive)
	return err
}

func getActiveGuildMission(tx *sqlx.Tx, guildID uint32, forUpdate bool) (*GuildMissionRun, error) {
	query := `SELECT ` + guildMissionRunColumns + `
		FROM guild_mission_runs
		WHERE guild_id = $1 AND state = $2
		ORDER BY id DESC
		LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}

	run := &GuildMissionRun{}
	if err := tx.Get(run, query, guildID, GuildMissionRunActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return run, nil
}

// GetSnapshot returns the selected target and unexpired completed effects.
func (r *GuildMissionRepository) GetSnapshot(charID uint32, now time.Time) (GuildMissionSnapshot, error) {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return GuildMissionSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	guildID, err := lockGuildMissionMember(tx, charID)
	if err != nil {
		return GuildMissionSnapshot{}, err
	}
	if err := expireGuildMissionTarget(tx, guildID, now); err != nil {
		return GuildMissionSnapshot{}, err
	}

	active, err := getActiveGuildMission(tx, guildID, false)
	if err != nil {
		return GuildMissionSnapshot{}, err
	}

	effects := make([]GuildMissionRun, 0)
	if err := tx.Select(&effects, `SELECT `+guildMissionRunColumns+`
		FROM guild_mission_runs
		WHERE guild_id = $1
		  AND state = $2
		  AND effect_expires_at > $3
		ORDER BY completed_at DESC, id DESC
		LIMIT 10
	`, guildID, GuildMissionRunCompleted, now); err != nil {
		return GuildMissionSnapshot{}, err
	}

	if err := tx.Commit(); err != nil {
		return GuildMissionSnapshot{}, err
	}
	return GuildMissionSnapshot{Active: active, Effects: effects}, nil
}

// Start selects a target for the actor's guild. Repeating the same request
// while it is active is treated as an idempotent success.
func (r *GuildMissionRepository) Start(charID uint32, def GuildMissionDefinition, now time.Time) (GuildMissionRun, error) {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return GuildMissionRun{}, err
	}
	defer func() { _ = tx.Rollback() }()

	guildID, err := lockGuildMissionMember(tx, charID)
	if err != nil {
		return GuildMissionRun{}, err
	}
	if err := expireGuildMissionTarget(tx, guildID, now); err != nil {
		return GuildMissionRun{}, err
	}

	active, err := getActiveGuildMission(tx, guildID, true)
	if err != nil {
		return GuildMissionRun{}, err
	}
	if active != nil {
		if active.MissionID == def.ID {
			if err := tx.Commit(); err != nil {
				return GuildMissionRun{}, err
			}
			return *active, nil
		}
		return GuildMissionRun{}, ErrGuildMissionAlreadyActive
	}

	run := GuildMissionRun{}
	err = tx.Get(&run, `INSERT INTO guild_mission_runs (
			guild_id, mission_id, target_type, target_id, required_count,
			skip_tickets, progress_per_exchange, cancel_ticket_cost,
			is_gr, reward_type, reward_level, progress, state,
			set_by, set_at, target_expires_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, 0, $12,
			$13, $14, $15, $14
		)
		RETURNING `+guildMissionRunColumns,
		guildID, def.ID, def.Type, def.Goal, def.Quantity,
		def.SkipTickets, guildMissionDefaultProgressPerExchange,
		guildMissionDefaultCancelTicketCost,
		def.GR, def.RewardType, def.RewardLevel,
		GuildMissionRunActive, charID, now, now.Add(guildMissionTargetLifetime),
	)
	if err != nil {
		return GuildMissionRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return GuildMissionRun{}, err
	}
	return run, nil
}

// AddProgress adds a reported amount to the active target. A report larger than
// the remaining requirement is rejected atomically so the protocol failure
// result can make the client restore every item or ticket it pre-consumed.
// Completion is committed exactly once while the run row is locked.
func (r *GuildMissionRepository) AddProgress(charID, missionID, requested uint32, now time.Time) (GuildMissionProgressResult, error) {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return GuildMissionProgressResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	guildID, err := lockGuildMissionMember(tx, charID)
	if err != nil {
		return GuildMissionProgressResult{}, err
	}
	if err := expireGuildMissionTarget(tx, guildID, now); err != nil {
		return GuildMissionProgressResult{}, err
	}

	active, err := getActiveGuildMission(tx, guildID, true)
	if err != nil {
		return GuildMissionProgressResult{}, err
	}
	if active == nil {
		return GuildMissionProgressResult{}, ErrGuildMissionNoActiveTarget
	}
	if active.MissionID != missionID {
		return GuildMissionProgressResult{}, ErrGuildMissionTargetMismatch
	}

	remaining := active.RequiredCount - active.Progress
	if requested > remaining {
		return GuildMissionProgressResult{}, ErrGuildMissionTooMuchProgress
	}
	applied := requested

	if applied > 0 {
		if _, err := tx.Exec(`
			INSERT INTO guild_mission_contributions (
				mission_run_id, character_id, amount, first_at, updated_at
			) VALUES ($1, $2, $3, $4, $4)
			ON CONFLICT (mission_run_id, character_id)
			DO UPDATE SET
				amount = guild_mission_contributions.amount + EXCLUDED.amount,
				updated_at = EXCLUDED.updated_at
		`, active.ID, charID, applied, now); err != nil {
			return GuildMissionProgressResult{}, err
		}
	}

	active.Progress += applied
	completed := active.Progress >= active.RequiredCount
	if completed {
		effectExpiresAt := now.Add(guildMissionEffectLifetime)
		if err := tx.Get(active, `UPDATE guild_mission_runs
			SET progress = $2,
			    state = $3,
			    completed_by = $4,
			    completed_at = $5,
			    effect_expires_at = $6,
			    updated_at = $5
			WHERE id = $1 AND state = $7
			RETURNING `+guildMissionRunColumns,
			active.ID, active.Progress, GuildMissionRunCompleted, charID,
			now, effectExpiresAt, GuildMissionRunActive,
		); err != nil {
			return GuildMissionProgressResult{}, err
		}
	} else if applied > 0 {
		if err := tx.Get(active, `UPDATE guild_mission_runs
			SET progress = $2, updated_at = $3
			WHERE id = $1 AND state = $4
			RETURNING `+guildMissionRunColumns,
			active.ID, active.Progress, now, GuildMissionRunActive,
		); err != nil {
			return GuildMissionProgressResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return GuildMissionProgressResult{}, err
	}
	return GuildMissionProgressResult{
		Run:       *active,
		Applied:   applied,
		Completed: completed,
	}, nil
}

// Cancel marks the matching active target as cancelled without discarding its
// audit history.
func (r *GuildMissionRepository) Cancel(charID, missionID uint32, now time.Time) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	guildID, err := lockGuildMissionMember(tx, charID)
	if err != nil {
		return err
	}
	if err := expireGuildMissionTarget(tx, guildID, now); err != nil {
		return err
	}

	active, err := getActiveGuildMission(tx, guildID, true)
	if err != nil {
		return err
	}
	if active == nil {
		return ErrGuildMissionNoActiveTarget
	}
	if active.MissionID != missionID {
		return ErrGuildMissionTargetMismatch
	}

	if _, err := tx.Exec(`
		UPDATE guild_mission_runs
		SET state = $2,
		    cancelled_by = $3,
		    cancelled_at = $4,
		    updated_at = $4
		WHERE id = $1 AND state = $5
	`, active.ID, GuildMissionRunCancelled, charID, now, GuildMissionRunActive); err != nil {
		return err
	}
	return tx.Commit()
}
