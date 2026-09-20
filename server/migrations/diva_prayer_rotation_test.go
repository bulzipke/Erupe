package migrations

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestDivaPrayerRotationMigration(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()
	_, err := db.Exec(`CREATE TABLE characters(id INTEGER PRIMARY KEY);
CREATE TABLE events(id INTEGER PRIMARY KEY);
CREATE TABLE diva_reward_receipts(char_id INTEGER,event_id INTEGER,reward_type INTEGER,catalog_key TEXT,claimed_at TIMESTAMPTZ);
INSERT INTO characters VALUES(1),(2),(3);
INSERT INTO events VALUES(10),(11),(12);
INSERT INTO diva_reward_receipts VALUES
 (1,10,1,'norma-gr-20000-gp','2026-09-01 12:34:56+09'),
 (1,10,1,'norma-gr-24500-ore',NULL),
 (2,11,0,'daily-1-3694','2026-09-02 01:02:03+09');`)
	if err != nil {
		t.Fatal(err)
	}
	const receiptSnapshot = `SELECT json_agg(r ORDER BY char_id,event_id,reward_type,catalog_key)::TEXT FROM diva_reward_receipts r`
	var receiptsBefore string
	if err := db.Get(&receiptsBefore, receiptSnapshot); err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0051_diva_prayer_rotation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatalf("apply rotation migration: %v", err)
	}

	insert := func(charID, eventID int, schedule string, next int64) error {
		_, err := db.Exec(`INSERT INTO diva_prayer_rotation_progress
 (char_id,event_id,schedule_key,schedule_fingerprint,next_index) VALUES($1,$2,$3,$4,$5)`,
			charID, eventID, schedule, "fingerprint-"+schedule, next)
		return err
	}
	wantSQLState := func(t *testing.T, err error, state string) {
		t.Helper()
		var pgErr *pq.Error
		if !errors.As(err, &pgErr) || string(pgErr.Code) != state {
			t.Fatalf("error = %v, want PostgreSQL SQLSTATE %s", err, state)
		}
	}
	count := func(t *testing.T, query string, want int) {
		t.Helper()
		var got int
		if err := db.Get(&got, query); err != nil || got != want {
			t.Fatalf("%s: count=%d, want=%d, err=%v", query, got, want, err)
		}
	}

	t.Run("default and inclusive bounds", func(t *testing.T) {
		if _, err := db.Exec(`INSERT INTO diva_prayer_rotation_progress
 (char_id,event_id,schedule_key,schedule_fingerprint) VALUES(1,10,'original','original-fingerprint')`); err != nil {
			t.Fatal(err)
		}
		var next int64
		if err := db.Get(&next, `SELECT next_index FROM diva_prayer_rotation_progress WHERE char_id=1 AND event_id=10`); err != nil || next != 0 {
			t.Fatalf("initial cursor=%d, err=%v", next, err)
		}
		if err := insert(1, 11, "original", 4294967295); err != nil {
			t.Fatalf("inclusive uint32 upper bound: %v", err)
		}
		wantSQLState(t, insert(3, 12, "invalid-negative", -1), "23514")
		wantSQLState(t, insert(3, 12, "invalid-overflow", 4294967296), "23514")
	})

	t.Run("one cursor per character and round irrespective of schedule", func(t *testing.T) {
		wantSQLState(t, insert(1, 10, "changed-policy", 0), "23505")
		if err := insert(2, 10, "other-character", 17); err != nil {
			t.Fatal(err)
		}
		if err := insert(2, 11, "other-round", 31); err != nil {
			t.Fatal(err)
		}
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress`, 4)
	})

	t.Run("rerun preserves progress and existing receipts", func(t *testing.T) {
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("migration rerun must be idempotent: %v", err)
		}
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress`, 4)
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE char_id=2 AND event_id=10
 AND schedule_key='other-character' AND schedule_fingerprint='fingerprint-other-character' AND next_index=17`, 1)
		var receiptsAfter string
		if err := db.Get(&receiptsAfter, receiptSnapshot); err != nil {
			t.Fatal(err)
		}
		if receiptsAfter != receiptsBefore {
			t.Fatalf("migration changed existing offered/claimed receipts:\nbefore=%s\nafter=%s", receiptsBefore, receiptsAfter)
		}
	})

	t.Run("foreign keys reject orphan progress and cascade only matching rows", func(t *testing.T) {
		wantSQLState(t, insert(999, 10, "missing-character", 0), "23503")
		wantSQLState(t, insert(1, 999, "missing-round", 0), "23503")
		if _, err := db.Exec(`DELETE FROM characters WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE char_id=1`, 0)
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE char_id=2`, 2)
		if _, err := db.Exec(`DELETE FROM events WHERE id=10`); err != nil {
			t.Fatal(err)
		}
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE event_id=10`, 0)
		count(t, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE char_id=2 AND event_id=11 AND next_index=31`, 1)
	})
}
