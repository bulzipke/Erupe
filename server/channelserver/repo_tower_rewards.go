package channelserver

import (
	"database/sql"
	"errors"
	"math"
	"time"
)

const (
	towerRewardFloor   = 1
	towerRewardAdvance = 2
	towerRewardDaily   = 3
)

type TowerDailyCounters struct {
	Floors, Antiques, Chests, Cats, TRP, Slays int32
}

type TowerRewardState struct {
	Floors          int32
	Daily           []TowerDailyDay
	AdvanceEligible bool
	Claimed         map[uint64]bool
}

type TowerDailyDay struct {
	Start    time.Time
	Counters TowerDailyCounters
}

func towerRewardKey(kind, index int32) uint64 {
	return uint64(uint32(kind))<<32 | uint64(uint32(index))
}

// RecordTowerRun is called only after the once-per-completed-quest submission
// guard. The current event's floor and daily counters commit together.
func (r *TowerRepository) RecordTowerRun(earthID int32, charID uint32, block uint8, dayStart time.Time, stats TowerMissionStats) error {
	if charID == 0 || (block != 1 && block != 2) || !stats.Valid() || stats.Floors == 0 {
		return errors.New("invalid tower run receipt")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	b1, b2 := 0, 0
	if block == 1 {
		b1 = int(stats.Floors)
	} else {
		b2 = int(stats.Floors)
	}
	_, err = tx.Exec(`INSERT INTO tower_event_progress (earth_id, character_id, block1_floors, block2_floors)
		VALUES ($1,$2,$3,$4) ON CONFLICT (earth_id, character_id) DO UPDATE
		SET block1_floors=tower_event_progress.block1_floors+EXCLUDED.block1_floors,
		    block2_floors=tower_event_progress.block2_floors+EXCLUDED.block2_floors`,
		earthID, charID, b1, b2)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO tower_daily_progress
		(earth_id, character_id, day_start, floors, antiques, chests, cats, trp, slays)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (earth_id, character_id, day_start) DO UPDATE SET
		floors=tower_daily_progress.floors+EXCLUDED.floors,
		antiques=tower_daily_progress.antiques+EXCLUDED.antiques,
		chests=tower_daily_progress.chests+EXCLUDED.chests,
		cats=tower_daily_progress.cats+EXCLUDED.cats,
		trp=tower_daily_progress.trp+EXCLUDED.trp,
		slays=tower_daily_progress.slays+EXCLUDED.slays`,
		earthID, charID, dayStart.UTC(), stats.Floors, stats.Antiques, stats.Chests,
		stats.Cats, stats.TRP, stats.Slays)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// The client reports chest/antique/monster counters after its Tower progress
// packet. Floors and TRP were already recorded by RecordTowerRun.
func (r *TowerRepository) RecordTowerDailyExtras(earthID int32, charID uint32, dayStart time.Time, stats TowerMissionStats) error {
	if charID == 0 || !stats.Valid() {
		return errors.New("invalid tower daily counters")
	}
	_, err := r.db.Exec(`INSERT INTO tower_daily_progress
		(earth_id, character_id, day_start, antiques, chests, cats, slays)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (earth_id, character_id, day_start) DO UPDATE SET
		antiques=tower_daily_progress.antiques+EXCLUDED.antiques,
		chests=tower_daily_progress.chests+EXCLUDED.chests,
		cats=tower_daily_progress.cats+EXCLUDED.cats,
		slays=tower_daily_progress.slays+EXCLUDED.slays`,
		earthID, charID, dayStart.UTC(), stats.Antiques, stats.Chests, stats.Cats, stats.Slays)
	return err
}

// The original scout-score increments were not recovered. One validated
// completed floor contributes 100 server-wide points per zone; the client
// applies the existing 1011/1012 monster/material reveal thresholds.
func (r *TowerRepository) GetTowerScoutScores(earthID int32) (uint32, uint32, error) {
	var b1, b2 int64
	err := r.db.QueryRow(`SELECT COALESCE(SUM(block1_floors),0)*100,
		COALESCE(SUM(block2_floors),0)*100 FROM tower_event_progress WHERE earth_id=$1`,
		earthID).Scan(&b1, &b2)
	if err != nil {
		return 0, 0, err
	}
	if b1 > math.MaxUint32 {
		b1 = math.MaxUint32
	}
	if b2 > math.MaxUint32 {
		b2 = math.MaxUint32
	}
	return uint32(b1), uint32(b2), nil
}

func (r *TowerRepository) GetTowerRewardState(earthID int32, charID uint32, _ time.Time) (TowerRewardState, error) {
	state := TowerRewardState{Claimed: make(map[uint64]bool)}
	err := r.db.QueryRow(`SELECT COALESCE(block1_floors+block2_floors,0)
		FROM tower_event_progress WHERE earth_id=$1 AND character_id=$2`,
		earthID, charID).Scan(&state.Floors)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	dailyRows, err := r.db.Query(`SELECT day_start, floors, antiques, chests, cats, trp, slays
		FROM tower_daily_progress WHERE earth_id=$1 AND character_id=$2
		ORDER BY day_start`, earthID, charID)
	if err != nil {
		return state, err
	}
	for dailyRows.Next() {
		var day TowerDailyDay
		if err := dailyRows.Scan(&day.Start, &day.Counters.Floors, &day.Counters.Antiques,
			&day.Counters.Chests, &day.Counters.Cats, &day.Counters.TRP,
			&day.Counters.Slays); err != nil {
			_ = dailyRows.Close()
			return state, err
		}
		state.Daily = append(state.Daily, day)
	}
	if err := dailyRows.Err(); err != nil {
		_ = dailyRows.Close()
		return state, err
	}
	_ = dailyRows.Close()
	err = r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM tower_guardian_participants
		WHERE earth_id=$1 AND character_id=$2 AND before_casual)`,
		earthID, charID).Scan(&state.AdvanceEligible)
	if err != nil {
		return state, err
	}
	rows, err := r.db.Query(`SELECT reward_kind, reward_index FROM tower_reward_claims
		WHERE earth_id=$1 AND character_id=$2`, earthID, charID)
	if err != nil {
		return state, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, index int32
		if err := rows.Scan(&kind, &index); err != nil {
			return state, err
		}
		state.Claimed[towerRewardKey(kind, index)] = true
	}
	return state, rows.Err()
}

// RecordTowerRewardClaim stores the idempotency receipt of one present-box
// reward. The client puts the item into the hunter's own inventory before it
// sends the claim (MSG_MHF_PRESENT_BOX Op=2) and uploads its savedata in the
// same step, so the server must not deposit a second copy anywhere. It
// reports false when the reward already had a receipt.
func (r *TowerRepository) RecordTowerRewardClaim(earthID int32, charID uint32, kind, index int32, itemID uint16, quantity uint16) (bool, error) {
	if charID == 0 || kind < towerRewardFloor || kind > towerRewardDaily || index <= 0 || itemID == 0 || quantity == 0 || quantity > 99 {
		return false, errors.New("invalid tower reward")
	}
	result, err := r.db.Exec(`INSERT INTO tower_reward_claims
		(earth_id, character_id, reward_kind, reward_index, item_id, quantity)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
		earthID, charID, kind, index, itemID, quantity)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}
