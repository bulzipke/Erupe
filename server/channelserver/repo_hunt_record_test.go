package channelserver

import (
	"database/sql"
	"testing"
	"time"
)

func TestHuntRecordRepositoryKeepsPersonalBest(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	userID := CreateTestUser(t, db, "hunt_record_user")
	charID := CreateTestCharacter(t, db, userID, "HuntRecordHunter")
	repo := NewHuntRecordRepository(db)
	now := time.Now().UTC()
	bestWeapon := uint8(3)
	best := HuntRecordUpsert{
		CharacterID:    charID,
		MonsterID:      6,
		QuestID:        100,
		QuestName:      "첫 기록",
		RankKind:       "hr",
		VariantKind:    "normal",
		QuestVariant1:  1,
		QuestVariant2:  2,
		QuestVariant3:  3,
		QuestVariant4:  4,
		RankBand:       5,
		StatTable1:     6,
		StatTable2:     7,
		WeaponType:     &bestWeapon,
		BestTimeFrames: 3_000,
		RecordedAt:     now,
	}

	if err := repo.UpsertPersonalBest(best); err != nil {
		t.Fatalf("insert personal best: %v", err)
	}
	slower := best
	slower.QuestName = "느린 기록"
	slower.QuestVariant1 = 11
	slower.QuestVariant2 = 12
	slower.QuestVariant3 = 13
	slower.QuestVariant4 = 14
	slower.RankBand = 15
	slower.StatTable1 = 16
	slower.StatTable2 = 17
	slowerWeapon := uint8(4)
	slower.WeaponType = &slowerWeapon
	slower.BestTimeFrames = 3_600
	slower.RecordedAt = now.Add(time.Minute)
	if err := repo.UpsertPersonalBest(slower); err != nil {
		t.Fatalf("write slower time: %v", err)
	}
	faster := best
	faster.QuestName = "빠른 기록"
	faster.QuestVariant1 = 21
	faster.QuestVariant2 = 22
	faster.QuestVariant3 = 23
	faster.QuestVariant4 = 24
	faster.RankBand = ^uint16(0)
	faster.StatTable1 = ^uint32(0)
	faster.StatTable2 = ^uint8(0)
	fasterWeapon := uint8(12)
	faster.WeaponType = &fasterWeapon
	faster.BestTimeFrames = 2_400
	faster.RecordedAt = now.Add(2 * time.Minute)
	if err := repo.UpsertPersonalBest(faster); err != nil {
		t.Fatalf("write faster time: %v", err)
	}
	equal := faster
	equal.QuestName = "같은 시간 기록"
	equal.QuestVariant1 = 31
	equalWeapon := uint8(13)
	equal.WeaponType = &equalWeapon
	equal.RecordedAt = now.Add(3 * time.Minute)
	if err := repo.UpsertPersonalBest(equal); err != nil {
		t.Fatalf("write equal time: %v", err)
	}

	var frames uint32
	var questName string
	var questVariant1, questVariant2, questVariant3, questVariant4 uint8
	var rankBand uint16
	var statTable1 uint32
	var statTable2 uint8
	var weaponType uint8
	var recordedAt time.Time
	if err := db.QueryRow(`
		SELECT quest_name, quest_variant1, quest_variant2, quest_variant3, quest_variant4,
		       rank_band, stat_table1, stat_table2, weapon_type, best_time_frames, recorded_at
		FROM monster_hunt_records
		WHERE character_id=$1 AND monster_id=$2 AND quest_id=$3
		  AND rank_kind=$4 AND variant_kind=$5
	`, charID, 6, 100, "hr", "normal").Scan(
		&questName,
		&questVariant1,
		&questVariant2,
		&questVariant3,
		&questVariant4,
		&rankBand,
		&statTable1,
		&statTable2,
		&weaponType,
		&frames,
		&recordedAt,
	); err != nil {
		t.Fatalf("query personal best: %v", err)
	}
	if frames != 2_400 {
		t.Errorf("best frames = %d, want 2400", frames)
	}
	if questName != "빠른 기록" {
		t.Errorf("quest name = %q, want %q", questName, "빠른 기록")
	}
	if questVariant1 != 21 || questVariant2 != 22 || questVariant3 != 23 || questVariant4 != 24 {
		t.Errorf("quest variants = %d/%d/%d/%d, want 21/22/23/24", questVariant1, questVariant2, questVariant3, questVariant4)
	}
	if rankBand != ^uint16(0) || statTable1 != ^uint32(0) || statTable2 != ^uint8(0) {
		t.Errorf("raw quest metadata = rank_band %d, stat_table1 %d, stat_table2 %d", rankBand, statTable1, statTable2)
	}
	if weaponType != fasterWeapon {
		t.Errorf("weapon type = %d, want faster PB weapon %d", weaponType, fasterWeapon)
	}
	if !recordedAt.Equal(now.Add(2 * time.Minute)) {
		t.Errorf("recorded_at = %v, want %v", recordedAt, now.Add(2*time.Minute))
	}

	otherQuest := best
	otherQuest.QuestID = 200
	otherQuest.QuestName = "별도 퀘스트"
	otherQuest.BestTimeFrames = 2_700
	otherQuest.RecordedAt = now.Add(4 * time.Minute)
	if err := repo.UpsertPersonalBest(otherQuest); err != nil {
		t.Fatalf("insert separate quest best: %v", err)
	}

	gRank := best
	gRank.RankKind = "g"
	gRank.QuestName = "G급 기록"
	gRank.BestTimeFrames = 2_600
	gRank.RecordedAt = now.Add(5 * time.Minute)
	if err := repo.UpsertPersonalBest(gRank); err != nil {
		t.Fatalf("insert separate rank best: %v", err)
	}

	zenith := best
	zenith.VariantKind = "zenith"
	zenith.QuestName = "천이종 기록"
	zenith.BestTimeFrames = 2_500
	zenith.RecordedAt = now.Add(6 * time.Minute)
	if err := repo.UpsertPersonalBest(zenith); err != nil {
		t.Fatalf("insert separate variant best: %v", err)
	}

	hardcore := best
	hardcore.VariantKind = "hardcore"
	hardcore.QuestName = "hardcore record"
	hardcore.BestTimeFrames = 2_450
	hardcore.RecordedAt = now.Add(7 * time.Minute)
	if err := repo.UpsertPersonalBest(hardcore); err != nil {
		t.Fatalf("insert separate hardcore best: %v", err)
	}

	ambiguous := best
	ambiguous.VariantKind = "hardcore_optional"
	if err := repo.UpsertPersonalBest(ambiguous); err == nil {
		t.Fatal("ambiguous optional-HC record was accepted")
	}
	var records int
	if err := db.Get(&records, `
		SELECT COUNT(*) FROM monster_hunt_records
		WHERE character_id=$1 AND monster_id=$2
	`, charID, 6); err != nil {
		t.Fatalf("count hunt records: %v", err)
	}
	if records != 5 {
		t.Errorf("stored quest/rank/variant rows = %d, want 5", records)
	}

	conquest := best
	conquest.MonsterID = 107
	conquest.QuestID = 300
	conquest.RankKind = "g"
	conquest.VariantKind = "conquest"
	conquest.ConquestLevel = 1
	conquest.BestTimeFrames = 4_000
	if err := repo.UpsertPersonalBest(conquest); err != nil {
		t.Fatalf("insert level-1 Conquest best: %v", err)
	}
	level200 := conquest
	level200.ConquestLevel = 200
	level200.BestTimeFrames = 5_000
	if err := repo.UpsertPersonalBest(level200); err != nil {
		t.Fatalf("insert level-200 Conquest best: %v", err)
	}
	slowerLevel1 := conquest
	slowerLevel1.BestTimeFrames = 4_500
	if err := repo.UpsertPersonalBest(slowerLevel1); err != nil {
		t.Fatalf("write slower level-1 Conquest time: %v", err)
	}
	var conquestRows []struct {
		Level  uint16 `db:"conquest_level"`
		Frames uint32 `db:"best_time_frames"`
	}
	if err := db.Select(&conquestRows, `
		SELECT conquest_level, best_time_frames
		FROM monster_hunt_records
		WHERE character_id=$1 AND monster_id=$2 AND quest_id=$3
		  AND rank_kind='g' AND variant_kind='conquest'
		ORDER BY conquest_level
	`, charID, 107, 300); err != nil {
		t.Fatalf("query Conquest personal bests: %v", err)
	}
	if len(conquestRows) != 2 ||
		conquestRows[0].Level != 1 || conquestRows[0].Frames != 4_000 ||
		conquestRows[1].Level != 200 || conquestRows[1].Frames != 5_000 {
		t.Fatalf("Conquest rows = %+v, want levels 1/200 with independent bests", conquestRows)
	}

	invalidLevelCategory := best
	invalidLevelCategory.QuestID = 301
	invalidLevelCategory.ConquestLevel = 1
	if err := repo.UpsertPersonalBest(invalidLevelCategory); err == nil {
		t.Fatal("non-Conquest record with a Conquest level was accepted")
	}

	unknownWeapon := best
	unknownWeapon.QuestID = 201
	unknownWeapon.QuestName = "기존 무기 미상 기록"
	unknownWeapon.WeaponType = nil
	if err := repo.UpsertPersonalBest(unknownWeapon); err != nil {
		t.Fatalf("insert record without weapon: %v", err)
	}
	var nullableWeapon sql.NullInt16
	if err := db.QueryRow(`
		SELECT weapon_type FROM monster_hunt_records
		WHERE character_id=$1 AND monster_id=$2 AND quest_id=$3
		  AND rank_kind=$4 AND variant_kind=$5
	`, charID, 6, 201, "hr", "normal").Scan(&nullableWeapon); err != nil {
		t.Fatalf("query nullable weapon: %v", err)
	}
	if nullableWeapon.Valid {
		t.Fatalf("record without a captured departure weapon stored %d, want NULL", nullableWeapon.Int16)
	}
}
