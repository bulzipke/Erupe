package entranceserver

import (
	"context"
	"database/sql"
	"erupe-ce/common/accountprivilege"
	"io"
	"net"
	"time"
)

type privilegeSession struct {
	Token               string
	Newcomer, Returning sql.NullTime
}

type privilegeSessionRepo interface {
	GetAccountPrivilege(context.Context, uint32, uint32, time.Time, time.Duration) (privilegeSession, error)
}

// This read-only exchange happens before world selection. It does not renew a
// session or grant privileges, and is separate from the native encrypted frames.
func (s *Server) handleAccountPrivilege(conn net.Conn, header []byte) {
	deadline := time.Now().Add(3 * time.Second)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	repo, ok := s.sessionRepo.(privilegeSessionRepo)
	if !ok {
		return
	}
	b := make([]byte, accountprivilege.RequestSize)
	copy(b, header)
	if _, err := io.ReadFull(conn, b[8:]); err != nil {
		return
	}
	r, err := accountprivilege.ParseRequest(b)
	if err != nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	now := time.Now()
	session, err := repo.GetAccountPrivilege(ctx, r.TokenID, r.CharacterID, now, s.erupeConfig.Sign.SessionTokenExpiry())
	if err != nil || !accountprivilege.Authenticate(b, session.Token) {
		return
	}
	response := accountprivilege.Response(r, now, session.Newcomer.Time, session.Returning.Time, session.Token)
	for len(response) > 0 {
		n, err := conn.Write(response)
		if err != nil || n == 0 {
			return
		}
		response = response[n:]
	}
}

func (r *EntranceSessionRepository) GetAccountPrivilege(ctx context.Context, tokenID, charID uint32, now time.Time, idle time.Duration) (privilegeSession, error) {
	var result privilegeSession
	var oldest any
	if idle > 0 {
		oldest = now.Add(-idle)
	}
	err := r.db.QueryRowContext(ctx, `
		SELECT s.token, u.newcomer_expires, u.return_expires
		FROM sign_sessions s JOIN users u ON u.id=s.user_id
		JOIN characters c ON c.user_id=u.id AND c.id=$2 AND NOT c.deleted
		WHERE s.id=$1 AND ($3::timestamptz IS NULL OR s.last_used_at >= $3)
		AND NOT EXISTS (SELECT 1 FROM bans b WHERE b.user_id=u.id AND (b.expires IS NULL OR b.expires > $4))`,
		tokenID, charID, oldest, now).Scan(&result.Token, &result.Newcomer, &result.Returning)
	return result, err
}
