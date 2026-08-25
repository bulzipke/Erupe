package channelserver

import "time"

// ListMeals returns all meals for a guild.
func (r *GuildRepository) ListMeals(guildID uint32) ([]*GuildMeal, error) {
	rows, err := r.db.Queryx("SELECT id, meal_id, level, created_at FROM guild_meals WHERE guild_id = $1", guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var meals []*GuildMeal
	for rows.Next() {
		meal := &GuildMeal{}
		if err := rows.StructScan(meal); err != nil {
			continue
		}
		meals = append(meals, meal)
	}
	return meals, nil
}

// CreateMeal inserts a new guild meal and returns the new ID.
func (r *GuildRepository) CreateMeal(guildID, mealID, level uint32, createdAt time.Time) (uint32, error) {
	var id uint32
	err := r.db.QueryRow(
		"INSERT INTO guild_meals (guild_id, meal_id, level, created_at) VALUES ($1, $2, $3, $4) RETURNING id",
		guildID, mealID, level, createdAt).Scan(&id)
	return id, err
}

// CreateMealForGuild creates a meal only for the actor's current guild.
func (r *GuildRepository) CreateMealForGuild(guildID, actorCharID, mealID, level uint32, createdAt time.Time) (uint32, error) {
	var id uint32
	err := r.db.QueryRow(`
		INSERT INTO guild_meals (guild_id, meal_id, level, created_at)
		SELECT $1, $3, $4, $5
		WHERE EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = $1
			  AND gc.character_id = $2
		)
		RETURNING id
	`, guildID, actorCharID, mealID, level, createdAt).Scan(&id)
	return id, err
}

// UpdateMeal updates an existing guild meal's fields.
func (r *GuildRepository) UpdateMeal(mealID, newMealID, level uint32, createdAt time.Time) error {
	_, err := r.db.Exec("UPDATE guild_meals SET meal_id = $1, level = $2, created_at = $3 WHERE id = $4",
		newMealID, level, createdAt, mealID)
	return err
}

// UpdateMealForGuild updates a meal only when it belongs to the actor's current
// guild membership.
func (r *GuildRepository) UpdateMealForGuild(guildID, actorCharID, mealID, newMealID, level uint32, createdAt time.Time) error {
	return requireGuildScopedMutation(r.db.Exec(`
		UPDATE guild_meals gm
		SET meal_id = $1, level = $2, created_at = $3
		WHERE gm.id = $4
		  AND gm.guild_id = $5
		  AND EXISTS (
			SELECT 1
			FROM guild_characters gc
			WHERE gc.guild_id = gm.guild_id
			  AND gc.character_id = $6
		  )
	`, newMealID, level, createdAt, mealID, guildID, actorCharID))
}

// ClaimHuntBox updates the box_claimed timestamp for a guild character.
func (r *GuildRepository) ClaimHuntBox(charID uint32, claimedAt time.Time) error {
	_, err := r.db.Exec(`UPDATE guild_characters SET box_claimed=$1 WHERE character_id=$2`, claimedAt, charID)
	return err
}
