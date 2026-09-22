package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// Map activation is deliberately separate from the personal reward cutover.
// A specifically activated historical round may expose new map progress and
// map rewards without making its old personal totals reward-eligible.
type DivaMapRewardWindowRepository interface {
	GetDivaMapRewardEvent(now time.Time) (DivaEvent, error)
}

func divaMapRewardEvent(q sqlx.Queryer, now time.Time) (DivaEvent, error) {
	// Match PostgreSQL precision without rounding a boundary-1ns request forward.
	now = now.Truncate(time.Microsecond)
	var row struct {
		DivaEvent
		PersonalLegacy bool `db:"personal_legacy"`
		MapActivated   bool `db:"map_activated"`
	}
	err := sqlx.Get(q, &row, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time,
		EXISTS(SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=e.id) AS personal_legacy,
		EXISTS(SELECT 1 FROM diva_map_activations a WHERE a.event_id=e.id AND a.activated_at<=$1) AS map_activated
		FROM diva_interception_periods p JOIN events e ON e.id=p.event_id
		WHERE p.starts_at<=$1 AND e.event_type='diva'
		ORDER BY p.starts_at DESC,p.event_id DESC LIMIT 1`, now)
	if errors.Is(err, sql.ErrNoRows) {
		return DivaEvent{}, nil
	}
	if err != nil {
		return DivaEvent{}, err
	}
	// Eligibility must not be a WHERE filter: an ineligible NEW period still
	// closes the older period, rather than reviving its map/reward page.
	if row.PersonalLegacy && !row.MapActivated {
		return DivaEvent{}, nil
	}
	return row.DivaEvent, nil
}

func (r *DivaRepository) GetDivaMapRewardEvent(now time.Time) (DivaEvent, error) {
	return divaMapRewardEvent(r.db, now)
}

// Lock ordering and lifecycle publication match the personal reward path.
// Only the final eligibility selector differs. A zero clock means sample the
// database time AFTER waiting for the lock, never a stale pre-boundary time.
func lockDivaMapRewardWindow(tx *sqlx.Tx, now time.Time, modes ...int) (DivaEvent, error) {
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
		case 1, 3:
			// Forced prayer/welcome must not invent a new actual interception.
		default:
			return DivaEvent{}, ErrDivaInterceptionRewardExpired
		}
	}
	return divaMapRewardEvent(tx, now)
}
