package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// lockDailyPointTime serializes the claim, expiry and delta writers on the same
// character row. NULL retains the existing ReadTime fallback, not a new period.
func lockDailyPointTime(tx *sqlx.Tx, charID uint32) (time.Time, error) {
	var stored sql.NullTime
	if err := tx.QueryRow(`SELECT daily_time FROM characters WHERE id=$1 FOR UPDATE`, charID).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrCharacterNotFound
		}
		return time.Time{}, err
	}
	if !stored.Valid {
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), nil
	}
	return stored.Time, nil
}

// ClaimDailyCafe records one claim for a later caller-supplied daily boundary.
// The existing 5 NP credit, cap and allowances commit together. Being at the NP
// cap does not prevent the claim or change the separate bond-bonus response.
func (r *CharacterRepository) ClaimDailyCafe(charID uint32, claimUntil time.Time, bonus, daily uint32, maximumNP int) (bool, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	stored, err := lockDailyPointTime(tx, charID)
	if err != nil {
		return false, err
	}
	if !claimUntil.After(stored) {
		return false, tx.Commit()
	}
	var confirmed uint32
	err = tx.QueryRow(`UPDATE characters SET
		netcafe_points=LEAST(COALESCE(netcafe_points,0)::bigint+5,$5::bigint),
		daily_time=$2,bonus_quests=$3::bigint,daily_quests=$4::bigint
		WHERE id=$1 AND COALESCE(daily_time,$6)<$2 RETURNING id`,
		charID, claimUntil, bonus, daily, maximumNP,
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).Scan(&confirmed)
	if err != nil {
		return false, fmt.Errorf("claim daily cafe: %w", err)
	}
	if confirmed != charID {
		return false, fmt.Errorf("claim daily cafe: unexpected character %d", confirmed)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ReadDailyEtcPoints expires only the still-expired row it locked, then reads
// that same state. A failed read rolls the reset back. The strict comparison
// matches the existing now.After(daily_time) behavior.
func (r *CharacterRepository) ReadDailyEtcPoints(charID uint32, now time.Time) (uint32, uint32, uint32, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	stored, err := lockDailyPointTime(tx, charID)
	if err != nil {
		return 0, 0, 0, err
	}
	var bonus, daily, promo uint32
	if now.After(stored) {
		err = tx.QueryRow(`UPDATE characters SET bonus_quests=0,daily_quests=0
			WHERE id=$1 AND COALESCE(daily_time,$3)=$2
			RETURNING bonus_quests,daily_quests,promo_points`,
			charID, stored, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).Scan(&bonus, &daily, &promo)
	} else {
		err = tx.QueryRow(`SELECT bonus_quests,daily_quests,promo_points FROM characters WHERE id=$1`,
			charID).Scan(&bonus, &daily, &promo)
	}
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read daily etc points: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return bonus, daily, promo, nil
}

// AdjustEtcPoint applies the existing generic signed delta and zero floor.
// It does not authenticate a quest, reserve a point or reject an empty balance.
func (r *CharacterRepository) AdjustEtcPoint(charID uint32, pointType uint8, delta int16) error {
	var column string
	switch pointType {
	case 0:
		column = "bonus_quests"
	case 1:
		column = "daily_quests"
	case 2:
		column = "promo_points"
	default:
		return nil // preserve the unsupported-type no-op
	}
	var confirmed int64
	err := r.db.QueryRow(`UPDATE characters SET `+column+`=GREATEST(0,COALESCE(`+column+`,0)::bigint+$2::bigint)
		WHERE id=$1 RETURNING `+column, charID, delta).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCharacterNotFound
	}
	return err
}

// AddCappedNetcafePoints keeps addPointNetcafe's min(current+delta, maximum)
// arithmetic while serializing it with other writers. The DB's existing int4
// range and nonnegative constraint remain authoritative; no new clamp is added.
func (r *CharacterRepository) AddCappedNetcafePoints(charID uint32, delta int, maximumNP int) error {
	var confirmed int64
	err := r.db.QueryRow(`UPDATE characters SET netcafe_points=LEAST(COALESCE(netcafe_points,0)::bigint+$2::bigint,$3::bigint)
		WHERE id=$1 RETURNING netcafe_points`, charID, delta, maximumNP).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCharacterNotFound
	}
	return err
}
