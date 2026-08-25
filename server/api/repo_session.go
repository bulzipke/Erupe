package api

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

const sessionCleanupTimeout = 500 * time.Millisecond

// APISessionRepository implements APISessionRepo with PostgreSQL.
type APISessionRepository struct {
	db     *sqlx.DB
	expiry time.Duration
}

// NewAPISessionRepository creates a new APISessionRepository.
func NewAPISessionRepository(db *sqlx.DB, expiry ...time.Duration) *APISessionRepository {
	var tokenExpiry time.Duration
	if len(expiry) > 0 {
		tokenExpiry = expiry[0]
	}
	return &APISessionRepository{db: db, expiry: tokenExpiry}
}

func (r *APISessionRepository) cleanupExpired(ctx context.Context) error {
	if r.expiry <= 0 {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, sessionCleanupTimeout)
	defer cancel()
	_, err := r.db.ExecContext(cleanupCtx, `DELETE FROM sign_sessions
		WHERE server_id IS NULL
		  AND last_used_at < NOW() - ($1 * INTERVAL '1 second')`, r.expiry.Seconds())
	return err
}

func (r *APISessionRepository) CreateToken(ctx context.Context, uid uint32, token string) (uint32, error) {
	// Expiry cleanup is best-effort: a locked stale row must not prevent a new
	// login. Startup performs the same cleanup with explicit error logging.
	_ = r.cleanupExpired(ctx)
	var tid uint32
	err := r.db.QueryRowContext(ctx, "INSERT INTO sign_sessions (user_id, token) VALUES ($1, $2) RETURNING id", uid, token).Scan(&tid)
	return tid, err
}

func (r *APISessionRepository) GetUserIDByToken(ctx context.Context, token string) (uint32, error) {
	query := `UPDATE sign_sessions SET last_used_at = NOW() WHERE token = $1`
	args := []interface{}{token}
	if r.expiry > 0 {
		args = append(args, r.expiry.Seconds())
		query += ` AND last_used_at >= NOW() - ($2 * INTERVAL '1 second')`
	}
	query += ` RETURNING user_id`
	var userID uint32
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&userID)
	return userID, err
}
