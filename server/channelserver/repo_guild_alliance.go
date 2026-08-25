package channelserver

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

const allianceInfoSelectSQL = `
SELECT
ga.id,
ga.name,
created_at,
ga.recruiting,
parent_id,
CASE
	WHEN sub1_id IS NULL THEN 0
	ELSE sub1_id
END,
CASE
	WHEN sub2_id IS NULL THEN 0
	ELSE sub2_id
END
FROM guild_alliances ga
`

// GetAllianceByID loads alliance data including parent and sub guilds.
func (r *GuildRepository) GetAllianceByID(allianceID uint32) (*GuildAlliance, error) {
	rows, err := r.db.Queryx(fmt.Sprintf(`%s WHERE ga.id = $1`, allianceInfoSelectSQL), allianceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, nil
	}
	return r.scanAllianceWithGuilds(rows)
}

// ListAlliances returns all alliances with their guild data populated.
func (r *GuildRepository) ListAlliances() ([]*GuildAlliance, error) {
	rows, err := r.db.Queryx(allianceInfoSelectSQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var alliances []*GuildAlliance
	for rows.Next() {
		alliance, err := r.scanAllianceWithGuilds(rows)
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

// CreateAllianceForMember creates an alliance only while actorCharID is an
// actual member of parentGuildID. It intentionally adds no new role policy.
func (r *GuildRepository) CreateAllianceForMember(name string, parentGuildID, actorCharID uint32) error {
	return requireGuildScopedMutation(r.db.Exec(`
		INSERT INTO guild_alliances (name, parent_id)
		SELECT $1, $2
		WHERE EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = $2
			  AND gc.character_id = $3
		)
	`, name, parentGuildID, actorCharID))
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

// LeaveAlliance removes the actor's own guild from a subordinate alliance
// slot. Both leadership and membership are checked in the same statement.
func (r *GuildRepository) LeaveAlliance(allianceID, guildID, actorCharID uint32) error {
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
			WHERE g.id = $2
			  AND g.leader_id = $3
		  )
	`, allianceID, guildID, actorCharID))
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

// scanAllianceWithGuilds scans an alliance row and populates its guild data.
func (r *GuildRepository) scanAllianceWithGuilds(rows *sqlx.Rows) (*GuildAlliance, error) {
	alliance := &GuildAlliance{}
	if err := rows.StructScan(alliance); err != nil {
		return nil, err
	}

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
