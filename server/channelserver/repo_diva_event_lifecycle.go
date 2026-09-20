package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Kept separate from DivaRepo so older in-memory repositories can still be
// used by unrelated handlers. The production repository implements this API.
type DivaEventLifecycleRepository interface {
	EnsureDivaEvent(now time.Time, override int) (DivaEvent, error)
}

func divaLifecycleStart(now time.Time, mode int) time.Time {
	t := now.In(divaLocation)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, divaLocation)
	switch mode {
	case 2:
		return day.Add(-time.Duration(divaPhaseDuration+divaInterlude) * time.Second)
	case 3:
		return day.Add(-time.Duration(divaPhaseDuration+divaWeekDuration+divaInterlude) * time.Second)
	default:
		return day.Add(24 * time.Hour)
	}
}

func divaLifecycleExpiry(event DivaEvent, mode int) time.Time {
	seconds := int64(divaTotalLifespan)
	switch mode {
	case 2:
		seconds = divaPhaseDuration + divaWeekDuration
	case 3:
		seconds = divaPhaseDuration + 2*divaWeekDuration
	}
	return time.Unix(int64(event.StartTime)+seconds, 0)
}

// Resolve an event under a database-wide lock. No normal/forced rollover deletes
// old events, song journals, bead history, interception progress or receipts.
func (r *DivaRepository) EnsureDivaEvent(now time.Time, override int) (DivaEvent, error) {
	if override == 1 {
		return r.EnsureDivaSongEvent(now)
	}
	if override == 0 {
		return DivaEvent{}, nil
	}
	if override < -1 || override > 3 {
		return DivaEvent{}, fmt.Errorf("unsupported diva phase override: %d", override)
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Same lock as EnsureDivaSongEvent, covering all phase-anchor creation.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(1146507841)"); err != nil {
		return DivaEvent{}, err
	}
	event, err := ensureDivaEventTx(tx, now, override)
	if err != nil {
		return DivaEvent{}, err
	}
	return event, tx.Commit()
}

// Caller holds the shared lifecycle advisory lock. Reward transactions also
// use this helper after reading the DB clock to close boundary races.
func ensureDivaEventTx(tx *sqlx.Tx, now time.Time, override int) (DivaEvent, error) {
	var event DivaEvent
	err := tx.Get(&event, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time
		FROM diva_event_lifecycle l JOIN events e ON e.id=l.event_id
		WHERE l.mode=$1 AND e.event_type='diva'`, override)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DivaEvent{}, err
	}
	firstAnchor := event.ID == 0
	if firstAnchor && override == -1 {
		// Adopt an existing schedule only once. Later cycles use their own stable
		// pointer rather than accidentally adopting a backdated debug event.
		err = tx.Get(&event, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time
			FROM events e WHERE e.event_type='diva'
			AND NOT EXISTS(SELECT 1 FROM diva_debug_song_event d WHERE d.event_id=e.id)
			AND NOT EXISTS(SELECT 1 FROM diva_event_lifecycle l WHERE l.event_id=e.id AND l.mode IN (2,3))
			ORDER BY e.start_time DESC,e.id DESC LIMIT 1`)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return DivaEvent{}, err
		}
	}
	// Preserve a completed normal round even when nobody requested its schedule
	// during interception itself. Record before advancing the active pointer.
	if err = recordStartedDivaInterceptionPeriod(tx, event, override, now); err != nil {
		return DivaEvent{}, err
	}
	if event.ID == 0 || !now.Before(divaLifecycleExpiry(event, override)) {
		legacyBootstrap := false
		if firstAnchor && (override == 2 || override == 3) {
			// An already-running legacy round must not turn reward-eligible merely
			// because a formerly unanchored forced phase obtains a new DB ID.
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM diva_interception_legacy_events l
				JOIN events e ON e.id=l.event_id WHERE e.event_type='diva'
				AND e.start_time <= $1 AND e.start_time + INTERVAL '2977200 seconds' > $1)`, now).Scan(&legacyBootstrap)
			if err != nil {
				return DivaEvent{}, err
			}
		}
		start := divaLifecycleStart(now, override)
		if start.Unix() <= 0 || start.Unix()+divaTotalLifespan > 0xffffffff {
			return DivaEvent{}, errors.New("diva event is outside protocol timestamp range")
		}
		if err = tx.QueryRow(`INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id`, start.UTC()).Scan(&event.ID); err != nil {
			return DivaEvent{}, err
		}
		event.StartTime = uint32(start.Unix())
		if legacyBootstrap {
			if _, err = tx.Exec(`INSERT INTO diva_interception_legacy_events(event_id) VALUES($1)`, event.ID); err != nil {
				return DivaEvent{}, err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO diva_event_lifecycle(mode,event_id) VALUES($1,$2)
		ON CONFLICT(mode) DO UPDATE SET event_id=EXCLUDED.event_id`, override, event.ID); err != nil {
		return DivaEvent{}, err
	}
	if err = recordStartedDivaInterceptionPeriod(tx, event, override, now); err != nil {
		return DivaEvent{}, err
	}
	return event, nil
}
