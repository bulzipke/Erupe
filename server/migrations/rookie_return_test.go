package migrations

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRookieReturnFirstCharacter(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := ensureVersionTable(db); err != nil {
		t.Fatal(err)
	}
	files, err := readMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.version < 80 {
			if err := applyMigration(db, file); err != nil {
				t.Fatal(err)
			}
		}
	}
	var legacy uint32
	if err := db.Get(&legacy, "INSERT INTO users(username,password) VALUES('legacy','test') RETURNING id"); err != nil {
		t.Fatal(err)
	}
	// Verify the historical migration in isolation. 0084 supersedes this grant.
	for _, file := range files {
		if file.version == 80 {
			if err := applyMigration(db, file); err != nil {
				t.Fatal(err)
			}
		}
	}
	newUser := func(name string) uint32 {
		t.Helper()
		var uid uint32
		if err := db.Get(&uid, "INSERT INTO users(username,password) VALUES($1,'test') RETURNING id", name); err != nil {
			t.Fatal(err)
		}
		return uid
	}
	create := func(uid uint32) {
		t.Helper()
		if _, err := db.Exec("INSERT INTO characters(user_id,is_female,is_new_character,name,unk_desc_string,hr,gr,weapon_type,last_login) VALUES($1,false,true,'','',0,0,0,0)", uid); err != nil {
			t.Fatal(err)
		}
	}
	read := func(uid uint32) (sql.NullTime, bool) {
		t.Helper()
		var expiry sql.NullTime
		var pending bool
		if err := db.QueryRow("SELECT return_expires,rookie_return_pending FROM users WHERE id=$1", uid).Scan(&expiry, &pending); err != nil {
			t.Fatal(err)
		}
		return expiry, pending
	}
	create(legacy)
	if expiry, pending := read(legacy); expiry.Valid || pending {
		t.Fatalf("existing account was granted newcomer status: %v %v", expiry, pending)
	}
	uid := newUser("newcomer")
	if expiry, pending := read(uid); expiry.Valid || !pending {
		t.Fatalf("registration started the window: %v %v", expiry, pending)
	}
	create(uid)
	first, pending := read(uid)
	want := time.Now().Add(30 * 24 * time.Hour)
	if !first.Valid || pending || first.Time.Sub(want).Abs() > 5*time.Second {
		t.Fatalf("first character expiry=%v pending=%v want=%v", first, pending, want)
	}
	create(uid)
	if next, _ := read(uid); !next.Time.Equal(first.Time) {
		t.Fatalf("additional character renewed status: %v -> %v", first, next)
	}
	expired := time.Now().Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err := db.Exec("UPDATE users SET return_expires=$1 WHERE id=$2", expired, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM characters WHERE user_id=$1", uid); err != nil {
		t.Fatal(err)
	}
	create(uid)
	if next, _ := read(uid); !next.Time.Equal(expired) {
		t.Fatalf("delete/recreate renewed status: %v", next)
	}
	longerUID := newUser("already-longer")
	longer := time.Now().Add(40 * 24 * time.Hour).Truncate(time.Microsecond)
	if _, err := db.Exec("UPDATE users SET return_expires=$1 WHERE id=$2", longer, longerUID); err != nil {
		t.Fatal(err)
	}
	create(longerUID)
	if next, pending := read(longerUID); !next.Time.Equal(longer) || pending {
		t.Fatalf("existing longer expiry was shortened: %v", next)
	}
	rollbackUID := newUser("rollback")
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO characters(user_id,is_female,is_new_character,name,unk_desc_string,hr,gr,weapon_type,last_login) VALUES($1,false,true,'','',0,0,0,0)", rollbackUID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if expiry, pending := read(rollbackUID); expiry.Valid || !pending {
		t.Fatalf("rolled-back creation consumed the grant: %v %v", expiry, pending)
	}
	concurrentUID := newUser("concurrent")
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.Exec("INSERT INTO characters(user_id,is_female,is_new_character,name,unk_desc_string,hr,gr,weapon_type,last_login) VALUES($1,false,true,'','',0,0,0,0)", concurrentUID)
			if err != nil {
				failures <- fmt.Errorf("concurrent insert: %w", err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if expiry, pending := read(concurrentUID); !expiry.Valid || pending || expiry.Time.Sub(time.Now().Add(30*24*time.Hour)).Abs() > 5*time.Second {
		t.Fatalf("concurrent grant invalid: %v %v", expiry, pending)
	}
}
