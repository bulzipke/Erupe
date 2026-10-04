package channelserver

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// CafeRepository centralizes all database access for cafe-related tables.
type CafeRepository struct {
	db *sqlx.DB
}

// NewCafeRepository creates a new CafeRepository.
func NewCafeRepository(db *sqlx.DB) *CafeRepository {
	return &CafeRepository{db: db}
}

// ResetAccepted deletes all accepted cafe bonuses for a character.
func (r *CafeRepository) ResetAccepted(charID uint32) error {
	_, err := r.db.Exec(`DELETE FROM cafe_accepted WHERE character_id=$1`, charID)
	return err
}

// GetBonuses returns all cafe bonuses with their claimed status for a character.
func (r *CafeRepository) GetBonuses(charID uint32) ([]CafeBonus, error) {
	var result []CafeBonus
	err := r.db.Select(&result, `
	SELECT cb.id, time_req, item_type, item_id, quantity,
	(
		SELECT count(*)
		FROM cafe_accepted ca
		WHERE cb.id = ca.cafe_id AND ca.character_id = $1
	)::int::bool AS claimed
	FROM cafebonus cb ORDER BY id ASC;`, charID)
	return result, err
}

// GetClaimable returns unclaimed cafe bonuses where the character has enough accumulated time.
func (r *CafeRepository) GetClaimable(charID uint32, elapsedSec int64) ([]CafeBonus, error) {
	var result []CafeBonus
	err := r.db.Select(&result, `
	SELECT c.id, time_req, item_type, item_id, quantity
	FROM cafebonus c
	WHERE (
		SELECT count(*)
		FROM cafe_accepted ca
		WHERE c.id = ca.cafe_id AND ca.character_id = $1
	) < 1 AND (
		SELECT ch.cafe_time + $2
		FROM characters ch
		WHERE ch.id = $1
	) >= time_req`, charID, elapsedSec)
	return result, err
}

// GetBonusItem returns the item type and quantity for a specific cafe bonus.
func (r *CafeRepository) GetBonusItem(bonusID uint32) (itemType, quantity uint32, err error) {
	err = r.db.QueryRow(`SELECT cb.id, item_type, quantity FROM cafebonus cb WHERE cb.id=$1`, bonusID).Scan(&bonusID, &itemType, &quantity)
	return
}

// AcceptBonus records that a character has accepted a cafe bonus.
func (r *CafeRepository) AcceptBonus(bonusID, charID uint32) error {
	_, err := r.db.Exec("INSERT INTO cafe_accepted VALUES ($1, $2)", bonusID, charID)
	return err
}

// CafeBonusClaim is the outcome of ClaimBonus.
type CafeBonusClaim struct {
	Claimed  bool // Recorded as accepted.
	Capped   bool // Not claimed: N points are already at the cap.
	ItemType uint32
	Granted  int // N points credited (item type 17 only).
}

// ClaimBonus accepts one eligible, not yet accepted bonus. elapsedSec is the
// current session's uncommitted time. N point bonuses (item type 17) credit at
// most maxPoints-current and are left unclaimed while already at the cap. The
// character row lock serializes automatic and manual claims.
func (r *CafeRepository) ClaimBonus(charID, bonusID uint32, elapsedSec int64, maxPoints int) (CafeBonusClaim, error) {
	var claim CafeBonusClaim
	tx, err := r.db.Beginx()
	if err != nil {
		return claim, err
	}
	defer tx.Rollback() //nolint:errcheck // rollback is no-op after commit

	var points, cafeTime int64
	if err := tx.QueryRow(`SELECT COALESCE(netcafe_points, 0), COALESCE(cafe_time, 0) FROM characters WHERE id=$1 FOR UPDATE`,
		charID).Scan(&points, &cafeTime); err != nil {
		return claim, err
	}
	var timeReq int64
	var quantity int
	err = tx.QueryRow(`SELECT time_req, item_type, quantity FROM cafebonus WHERE id=$1`, bonusID).Scan(&timeReq, &claim.ItemType, &quantity)
	if errors.Is(err, sql.ErrNoRows) {
		return claim, nil
	} else if err != nil {
		return claim, err
	}
	if cafeTime+elapsedSec < timeReq {
		return claim, nil
	}
	var accepted bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM cafe_accepted WHERE cafe_id=$1 AND character_id=$2)`,
		bonusID, charID).Scan(&accepted); err != nil || accepted {
		return claim, err
	}
	if claim.ItemType == cafeBonusItemNetcafePoints {
		room := int64(maxPoints) - points
		if room <= 0 {
			claim.Capped = true
			return claim, nil
		}
		claim.Granted = int(min(int64(quantity), room))
		if _, err := tx.Exec(`UPDATE characters SET netcafe_points=$2 WHERE id=$1`, charID, points+int64(claim.Granted)); err != nil {
			return CafeBonusClaim{}, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO cafe_accepted VALUES ($1, $2)`, bonusID, charID); err != nil {
		return CafeBonusClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return CafeBonusClaim{}, err
	}
	claim.Claimed = true
	return claim, nil
}
