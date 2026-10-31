package api

import (
	"context"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
)

// All writes are connection-local TEMP tables. Never use the destructive
// package schema-reset helpers against an operator-supplied database.
func TestDashboardRankingEligibilityPostgres(t *testing.T) {
	dsn := os.Getenv("RANKING_TEMP_TEST_DSN")
	if dsn == "" {
		t.Skip("RANKING_TEMP_TEST_DSN unset")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`SET search_path TO pg_temp;
		CREATE TEMP TABLE guilds(id int,name text,rank_rp int,return_type int);
		INSERT INTO guilds VALUES (1,'automatic-return',9999,2),(2,'automatic-rookie',9000,1),(3,'ordinary',100,0);
		CREATE TEMP TABLE characters(id int,name text,deleted bool,is_new_character bool);
		INSERT INTO characters SELECT n, 'hunter-'||n, false, false FROM generate_series(1,25) AS n;
		CREATE TEMP TABLE monster_hunt_records(character_id int,monster_id int,rank_kind text,variant_kind text,conquest_level int,weapon_type int,quest_id int,quest_name text,best_time_frames int,client_settings jsonb);
		INSERT INTO monster_hunt_records VALUES
		(1,6,'g','normal',0,0,1,'legacy-fast',1,NULL),
		(1,6,'g','normal',0,0,2,'npc-free-slower',400,'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":false,"solo":true}'),
		(2,6,'g','normal',0,0,3,'npc-free-fast',300,'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":false,"solo":true}'),
		(3,6,'g','normal',0,0,4,'legacy-null',10,NULL),
		(4,6,'g','normal',0,0,5,'npc-used',20,'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":true,"solo":true}'),
		(5,6,'g','normal',0,0,6,'legacy-missing-fields',30,'{"version":1,"supportKnown":true}'),
		(6,6,'g','normal',0,0,7,'npc-unknown',40,'{"supportKnown":true,"supportUsed":false,"npcKnown":false,"npcUsed":false,"solo":true}'),
		(7,6,'g','normal',0,0,8,'party',1,'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":false,"solo":false}'),
		(8,6,'g','normal',0,0,9,'support-boost',1,'{"supportKnown":true,"supportUsed":true,"npcKnown":true,"npcUsed":false,"solo":true}'),
		(9,6,'g','normal',0,0,10,'malformed-solo',1,'{"solo":"true"}'),
		(10,93,'g','normal',0,0,11,'raviente',1,NULL),
		(10,149,'g','normal',0,0,12,'berserk-raviente',1,NULL);
		INSERT INTO monster_hunt_records
		SELECT 1,6,'g','normal',0,0,10+n,'ineligible',n,
		'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":false,"solo":true}'::jsonb || change
		FROM (VALUES(1,'{"npcUsed":true}'::jsonb),(2,'{"solo":false}'::jsonb),(3,'{"supportUsed":true}'::jsonb),(4,'{"npcKnown":false}'::jsonb),(5,'{"npcUsed":"false"}'::jsonb)) AS cases(n,change);
		-- Top ten must be chosen after NPC priority, not just sorted afterwards.
		INSERT INTO monster_hunt_records
		SELECT n,15,'g','normal',0,0,100+n,'npc-free-slow',10000+n,
		'{"supportKnown":true,"supportUsed":false,"npcKnown":true,"npcUsed":false,"solo":true}'::jsonb
		FROM generate_series(1,11) AS n;
		INSERT INTO monster_hunt_records VALUES(12,15,'g','normal',0,0,200,'npc-fast',1,'{"solo":true,"npcUsed":true}');`)
	if err != nil {
		t.Fatal(err)
	}
	var guilds []GuildRankEntry
	if err := db.SelectContext(context.Background(), &guilds, dashboardGuildRankingsQuery); err != nil {
		t.Fatal(err)
	}
	if len(guilds) != 1 || guilds[0].Name != "ordinary" {
		t.Fatalf("automatic guilds leaked into RP rankings: %+v", guilds)
	}
	var times []MonsterTimeRankEntry
	if err := db.SelectContext(context.Background(), &times, dashboardMonsterTimesQuery); err != nil {
		t.Fatal(err)
	}
	if len(times) != 16 {
		t.Fatalf("expected six prioritized records and ten top records, got %+v", times)
	}
	for i, want := range []struct {
		quest    int
		frames   int64
		assisted bool
	}{{3, 300, false}, {2, 400, false}, {4, 10, true}, {5, 20, true}, {6, 30, true}, {7, 40, true}} {
		got := times[i]
		if got.MonsterID != 6 || got.QuestID != want.quest || got.Frames != want.frames || got.NPCAssisted != want.assisted {
			t.Errorf("rank %d = %+v, want %+v", i+1, got, want)
		}
	}
	for i, got := range times[6:] {
		if got.MonsterID != 15 || got.QuestID != 101+i || got.NPCAssisted {
			t.Errorf("top ten did not prefer NPC-free records: %+v", got)
		}
	}
}
