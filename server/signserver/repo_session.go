package signserver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

const sessionCleanupTimeout = 500 * time.Millisecond

// SignSessionRepository implements SignSessionRepo with PostgreSQL.
type SignSessionRepository struct {
	db     *sqlx.DB
	expiry time.Duration
}

// NewSignSessionRepository creates a new SignSessionRepository.
func NewSignSessionRepository(db *sqlx.DB, expiry ...time.Duration) *SignSessionRepository {
	var tokenExpiry time.Duration
	if len(expiry) > 0 {
		tokenExpiry = expiry[0]
	}
	return &SignSessionRepository{db: db, expiry: tokenExpiry}
}

func (r *SignSessionRepository) cleanupExpired() error {
	if r.expiry <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionCleanupTimeout)
	defer cancel()
	_, err := r.db.ExecContext(ctx, `DELETE FROM sign_sessions
		WHERE server_id IS NULL
		  AND last_used_at < NOW() - ($1 * INTERVAL '1 second')`, r.expiry.Seconds())
	return err
}

func (r *SignSessionRepository) RegisterUID(uid uint32, token string) (uint32, error) {
	_ = r.cleanupExpired()
	var tid uint32
	err := r.db.QueryRow(`INSERT INTO sign_sessions (user_id, token) VALUES ($1, $2) RETURNING id`, uid, token).Scan(&tid)
	return tid, err
}

func (r *SignSessionRepository) RegisterPSN(psnID, token string) (uint32, error) {
	_ = r.cleanupExpired()
	var tid uint32
	err := r.db.QueryRow(`INSERT INTO sign_sessions (psn_id, token) VALUES ($1, $2) RETURNING id`, psnID, token).Scan(&tid)
	return tid, err
}

func (r *SignSessionRepository) Validate(token string, tokenID uint32) (bool, error) {
	query := `UPDATE sign_sessions SET last_used_at = NOW() WHERE token = $1`
	args := []interface{}{token}
	if tokenID > 0 {
		query += ` AND id = $2`
		args = append(args, tokenID)
	}
	if r.expiry > 0 {
		args = append(args, r.expiry.Seconds())
		query += fmt.Sprintf(` AND last_used_at >= NOW() - ($%d * INTERVAL '1 second')`, len(args))
	}
	query += ` RETURNING 1`
	var exists int
	err := r.db.QueryRow(query, args...).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *SignSessionRepository) GetPSNIDByToken(token string) (string, error) {
	query := `UPDATE sign_sessions SET last_used_at = NOW() WHERE token = $1`
	args := []interface{}{token}
	if r.expiry > 0 {
		args = append(args, r.expiry.Seconds())
		query += ` AND last_used_at >= NOW() - ($2 * INTERVAL '1 second')`
	}
	query += ` RETURNING psn_id`
	var psnID string
	err := r.db.QueryRow(query, args...).Scan(&psnID)
	return psnID, err
}
