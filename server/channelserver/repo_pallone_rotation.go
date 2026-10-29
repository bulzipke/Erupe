package channelserver

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	cfg "erupe-ce/config"
)

var _ PalloneRotationRepository = (*PalloneRepository)(nil)

// Automatic and explicit registration share one transaction lock. Channels
// cannot create different IDs at the boundary, reuse an old ledger, or change
// an already published round. No character state is deleted on rollover.
func (r *PalloneRepository) EnsurePalloneSchedule(now time.Time, o cfg.PalloneFestivalOptions, digest string, routes []PalloneRoute) (PalloneSchedule, error) {
	var result PalloneSchedule
	start, err := palloneInitialStart(now, o)
	if err != nil || !o.Enabled {
		return result, ErrPalloneInvalid
	}
	if raw, err := hex.DecodeString(digest); err != nil || len(raw) != 32 || digest != fmt.Sprintf("%x", raw) {
		return result, ErrPalloneInvalid
	}
	probe, err := palloneRotationEvent(1, start, o)
	if err != nil || validatePalloneEvent(probe, routes) != nil {
		return result, ErrPalloneInvalid
	}
	routes = append([]PalloneRoute(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Key < routes[j].Key })
	tx, err := r.db.Beginx()
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1)`, palloneLifecycleLock); err != nil {
		return result, err
	}
	var current PalloneEvent
	var first int32
	var anchor time.Time
	var activeDays, claimDays int
	var savedDigest string
	err = tx.QueryRow(`SELECT e.id,e.starts_at,e.ends_at,e.claim_until,l.first_event_id,l.anchor,l.active_days,l.claim_days,l.profile_sha256
		FROM pallone_event_lifecycle l JOIN pallone_events e ON e.id=l.event_id WHERE l.singleton`).Scan(
		&current.ID, &current.Start, &current.End, &current.ClaimUntil, &first, &anchor, &activeDays, &claimDays, &savedDigest)
	cycle := time.Duration(o.ActiveDays) * 24 * time.Hour
	if errors.Is(err, sql.ErrNoRows) {
		var highest int64
		if err = tx.Get(&highest, `SELECT COALESCE(MAX(id),0) FROM pallone_events`); err != nil {
			return result, err
		}
		current, err = palloneRotationEvent(highest+1, start, o)
		if err != nil {
			return result, err
		}
		first, anchor = current.ID, start
		if err = registerPalloneEvent(tx, current, routes); err != nil {
			return result, err
		}
		if _, err = tx.Exec(`INSERT INTO pallone_event_lifecycle(singleton,event_id,first_event_id,anchor,active_days,claim_days,profile_sha256)
			VALUES(true,$1,$1,$2,$3,$4,$5)`, current.ID, anchor, o.ActiveDays, o.ClaimDays, digest); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	} else {
		if activeDays != o.ActiveDays || claimDays != o.ClaimDays || savedDigest != digest {
			return result, fmt.Errorf("persisted Pallone durations/profile differ; change the schedule explicitly while stopped")
		}
		if o.StartAt != "" {
			configured, err := time.Parse(time.RFC3339, o.StartAt)
			if err != nil || configured.After(anchor) || anchor.Sub(configured)%cycle != 0 {
				return result, fmt.Errorf("persisted Pallone anchor differs")
			}
		}
		want, err := palloneRotationEvent(int64(current.ID), current.Start, o)
		if err != nil || !current.End.Equal(want.End) || !current.ClaimUntil.Equal(want.ClaimUntil) || current.Start.Before(anchor) ||
			current.Start.Sub(anchor)%cycle != 0 || int64(current.ID)-int64(first) != int64(current.Start.Sub(anchor)/cycle) {
			return result, ErrPalloneConflict
		}
		storedRoutes, err := readPalloneRoutes(tx, current.ID)
		if err != nil {
			return result, err
		}
		if !reflect.DeepEqual(routes, storedRoutes) {
			return result, ErrPalloneConflict
		}
		if !now.Before(current.End) {
			steps := int64(now.Sub(current.Start) / cycle)
			current, err = palloneRotationEvent(int64(current.ID)+steps, current.Start.Add(time.Duration(steps)*cycle), o)
			if err != nil {
				return result, err
			}
			if err = registerPalloneEvent(tx, current, routes); err != nil {
				return result, err
			}
			if _, err = tx.Exec(`UPDATE pallone_event_lifecycle SET event_id=$1 WHERE singleton`, current.ID); err != nil {
				return result, err
			}
		}
	}
	result.Current = current
	result.Next, err = palloneRotationEvent(int64(current.ID)+1, current.End, o)
	if err != nil {
		return result, err
	}
	if err = registerPalloneEvent(tx, result.Next, routes); err != nil {
		return result, err
	}
	if current.ID > first {
		var previous PalloneEvent
		err = tx.Get(&previous, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1`, current.ID-1)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if err == nil && previous.End.Equal(current.Start) && previous.Claimable(now) {
			result.Previous = &previous
		}
	}
	return result, tx.Commit()
}
