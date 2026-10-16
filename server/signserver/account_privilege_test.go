package signserver

import (
	"bytes"
	"encoding/binary"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"testing"
	"time"
)

func TestSignResponseSeparateAccountDeadlines(t *testing.T) {
	c := &cfg.Config{DebugOptions: cfg.DebugOptions{CapLink: cfg.CapLinkOptions{Values: []uint16{0, 0, 0, 0, 0}}}}
	s := newMakeSignResponseServer(c)
	r := s.userRepo.(*mockSignUserRepo)
	r.lastLogin = time.Now()
	r.returnExpiry = time.Unix(1901111111, 0)
	r.newcomerExpiry = time.Unix(1902222222, 0)
	session := &Session{logger: zap.NewNop(), server: s, rawConn: newMockConn()}
	response := session.makeSignResponse(1)
	want := make([]byte, 8)
	binary.BigEndian.PutUint32(want, 1901111111)
	binary.BigEndian.PutUint32(want[4:], 1902222222)
	if !bytes.Contains(response, want) {
		t.Fatal("native sign packet lost separate returning/newcomer timestamps")
	}
}
