package channelserver

import (
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestNPCPriorityPersonalBestPostgres(t *testing.T) {
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
		CREATE TEMP TABLE monster_hunt_records(
		character_id int,monster_id int,quest_id int,quest_name text,rank_kind text,variant_kind text,conquest_level int,
		quest_variant1 int,quest_variant2 int,quest_variant3 int,quest_variant4 int,rank_band int,stat_table1 bigint,stat_table2 int,
		weapon_type int,best_time_frames bigint,recorded_at timestamptz,client_settings jsonb,
		PRIMARY KEY(character_id,monster_id,quest_id,rank_kind,variant_kind,conquest_level));
		INSERT INTO monster_hunt_records(character_id,monster_id,quest_id,rank_kind,variant_kind,conquest_level,best_time_frames)
		VALUES(1,6,50001,'g','normal',0,1),(1,6,50002,'g','normal',0,2);`)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewHuntRecordRepository(db)
	record := HuntRecordUpsert{
		CharacterID: 1, MonsterID: 6, QuestID: 50001, RankKind: "g", VariantKind: "normal", RecordedAt: time.Now(),
		ClientSettings: &HuntClientSettings{Version: 1, SupportKnown: true, NPCKnown: true, Solo: true},
	}
	for _, tc := range []struct {
		frames uint32
		npc    bool
		solo   bool
		want   uint32
	}{
		{1000, true, true, 1},     // Legacy and NPC-assisted times share a priority.
		{1000, false, true, 1000}, // NPC-free evidence outranks even a faster legacy time.
		{1, true, true, 1000},
		{1, false, false, 1000},
		{1100, false, true, 1000},
		{900, false, true, 900},
	} {
		record.BestTimeFrames = tc.frames
		record.ClientSettings.NPCUsed, record.ClientSettings.Solo = tc.npc, tc.solo
		if err := repo.UpsertPersonalBest(record); err != nil {
			t.Fatal(err)
		}
		var frames uint32
		if err := db.Get(&frames, `SELECT best_time_frames FROM monster_hunt_records WHERE quest_id=50001`); err != nil {
			t.Fatal(err)
		}
		if frames != tc.want {
			t.Fatalf("frames=%d want=%d for %+v", frames, tc.want, tc)
		}
	}
	var untouched bool
	if err := db.Get(&untouched, `SELECT client_settings IS NULL AND best_time_frames=2 FROM monster_hunt_records WHERE quest_id=50002`); err != nil || !untouched {
		t.Fatalf("unrelated legacy record was changed: %v", err)
	}
	// Starting without a legacy row must still retain NPC-assisted/unknown
	// solo records, and an NPC-free clear must supersede them even if slower.
	record.QuestID = 50003
	for _, tc := range []struct {
		frames   uint32
		npcKnown bool
		npcUsed  bool
		want     uint32
	}{
		{1000, true, true, 1000},
		{1100, true, true, 1000},
		{900, true, true, 900},
		{800, false, false, 800},
		{2000, true, false, 2000},
		{1, true, true, 2000},
		{1, false, false, 2000},
		{1900, true, false, 1900},
	} {
		record.BestTimeFrames = tc.frames
		record.ClientSettings.NPCKnown, record.ClientSettings.NPCUsed = tc.npcKnown, tc.npcUsed
		if err := repo.UpsertPersonalBest(record); err != nil {
			t.Fatal(err)
		}
		var frames uint32
		if err := db.Get(&frames, `SELECT best_time_frames FROM monster_hunt_records WHERE quest_id=50003`); err != nil {
			t.Fatal(err)
		}
		if frames != tc.want {
			t.Fatalf("NPC priority frames=%d want=%d for %+v", frames, tc.want, tc)
		}
	}
	// Raviente retains its existing time-only rule, independent of NPC proof.
	for _, monsterID := range []int{93, 149} {
		record.MonsterID = monsterID
		record.ClientSettings.Solo = false
		for _, frames := range []uint32{1000, 500, 1200} {
			record.BestTimeFrames = frames
			record.ClientSettings.NPCUsed = frames == 500
			if err := repo.UpsertPersonalBest(record); err != nil {
				t.Fatal(err)
			}
		}
		var frames uint32
		if err := db.Get(&frames, `SELECT best_time_frames FROM monster_hunt_records WHERE monster_id=$1 AND quest_id=50003`, monsterID); err != nil || frames != 500 {
			t.Fatalf("Raviente time-only best=%d err=%v", frames, err)
		}
	}
}
