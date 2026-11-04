package channelserver

import (
	"encoding/json"
	"os"
	"reflect"
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
	// Support ON, HP display and NPC use share a priority, and are all saved.
	// An unassisted clear must supersede them even when it is slower.
	record.QuestID = 50004
	for _, tc := range []struct {
		frames  uint32
		support bool
		hp      bool
		hpUsed  bool
		npc     bool
		want    uint32
	}{
		{1000, true, false, false, false, 1000},
		{900, false, false, true, false, 900},
		{850, false, true, false, false, 850},
		{800, false, false, false, true, 800},
		{750, true, false, false, false, 750},
		{2000, false, false, false, false, 2000},
		{1, true, false, false, false, 2000},
		{1, false, false, true, false, 2000},
		{1, false, true, false, false, 2000},
		{1, false, false, false, true, 2000},
		{1900, false, false, false, false, 1900},
	} {
		record.BestTimeFrames = tc.frames
		record.ClientSettings.SupportUsed = tc.support
		record.ClientSettings.HPView, record.ClientSettings.HPViewUsed = tc.hp, tc.hpUsed
		record.ClientSettings.NPCUsed = tc.npc
		if err := repo.UpsertPersonalBest(record); err != nil {
			t.Fatal(err)
		}
		var frames uint32
		var support, hp, hpUsed, npc bool
		if err := db.QueryRow(`SELECT best_time_frames,
			(client_settings->>'supportUsed')::bool, (client_settings->>'hpView')::bool,
			(client_settings->>'hpViewUsed')::bool, (client_settings->>'npcUsed')::bool
			FROM monster_hunt_records WHERE quest_id=50004`).Scan(&frames, &support, &hp, &hpUsed, &npc); err != nil {
			t.Fatal(err)
		}
		if frames != tc.want {
			t.Fatalf("assisted priority frames=%d want=%d for %+v", frames, tc.want, tc)
		}
		if tc.frames == tc.want && (support != tc.support || hp != tc.hp || hpUsed != tc.hpUsed || npc != tc.npc) {
			t.Fatalf("winning time lost usage evidence for %+v", tc)
		}
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
	// Historical bead metadata must stay with the winning time permanently.
	record.QuestID = 50005
	record.ClientSettings = &HuntClientSettings{Version: 1, SupportKnown: true, NPCKnown: true, Solo: true,
		DivaBeads: &HuntDivaBeads{Round: 5, Beads: []string{"결집", "봉뢰", "가호", "단력"}, Levels: []int{1, 2, 1, 1}}}
	for _, tc := range []struct {
		frames     uint32
		beads      *HuntDivaBeads
		wantRound  uint32
		wantLevels []int
	}{
		{1000, record.ClientSettings.DivaBeads, 5, []int{1, 2, 1, 1}},
		{1100, nil, 5, []int{1, 2, 1, 1}},
		{900, &HuntDivaBeads{Round: 6, Beads: []string{"폭풍"}, Levels: []int{3}}, 6, []int{3}},
		{800, nil, 0, nil},
	} {
		record.BestTimeFrames = tc.frames
		record.ClientSettings.DivaBeads = tc.beads
		if err := repo.UpsertPersonalBest(record); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err := db.Get(&raw, `SELECT client_settings FROM monster_hunt_records WHERE quest_id=50005`); err != nil {
			t.Fatal(err)
		}
		var saved HuntClientSettings
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		if (tc.wantRound == 0 && saved.DivaBeads != nil) || (tc.wantRound != 0 && (saved.DivaBeads == nil || saved.DivaBeads.Round != tc.wantRound)) {
			t.Fatalf("saved time lost/reused another hunt's beads: %s", raw)
		}
		if saved.DivaBeads != nil && !reflect.DeepEqual(saved.DivaBeads.Levels, tc.wantLevels) {
			t.Fatalf("saved time lost/reused another hunt's levels: %s", raw)
		}
	}
	// Raviente retains its existing time-only rule, independent of NPC proof.
	record.QuestID = 50003
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
