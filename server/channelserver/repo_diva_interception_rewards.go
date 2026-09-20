package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

var ErrDivaInterceptionRewardExpired = errors.New("diva interception reward window is not open")

type DivaInterceptionRewardWindowRepository interface {
	GetDivaInterceptionRewardEvent(now time.Time) (DivaEvent, error)
	OfferDivaInterceptionRewards(charID, eventID uint32, rewards []DivaRewardCatalogEntry, override int) ([]DivaRewardOffer, error)
	PrepareDivaInterceptionRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error)
}

func (r *DivaRepository) OfferDivaInterceptionRewards(charID, eventID uint32, rewards []DivaRewardCatalogEntry, override int) ([]DivaRewardOffer, error) {
	return r.offerDivaRewardsAt(charID, eventID, 6, rewards, time.Time{}, override)
}

func (r *DivaRepository) PrepareDivaInterceptionRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error) {
	return r.prepareDivaRewardClaimsAt(charID, 6, ids, time.Time{}, override)
}

// Called while the lifecycle advisory lock is held. These absolute dates are
// immutable; a forced prayer/welcome anchor must never expire real rewards.
func recordDivaInterceptionPeriod(tx *sqlx.Tx, event DivaEvent, mode int) error {
	if mode != -1 && mode != 2 {
		return nil
	}
	start, end := divaInterceptionWindow(event)
	_, err := tx.Exec(`INSERT INTO diva_interception_periods(event_id,starts_at,ends_at)
		VALUES($1,$2,$3) ON CONFLICT(event_id) DO NOTHING`, event.ID, start.UTC(), end.UTC())
	return err
}

// A future normal schedule is not yet an actual interception: an administrator
// can switch to forced prayer before it begins. Every query/claim first resolves
// the lifecycle, so the first request at/after the boundary records it atomically.
func recordStartedDivaInterceptionPeriod(tx *sqlx.Tx, event DivaEvent, mode int, now time.Time) error {
	start, _ := divaInterceptionWindow(event)
	if event.ID == 0 || now.Before(start) {
		return nil
	}
	return recordDivaInterceptionPeriod(tx, event, mode)
}

// Native requests have no round selector. The most recently begun interception
// owns the one available reward page until the next interception begins. A
// character with no points in the new round must not fall back to older prizes.
func divaInterceptionRewardEvent(q sqlx.Queryer, now time.Time) (DivaEvent, error) {
	// PostgreSQL timestamps have microsecond precision. lib/pq can send nine
	// fractional digits, which PostgreSQL rounds: boundary-1ns would otherwise
	// become the boundary itself and expire the previous round prematurely.
	now = now.Truncate(time.Microsecond)
	var row struct {
		DivaEvent
		Legacy bool `db:"legacy"`
	}
	err := sqlx.Get(q, &row, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time,
		EXISTS(SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=e.id) AS legacy
		FROM diva_interception_periods p JOIN events e ON e.id=p.event_id
		WHERE p.starts_at <= $1 AND e.event_type='diva'
		ORDER BY p.starts_at DESC,p.event_id DESC LIMIT 1`, now)
	if errors.Is(err, sql.ErrNoRows) {
		return DivaEvent{}, nil
	}
	if err != nil {
		return DivaEvent{}, err
	}
	if row.Legacy {
		return DivaEvent{}, nil
	}
	return row.DivaEvent, nil
}

func (r *DivaRepository) GetDivaInterceptionRewardEvent(now time.Time) (DivaEvent, error) {
	return divaInterceptionRewardEvent(r.db, now)
}

// Offers and fresh claims serialize with all servers' schedule creation. Check
// the DB clock only after acquiring the lock, so a blocked request cannot use
// its stale pre-boundary wall clock. An explicit clock is only used by tests.
func lockDivaInterceptionRewardWindow(tx *sqlx.Tx, now time.Time, modes ...int) (DivaEvent, error) {
	if _, err := tx.Exec("SELECT pg_advisory_xact_lock(1146507841)"); err != nil {
		return DivaEvent{}, err
	}
	if now.IsZero() {
		if err := tx.QueryRow("SELECT clock_timestamp()").Scan(&now); err != nil {
			return DivaEvent{}, err
		}
	}
	if len(modes) > 0 {
		switch modes[0] {
		case -1, 2:
			if _, err := ensureDivaEventTx(tx, now, modes[0]); err != nil {
				return DivaEvent{}, err
			}
		case 1, 3: // These modes do not begin another actual interception.
		default:
			return DivaEvent{}, ErrDivaInterceptionRewardExpired
		}
	}
	return divaInterceptionRewardEvent(tx, now)
}
