package migrations

import "testing"

func TestDivaCustomRewardUpgradeAndMembershipHistory(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE characters(id integer PRIMARY KEY);
 CREATE TABLE events(id integer PRIMARY KEY);
 CREATE TABLE guild_characters(character_id bigint,guild_id bigint);
 CREATE TABLE diva_reward_receipts(char_id int,event_id int,reward_type int,catalog_key text,claimed_at timestamptz);
 INSERT INTO characters VALUES(1),(2); INSERT INTO events VALUES(10);
 INSERT INTO guild_characters VALUES(1,50);
 INSERT INTO diva_reward_receipts VALUES(1,10,0,'daily-1-3694',NOW()),
 (1,10,0,'daily-5-261b',NULL),(1,10,0,'daily-5-31e9',NOW()),
 (1,10,2,'rank-100-gp',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0049_diva_custom_reward_groups.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_reward_groups WHERE char_id=1 AND event_id=10 AND reward_type=0 AND variant='gr'`); err != nil || n != 2 {
		t.Fatalf("original offers/claims not preserved %d %v", n, err)
	}
	var recent bool
	if err = db.Get(&recent, `SELECT valid_from>NOW()-INTERVAL '1 minute' AND valid_until IS NULL FROM diva_guild_membership_history WHERE char_id=1`); err != nil || !recent {
		t.Fatalf("seed invented historical membership %v %v", recent, err)
	}
	if _, err = db.Exec(`UPDATE guild_characters SET guild_id=50 WHERE character_id=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_guild_membership_history`); err != nil || n != 1 {
		t.Fatalf("same guild split interval %d %v", n, err)
	}
	if _, err = db.Exec(`UPDATE guild_characters SET guild_id=60 WHERE character_id=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_guild_membership_history WHERE char_id=1 AND guild_id=50 AND valid_until IS NOT NULL`); err != nil || n != 1 {
		t.Fatalf("transfer did not close interval %d %v", n, err)
	}
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_guild_membership_history WHERE char_id=1 AND guild_id=60 AND valid_until IS NULL`); err != nil || n != 1 {
		t.Fatalf("transfer did not open interval %d %v", n, err)
	}
	if _, err = db.Exec(`DELETE FROM guild_characters WHERE character_id=1; INSERT INTO guild_characters VALUES(2,60)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_guild_membership_history WHERE char_id=1 AND valid_until IS NULL`); err != nil || n != 0 {
		t.Fatalf("departure left active interval %d %v", n, err)
	}
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_guild_membership_history WHERE char_id=2 AND guild_id=60 AND valid_until IS NULL`); err != nil || n != 1 {
		t.Fatalf("join missing %d %v", n, err)
	}
}
