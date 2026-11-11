package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const allianceInfoSelectSQL = `
SELECT
ga.id,
ga.name,
created_at,
ga.recruiting,
parent_id,
COALESCE(sub1_id, 0) AS sub1_id,
COALESCE(sub2_id, 0) AS sub2_id
FROM guild_alliances ga
`

// GetAllianceByID loads alliance data including parent and sub guilds.
func (r *GuildRepository) GetAllianceByID(allianceID uint32) (*GuildAlliance, error) {
	alliance := &GuildAlliance{}
	err := r.db.Get(alliance, allianceInfoSelectSQL+` WHERE ga.id = $1`, allianceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.populateAllianceGuilds(alliance)
}

// ListAlliances returns all alliances with their guild data populated.
func (r *GuildRepository) ListAlliances() ([]*GuildAlliance, error) {
	var rows []*GuildAlliance
	if err := r.db.Select(&rows, allianceInfoSelectSQL); err != nil {
		return nil, err
	}
	var alliances []*GuildAlliance
	for _, row := range rows {
		alliance, err := r.populateAllianceGuilds(row)
		if err != nil {
			continue
		}
		alliances = append(alliances, alliance)
	}
	return alliances, nil
}

// CreateAlliance creates a new guild alliance with the given parent guild.
func (r *GuildRepository) CreateAlliance(name string, parentGuildID uint32) error {
	_, err := r.db.Exec("INSERT INTO guild_alliances (name, parent_id) VALUES ($1, $2)", name, parentGuildID)
	return err
}

// CreateAllianceForMember checks the current guild leader and serializes
// alliance creation so retries cannot leave a guild in multiple alliances.
func (r *GuildRepository) CreateAllianceForMember(name string, parentGuildID, actorCharID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`LOCK TABLE guild_alliances IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if err := requireGuildScopedMutation(tx.Exec(`
		INSERT INTO guild_alliances (name, parent_id)
		SELECT $1::varchar, $2::integer
		WHERE EXISTS (
			SELECT 1
			FROM guild_characters gc
			JOIN guilds g ON g.id = gc.guild_id AND g.leader_id = gc.character_id
			WHERE gc.guild_id = $2
			  AND gc.character_id = $3
		)
		AND NOT EXISTS (
			SELECT 1 FROM guild_alliances
			WHERE name = $1 OR $2 IN (parent_id, sub1_id, sub2_id)
		)
		AND NOT EXISTS (SELECT 1 FROM guilds WHERE name = $1)
		AND NOT EXISTS (SELECT 1 FROM guild_alliance_applications WHERE guild_id = $2)
	`, name, parentGuildID, actorCharID)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAlliance removes an alliance by ID.
func (r *GuildRepository) DeleteAlliance(allianceID uint32) error {
	_, err := r.db.Exec("DELETE FROM guild_alliances WHERE id=$1", allianceID)
	return err
}

// RemoveGuildFromAlliance removes a guild from its alliance, shifting sub2 into sub1's slot if needed.
func (r *GuildRepository) RemoveGuildFromAlliance(allianceID, guildID, subGuild1ID, subGuild2ID uint32) error {
	_ = subGuild1ID
	_ = subGuild2ID
	return requireGuildScopedMutation(r.db.Exec(`
		UPDATE guild_alliances
		SET sub1_id = CASE WHEN sub1_id = $2 THEN sub2_id ELSE sub1_id END,
		    sub2_id = NULL
		WHERE id = $1
		  AND (sub1_id = $2 OR sub2_id = $2)
	`, allianceID, guildID))
}

// LeaveAlliance withdraws the actor's application or removes the actor's guild
// from a subordinate slot. The client uses the same action in both cases.
func (r *GuildRepository) LeaveAlliance(allianceID, guildID, actorCharID uint32) error {
	var changed bool
	err := r.db.Get(&changed, `
		WITH leader AS (
			SELECT 1
			FROM guilds g
			JOIN guild_characters gc
			  ON gc.guild_id = g.id
			 AND gc.character_id = $3
			WHERE g.id = $2
			  AND g.leader_id = $3
		), withdrawn AS (
			DELETE FROM guild_alliance_applications
			WHERE alliance_id = $1 AND guild_id = $2 AND EXISTS (SELECT 1 FROM leader)
			RETURNING guild_id
		), left_alliance AS (
			UPDATE guild_alliances ga
			SET sub1_id = CASE WHEN ga.sub1_id = $2 THEN ga.sub2_id ELSE ga.sub1_id END,
			    sub2_id = NULL
			WHERE ga.id = $1
			  AND (ga.sub1_id = $2 OR ga.sub2_id = $2)
			  AND EXISTS (SELECT 1 FROM leader)
			RETURNING ga.id
		)
		SELECT EXISTS (SELECT 1 FROM withdrawn) OR EXISTS (SELECT 1 FROM left_alliance)
	`, allianceID, guildID, actorCharID)
	if err == nil && !changed {
		return errGuildScopedMutationRejected
	}
	return err
}

// KickGuildFromAlliance removes a subordinate guild only when actorCharID is
// the current leader and an actual member of the alliance's parent guild.
func (r *GuildRepository) KickGuildFromAlliance(allianceID, guildID, actorCharID uint32) error {
	return requireGuildScopedMutation(r.db.Exec(`
		UPDATE guild_alliances ga
		SET sub1_id = CASE WHEN ga.sub1_id = $2 THEN ga.sub2_id ELSE ga.sub1_id END,
		    sub2_id = NULL
		WHERE ga.id = $1
		  AND (ga.sub1_id = $2 OR ga.sub2_id = $2)
		  AND EXISTS (
			SELECT 1
			FROM guilds g
			JOIN guild_characters gc
			  ON gc.guild_id = g.id
			 AND gc.character_id = $3
			WHERE g.id = ga.parent_id
			  AND g.leader_id = $3
		  )
	`, allianceID, guildID, actorCharID))
}

// SetAllianceRecruiting updates whether an alliance is accepting applications.
func (r *GuildRepository) SetAllianceRecruiting(allianceID uint32, recruiting bool) error {
	_, err := r.db.Exec("UPDATE guild_alliances SET recruiting=$1 WHERE id=$2", recruiting, allianceID)
	return err
}

func (r *GuildRepository) populateAllianceGuilds(alliance *GuildAlliance) (*GuildAlliance, error) {
	parentGuild, err := r.GetByID(alliance.ParentGuildID)
	if err != nil {
		return nil, err
	}
	if parentGuild == nil {
		return nil, fmt.Errorf("alliance %d references non-existent parent guild %d", alliance.ID, alliance.ParentGuildID)
	}
	alliance.ParentGuild = *parentGuild
	alliance.TotalMembers += parentGuild.MemberCount

	if alliance.SubGuild1ID > 0 {
		subGuild1, err := r.GetByID(alliance.SubGuild1ID)
		if err != nil {
			return nil, err
		}
		if subGuild1 == nil {
			return nil, fmt.Errorf("alliance %d references non-existent sub guild 1 %d", alliance.ID, alliance.SubGuild1ID)
		}
		alliance.SubGuild1 = *subGuild1
		alliance.TotalMembers += subGuild1.MemberCount
	}

	if alliance.SubGuild2ID > 0 {
		subGuild2, err := r.GetByID(alliance.SubGuild2ID)
		if err != nil {
			return nil, err
		}
		if subGuild2 == nil {
			return nil, fmt.Errorf("alliance %d references non-existent sub guild 2 %d", alliance.ID, alliance.SubGuild2ID)
		}
		alliance.SubGuild2 = *subGuild2
		alliance.TotalMembers += subGuild2.MemberCount
	}

	return alliance, nil
}
