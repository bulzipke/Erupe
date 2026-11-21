package channelserver

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// StampRepository centralizes all database access for the stamps table.
type StampRepository struct {
	db *sqlx.DB
}

// NewStampRepository creates a new StampRepository.
func NewStampRepository(db *sqlx.DB) *StampRepository {
	return &StampRepository{db: db}
}

// ClaimDaily adds one stamp if this character and type have not claimed the current period.
func (r *StampRepository) ClaimDaily(charID uint32, stampType string, periodStart time.Time) (total, redeemed uint16, added bool, err error) {
	if stampType != "hl" && stampType != "ex" {
		return 0, 0, false, fmt.Errorf("invalid stamp type %q", stampType)
	}
	// The conflict check and increment share a row lock across channels and processes.
	query := fmt.Sprintf(`
		INSERT INTO stamps (character_id, %[1]s_checked, %[1]s_total) VALUES ($1, $2, 1)
		ON CONFLICT (character_id) DO UPDATE
		SET %[1]s_checked = EXCLUDED.%[1]s_checked, %[1]s_total = COALESCE(stamps.%[1]s_total, 0) + 1
		WHERE stamps.%[1]s_checked IS NULL OR stamps.%[1]s_checked < EXCLUDED.%[1]s_checked
		RETURNING %[1]s_total, COALESCE(%[1]s_redeemed, 0)`, stampType)
	err = r.db.QueryRow(query, charID, periodStart).Scan(&total, &redeemed)
	if err == sql.ErrNoRows {
		total, redeemed, err = r.GetTotals(charID, stampType)
		return total, redeemed, false, err
	}
	return total, redeemed, err == nil, err
}

// GetTotals returns the total and redeemed counts for a given stamp type.
func (r *StampRepository) GetTotals(charID uint32, stampType string) (total, redeemed uint16, err error) {
	err = r.db.QueryRow(fmt.Sprintf("SELECT COALESCE(%[1]s_total, 0), COALESCE(%[1]s_redeemed, 0) FROM stamps WHERE character_id=$1", stampType), charID).Scan(&total, &redeemed)
	return
}

// ExchangeYearly performs a yearly stamp exchange, subtracting 48 from both hl_total and hl_redeemed.
func (r *StampRepository) ExchangeYearly(charID uint32) (total, redeemed uint16, err error) {
	err = r.db.QueryRow("UPDATE stamps SET hl_total=hl_total-48, hl_redeemed=hl_redeemed-48 WHERE character_id=$1 RETURNING hl_total, hl_redeemed", charID).Scan(&total, &redeemed)
	return
}

// Exchange performs a stamp exchange, adding 8 to the redeemed count for a given stamp type.
func (r *StampRepository) Exchange(charID uint32, stampType string) (total, redeemed uint16, err error) {
	err = r.db.QueryRow(fmt.Sprintf("UPDATE stamps SET %s_redeemed=%s_redeemed+8 WHERE character_id=$1 RETURNING %s_total, %s_redeemed", stampType, stampType, stampType, stampType), charID).Scan(&total, &redeemed)
	return
}

// GetMonthlyClaimed returns the last monthly item claim time for the given type.
func (r *StampRepository) GetMonthlyClaimed(charID uint32, monthlyType string) (time.Time, error) {
	var claimed time.Time
	err := r.db.QueryRow(
		fmt.Sprintf("SELECT %s_claimed FROM stamps WHERE character_id=$1", monthlyType), charID,
	).Scan(&claimed)
	return claimed, err
}

// SetMonthlyClaimed updates the monthly item claim time for the given type.
func (r *StampRepository) SetMonthlyClaimed(charID uint32, monthlyType string, now time.Time) error {
	_, err := r.db.Exec(
		fmt.Sprintf("UPDATE stamps SET %s_claimed=$1 WHERE character_id=$2", monthlyType), now, charID,
	)
	return err
}
