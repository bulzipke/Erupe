package channelserver

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// WeaponUsageRepository records server-observed quest departures by weapon
// class.  It deliberately reads the character's persisted weapon type rather
// than trusting a client-supplied statistics packet.
type WeaponUsageRepository struct {
	db *sqlx.DB
}

// NewWeaponUsageRepository creates a WeaponUsageRepository.
func NewWeaponUsageRepository(db *sqlx.DB) *WeaponUsageRepository {
	return &WeaponUsageRepository{db: db}
}

// RecordQuestDeparture atomically increments the usage row matching the
// character's currently persisted weapon type and returns that type.  ok is
// false when the character is unavailable or its type is outside the 14 known
// hunter weapon classes.
func (r *WeaponUsageRepository) RecordQuestDeparture(characterID uint32) (weaponType uint8, ok bool, err error) {
	if r == nil || r.db == nil {
		return 0, false, nil
	}
	var stored int16
	err = r.db.QueryRow(`
		INSERT INTO weapon_usage_stats (weapon_type, usage_count)
		SELECT character.weapon_type::integer, 1
		FROM characters AS character
		WHERE character.id = $1
		  AND character.deleted = false
		  AND COALESCE(character.is_new_character, false) = false
		  AND character.weapon_type::integer >= 0
		  AND character.weapon_type::integer < 14
		ON CONFLICT (weapon_type) DO UPDATE
		SET usage_count = weapon_usage_stats.usage_count + 1
		RETURNING weapon_type
	`, characterID).Scan(&stored)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("record quest-departure weapon usage: %w", err)
	}
	return uint8(stored), true, nil
}
