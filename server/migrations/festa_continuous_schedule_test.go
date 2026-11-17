package migrations

import "testing"

func TestFestaContinuousMigrationKeepsActiveChapterAndRecords(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,event_type text,start_time timestamptz);
	CREATE TABLE festa_registrations(guild_id integer,team text);
	CREATE TABLE festa_submissions(character_id integer,guild_id integer,trial_type integer,souls integer,timestamp timestamptz);
	CREATE TABLE festa_prizes_accepted(prize_id integer,character_id integer);
	CREATE TABLE rotation_schedule_adjustments(event_type text,event_id integer,previous_schedule jsonb,PRIMARY KEY(event_type,event_id));
	INSERT INTO events VALUES(11,'festa',now()-interval '60 days'),(12,'festa',now()-interval '8 days'),(13,'diva',now());
	INSERT INTO festa_registrations VALUES(1,'blue'),(2,'red');
	INSERT INTO festa_submissions VALUES(3,1,1,123,now());
	INSERT INTO festa_prizes_accepted VALUES(7,3),(0,3);
	CREATE TEMP TABLE original_events AS SELECT * FROM events;`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0099_festa_continuous_schedule.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var valid bool
	err = db.Get(&valid, `SELECT
	(SELECT event_id=12 FROM festa_event_lifecycle WHERE singleton)
	AND (SELECT festa_continuous AND extract(isodow FROM start_time AT TIME ZONE 'Asia/Seoul')=1
	AND (start_time AT TIME ZONE 'Asia/Seoul')::time='12:00' AND now()>=start_time+interval '7 days'
	AND now()<start_time+interval '14 days' FROM events WHERE id=12)
	AND (SELECT count(*)=2 AND bool_and(event_id=12) FROM festa_registrations)
	AND (SELECT count(*)=1 AND sum(souls)=123 AND bool_and(event_id=12) FROM festa_submissions)
	AND (SELECT count(*)=2 AND bool_and(event_id=12) FROM festa_prizes_accepted)
	AND (SELECT count(*)=1 FROM rotation_schedule_adjustments WHERE event_type='festa')
	AND NOT EXISTS(SELECT id,event_type,start_time FROM events WHERE id<>12 EXCEPT SELECT * FROM original_events WHERE id<>12)`)
	if err != nil || !valid {
		t.Fatalf("lost active soul phase or records: %v %v", valid, err)
	}
}

func TestFestaFPCurveMigrationSwitchesOnlyUnopenedMainRewards(t *testing.T) {
	data, err := migrationFS.ReadFile("sql/0115_festa_fp_curve.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		hoursOld   int
		mainClaim  bool
		wantSwitch bool
	}{
		{name: "gathering with a goal receipt", hoursOld: 192, wantSwitch: true},
		{name: "main prize already open", hoursOld: 338},
		{name: "main prize already claimed", hoursOld: 192, mainClaim: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE events(id integer PRIMARY KEY,start_time timestamptz,festa_continuous boolean);
			CREATE TABLE festa_event_lifecycle(singleton boolean PRIMARY KEY,event_id integer);
			CREATE TABLE festa_goal_rounds(event_id integer PRIMARY KEY,souls_per_participant integer);
			CREATE TABLE festa_prizes_accepted(event_id integer,prize_id integer);
			INSERT INTO festa_goal_rounds VALUES(12,500),(14,500);
			INSERT INTO festa_event_lifecycle VALUES(true,14);
			INSERT INTO festa_prizes_accepted VALUES(14,7);`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO events VALUES(14,now()-$1::integer*interval '1 hour',true)`, tc.hoursOld); err != nil {
				t.Fatal(err)
			}
			if tc.mainClaim {
				if _, err := db.Exec(`INSERT INTO festa_prizes_accepted VALUES(14,0)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(string(data)); err != nil {
				t.Fatal(err)
			}
			var active, previous bool
			var goal, receipts int
			err := db.QueryRow(`SELECT active.rebalanced_fp,previous.rebalanced_fp,active.souls_per_participant,
				(SELECT count(*) FROM festa_prizes_accepted WHERE event_id=14 AND prize_id=7)
				FROM festa_goal_rounds active CROSS JOIN festa_goal_rounds previous
				WHERE active.event_id=14 AND previous.event_id=12`).Scan(&active, &previous, &goal, &receipts)
			if err != nil || active != tc.wantSwitch || previous || goal != 500 || receipts != 1 {
				t.Fatalf("active FP switch=%t, previous=%t, target=%d, goal receipts=%d: %v", active, previous, goal, receipts, err)
			}
		})
	}
}

func TestFestaDailyMossMigrationRetiresOldTicketQuest(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE festa_trials(objective integer,goal_id integer,reward integer);
		CREATE TABLE event_quests(quest_id integer,quest_type integer,active_days integer,inactive_days integer);
		INSERT INTO festa_trials VALUES(5,53034,400),(5,53226,200),(5,63303,99),(1,27,1);
		INSERT INTO event_quests VALUES(53034,26,NULL,NULL),(53226,26,0,0),(63303,26,0,0);`); err != nil {
		t.Fatal(err)
	}
	data, err := migrationFS.ReadFile("sql/0116_festa_daily_moss_reward.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	var valid bool
	err = db.Get(&valid, `SELECT
		(SELECT active_days=1 AND inactive_days=-1 FROM event_quests WHERE quest_id=53034 AND quest_type=26)
		AND (SELECT count(*)=2 AND bool_and(active_days=0 AND inactive_days=0) FROM event_quests WHERE quest_id IN(53226,63303))
		AND (SELECT count(*)=4 AND sum(reward)=300 FROM festa_trials)
		AND (SELECT reward=0 FROM festa_trials WHERE objective=5 AND goal_id=53034)`)
	if err != nil || !valid {
		t.Fatalf("old ticket quest was not retired independently of other dailies: %v %v", valid, err)
	}
}
