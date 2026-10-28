package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

const festaLifecycleLock int64 = 1178940481

var errFestaRoundChanged = errors.New("Festa round or chapter changed")

type FestaEventLifecycleRepository interface {
	EnsureFestaEvent(time.Time) (FestaEvent, error)
	ForFestaEvent(uint32) FestaRepo
}

func (r *FestaRepository) ForFestaEvent(id uint32) FestaRepo {
	return &FestaRepository{db: r.db, eventID: &id}
}

func (r *FestaRepository) EnsureFestaEvent(now time.Time) (FestaEvent, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return FestaEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1)`, festaLifecycleLock); err != nil {
		return FestaEvent{}, err
	}
	var e FestaEvent
	err = tx.Get(&e, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time,e.festa_continuous
		FROM festa_event_lifecycle l JOIN events e ON e.id=l.event_id WHERE l.singleton`)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	if e.ID == 0 || now.Unix() >= int64(e.StartTime)+festaCycleSeconds {
		start := rotationMondayNoon(now)
		if e.ID != 0 {
			cycle := time.Duration(festaCycleSeconds) * time.Second
			oldStart := time.Unix(int64(e.StartTime), 0)
			start = oldStart.Add(now.Sub(oldStart) / cycle * cycle)
		}
		if start.Unix() <= 0 || start.Unix()+festaCycleSeconds > 1<<32-1 {
			return e, errors.New("Festa schedule outside protocol timestamp range")
		}
		if err = tx.QueryRow(`INSERT INTO events(event_type,start_time,festa_continuous) VALUES('festa',$1,true) RETURNING id`, start.UTC()).Scan(&e.ID); err != nil {
			return e, err
		}
		e.StartTime, e.Continuous = uint32(start.Unix()), true
		// Old rounds and their state remain immutable history. Only vote slots
		// belong to the current guild membership and need clearing at rollover.
		if _, err = tx.Exec(`UPDATE guild_characters SET trial_vote=NULL`); err != nil {
			return e, err
		}
		if _, err = tx.Exec(`INSERT INTO festa_event_lifecycle(singleton,event_id) VALUES(true,$1)
			ON CONFLICT(singleton) DO UPDATE SET event_id=EXCLUDED.event_id`, e.ID); err != nil {
			return e, err
		}
	}
	return e, tx.Commit()
}

// Bound writes serialize against rollover and check the DB clock after taking
// the lock. A stale result or receipt cannot become new-round progress.
func (r *FestaRepository) writeFesta(phases uint, fn func(*sqlx.Tx) error) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if r.eventID != nil {
		if _, err = tx.Exec(`SELECT pg_advisory_xact_lock_shared($1)`, festaLifecycleLock); err != nil {
			return err
		}
		var e FestaEvent
		var now time.Time
		err = tx.QueryRow(`SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint,e.festa_continuous,clock_timestamp()
			FROM festa_event_lifecycle l JOIN events e ON e.id=l.event_id WHERE l.singleton`).Scan(&e.ID, &e.StartTime, &e.Continuous, &now)
		if err != nil {
			return err
		}
		if e.ID != *r.eventID || phases&(1<<e.phase(now)) == 0 {
			return errFestaRoundChanged
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
