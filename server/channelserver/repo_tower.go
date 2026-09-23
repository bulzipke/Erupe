package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"erupe-ce/common/stringsupport"

	"github.com/jmoiron/sqlx"
)

// TowerRepository centralizes all database access for tower-related tables
// (tower, guilds tower columns, guild_characters tower columns).
type TowerRepository struct {
	db *sqlx.DB
}

// NewTowerRepository creates a new TowerRepository.
func NewTowerRepository(db *sqlx.DB) *TowerRepository {
	return &TowerRepository{db: db}
}

// RecordGuardianKill credits a quest-stage instance only once, regardless of
// how many party members send the same monster in their result logs.
func (r *TowerRepository) RecordGuardianKill(earthID int32, block uint8, runID string, charID uint32) error {
	if block < 1 || block > 2 || runID == "" || charID == 0 {
		return errors.New("invalid tower guardian kill receipt")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize a zone's global kill count so the 4000th unlock boundary is
	// recorded consistently, including for all hunters in the same party.
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1::bigint)`,
		int64(8100000000)+int64(earthID)*2+int64(block)); err != nil {
		return err
	}
	var beforeCount int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tower_guardian_kills
		WHERE earth_id=$1 AND block=$2`, earthID, block).Scan(&beforeCount); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO tower_guardian_kills
		(earth_id, block, run_id, character_id, before_casual)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (earth_id, block, run_id) DO NOTHING`,
		earthID, block, runID, charID, beforeCount < 4000); err != nil {
		return err
	}
	var beforeCasual bool
	if err := tx.QueryRow(`SELECT before_casual FROM tower_guardian_kills
		WHERE earth_id=$1 AND block=$2 AND run_id=$3`,
		earthID, block, runID).Scan(&beforeCasual); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO tower_guardian_participants
		(earth_id, block, run_id, character_id, before_casual)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (earth_id, block, run_id, character_id) DO NOTHING`,
		earthID, block, runID, charID, beforeCasual); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTowerSurveyHistory returns the floors the character cleared in EarthID
// earthID and the four rounds before it, newest first.
func (r *TowerRepository) GetTowerSurveyHistory(earthID int32, charID uint32) ([5]int32, error) {
	var floors [5]int32
	if earthID <= 0 || charID == 0 {
		return floors, nil
	}
	rows, err := r.db.Query(`SELECT earth_id, block1_floors+block2_floors FROM tower_event_progress
		WHERE character_id=$1 AND earth_id BETWEEN $2 AND $3`, charID, earthID-4, earthID)
	if err != nil {
		return floors, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, total int32
		if err := rows.Scan(&id, &total); err != nil {
			return floors, err
		}
		if i := earthID - id; i >= 0 && i < 5 {
			floors[i] = total
		}
	}
	return floors, rows.Err()
}

