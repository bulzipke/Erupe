package entranceserver

import (
	"context"
	cfg "erupe-ce/config"
	"fmt"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPrivilegeOwnershipAndExpiryDB(t *testing.T) {
	// Deliberately opt-in and local: never run this against a production DB.
	if os.Getenv("TEST_DB_HOST") != "127.0.0.1" || os.Getenv("TEST_DB_NAME") != "erupe_rookie_test" {
		t.Skip("isolated newcomer test database not configured")
	}
	dsn := fmt.Sprintf("host=127.0.0.1 port=%s user=%s password=%s dbname=erupe_rookie_test sslmode=disable", os.Getenv("TEST_DB_PORT"), os.Getenv("TEST_DB_USER"), os.Getenv("TEST_DB_PASSWORD"))
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var uid, cid, tid uint32
	if err = db.Get(&uid, "INSERT INTO users(username,password,rookie_return_pending,newcomer_expires) VALUES($1,'test',false,now()+interval '30 days') RETURNING id", fmt.Sprintf("bridge-%d", time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec("DELETE FROM bans WHERE user_id=$1", uid)
		_, _ = db.Exec("DELETE FROM sign_sessions WHERE user_id=$1", uid)
		_, _ = db.Exec("DELETE FROM characters WHERE user_id=$1", uid)
		_, _ = db.Exec("DELETE FROM users WHERE id=$1", uid)
	}()
	if err = db.Get(&cid, `INSERT INTO characters(user_id,is_female,is_new_character,name,unk_desc_string,hr,gr,weapon_type,last_login) VALUES($1,false,true,'','',0,0,0,0) RETURNING id`, uid); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&tid, "INSERT INTO sign_sessions(user_id,token) VALUES($1,'0123456789abcdef') RETURNING id", uid); err != nil {
		t.Fatal(err)
	}
	r := NewEntranceSessionRepository(db)
	now := time.Now()
	if p, err := r.GetAccountPrivilege(context.Background(), tid, cid, now, time.Hour); err != nil || !p.Newcomer.Valid || p.Returning.Valid {
		t.Fatal(p, err)
	}
	if _, err = r.GetAccountPrivilege(context.Background(), tid, cid+999999, now, time.Hour); err == nil {
		t.Fatal("unowned character accepted")
	}
	if _, err = db.Exec("UPDATE sign_sessions SET last_used_at=$1 WHERE id=$2", now.Add(-2*time.Hour), tid); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetAccountPrivilege(context.Background(), tid, cid, now, time.Hour); err == nil {
		t.Fatal("expired session accepted")
	}
	if _, err = r.GetAccountPrivilege(context.Background(), tid, cid, now, 0); err != nil {
		t.Fatal("disabled expiry", err)
	}
	if _, err = db.Exec("UPDATE sign_sessions SET last_used_at=$1 WHERE id=$2", now, tid); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE characters SET deleted=true WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetAccountPrivilege(context.Background(), tid, cid, now, time.Hour); err == nil {
		t.Fatal("deleted character accepted")
	}
	if _, err = db.Exec("UPDATE characters SET deleted=false WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO bans(user_id) VALUES($1)", uid); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetAccountPrivilege(context.Background(), tid, cid, now, time.Hour); err == nil {
		t.Fatal("banned account accepted")
	}
	if _, err = db.Exec("DELETE FROM bans WHERE user_id=$1", uid); err != nil {
		t.Fatal(err)
	}
	if executable := os.Getenv("PRIVILEGE_CLIENT_TEST_EXE"); executable != "" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		s := NewServer(&Config{Logger: zap.NewNop(), DB: db, ErupeConfig: &cfg.Config{}})
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := listener.Accept()
			if err == nil {
				s.handleEntranceServerConnection(conn)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, executable, listener.Addr().String(), fmt.Sprint(tid), fmt.Sprint(cid)).CombinedOutput()
		if err != nil || !strings.Contains(string(output), "ok=1") || !strings.Contains(string(output), "returning=0") {
			t.Fatalf("Win32 client exchange: %v %s", err, output)
		}
		<-done
		t.Log(string(output))
	}
}
