package channelserver

import (
	"context"
	"database/sql"
	"errors"
)

// The PC client reads at most 40 entries in the alliance applicant list.
const maxAllianceApplicants = 40

// ApplyToAlliance records one pending request per guild. The alliance table
// lock serializes the capacity check with approval and alliance creation.
func (r *GuildRepository) ApplyToAlliance(allianceID, guildID, actorCharID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Match guild disband's lock order. Inserting the application also needs
	// the guild row for its foreign key.
	var lockedGuildID uint32
	if err := tx.Get(&lockedGuildID, `SELECT id FROM guilds WHERE id = $1 FOR UPDATE`, guildID); err != nil {
		return err
	}
	if _, err := tx.Exec(`LOCK TABLE guild_alliances IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if err := requireGuildScopedMutation(tx.Exec(`
		INSERT INTO guild_alliance_applications (guild_id, alliance_id)
		SELECT g.id, ga.id
		FROM guilds g
		JOIN guild_characters gc ON gc.guild_id = g.id AND gc.character_id = g.leader_id
		CROSS JOIN guild_alliances ga
		WHERE g.id = $2 AND g.leader_id = $3 AND ga.id = $1
		  AND ga.recruiting
		  AND (ga.sub1_id IS NULL OR ga.sub2_id IS NULL)
		  AND NOT EXISTS (
		      SELECT 1 FROM guild_alliances WHERE g.id IN (parent_id, sub1_id, sub2_id)
		  )
		  AND (SELECT count(*) FROM guild_alliance_applications WHERE alliance_id = ga.id) < $4
		ON CONFLICT (guild_id) DO NOTHING
	`, allianceID, guildID, actorCharID, maxAllianceApplicants)); err != nil {
		return err
	}
	return tx.Commit()
}

// DecideAllianceApplication consumes a request for this alliance as its current
// parent guild leader. Approval and the membership update commit together.
func (r *GuildRepository) DecideAllianceApplication(allianceID, parentGuildID, applicantGuildID, actorCharID uint32, accept bool) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`LOCK TABLE guild_alliances IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if err := requireGuildScopedMutation(tx.Exec(`
		DELETE FROM guild_alliance_applications app
		USING guild_alliances ga, guilds g, guild_characters gc
		WHERE app.alliance_id = $1 AND app.guild_id = $3
		  AND ga.id = app.alliance_id AND ga.parent_id = $2
		  AND g.id = ga.parent_id AND g.leader_id = $4
		  AND gc.guild_id = g.id AND gc.character_id = g.leader_id
	`, allianceID, parentGuildID, applicantGuildID, actorCharID)); err != nil {
		return err
	}
	if accept {
		if err := requireGuildScopedMutation(tx.Exec(`
			UPDATE guild_alliances ga
			SET sub1_id = COALESCE(ga.sub1_id, $2::integer),
			    sub2_id = CASE WHEN ga.sub1_id IS NULL THEN ga.sub2_id ELSE $2::integer END
			WHERE ga.id = $1
			  AND (ga.sub1_id IS NULL OR ga.sub2_id IS NULL)
			  AND NOT EXISTS (
			      SELECT 1 FROM guild_alliances WHERE $2 IN (parent_id, sub1_id, sub2_id)
			  )
		`, allianceID, applicantGuildID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *GuildRepository) GetPendingAllianceID(guildID uint32) (uint32, error) {
	var allianceID uint32
	err := r.db.Get(&allianceID, `SELECT alliance_id FROM guild_alliance_applications WHERE guild_id = $1`, guildID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return allianceID, err
}

func (r *GuildRepository) ListAllianceApplicants(allianceID uint32) ([]Guild, error) {
	var applicants []Guild
	err := r.db.Select(&applicants, `
		SELECT g.id, g.name, g.leader_id, c.name AS leader_name, g.rank_rp,
		       (SELECT count(*) FROM guild_characters gc WHERE gc.guild_id = g.id) AS member_count
		FROM guild_alliance_applications app
		JOIN guilds g ON g.id = app.guild_id
		JOIN characters c ON c.id = g.leader_id
		WHERE app.alliance_id = $1
		  AND EXISTS (
		      SELECT 1 FROM guild_characters gc WHERE gc.guild_id = g.id AND gc.character_id = g.leader_id
		  )
		ORDER BY app.created_at, app.guild_id
		LIMIT $2
	`, allianceID, maxAllianceApplicants)
	return applicants, err
}
