package migrations

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"erupe-ce/common/mhfcid"

	"github.com/jmoiron/sqlx"
)

func variedIDTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	// testDB clears public, so never allow these tests to target an actual server.
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("character ID tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE SEQUENCE characters_id_seq AS integer;
		CREATE TABLE characters(id integer PRIMARY KEY DEFAULT nextval('characters_id_seq'),name text,deleted boolean DEFAULT false);
		CREATE TABLE user_binary(id integer PRIMARY KEY);
		CREATE TABLE friends(character_id integer REFERENCES characters(id));`); err != nil {
		t.Fatal(err)
	}
	return db
}

func applyVariedIDs(t *testing.T, db *sqlx.DB) {
	t.Helper()
	data, err := migrationFS.ReadFile("sql/0085_varied_character_ids.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Match the production migration's transaction and table-lock lifetime.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(string(data)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestVariedCharacterIDs(t *testing.T) {
	db := variedIDTestDB(t)
	if _, err := db.Exec(`INSERT INTO characters VALUES(56,'legacy',false),(77,'deleted',true);
		INSERT INTO user_binary VALUES(200); INSERT INTO friends VALUES(56);
		SELECT setval('characters_id_seq',250,true);`); err != nil {
		t.Fatal(err)
	}
	applyVariedIDs(t, db)
	var preserved bool
	if err := db.Get(&preserved, `SELECT
		(SELECT count(*)=2 FROM characters WHERE (id=56 AND name='legacy') OR (id=77 AND deleted))
		AND (SELECT character_id=56 FROM friends)
		AND (SELECT last_value=250 FROM characters_id_seq)
		AND (SELECT seqmin=251 AND NOT seqcycle FROM pg_sequence WHERE seqrelid='character_id_counter_seq'::regclass)`); err != nil || !preserved {
		t.Fatalf("legacy rows, references or reserved sequence boundary changed: %v %v", preserved, err)
	}
	// Even explicit IDs imported after cutover and orphaned binary rows are skipped.
	if _, err := db.Exec(`INSERT INTO characters(id) VALUES(permute_character_id(251));
		INSERT INTO user_binary VALUES(permute_character_id(252));`); err != nil {
		t.Fatal(err)
	}
	var first, expected uint32
	if err := db.Get(&first, `INSERT INTO characters(name) VALUES('first') RETURNING id`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&expected, `SELECT permute_character_id(253)`); err != nil || first != expected {
		t.Fatalf("collision was not skipped: %d want %d (%v)", first, expected, err)
	}
	var unique bool
	if err := db.Get(&unique, `SELECT count(DISTINCT id)=100000 AND min(id)>0 AND max(id)<1073741824
		FROM (SELECT permute_character_id(n) AS id FROM generate_series(1,100000) n) samples`); err != nil || !unique {
		t.Fatalf("permutation collision or native range overflow: %v %v", unique, err)
	}
	var ids []uint32
	if err := db.Select(&ids, `INSERT INTO characters(name) SELECT 'bulk' FROM generate_series(1,5000) RETURNING id`); err != nil {
		t.Fatal(err)
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRTUVWXYZ"
	lastDigits := make(map[byte]bool)
	for _, id := range ids {
		if id <= 250 || id >= 1<<30 {
			t.Fatalf("reserved or out-of-range ID %d", id)
		}
		var code [6]byte
		for i := range code {
			code[i] = alphabet[(id>>uint(5*i))&31]
		}
		if got := mhfcid.ConvertCID(string(code[:])); got != id {
			t.Fatalf("native six-character lookup failed: %s -> %d, want %d", code, got, id)
		}
		lastDigits[code[5]] = true
	}
	if len(lastDigits) != 32 {
		t.Fatalf("IDs are not spread across all six digits: %d last digits", len(lastDigits))
	}
	// Sequence draws remain consumed after rollback and physical deletion.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	var rolledBack uint32
	if err = tx.Get(&rolledBack, `INSERT INTO characters(name) VALUES('rollback') RETURNING id`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM characters WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	var workers sync.WaitGroup
	errors := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 32; i++ {
				var id uint32
				if err := db.Get(&id, `INSERT INTO characters(name) VALUES('concurrent') RETURNING id`); err != nil {
					errors <- err
					return
				}
				if id <= 250 || id >= 1<<30 || id == first || id == rolledBack {
					errors <- fmt.Errorf("reused or invalid ID %d", id)
					return
				}
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM characters WHERE name='concurrent'`); err != nil || count != 256 {
		t.Fatalf("concurrent creations: %d want 256 (%v)", count, err)
	}
	for _, invalid := range []int64{0, -1, 1 << 30} {
		if err := db.Get(&count, `SELECT permute_character_id($1)`, invalid); err == nil {
			t.Errorf("out-of-range input accepted: %d", invalid)
		}
	}
	if _, err := db.Exec(`SELECT setval('character_id_counter_seq',1073741823,true)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&count, `SELECT next_character_id()`); err == nil || err == sql.ErrNoRows {
		t.Fatal("exhausted allocator must fail without wrapping", err)
	}
}

func TestVariedCharacterIDsEmptyDatabase(t *testing.T) {
	db := variedIDTestDB(t)
	applyVariedIDs(t, db)
	var got, expected int
	if err := db.Get(&got, `INSERT INTO characters(name) VALUES('new') RETURNING id`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&expected, `SELECT permute_character_id(1)`); err != nil || got != expected {
		t.Fatalf("empty database first ID: %d want %d (%v)", got, expected, err)
	}
}
