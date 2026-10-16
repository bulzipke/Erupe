package entranceserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"erupe-ce/common/accountprivilege"
	cfg "erupe-ce/config"
	"go.uber.org/zap"
	"io"
	"net"
	"testing"
	"time"
)

type privilegeMock struct {
	result          privilegeSession
	err             error
	tokenID, charID uint32
}

func (m *privilegeMock) GetServerIDForCharacter(uint32) (uint16, error) { return 0, nil }
func (m *privilegeMock) GetAccountPrivilege(_ context.Context, tid, cid uint32, _ time.Time, _ time.Duration) (privilegeSession, error) {
	m.tokenID = tid
	m.charID = cid
	return m.result, m.err
}

func TestPrivilegeEntranceExchange(t *testing.T) {
	for _, scenario := range []string{"valid", "bad-mac", "missing-session", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			const token = "0123456789abcdef"
			repo := &privilegeMock{result: privilegeSession{Token: token, Newcomer: sql.NullTime{Time: time.Unix(1900000000, 0), Valid: true}}}
			if scenario == "missing-session" {
				repo.err = errors.New("not found")
			}
			s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
			s.sessionRepo = repo
			server, client := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			go func() { defer close(done); s.handleEntranceServerConnection(server) }()
			_ = client.SetDeadline(time.Now().Add(2 * time.Second))
			b := make([]byte, 64)
			copy(b, accountprivilege.RequestMagic)
			binary.BigEndian.PutUint32(b[8:], 123)
			binary.BigEndian.PutUint32(b[12:], 456)
			for i := 16; i < 32; i++ {
				b[i] = byte(i)
			}
			m := hmac.New(sha256.New, []byte(token))
			_, _ = m.Write(b[:32])
			copy(b[32:], m.Sum(nil))
			if scenario == "bad-mac" {
				b[63] ^= 1
			}
			if scenario == "truncated" {
				_, _ = client.Write(b[:20])
				_ = client.Close()
				<-done
				return
			}
			if _, err := client.Write(b); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, 72)
			_, err := io.ReadFull(client, response)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("unauthenticated response")
				}
				<-done
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if repo.tokenID != 123 || repo.charID != 456 || binary.BigEndian.Uint32(response[12:]) != 1900000000 || binary.BigEndian.Uint32(response[16:]) != 0 {
				t.Fatal("wrong account state")
			}
			m = hmac.New(sha256.New, []byte(token))
			_, _ = m.Write(response[:40])
			if !hmac.Equal(m.Sum(nil), response[40:]) || string(response[24:40]) != string(b[16:32]) {
				t.Fatal("invalid signed response")
			}
			<-done
		})
	}
}
