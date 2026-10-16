package migrations

import (
	"database/sql"
	"go.uber.org/zap"
	"testing"
	"time"
)

func TestAccountNewcomerSeparateFromReturning(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := ensureVersionTable(db); err != nil {
		t.Fatal(err)
	}
	files, err := readMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.version < 84 {
			if err := applyMigration(db, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	var old uint32
	if err := db.Get(&old, "INSERT INTO users(username,password,rookie_return_pending,return_expires) VALUES('old-return','test',false,now()+interval '10 days') RETURNING id"); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(db, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err := db.Get(&preserved, "SELECT return_expires>now() AND newcomer_expires IS NULL FROM users WHERE id=$1", old); err != nil || !preserved {
		t.Fatal("lost historical status", err)
	}
	var uid uint32
	if err := db.Get(&uid, "INSERT INTO users(username,password) VALUES('new-account','test') RETURNING id"); err != nil {
		t.Fatal(err)
	}
	read := func() (sql.NullTime, sql.NullTime, bool) {
		t.Helper()
		var n, r sql.NullTime
		var p bool
		if err := db.QueryRow("SELECT newcomer_expires,return_expires,rookie_return_pending FROM users WHERE id=$1", uid).Scan(&n, &r, &p); err != nil {
			t.Fatal(err)
		}
		return n, r, p
	}
	if n, r, p := read(); n.Valid || r.Valid || !p {
		t.Fatal("registration started first-character window")
	}
	insert := `INSERT INTO characters(user_id,is_female,is_new_character,name,unk_desc_string,hr,gr,weapon_type,last_login) VALUES($1,false,true,'','',0,0,0,0)`
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(insert, uid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, r, p := read(); n.Valid || r.Valid || !p {
		t.Fatal("rollback consumed grant")
	}
	if _, err := db.Exec(insert, uid); err != nil {
		t.Fatal(err)
	}
	first, returning, pending := read()
	if !first.Valid || returning.Valid || pending || first.Time.Sub(time.Now().Add(30*24*time.Hour)).Abs() > 5*time.Second {
		t.Fatalf("incorrect separated entitlement: %v %v %v", first, returning, pending)
	}
	if _, err := db.Exec(insert, uid); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := read(); !n.Time.Equal(first.Time) {
		t.Fatal("additional character renewed grant")
	}
	if _, err := db.Exec("DELETE FROM characters WHERE user_id=$1", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, uid); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := read(); !n.Time.Equal(first.Time) {
		t.Fatal("deletion/recreation renewed grant")
	}
}
