package migrations

import "testing"

func TestDivaHRMilestoneUpgrade(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE diva_reward_groups(char_id integer,event_id integer,
 reward_type smallint CHECK(reward_type IN (0,3)),group_key text,variant text,
 PRIMARY KEY(char_id,event_id,reward_type,group_key));
 CREATE TABLE diva_reward_receipts(char_id integer,event_id integer,reward_type integer,catalog_key text,claimed_at timestamptz);
 CREATE TABLE diva_prizes(id integer,type text,points_req integer,gr boolean,repeatable boolean);
 INSERT INTO diva_prizes VALUES(10,'personal',10000,true,false),(11,'personal',20000,true,false);
 INSERT INTO diva_reward_receipts VALUES(1,5,1,'norma-gr-20000-gp',NOW()),
 (1,5,1,'norma-gr-20000-2c15',NULL),(1,5,6,'tactics-personal-10',NOW()),
 (1,5,6,'tactics-personal-11',NULL),(2,5,0,'daily-1-3694',NOW());`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0050_diva_hr_milestone_groups.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.Get(&n, `SELECT COUNT(*) FROM diva_reward_groups WHERE char_id=1 AND variant='gr'`); err != nil || n != 3 {
		t.Fatalf("old offered/claimed bundles not protected: %d %v", n, err)
	}
	if _, err = db.Exec(`INSERT INTO diva_reward_groups VALUES(2,5,1,'milestone-100','hr-2-99'),(2,5,6,'milestone-1000','hr-2-99')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO diva_reward_groups VALUES(2,5,7,'milestone-1000','gr')`); err == nil {
		t.Fatal("unimplemented group accepted")
	}
}
