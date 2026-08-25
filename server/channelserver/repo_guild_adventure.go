package channelserver

import (
	"context"

	"erupe-ce/common/stringsupport"
)

// ListAdventures returns all adventures for a guild.
func (r *GuildRepository) ListAdventures(guildID uint32) ([]*GuildAdventure, error) {
	rows, err := r.db.Queryx(
		"SELECT id, destination, charge, depart, return, collected_by FROM guild_adventures WHERE guild_id = $1", guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var adventures []*GuildAdventure
	for rows.Next() {
		adv := &GuildAdventure{}
		if err := rows.StructScan(adv); err != nil {
			continue
		}
		adventures = append(adventures, adv)
	}
	return adventures, nil
}

// CreateAdventure inserts a new guild adventure.
func (r *GuildRepository) CreateAdventure(guildID, destination uint32, depart, returnTime int64) error {
	_, err := r.db.Exec(
		"INSERT INTO guild_adventures (guild_id, destination, depart, return) VALUES ($1, $2, $3, $4)",
		guildID, destination, depart, returnTime)
	return err
}

// CreateAdventureForGuild creates an adventure only for the actor's current guild.
func (r *GuildRepository) CreateAdventureForGuild(guildID, actorCharID, destination uint32, depart, returnTime int64) error {
	return requireGuildScopedMutation(r.db.Exec(`
		INSERT INTO guild_adventures (guild_id, destination, depart, return)
		SELECT $1, $3, $4, $5
		WHERE EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = $1
			  AND gc.character_id = $2
		)
	`, guildID, actorCharID, destination, depart, returnTime))
}

// CreateAdventureWithCharge inserts a new guild adventure with an initial charge (Diva variant).
func (r *GuildRepository) CreateAdventureWithCharge(guildID, destination, charge uint32, depart, returnTime int64) error {
	_, err := r.db.Exec(
		"INSERT INTO guild_adventures (guild_id, destination, charge, depart, return) VALUES ($1, $2, $3, $4, $5)",
		guildID, destination, charge, depart, returnTime)
	return err
}

// CreateAdventureWithChargeForGuild creates a charged adventure only for the
// actor's current guild.
func (r *GuildRepository) CreateAdventureWithChargeForGuild(guildID, actorCharID, destination, charge uint32, depart, returnTime int64) error {
	return requireGuildScopedMutation(r.db.Exec(`
		INSERT INTO guild_adventures (guild_id, destination, charge, depart, return)
		SELECT $1, $3, $4, $5, $6
		WHERE EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = $1
			  AND gc.character_id = $2
		)
	`, guildID, actorCharID, destination, charge, depart, returnTime))
}

// CollectAdventure marks an adventure as collected by the given character (CSV append).
// Uses SELECT FOR UPDATE to prevent concurrent double-collect.
func (r *GuildRepository) CollectAdventure(adventureID uint32, charID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var collectedBy string
	err = tx.QueryRow("SELECT collected_by FROM guild_adventures WHERE id = $1 FOR UPDATE", adventureID).Scan(&collectedBy)
	if err != nil {
		return err
	}
	collectedBy = stringsupport.CSVAdd(collectedBy, int(charID))
	if _, err = tx.Exec("UPDATE guild_adventures SET collected_by = $1 WHERE id = $2", collectedBy, adventureID); err != nil {
		return err
	}
	return tx.Commit()
}

// CollectAdventureForGuild records a collection only for an adventure in the
// actor's current guild.
func (r *GuildRepository) CollectAdventureForGuild(guildID, adventureID, charID uint32) error {
	tx, err := r.db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var collectedBy string
	err = tx.QueryRow(`
		SELECT ga.collected_by
		FROM guild_adventures ga
		WHERE ga.id = $1
		  AND ga.guild_id = $2
		  AND EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = ga.guild_id
			  AND gc.character_id = $3
		  )
		FOR UPDATE
	`, adventureID, guildID, charID).Scan(&collectedBy)
	if err != nil {
		return err
	}
	collectedBy = stringsupport.CSVAdd(collectedBy, int(charID))
	if err := requireGuildScopedMutation(tx.Exec(`
		UPDATE guild_adventures ga
		SET collected_by = $1
		WHERE ga.id = $2
		  AND ga.guild_id = $3
		  AND EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = ga.guild_id
			  AND gc.character_id = $4
		  )
	`, collectedBy, adventureID, guildID, charID)); err != nil {
		return err
	}
	return tx.Commit()
}

// ChargeAdventure adds charge to a guild adventure.
func (r *GuildRepository) ChargeAdventure(adventureID uint32, amount uint32) error {
	_, err := r.db.Exec("UPDATE guild_adventures SET charge = charge + $1 WHERE id = $2", amount, adventureID)
	return err
}

// ChargeAdventureForGuild charges an adventure only when it belongs to the
// actor's current guild.
func (r *GuildRepository) ChargeAdventureForGuild(guildID, adventureID, charID, amount uint32) error {
	return requireGuildScopedMutation(r.db.Exec(`
		UPDATE guild_adventures ga
		SET charge = charge + $1
		WHERE ga.id = $2
		  AND ga.guild_id = $3
		  AND EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = ga.guild_id
			  AND gc.character_id = $4
		  )
	`, amount, adventureID, guildID, charID))
}
