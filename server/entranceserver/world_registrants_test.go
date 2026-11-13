package entranceserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"erupe-ce/common/accountprivilege"
	cfg "erupe-ce/config"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
)

const worldRosterTestToken = "0123456789abcdef"

type worldRosterMock struct {
	privilegeMock
	rows  []worldRegistrant
	query uint32
}

func (m *worldRosterMock) LoadWorldRoster(_ context.Context, cid uint32) ([]worldRegistrant, error) {
	m.query = cid
	return m.rows, nil
}

func worldRosterTestRows() []worldRegistrant {
	return []worldRegistrant{{85, "카칸", false}, {73, "Friend", false}, {85, "카칸", true}}
}

func TestWorldRosterStartup(t *testing.T) {
	for _, scenario := range []string{"both", "empty", "legacy", "bad-mac"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &worldRosterMock{
				privilegeMock: privilegeMock{result: privilegeSession{Token: worldRosterTestToken}},
				rows:          worldRosterTestRows(),
			}
			if scenario == "empty" {
				repo.rows = nil
			}
			s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
			s.sessionRepo = repo
			server, client := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(2 * time.Second))
			done := make(chan struct{})
			go func() { defer close(done); s.handleEntranceServerConnection(server) }()
			request := make([]byte, accountprivilege.RequestSize)
			copy(request, accountprivilege.RequestMagic)
			binary.BigEndian.PutUint32(request[8:], 123)
			binary.BigEndian.PutUint32(request[12:], 456)
			copy(request[16:], "WLR1abcdefghijkl")
			if scenario == "legacy" {
				request[16] = 'x'
			}
			mac := hmac.New(sha256.New, []byte(worldRosterTestToken))
			_, _ = mac.Write(request[:32])
			copy(request[32:], mac.Sum(nil))
			if scenario == "bad-mac" {
				request[63] ^= 1
			}
			if _, err := client.Write(request); err != nil {
				t.Fatal(err)
			}
			response, err := io.ReadAll(client)
			<-done
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "bad-mac" {
				if len(response) != 0 || repo.query != 0 {
					t.Fatalf("unauthenticated response %d bytes, query=%d", len(response), repo.query)
				}
				return
			}
			if scenario == "legacy" {
				if len(response) != accountprivilege.ResponseSize || repo.query != 0 {
					t.Fatalf("legacy response %d bytes, query=%d", len(response), repo.query)
				}
				return
			}
			if len(response) != accountprivilege.ResponseSize+28+len(repo.rows)*20+32 || repo.query != 456 {
				t.Fatalf("roster response %d bytes, query=%d", len(response), repo.query)
			}
			roster := response[accountprivilege.ResponseSize:]
			if string(roster[:4]) != worldRosterMarker || string(roster[4:20]) != string(request[16:32]) {
				t.Fatal("wrong roster marker or nonce")
			}
			mac.Reset()
			_, _ = mac.Write(roster[:len(roster)-32])
			if !hmac.Equal(roster[len(roster)-32:], mac.Sum(nil)) {
				t.Fatal("wrong roster MAC")
			}
			friends, guild := binary.BigEndian.Uint32(roster[20:]), binary.BigEndian.Uint32(roster[24:])
			if scenario == "both" && (friends != 2 || guild != 1 || binary.BigEndian.Uint32(roster[28:]) != 85 ||
				binary.BigEndian.Uint32(roster[68:]) != 85 || string(roster[32:37]) != "\xc4\xab\xc4\xad\x00") {
				t.Fatalf("wrong roster counts, overlapping ID or CP949 name: %x", roster)
			}
			if scenario == "empty" && (friends != 0 || guild != 0) {
				t.Fatal("wrong empty roster")
			}
		})
	}
}

func TestWorldRosterWin32(t *testing.T) {
	exe := os.Getenv("WORLD_ROSTER_WIN32_TEST_EXE")
	if exe == "" {
		t.Skip("Win32 client test executable not configured")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub := hex.EncodeToString(elliptic.Marshal(key.Curve, key.X, key.Y)[1:])
	for _, mode := range []string{"roster", "old-server"} {
		t.Run(mode, func(t *testing.T) {
			repo := &worldRosterMock{
				privilegeMock: privilegeMock{result: privilegeSession{Token: worldRosterTestToken}},
				rows:          worldRosterTestRows(),
			}
			s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}})
			s.privilegeSigner, err = accountprivilege.NewSigner(string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), 7)
			if err != nil {
				t.Fatal(err)
			}
			s.sessionRepo = repo
			if mode == "old-server" {
				s.sessionRepo = &repo.privilegeMock
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					s.handleEntranceServerConnection(conn)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, exe, listener.Addr().String(), pub, mode).CombinedOutput()
			if err != nil || !strings.Contains(string(output), "verified Win32 startup "+mode) {
				t.Fatalf("Win32 startup: %v %s", err, output)
			}
			t.Log(strings.TrimSpace(string(output)))
		})
	}
}

func TestWorldRosterDB(t *testing.T) {
	dsn := os.Getenv("WORLD_ROSTER_LOCAL_DB")
	if dsn == "" {
		t.Skip("local roster test database not configured")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TEMP TABLE characters(id integer PRIMARY KEY, name text, friends text,
			deleted boolean DEFAULT false, is_new_character boolean DEFAULT false);
		CREATE TEMP TABLE guild_characters(character_id bigint PRIMARY KEY, guild_id bigint, order_index int);
		INSERT INTO characters(id, name, friends) VALUES
			(1,'Owner','3, 2, 3, invalid, 4, 5, 1, 9999999999'), (2,'Second',''), (3,'First',''),
			(4,'Deleted',''), (5,'New',''), (6,'Guild',''), (7,'Other guild',''), (8,'No guild','');
		UPDATE characters SET deleted=true WHERE id=4;
		UPDATE characters SET is_new_character=true WHERE id=5;
		INSERT INTO guild_characters VALUES (1,1,1), (3,1,3), (4,1,2), (5,1,4), (6,1,2), (7,2,1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewEntranceSessionRepository(db)
	ctx := context.Background()
	rows, err := repo.LoadWorldRoster(ctx, 1)
	want := []worldRegistrant{{3, "First", false}, {2, "Second", false}, {6, "Guild", true}, {3, "First", true}}
	if err != nil || len(rows) != len(want) {
		t.Fatalf("rows %v, err %v", rows, err)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
	rows, err = repo.LoadWorldRoster(ctx, 8)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unguilded empty roster: %v %v", rows, err)
	}
}