// GetTowerDailyBin returns the client's saved daily-mission progress
// (MSG_MHF_GET_TINY_BIN 0/1/1), or nil when none is stored.
func (r *TowerRepository) GetTowerDailyBin(charID uint32) ([]byte, error) {
	var data []byte
	err := r.db.QueryRow(`SELECT data FROM tower_daily_bins WHERE character_id=$1`, charID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return data, err
}

// GetRoadSkills returns the Hunting Road skill levels (every skill but the
// Tower-only ones) as a 64-entry CSV; a character without a row has none.
func (r *TowerRepository) GetRoadSkills(charID uint32) (string, error) {
	var skills string
	err := r.db.QueryRow(`SELECT skills FROM road_skills WHERE character_id=$1`, charID).Scan(&skills)
	if errors.Is(err, sql.ErrNoRows) {
		return EmptyTowerCSV(64), nil
	}
	return skills, err
}

// UpdateRoadSkills stores the Hunting Road skill levels.
func (r *TowerRepository) UpdateRoadSkills(charID uint32, skills string) error {
	if charID == 0 || len(stringsupport.CSVElems(skills)) != 64 {
		return errors.New("invalid road skills")
	}
	_, err := r.db.Exec(`INSERT INTO road_skills (character_id, skills) VALUES ($1,$2)
		ON CONFLICT (character_id) DO UPDATE SET skills=EXCLUDED.skills, updated_at=now()`, charID, skills)
	return err
}

// SaveTowerDailyBin stores the client's daily-mission progress verbatim.
func (r *TowerRepository) SaveTowerDailyBin(charID uint32, data []byte) error {
	if charID == 0 || len(data) != towerDailyBinSize {
		return errors.New("invalid tower daily progress")
	}
	_, err := r.db.Exec(`INSERT INTO tower_daily_bins (character_id, data) VALUES ($1,$2)
		ON CONFLICT (character_id) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, charID, data)
	return err
}

// GetGuardianKills reads the current manually selected EarthID's global counts.
func (r *TowerRepository) GetGuardianKills(earthID int32) (uint32, uint32, error) {
	var block1, block2 uint32
	err := r.db.QueryRow(
		`SELECT COUNT(*) FILTER (WHERE block=1), COUNT(*) FILTER (WHERE block=2)
		 FROM tower_guardian_kills WHERE earth_id=$1`, earthID,
	).Scan(&block1, &block2)
	return block1, block2, err
}

// TowerData holds the core tower stats for a character.
type TowerData struct {
	TR     int32
	TRP    int32
	TSP    int32
	Block1 int32
	Block2 int32
	Skills string
}

// GetTowerData returns tower stats for a character, creating the row if it doesn't exist.
func (r *TowerRepository) GetTowerData(charID uint32) (TowerData, error) {
	var td TowerData
	err := r.db.QueryRow(
		`SELECT COALESCE(tr, 0), COALESCE(trp, 0), COALESCE(tsp, 0), COALESCE(block1, 0), COALESCE(block2, 0), COALESCE(skills, $1) FROM tower WHERE char_id=$2 LIMIT 1`,
		EmptyTowerCSV(64), charID,
	).Scan(&td.TR, &td.TRP, &td.TSP, &td.Block1, &td.Block2, &td.Skills)
	if err == nil {
		return td, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return td, err
	}
	// The legacy table has no unique key; avoid manufacturing another row on
	// ordinary reads or on transient SELECT errors.
	_, err = r.db.Exec(`INSERT INTO tower (char_id) SELECT $1 WHERE NOT EXISTS (SELECT 1 FROM tower WHERE char_id=$1)`, charID)
	return TowerData{Skills: EmptyTowerCSV(64)}, err
}

// GetSkills returns the skills CSV string for a character.
func (r *TowerRepository) GetSkills(charID uint32) (string, error) {
	var skills string
	err := r.db.QueryRow(`SELECT COALESCE(skills, $1) FROM tower WHERE char_id=$2`, EmptyTowerCSV(64), charID).Scan(&skills)
	return skills, err
}

// UpdateSkills stores the skill levels and deducts cost TSP. A zero cost only
// stores the levels: Hunting Road learns and resets are paid in Road SP.
func (r *TowerRepository) UpdateSkills(charID uint32, skills string, cost int32) error {
	if cost < 0 {
		return errors.New("invalid tower skill cost")
	}
	result, err := r.db.Exec(`UPDATE tower SET skills=$1, tsp=COALESCE(tsp, 0)-$2 WHERE char_id=$3 AND COALESCE(tsp, 0)>=$2`, skills, cost, charID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return errors.New("insufficient tower skill points or missing character")
	}
	return nil
}

// AddTSP adds Tower skill points (a Tower Status ＴＳＰ変換), capped like the
// client at 99,999,999.
func (r *TowerRepository) AddTSP(charID uint32, tsp int32) error {
	if tsp <= 0 {
		return errors.New("invalid tower skill point gain")
	}
	result, err := r.db.Exec(`UPDATE tower SET tsp=LEAST(COALESCE(tsp, 0)+$1, $2) WHERE char_id=$3`, tsp, towerTSPMax, charID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return errors.New("missing tower data")
	}
	return nil
}

// ResetTowerSkills clears every Tower-only skill level and refunds TSP (a
// Tower Status reset with 再覚之古書).
func (r *TowerRepository) ResetTowerSkills(charID uint32, refund int32) error {
	if refund < 0 {
		return errors.New("invalid tower skill refund")
	}
	result, err := r.db.Exec(`UPDATE tower SET skills=$1, tsp=LEAST(COALESCE(tsp, 0)+$2, $3) WHERE char_id=$4`,
		EmptyTowerCSV(64), refund, towerTSPMax, charID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return errors.New("missing tower data")
	}
	return nil
}

// UpdateProgress updates tower progress (TR, TRP, TSP, block1).
func (r *TowerRepository) UpdateProgress(charID uint32, tr, trp, cost, block1 int32) error {
	if tr < 0 || trp < 0 || cost < 0 || block1 < 0 {
		return errors.New("negative tower progress")
	}
	result, err := r.db.Exec(
		`UPDATE tower SET tr=$1, trp=COALESCE(trp, 0)+$2, tsp=COALESCE(tsp, 0)+$3, block1=COALESCE(block1, 0)+$4 WHERE char_id=$5`,
		tr, trp, cost, block1, charID,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return errors.New("missing tower progress row")
	}
	return nil
}

// UpdateBlockFloors records the best floor reached in a tower block (1 or 2).
// The ZZ client reports it through MsgMhfPostTowerInfo InfoType 6 only when a
// run beats the record GetTowerInfo handed out, so the stored value never goes
// down: GREATEST keeps a stale or duplicate report from lowering it.
func (r *TowerRepository) UpdateBlockFloors(charID uint32, block uint8, floors int32) error {
	if floors < 0 {
		return errors.New("negative tower floors")
	}
	var column string
	switch block {
	case 1:
		column = "block1"
	case 2:
		column = "block2"
	default:
		return errors.New("invalid tower block")
	}
	result, err := r.db.Exec(
		fmt.Sprintf(`UPDATE tower SET %s=GREATEST(COALESCE(%s, 0), $1) WHERE char_id=$2`, column, column),
		floors, charID,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return errors.New("missing tower progress row")
	}
	return nil
}

// AddTowerRankPoints credits the TRP a tower run reported, recomputes the Tower
// Rank from the linear per-rank cost (rank = trp/perRank + 1, never lowered) and
// grants tspPerRank TSP for every rank gained. It returns the new rank. The
// official TRP curve is not known, so both costs come from config. All SET
// expressions read the pre-update row, which keeps the rank delta consistent.
func (r *TowerRepository) AddTowerRankPoints(charID uint32, trp, perRank, tspPerRank int32) (int32, error) {
	if trp < 0 || perRank <= 0 || tspPerRank < 0 {
		return 0, errors.New("invalid tower rank points")
	}
	var newTR int32
	err := r.db.QueryRow(
		`UPDATE tower SET
			trp = COALESCE(trp, 0) + $1,
			tsp = COALESCE(tsp, 0) + GREATEST(0, (COALESCE(trp, 0) + $1) / $2 + 1 - GREATEST(COALESCE(tr, 0), 1)) * $3,
			tr = GREATEST(COALESCE(tr, 0), 1, (COALESCE(trp, 0) + $1) / $2 + 1)
		 WHERE char_id=$4 RETURNING tr`,
		trp, perRank, tspPerRank, charID,
	).Scan(&newTR)
	if err == sql.ErrNoRows {
		return 0, errors.New("missing tower progress row")
	}
	return newTR, err
}

// GetGems returns the gems CSV string for a character.
func (r *TowerRepository) GetGems(charID uint32) (string, error) {
	var gems string
	err := r.db.QueryRow(`SELECT COALESCE(gems, $1) FROM tower WHERE char_id=$2`, EmptyTowerCSV(30), charID).Scan(&gems)
	return gems, err
}

// UpdateGems saves the gems CSV string for a character.
func (r *TowerRepository) UpdateGems(charID uint32, gems string) error {
	_, err := r.db.Exec(`UPDATE tower SET gems=$1 WHERE char_id=$2`, gems, charID)
	return err
}

// GetGemHistory shows the latest received ancient-treasure gifts.
func (r *TowerRepository) GetGemHistory(charID uint32) ([]GemHistory, error) {
	rows, err := r.db.Query(`SELECT h.gem_id, h.message, h.created_at, c.name
		FROM tower_gem_history h JOIN characters c ON c.id=h.sender_id
		WHERE h.receiver_id=$1 ORDER BY h.created_at DESC, h.id DESC LIMIT 100`, charID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var history []GemHistory
	for rows.Next() {
		var entry GemHistory
		if err := rows.Scan(&entry.Gem, &entry.Message, &entry.Timestamp, &entry.Sender); err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return history, rows.Err()
}

// TransferGem gives one duplicate ancient treasure to a member of the same
// guild. Sender/recipient inventory and history commit together. Client CID
// is checked against membership and never used as the source character.
func (r *TowerRepository) TransferGem(senderID, receiverID uint32, gemID uint16, message uint16) error {
	group, slot := int(gemID>>8), int(gemID&0xff)
	if senderID == 0 || receiverID == 0 || senderID == receiverID || group >= 6 || slot < 1 || slot > 5 {
		return errors.New("invalid ancient treasure gift")
	}
	index := group*5 + slot - 1
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var sameGuild bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM guild_characters a JOIN guild_characters b ON a.guild_id=b.guild_id WHERE a.character_id=$1 AND b.character_id=$2 AND a.guild_id IS NOT NULL)`, senderID, receiverID).Scan(&sameGuild); err != nil {
		return err
	}
	if !sameGuild {
		return errors.New("ancient treasure recipient is not in the sender's guild")
	}
	// Advisory locks cover the no-row case in the legacy tower table. Acquire
	// both in numeric order to avoid a sender/receiver deadlock.
	first, second := senderID, receiverID
	if first > second {
		first, second = second, first
	}
	for _, charID := range []uint32{first, second} {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1::bigint)`, int64(7000000000)+int64(charID)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO tower (char_id) SELECT $1 WHERE NOT EXISTS (SELECT 1 FROM tower WHERE char_id=$1)`, charID); err != nil {
			return err
		}
	}
	readGems := func(charID uint32) ([]int, error) {
		var csv string
		if err := tx.QueryRow(`SELECT COALESCE(gems, $1) FROM tower WHERE char_id=$2 LIMIT 1 FOR UPDATE`, EmptyTowerCSV(30), charID).Scan(&csv); err != nil {
			return nil, err
		}
		values := stringsupport.CSVElems(csv)
		if len(values) != 30 {
			return nil, errors.New("invalid ancient treasure inventory")
		}
		return values, nil
	}
	senderGems, err := readGems(senderID)
	if err != nil {
		return err
	}
	receiverGems, err := readGems(receiverID)
	if err != nil {
		return err
	}
	if senderGems[index] < 2 || receiverGems[index] != 0 {
		return errors.New("ancient treasure gift requires a duplicate and an unowned recipient slot")
	}
	senderGems[index]--
	receiverGems[index]++
	encode := func(values []int) string {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = fmt.Sprint(value)
		}
		return strings.Join(parts, ",")
	}
	if _, err := tx.Exec(`UPDATE tower SET gems=$1 WHERE char_id=$2`, encode(senderGems), senderID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tower SET gems=$1 WHERE char_id=$2`, encode(receiverGems), receiverID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO tower_gem_history (receiver_id, sender_id, gem_id, message, created_at) VALUES ($1,$2,$3,$4,$5)`,
		receiverID, senderID, gemID, message, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// TenrouiraiProgressData holds the guild's tenrouirai (sky corridor) progress.
type TenrouiraiProgressData struct {
	Page     uint8
	Mission1 uint16
	Mission2 uint16
	Mission3 uint16
}

// TowerMissionStats contains the six counters from one completed tower run.
// The client reports these, so they are bounded and accepted at most once
// following a successful progress update on the same authenticated session.
type TowerMissionStats struct {
	Floors, Antiques, Chests, Cats, TRP, Slays uint16
}

func (s TowerMissionStats) Valid() bool {
	return s.Floors <= 4 && s.Antiques <= 100 && s.Chests <= 100 &&
		s.Cats <= 100 && s.TRP <= 50000 && s.Slays <= 100
}

func (s TowerMissionStats) missionValue(kind uint8) int {
	switch kind {
	case 1:
		return int(s.Floors)
	case 2:
		return int(s.Antiques)
	case 3:
		return int(s.Chests)
	case 4:
		return int(s.Cats)
	case 5:
		return int(s.TRP)
	case 6:
		return int(s.Slays)
	default:
		return 0
	}
}

// SubmitTenrouiraiProgress credits only the authenticated guild member's
// current three objectives, capped to the page goals in a single transaction.
func (r *TowerRepository) SubmitTenrouiraiProgress(guildID, charID uint32, stats TowerMissionStats) error {
	if !stats.Valid() {
		return errors.New("invalid tower investigation counters")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var page int
	if err := tx.QueryRow(`SELECT COALESCE(tower_mission_page, 1) FROM guilds WHERE id=$1 FOR UPDATE`, guildID).Scan(&page); err != nil {
		return err
	}
	if page < 1 || page > len(tenrouiraiData)/3 {
		return errors.New("tower investigation page out of range")
	}
	var memberID int
	var previous [3]int
	if err := tx.QueryRow(`SELECT id, COALESCE(tower_mission_1, 0), COALESCE(tower_mission_2, 0), COALESCE(tower_mission_3, 0) FROM guild_characters WHERE guild_id=$1 AND character_id=$2 ORDER BY id LIMIT 1 FOR UPDATE`, guildID, charID).
		Scan(&memberID, &previous[0], &previous[1], &previous[2]); err != nil {
		return err
	}
	var next [3]int
	for i := range next {
		mission := tenrouiraiData[(page-1)*3+i]
		next[i] = previous[i] + stats.missionValue(mission.Mission)
		if next[i] > int(mission.Goal) {
			next[i] = int(mission.Goal)
		}
	}
	if _, err := tx.Exec(`UPDATE guild_characters SET tower_mission_1=$1, tower_mission_2=$2, tower_mission_3=$3 WHERE id=$4`,
		next[0], next[1], next[2], memberID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTenrouiraiProgress returns the guild's tower mission page and aggregated mission scores.
func (r *TowerRepository) GetTenrouiraiProgress(guildID uint32) (TenrouiraiProgressData, error) {
	var p TenrouiraiProgressData
	if err := r.db.QueryRow(`SELECT COALESCE(tower_mission_page, 1) FROM guilds WHERE id=$1`, guildID).Scan(&p.Page); err != nil {
		return p, err
	}
	err := r.db.QueryRow(
		`SELECT LEAST(COALESCE(SUM(tower_mission_1), 0), 65535), LEAST(COALESCE(SUM(tower_mission_2), 0), 65535), LEAST(COALESCE(SUM(tower_mission_3), 0), 65535) FROM guild_characters WHERE guild_id=$1`,
		guildID,
	).Scan(&p.Mission1, &p.Mission2, &p.Mission3)
	return p, err
}

// GetTenrouiraiMissionScores returns per-character scores for a specific mission index (1-3).
func (r *TowerRepository) GetTenrouiraiMissionScores(guildID uint32, missionIndex uint8) ([]TenrouiraiCharScore, error) {
	if missionIndex < 1 || missionIndex > 3 {
		missionIndex = (missionIndex % 3) + 1
	}
	rows, err := r.db.Query(
		fmt.Sprintf(
			`SELECT name, tower_mission_%d FROM guild_characters gc INNER JOIN characters c ON gc.character_id = c.id WHERE guild_id=$1 AND tower_mission_%d IS NOT NULL ORDER BY tower_mission_%d DESC`,
			missionIndex, missionIndex, missionIndex,
		),
		guildID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var scores []TenrouiraiCharScore
	for rows.Next() {
		var cs TenrouiraiCharScore
		if err := rows.Scan(&cs.Name, &cs.Score); err != nil {
			return nil, err
		}
		scores = append(scores, cs)
	}
	return scores, rows.Err()
}

// GetGuildTowerRP returns the guild's tower RP.
func (r *TowerRepository) GetGuildTowerRP(guildID uint32) (uint32, error) {
	var rp uint32
	err := r.db.QueryRow(`SELECT tower_rp FROM guilds WHERE id=$1`, guildID).Scan(&rp)
	return rp, err
}

// GetGuildTowerPageAndRP returns the guild's tower mission page and donated RP.
func (r *TowerRepository) GetGuildTowerPageAndRP(guildID uint32) (page int, donated int, err error) {
	err = r.db.QueryRow(`SELECT tower_mission_page, tower_rp FROM guilds WHERE id=$1`, guildID).Scan(&page, &donated)
	return
}

// AdvanceTenrouiraiPage increments the guild's tower mission page and resets member mission progress.
func (r *TowerRepository) AdvanceTenrouiraiPage(guildID uint32) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE guilds SET tower_mission_page=tower_mission_page+1 WHERE id=$1`, guildID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE guild_characters SET tower_mission_1=NULL, tower_mission_2=NULL, tower_mission_3=NULL WHERE guild_id=$1`, guildID); err != nil {
		return err
	}
	return tx.Commit()
}

// DonateGuildTowerRP adds RP to the guild's tower total.
func (r *TowerRepository) DonateGuildTowerRP(guildID uint32, rp uint16) error {
	_, err := r.db.Exec(`UPDATE guilds SET tower_rp=tower_rp+$1 WHERE id=$2`, rp, guildID)
	return err
}
