// Package securityaudit persists append-only security observations without
// putting credentials or raw packet data in the audit log.
package securityaudit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmoiron/sqlx"
)

const recordTimeout = 500 * time.Millisecond

// Event is a security-relevant operation or validation result.
type Event struct {
	UserID      uint32
	CharacterID uint32
	Source      string
	Type        string
	Severity    string
	Decision    string
	Details     map[string]interface{}
}

// Repository stores append-only security observations.
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a security audit repository.
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// Record appends an event. A nil database is treated as audit-disabled so
// isolated unit tests and tools can reuse callers without a database.
func (r *Repository) Record(ctx context.Context, event Event) error {
	if r == nil || r.db == nil {
		return nil
	}

	details := event.Details
	if details == nil {
		details = map[string]interface{}{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	// Audit persistence must never indefinitely hold a gameplay ACK, save lock,
	// or HTTP response when PostgreSQL is slow or unavailable. A missed audit
	// event is logged by callers; gameplay correctness takes precedence.
	writeCtx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()

	_, err = r.db.ExecContext(writeCtx, `
		INSERT INTO security_audit_events
			(user_id, character_id, source, event_type, severity, decision, details)
		VALUES (NULLIF($1, 0), NULLIF($2, 0), $3, $4, $5, $6, $7::jsonb)
	`, int64(event.UserID), int64(event.CharacterID), event.Source, event.Type,
		event.Severity, event.Decision, string(encoded))
	return err
}
