package migrations

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestDivaHRPrayerRotationUpgrade(t *testing.T) {
	db := testDB(t)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE characters(id INTEGER PRIMARY KEY);
		CREATE TABLE events(id INTEGER PRIMARY KEY);
		CREATE TABLE diva_reward_receipts(id INTEGER PRIMARY KEY,char_id INTEGER,event_id INTEGER,reward_type SMALLINT,catalog_key TEXT,claimed_at TIMESTAMPTZ);
		CREATE TABLE diva_reward_groups(char_id INTEGER,event_id INTEGER,reward_type SMALLINT,group_key TEXT,variant TEXT,
		 PRIMARY KEY(char_id,event_id,reward_type,group_key));
		INSERT INTO characters VALUES(1),(2); INSERT INTO events VALUES(10),(11);
		INSERT INTO diva_reward_receipts VALUES
		 (1,1,10,1,'rotation-gr-crystals-v1-0','2026-09-01 12:34:56+09'),
		 (2,1,10,1,'rotation-gr-crystals-v1-1',NULL),
		 (3,1,10,1,'rotation-gr-crystals-v1-2',NULL),
		 (4,1,10,1,'rotation-gr-crystals-v1-4294864',NULL),
		 (5,1,10,1,'rotation-gr-crystals-v1-4294865',NULL),
		 (6,1,10,1,'rotation-gr-crystals-v1-99999999999999999999999999999',NULL),
		 (7,1,10,1,'rotation-gr-crystals-v1--1',NULL),
		 (8,1,10,1,'rotation-gr-crystals-v1-a',NULL),
		 (9,1,10,0,'rotation-gr-crystals-v1-3',NULL),
		 (10,1,10,1,'rotation-hr-prayer-v1-4',NULL),
		 (11,1,10,1,'norma-gr-20000-gp',NULL);
		INSERT INTO diva_reward_groups VALUES(1,10,1,'milestone-105000','preselected');`); err != nil {
		t.Fatal(err)
	}
	old, err := migrationFS.ReadFile("sql/0051_diva_prayer_rotation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(old)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO diva_prayer_rotation_progress
		(char_id,event_id,schedule_key,schedule_fingerprint,next_index)
		VALUES(1,10,'gr-crystals-v1','legacy-fingerprint',3),(2,11,'gr-crystals-v1','other-fingerprint',17)`); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT json_agg(r ORDER BY id)::TEXT FROM diva_reward_receipts r`
	var before string
	if err := db.Get(&before, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0052_diva_hr_prayer_rotation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE track='gr' AND
		 ((char_id=1 AND event_id=10 AND schedule_key='gr-crystals-v1' AND schedule_fingerprint='legacy-fingerprint' AND next_index=3)
		 OR (char_id=2 AND event_id=11 AND schedule_fingerprint='other-fingerprint' AND next_index=17))`); err != nil || count != 2 {
		t.Fatalf("legacy metadata changed: %d, %v", count, err)
	}
	var groups []struct {
		Key     string `db:"group_key"`
		Variant string `db:"variant"`
	}
	if err := db.Select(&groups, `SELECT group_key,variant FROM diva_reward_groups ORDER BY group_key`); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"milestone-103000": "gr", "milestone-104000": "gr", "milestone-105000": "preselected", "milestone-4294967000": "gr"}
	if len(groups) != len(want) {
		t.Fatalf("malformed/foreign receipt protected: %+v", groups)
	}
	for _, group := range groups {
		if want[group.Key] != group.Variant {
			t.Fatalf("unexpected backfill: %+v", group)
		}
	}
	if _, err := db.Exec(`INSERT INTO diva_prayer_rotation_progress(char_id,event_id,track,schedule_key,schedule_fingerprint,next_index)
		VALUES(1,10,'hr','hr-prayer-v1','hr-fingerprint',90)`); err != nil {
		t.Fatalf("HR cannot coexist with GR: %v", err)
	}
	for _, track := range []string{"hr", "gr", "invalid"} {
		_, err := db.Exec(`INSERT INTO diva_prayer_rotation_progress(char_id,event_id,track,schedule_key,schedule_fingerprint)
			VALUES(1,10,$1,'changed-policy','changed-fingerprint')`, track)
		var pgErr *pq.Error
		state := "23505"
		if track == "invalid" {
			state = "23514"
		}
		if !errors.As(err, &pgErr) || string(pgErr.Code) != state {
			t.Fatalf("track %s: %v, want %s", track, err, state)
		}
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatalf("migration rerun failed: %v", err)
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_prayer_rotation_progress WHERE track='hr' AND next_index=90 AND schedule_fingerprint='hr-fingerprint'`); err != nil || count != 1 {
		t.Fatalf("rerun reset HR progress: %d, %v", count, err)
	}
	var after string
	if err := db.Get(&after, snapshot); err != nil || after != before {
		t.Fatalf("migration mutated receipts: %v\nbefore=%s\nafter=%s", err, before, after)
	}
}
