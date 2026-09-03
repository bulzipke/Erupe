package channelserver

import (
	"time"

	"github.com/jmoiron/sqlx"
)

// HuntRecordRepository persists per-character personal-best hunt times.
type HuntRecordRepository struct {
	db *sqlx.DB
}

// HuntRecordUpsert is the raw quest-scoped hunt result retained for later
// leaderboard reclassification. RankKind and VariantKind are stored alongside
// the quest metadata so rankings can be rebuilt without losing the original
// per-quest personal best.
type HuntRecordUpsert struct {
	CharacterID    uint32
	MonsterID      int
	QuestID        uint16
	QuestName      string
	RankKind       string
	VariantKind    string
	QuestVariant1  uint8
	QuestVariant2  uint8
	QuestVariant3  uint8
	QuestVariant4  uint8
	RankBand       uint16
	StatTable1     uint32
	StatTable2     uint8
	WeaponType     *uint8
	BestTimeFrames uint32
	RecordedAt     time.Time
}

// NewHuntRecordRepository creates a personal-best hunt record repository.
func NewHuntRecordRepository(db *sqlx.DB) *HuntRecordRepository {
	return &HuntRecordRepository{db: db}
}

// UpsertPersonalBest keeps only the fastest observed time for a hunter,
// monster, quest, rank, and form. Equal or slower hunts leave the existing
// record and its metadata untouched.
func (r *HuntRecordRepository) UpsertPersonalBest(record HuntRecordUpsert) error {
	_, err := r.db.Exec(`
		INSERT INTO monster_hunt_records
			(character_id, monster_id, quest_id, quest_name, rank_kind, variant_kind,
			 quest_variant1, quest_variant2, quest_variant3, quest_variant4,
			 rank_band, stat_table1, stat_table2, weapon_type, best_time_frames, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (character_id, monster_id, quest_id, rank_kind, variant_kind) DO UPDATE
		SET quest_name = EXCLUDED.quest_name,
			quest_variant1 = EXCLUDED.quest_variant1,
			quest_variant2 = EXCLUDED.quest_variant2,
			quest_variant3 = EXCLUDED.quest_variant3,
			quest_variant4 = EXCLUDED.quest_variant4,
			rank_band = EXCLUDED.rank_band,
			stat_table1 = EXCLUDED.stat_table1,
			stat_table2 = EXCLUDED.stat_table2,
			weapon_type = EXCLUDED.weapon_type,
			best_time_frames = EXCLUDED.best_time_frames,
			recorded_at = EXCLUDED.recorded_at
		WHERE EXCLUDED.best_time_frames < monster_hunt_records.best_time_frames
	`,
		record.CharacterID,
		record.MonsterID,
		record.QuestID,
		record.QuestName,
		record.RankKind,
		record.VariantKind,
		record.QuestVariant1,
		record.QuestVariant2,
		record.QuestVariant3,
		record.QuestVariant4,
		record.RankBand,
		record.StatTable1,
		record.StatTable2,
		record.WeaponType,
		record.BestTimeFrames,
		record.RecordedAt,
	)
	return err
}
