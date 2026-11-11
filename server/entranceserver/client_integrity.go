package entranceserver

import (
	"context"
	"erupe-ce/common/clientintegrity"
	"go.uber.org/zap"
	"io"
	"net"
	"time"
)

func (s *Server) handleClientIntegrity(conn net.Conn, header []byte) {
	if s.integrity == nil {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	b := make([]byte, clientintegrity.HelloSize)
	copy(b, header)
	if _, err := io.ReadFull(conn, b[8:]); err != nil {
		return
	}
	h, err := clientintegrity.ParseHello(b)
	if err != nil {
		return
	}
	repo, ok := s.sessionRepo.(privilegeSessionRepo)
	if !ok {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	session, err := repo.GetAccountPrivilege(ctx, h.TokenID, h.CharacterID, time.Now(), s.erupeConfig.Sign.SessionTokenExpiry())
	cancel()
	if err != nil || !h.Authenticate(session.Token) {
		return
	}
	// Manager owns all active channel disconnects; failure never writes a player
	// save or bans the account. No secrets or packet bodies are logged here.
	if err = s.integrity.Serve(conn, h, session.Token); err != nil {
		s.logger.Info("Client integrity connection ended", zap.Uint32("charID", h.CharacterID), zap.Error(err))
	}
}
