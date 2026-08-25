package channelserver

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// SessionRepository centralizes all database access for sign_sessions and servers tables.
type SessionRepository struct {
	db     *sqlx.DB
	expiry time.Duration
}

// NewSessionRepository creates a new SessionRepository.
func NewSessionRepository(db *sqlx.DB, expiry ...time.Duration) *SessionRepository {
	var tokenExpiry time.Duration
	if len(expiry) > 0 {
		tokenExpiry = expiry[0]
	}
	return &SessionRepository{db: db, expiry: tokenExpiry}
}

// ValidateLoginToken validates that the given token, session ID, and character ID
// correspond to a valid sign session. Returns an error if the token is invalid.
func (r *SessionRepository) ValidateLoginToken(token string, sessionID uint32, charID uint32) error {
	var t string
	query := `UPDATE sign_sessions ss SET last_used_at = NOW()
		WHERE ss.token = $1 AND ss.id = $2
		  AND EXISTS (
			SELECT 1 FROM public.users u
			INNER JOIN characters c ON c.user_id = u.id
			WHERE c.id = $3 AND u.id = ss.user_id
		  )`
	args := []interface{}{token, sessionID, charID}
	if r.expiry > 0 {
		args = append(args, r.expiry.Seconds())
		query += fmt.Sprintf(` AND ss.last_used_at >= NOW() - ($%d * INTERVAL '1 second')`, len(args))
	}
	query += ` RETURNING ss.token`
	return r.db.QueryRow(query, args...).Scan(&t)
}

// BindSession associates a sign session token with a server and character.
func (r *SessionRepository) BindSession(token string, serverID uint16, charID uint32) error {
	_, err := r.db.Exec("UPDATE sign_sessions SET server_id=$1, char_id=$2, last_used_at=NOW() WHERE token=$3", serverID, charID, token)
	return err
}

// ClearSession removes the server and character association from a sign session.
func (r *SessionRepository) ClearSession(token string) error {
	_, err := r.db.Exec("UPDATE sign_sessions SET server_id=NULL, char_id=NULL WHERE token=$1", token)
	return err
}

// UpdatePlayerCount updates the current player count for a server.
func (r *SessionRepository) UpdatePlayerCount(serverID uint16, count int) error {
	_, err := r.db.Exec("UPDATE servers SET current_players=$1 WHERE server_id=$2", count, serverID)
	return err
}
