package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type apiEventRepository struct {
	db *sqlx.DB
}

func (r *apiEventRepository) RecurringEventSnapshot(ctx context.Context) (recurringEventSnapshot, error) {
	var result recurringEventSnapshot
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, query := range []struct {
		target interface{}
		sql    string
	}{
		{&result.Festa, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time,e.festa_continuous FROM festa_event_lifecycle l JOIN events e ON e.id=l.event_id WHERE l.singleton`},
		{&result.Diva, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time,e.diva_continuous FROM diva_event_lifecycle l JOIN events e ON e.id=l.event_id WHERE l.mode=-1`},
		{&result.Tower, `SELECT e.earth_id,e.starts_at,e.ends_at,e.claim_until FROM tower_event_lifecycle l JOIN tower_events e USING(earth_id) WHERE l.singleton`},
	} {
		if err = tx.GetContext(ctx, query.target, query.sql); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	return result, tx.Commit()
}

// NewAPIEventRepository creates an APIEventRepo backed by PostgreSQL.
func NewAPIEventRepository(db *sqlx.DB) APIEventRepo {
	return &apiEventRepository{db: db}
}

func (r *apiEventRepository) GetFeatureWeapon(ctx context.Context, startTime time.Time) (*FeatureWeaponRow, error) {
	var row FeatureWeaponRow
	err := r.db.GetContext(ctx, &row, `SELECT start_time, featured FROM feature_weapon WHERE start_time=$1`, startTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *apiEventRepository) GetActiveEvents(ctx context.Context, eventType string) ([]EventRow, error) {
	var rows []EventRow
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, (EXTRACT(epoch FROM start_time)::int) as start_time FROM events WHERE event_type=$1`, eventType)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
