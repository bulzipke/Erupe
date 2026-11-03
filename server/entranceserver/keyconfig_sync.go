package entranceserver

import (
	"context"
	"database/sql"
	"erupe-ce/common/keyconfigsync"
	"io"
	"net"
	"time"
)

type keyconfigSession struct {
	UserID uint32
	Token  string
}
type keyconfigRepo interface {
	KeyconfigSession(context.Context, uint32, uint32, time.Time, time.Duration) (keyconfigSession, error)
	LoadKeyconfig(context.Context, uint32, uint32) (uint32, []byte, error)
	SaveKeyconfig(context.Context, uint32, uint32, uint32, []byte) (uint32, bool, error)
}

func (s *Server) handleKeyconfig(conn net.Conn, prefix []byte) {
	ip := profileIP(conn.RemoteAddr())
	release, admitted := s.profileLimits.enter(ip, time.Now())
	if !admitted {
		return
	}
	defer release()
	deadline := time.Now().Add(5 * time.Second)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	repo, ok := s.sessionRepo.(keyconfigRepo)
	if !ok {
		return
	}
	header := make([]byte, keyconfigsync.HeaderSize+keyconfigsync.MACSize)
	copy(header, prefix)
	if _, err := io.ReadFull(conn, header[8:]); err != nil {
		return
	}
	r, err := keyconfigsync.ParseHeader(header[:keyconfigsync.HeaderSize])
	if err != nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	session, err := repo.KeyconfigSession(ctx, r.TokenID, r.CharacterID, time.Now(), s.erupeConfig.Sign.SessionTokenExpiry())
	if err != nil || !keyconfigsync.AuthenticateHeader(header, session.Token) {
		return
	}
	cost := r.Size
	if r.Operation == keyconfigsync.Load {
		cost = keyconfigsync.Limit(r.Slot)
	}
	if !s.profileLimits.allow(ip, session.UserID, int(cost), time.Now()) {
		writeProfileResponse(conn, keyconfigsync.Response(r, keyconfigsync.Throttled, 0, nil, session.Token))
		return
	}
	b := make([]byte, len(header)+int(r.Size)+keyconfigsync.MACSize)
	copy(b, header)
	if _, err = io.ReadFull(conn, b[len(header):]); err != nil || !keyconfigsync.Authenticate(b, session.Token) {
		return
	}
	status, revision := uint32(keyconfigsync.OK), uint32(0)
	var data []byte
	if r.Operation == keyconfigsync.Load {
		revision, data, err = repo.LoadKeyconfig(ctx, session.UserID, r.Slot)
		if err == sql.ErrNoRows {
			status = keyconfigsync.Absent
			err = nil
		} else if err == nil && (!keyconfigsync.ValidProfile(r.Slot, data) || revision == 0 || revision > keyconfigsync.MaxRevision) {
			status = keyconfigsync.Unavailable
			data = nil
		}
	} else {
		payload := b[keyconfigsync.HeaderSize+keyconfigsync.MACSize : len(b)-keyconfigsync.MACSize]
		if !keyconfigsync.ValidProfile(r.Slot, payload) {
			return
		}
		var saved bool
		revision, saved, err = repo.SaveKeyconfig(ctx, session.UserID, r.Slot, r.Revision, payload)
		if !saved {
			status = keyconfigsync.Conflict
		}
	}
	if err != nil {
		status = keyconfigsync.Unavailable
		revision = 0
		data = nil
	}
	writeProfileResponse(conn, keyconfigsync.Response(r, status, revision, data, session.Token))
}

func writeProfileResponse(conn net.Conn, response []byte) {
	for len(response) > 0 {
		n, err := conn.Write(response)
		if err != nil || n == 0 {
			return
		}
		response = response[n:]
	}
}

func (r *EntranceSessionRepository) KeyconfigSession(ctx context.Context, tid, cid uint32, now time.Time, idle time.Duration) (keyconfigSession, error) {
	var out keyconfigSession
	var oldest any
	if idle > 0 {
		oldest = now.Add(-idle)
	}
	err := r.db.QueryRowContext(ctx, `SELECT s.user_id,s.token FROM sign_sessions s
	 JOIN characters c ON c.user_id=s.user_id AND c.id=$2 AND NOT c.deleted
	 WHERE s.id=$1 AND ($3::timestamptz IS NULL OR s.last_used_at >= $3)
	 AND NOT EXISTS(SELECT 1 FROM bans b WHERE b.user_id=s.user_id AND (b.expires IS NULL OR b.expires>$4))`,
		tid, cid, oldest, now).Scan(&out.UserID, &out.Token)
	return out, err
}

func (r *EntranceSessionRepository) LoadKeyconfig(ctx context.Context, uid, slot uint32) (uint32, []byte, error) {
	var revision uint32
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT revision,data FROM account_keyconfigs WHERE user_id=$1 AND slot=$2`, uid, slot).Scan(&revision, &data)
	return revision, data, err
}

func (r *EntranceSessionRepository) SaveKeyconfig(ctx context.Context, uid, slot, revision uint32, data []byte) (uint32, bool, error) {
	if slot >= keyconfigsync.SlotCount || revision >= keyconfigsync.MaxRevision || !keyconfigsync.ValidProfile(slot, data) {
		return 0, false, nil
	}
	var next uint32
	// A single statement serializes concurrent inserts/updates for this account.
	// Revision zero can only create a missing row. A replay/stale PC cannot update it.
	var err error
	if revision == 0 {
		err = r.db.QueryRowContext(ctx, `INSERT INTO account_keyconfigs(user_id,slot,revision,data)
		 VALUES($1,$2,1,$3) ON CONFLICT(user_id,slot) DO NOTHING RETURNING revision`, uid, slot, data).Scan(&next)
	} else {
		err = r.db.QueryRowContext(ctx, `UPDATE account_keyconfigs SET revision=revision+1,data=$4,updated_at=NOW()
		 WHERE user_id=$1 AND slot=$2 AND revision=$3 AND revision<2147483646 RETURNING revision`, uid, slot, revision, data).Scan(&next)
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return next, err == nil, err
}
