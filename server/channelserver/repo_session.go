package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"erupe-ce/server/dashboarddelivery"
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
	return r.bindSession(token, serverID, charID, "")
}

// BindTrackedSession publishes a connection lease under the account lock.
func (r *SessionRepository) BindTrackedSession(token string, serverID uint16, charID uint32, id string) error {
	return r.bindSession(token, serverID, charID, id)
}

func (r *SessionRepository) bindSession(token string, serverID uint16, charID uint32, id string) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Dashboard currency grants hold this account row through their offline
	// check and commit. Serialize login publication with that same check.
	var userID uint32
	if err = tx.QueryRow(`SELECT u.id FROM users u JOIN sign_sessions ss ON ss.user_id=u.id
		WHERE ss.token=$1 FOR UPDATE OF u`, token).Scan(&userID); err != nil {
		return err
	}
	if id != "" {
		// Recover any already-offline queue before the client reads its balance.
		if err = dashboarddelivery.SettleLocked(context.Background(), tx, userID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO dashboard_coin_sessions(id,user_id,character_id,server_id,token)
			VALUES($1::uuid,$2,$3,$4,$5)`, id, userID, charID, serverID, token); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE sign_sessions SET server_id=$1, char_id=$2, last_used_at=NOW() WHERE token=$3", serverID, charID, token); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishTrackedSession runs only AFTER the logout save. The last successful
// saver releases the queue in the same transaction as the offline transition.
func (r *SessionRepository) FinishTrackedSession(ctx context.Context, id string, userID, charID uint32, token string, saved bool) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedID uint32
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dashboard_coin_sessions
		WHERE id=$1::uuid AND user_id=$2 AND character_id=$3 AND token=$4)`, id, userID, charID, token).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	} // Ambiguous successful COMMIT retry: do not touch newer bindings.
	if saved {
		// A later successful save of the same character also clears failed-save
		// tombstones left by an earlier disconnect. Do not clear live siblings.
		_, err = tx.ExecContext(ctx, `DELETE FROM dashboard_coin_sessions WHERE user_id=$2
			AND (id=$1::uuid OR (character_id=$3 AND save_failed))`, id, userID, charID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE dashboard_coin_sessions SET save_failed=true WHERE id=$1::uuid AND user_id=$2`, id, userID)
	}
	if err != nil {
		return err
	}
	// A channel transfer can rebind the same token before the old saver finishes.
	// Preserve its remaining live connection instead of clearing that binding.
	var serverID uint16
	var activeCharID uint32
	err = tx.QueryRowContext(ctx, `SELECT server_id,character_id FROM dashboard_coin_sessions
		WHERE token=$1 AND user_id=$2 AND NOT save_failed ORDER BY created_at DESC,id DESC LIMIT 1`, token, userID).Scan(&serverID, &activeCharID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `UPDATE sign_sessions SET server_id=NULL,char_id=NULL WHERE token=$1 AND user_id=$2`, token, userID)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE sign_sessions SET server_id=$3,char_id=$4 WHERE token=$1 AND user_id=$2`, token, userID, serverID, activeCharID)
	}
	if err != nil {
		return err
	}
	if saved {
		if err = dashboarddelivery.SettleLocked(ctx, tx, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Called only after owning the channel's listening socket. On process restart
// there are no more in-flight saves from this channel; its last committed DB
// snapshot is authoritative. Other channels' leases remain untouched.
func (r *SessionRepository) RecoverDashboardCoinSessions(ctx context.Context, serverID uint16) error {
	var users []uint32
	if err := r.db.SelectContext(ctx, &users, `SELECT DISTINCT user_id FROM dashboard_coin_sessions WHERE server_id=$1 ORDER BY user_id`, serverID); err != nil {
		return err
	}
	for _, userID := range users {
		err := func() error {
			tx, err := r.db.BeginTxx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			var locked uint32
			if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM dashboard_coin_sessions WHERE user_id=$1 AND server_id=$2`, userID, serverID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE sign_sessions SET server_id=NULL,char_id=NULL WHERE user_id=$1 AND server_id=$2`, userID, serverID); err != nil {
				return err
			}
			if err = dashboarddelivery.SettleLocked(ctx, tx, userID); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			return err
		}
	}
	return nil
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
