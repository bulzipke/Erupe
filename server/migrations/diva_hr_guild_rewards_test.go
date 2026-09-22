package migrations

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func TestDivaHRGuildRewardGroupsUpgrade(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("HR guild reward migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE diva_reward_groups(
		char_id INTEGER,event_id INTEGER,reward_type SMALLINT CHECK(reward_type IN (0,1,3,6)),
		group_key TEXT,variant TEXT,PRIMARY KEY(char_id,event_id,reward_type,group_key));
		CREATE TABLE diva_reward_receipts(
		id INTEGER PRIMARY KEY,char_id INTEGER,event_id INTEGER,reward_type SMALLINT,
		catalog_key TEXT,item_type SMALLINT,item_id INTEGER,quantity INTEGER,
		offered_at TIMESTAMPTZ,claimed_at TIMESTAMPTZ);
		INSERT INTO diva_reward_groups VALUES
		(8,10,0,'daily-1','gr'),(8,10,1,'milestone-20000','hr-2-4'),
		(8,10,3,'rank-1','gr'),(8,10,6,'milestone-10000','hr-5-7');`); err != nil {
		t.Fatal(err)
	}
	// These are the exact retained twelve GR receipt keys, not inferred keys
	// from a prefix. Separate characters also verify each 22-area item alone.
	approved := []struct {
		key   string
		areas int
	}{
		{"tactics-guild-2-7-1026", 2}, {"tactics-guild-3-7-1026", 3},
		{"tactics-guild-5-7-7456", 5}, {"tactics-guild-6-7-1026", 6},
		{"tactics-guild-8-7-7457", 8}, {"tactics-guild-10-7-1026", 10},
		{"tactics-guild-20-7-7458", 20}, {"tactics-guild-22-7-1026", 22},
		{"tactics-guild-22-7-13692", 22}, {"tactics-guild-22-7-13693", 22},
		{"tactics-guild-24-7-7463", 24}, {"tactics-guild-26-26-0", 26},
	}
	for i, row := range approved {
		for j, charID := range []int{1, 101 + i} {
			if _, err := db.Exec(`INSERT INTO diva_reward_receipts VALUES
				($1,$2,10,7,$3,7,1026,$4,'2026-09-01 12:34:56.123456+09',
				CASE WHEN $5 THEN TIMESTAMPTZ '2026-09-02 01:02:03.654321+09' ELSE NULL END)`,
				i*2+j+1, charID, row.key, 17+i, (i+j)%2 == 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO diva_reward_receipts VALUES
		(101,2,10,7,'tactics-guild-22-7-9999',7,9999,1,NOW(),NULL),
		(102,2,10,7,'tactics-guild-2-7-1026-extra',7,1026,1,NOW(),NOW()),
		(103,2,10,7,'custom-guild-hr-2',7,1026,1,NOW(),NULL),
		(104,2,10,5,'tactics-guild-2-7-1026',7,1026,1,NOW(),NULL),
		(105,2,10,6,'tactics-guild-3-7-1026',7,1026,1,NOW(),NOW()),
		(106,2,10,0,'tactics-guild-5-7-7456',7,7456,1,NOW(),NULL);`); err != nil {
		t.Fatal(err)
	}
	const receiptSnapshot = `SELECT json_agg(r ORDER BY id)::TEXT FROM diva_reward_receipts r`
	const otherGroupSnapshot = `SELECT json_agg(g ORDER BY reward_type)::TEXT FROM diva_reward_groups g WHERE reward_type<>7`
	var receiptsBefore, groupsBefore string
	if err := db.Get(&receiptsBefore, receiptSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&groupsBefore, otherGroupSnapshot); err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0057_diva_hr_guild_reward_groups.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_groups WHERE reward_type=7`); err != nil || count != 22 {
		t.Fatalf("exact-key backfill count=%d, want 10 shared + 12 individual, error=%v", count, err)
	}
	for i, row := range approved {
		var variant string
		for _, charID := range []int{1, 101 + i} {
			if err := db.Get(&variant, `SELECT variant FROM diva_reward_groups
				WHERE char_id=$1 AND event_id=10 AND reward_type=7 AND group_key=$2`,
				charID, fmt.Sprintf("milestone-%d", row.areas)); err != nil || variant != "gr" {
				t.Fatalf("receipt %s char %d: variant=%s error=%v", row.key, charID, variant, err)
			}
		}
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_groups WHERE char_id=1 AND reward_type=7 AND group_key='milestone-22'`); err != nil || count != 1 {
		t.Fatalf("22-area rewards split into more than one bundle: %d %v", count, err)
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_groups WHERE char_id=2`); err != nil || count != 0 {
		t.Fatalf("foreign/malformed receipt backfilled: %d %v", count, err)
	}
	var receiptsAfter, groupsAfter string
	if err := db.Get(&receiptsAfter, receiptSnapshot); err != nil || receiptsAfter != receiptsBefore {
		t.Fatalf("legacy receipt IDs, amounts or timestamps changed: %v\nbefore=%s\nafter=%s", err, receiptsBefore, receiptsAfter)
	}
	if err := db.Get(&groupsAfter, otherGroupSnapshot); err != nil || groupsAfter != groupsBefore {
		t.Fatalf("existing other-type groups changed: %v\nbefore=%s\nafter=%s", err, groupsBefore, groupsAfter)
	}
	if _, err := db.Exec(`INSERT INTO diva_reward_groups VALUES(3,10,7,'milestone-2','hr-2-4');
		INSERT INTO diva_reward_receipts VALUES(107,3,10,7,'tactics-guild-2-7-1026',7,1026,5,NOW(),NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&receiptsBefore, receiptSnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatalf("migration rerun failed: %v", err)
	}
	var variant string
	if err := db.Get(&variant, `SELECT variant FROM diva_reward_groups WHERE char_id=3 AND event_id=10 AND reward_type=7 AND group_key='milestone-2'`); err != nil || variant != "hr-2-4" {
		t.Fatalf("rerun replaced an existing rank choice: %s %v", variant, err)
	}
	if err := db.Get(&count, `SELECT COUNT(*) FROM diva_reward_groups WHERE reward_type=7`); err != nil || count != 23 {
		t.Fatalf("rerun duplicated or removed bundles: %d %v", count, err)
	}
	if err := db.Get(&receiptsAfter, receiptSnapshot); err != nil || receiptsAfter != receiptsBefore {
		t.Fatalf("rerun modified legacy receipts: %v", err)
	}
	if err := db.Get(&groupsAfter, otherGroupSnapshot); err != nil || groupsAfter != groupsBefore {
		t.Fatalf("rerun changed other-type groups: %v", err)
	}
	_, err = db.Exec(`INSERT INTO diva_reward_groups VALUES(3,10,5,'treasure-1','gr')`)
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || string(pgErr.Code) != "23514" {
		t.Fatalf("treasure type 5 should remain excluded from rank groups: %v", err)
	}
}
