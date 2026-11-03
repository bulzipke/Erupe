package entranceserver

import (
	"context"
	"database/sql"
	"erupe-ce/common/keyconfigsync"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyconfigPostgres(t *testing.T) {
	// Opt-in and local only; all fixtures are connection-local temporary tables.
	dsn := os.Getenv("KEYCONFIG_SYNC_TEST_DSN")
	if !strings.Contains(dsn, "host=127.0.0.1 ") || !strings.Contains(dsn, "dbname=keyconfig_sync_test ") {
		t.Skip("isolated local keyconfig database not configured")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TEMP TABLE users(id INTEGER PRIMARY KEY);
	 CREATE TEMP TABLE characters(id INTEGER PRIMARY KEY,user_id INTEGER,deleted BOOLEAN DEFAULT FALSE);
	 CREATE TEMP TABLE sign_sessions(id INTEGER PRIMARY KEY,user_id INTEGER,token TEXT,last_used_at TIMESTAMPTZ);
	 CREATE TEMP TABLE bans(user_id INTEGER,expires TIMESTAMPTZ);
	 INSERT INTO users VALUES(10),(11);
	 INSERT INTO characters(id,user_id) VALUES(456,10),(457,10),(458,11);
	 INSERT INTO sign_sessions VALUES(123,10,'0123456789abcdef',NOW());`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/sql/0102_account_keyconfigs.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.Replace(string(migration), "CREATE TABLE account_keyconfigs", "CREATE TEMP TABLE account_keyconfigs", 1)); err != nil {
		t.Fatal(err)
	}
	migration, err = os.ReadFile("../migrations/sql/0103_account_profile_ini.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	r := NewEntranceSessionRepository(db)
	ctx := context.Background()
	now := time.Now()
	for _, cid := range []uint32{456, 457} {
		if s, err := r.KeyconfigSession(ctx, 123, cid, now, time.Hour); err != nil || s.UserID != 10 {
			t.Fatal(s, err)
		}
	}
	if _, err = r.KeyconfigSession(ctx, 123, 458, now, time.Hour); err != sql.ErrNoRows {
		t.Fatal("ownership", err)
	}
	if _, err = db.Exec(`UPDATE sign_sessions SET last_used_at=NOW()-INTERVAL '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.KeyconfigSession(ctx, 123, 456, now, time.Hour); err != sql.ErrNoRows {
		t.Fatal("expiry", err)
	}
	if _, err = db.Exec(`UPDATE sign_sessions SET last_used_at=NOW();INSERT INTO bans VALUES(10,NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.KeyconfigSession(ctx, 123, 456, now, time.Hour); err != sql.ErrNoRows {
		t.Fatal("ban", err)
	}
	if _, err = db.Exec(`DELETE FROM bans`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../common/keyconfigsync/testdata/keyconfig.cfg")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []uint32{0, 1} {
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rev, saved, err := r.SaveKeyconfig(ctx, 10, 0, expected, data)
				if err != nil {
					t.Error(err)
				}
				if saved {
					wins.Add(1)
					if rev != expected+1 {
						t.Error(rev)
					}
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal("CAS winners", wins.Load())
		}
	}
	if rev, b, err := r.LoadKeyconfig(ctx, 10, 0); err != nil || rev != 2 || string(b) != string(data) {
		t.Fatal(rev, err)
	}
	if _, _, err = r.LoadKeyconfig(ctx, 11, 0); err != sql.ErrNoRows {
		t.Fatal("account isolation", err)
	}
	if _, _, err = r.LoadKeyconfig(ctx, 10, 1); err != sql.ErrNoRows {
		t.Fatal("slot isolation", err)
	}
	if _, ok, err := r.SaveKeyconfig(ctx, 10, 0, keyconfigsync.MaxRevision, data); err != nil || ok {
		t.Fatal("revision overflow", err)
	}
}
