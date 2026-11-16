package entranceserver

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"erupe-ce/common/keyconfigsync"
	cfg "erupe-ce/config"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
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
				rev, status, err := r.SaveKeyconfig(ctx, 10, 0, expected, data)
				if err != nil {
					t.Error(err)
				}
				if status == keyconfigsync.OK {
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
	if _, status, err := r.SaveKeyconfig(ctx, 10, 0, keyconfigsync.MaxRevision, data); err != nil || status != keyconfigsync.Conflict {
		t.Fatal("revision overflow", err)
	}

	t.Run("INI encoding", func(t *testing.T) {
		s := NewServer(&Config{Logger: zap.NewNop(), ErupeConfig: &cfg.Config{}, DB: db})
		check := func(op, revision uint32, payload []byte, status, wantRevision uint32, wantData []byte) {
			t.Helper()
			request := keyconfigRequest(123, 456, op, 2, revision, payload)
			header, err := keyconfigsync.ParseHeader(request[:keyconfigsync.HeaderSize])
			if err != nil {
				t.Fatal(err)
			}
			response, err := keyconfigExchange(t, s, request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(response, keyconfigsync.Response(header, status, wantRevision, wantData, "0123456789abcdef")) {
				t.Fatalf("unexpected signed INI response for operation %d and requested revision %d", op, revision)
			}
		}
		ini := []byte("[VIDEO]\r\nFPS_60=1\r\n[GAMEPAD_PRIORITY]\r\n00=Device\r\n[FIXED_SENTENCE]\r\nCWORDS_00=" + iniKoreanPhrase + "\r\nCWORDS_01=" + iniKoreanName + "\r\n")
		utf8Ini := bytes.Replace(ini, []byte(iniKoreanPhrase), []byte("하늘이 맑아요"), 1)
		utf8Ini = bytes.Replace(utf8Ini, []byte(iniKoreanName), []byte("친구 이름"), 1)
		resetPad := bytes.Replace(ini, []byte("00=Device\r\n"), []byte("\r\n"), 1)
		damaged := bytes.Replace(resetPad, []byte(iniKoreanPhrase), []byte(iniDamagedPhrase), 1)
		damaged = bytes.Replace(damaged, []byte(iniKoreanName), []byte(iniDamagedName), 1)
		edited := bytes.Replace(resetPad, []byte(iniKoreanName), []byte("Custom"), 1)
		unsupported := []byte("[VIDEO]\r\nFPS_60=1\r\n[FIXED_SENTENCE]\r\nCWORDS_00=😀\r\n")
		check(keyconfigsync.Save, 0, unsupported, keyconfigsync.Conflict, 0, nil)
		check(keyconfigsync.Load, 0, nil, keyconfigsync.Absent, 0, nil)
		check(keyconfigsync.Save, 0, utf8Ini, keyconfigsync.Conflict, 1, nil)
		check(keyconfigsync.Load, 0, nil, keyconfigsync.OK, 1, ini)
		check(keyconfigsync.Save, 1, resetPad, keyconfigsync.OK, 2, nil)
		check(keyconfigsync.Save, 1, damaged, keyconfigsync.Conflict, 0, nil)
		check(keyconfigsync.Save, 2, damaged, keyconfigsync.Conflict, 2, nil)
		check(keyconfigsync.Save, 2, unsupported, keyconfigsync.Conflict, 2, nil)
		check(keyconfigsync.Load, 0, nil, keyconfigsync.OK, 2, resetPad)
		check(keyconfigsync.Save, 2, edited, keyconfigsync.OK, 3, nil)
		check(keyconfigsync.Load, 0, nil, keyconfigsync.OK, 3, edited)
		japaneseUTF8 := []byte("\xef\xbb\xbf[VIDEO]\r\nFPS_60=1\r\n[CHATPHI_123456]\r\nPHINF_NAME_0=ｶ\r\n")
		japanese := []byte("[VIDEO]\r\nFPS_60=1\r\n[CHATPHI_123456]\r\nPHINF_NAME_0=\xb6\r\n")
		check(keyconfigsync.Save, 2, japaneseUTF8, keyconfigsync.Conflict, 0, nil)
		check(keyconfigsync.Save, 3, japaneseUTF8, keyconfigsync.Conflict, 4, nil)
		check(keyconfigsync.Load, 0, nil, keyconfigsync.OK, 4, japanese)
		check(keyconfigsync.Save, 4, japanese, keyconfigsync.OK, 5, nil)
	})
}
