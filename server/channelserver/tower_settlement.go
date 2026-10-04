package channelserver

import (
	"errors"
	"time"
)

// TowerSettlement contains delta values, except TR which is the resulting rank.
// Its equality is also the wire retry contract: a changed payload is not a retry.
type TowerSettlement struct {
	Block                uint8
	TR, TRP, TSP, Floors int32
}

func (v TowerSettlement) valid() bool {
	return v.Block >= 1 && v.Block <= 4 && v.TR >= 0 && v.TR <= 999 &&
		v.TRP >= 0 && v.TRP <= 50000 && v.TSP >= 0 && v.TSP <= 999 && v.Floors >= 0 && v.Floors <= 6
}

// SettleTowerRun commits rank, points, floors, event/daily counters and a retry
// receipt in one transaction. A lost commit response can safely use the same key.
func (r *TowerRepository) SettleTowerRun(charID uint32, runID string, earthID int32, dayStart time.Time, v TowerSettlement) (int32, error) {
	if charID == 0 || len(runID) != 32 || !v.valid() {
		return 0, errors.New("invalid tower settlement")
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Even a late old-round result must serialize with rollover. Permanent rank
	// is credited, but its floors go to the archived round, never the new one.
	historical := false
	if r.roundID != nil {
		err = r.checkTowerRound(tx)
		if errors.Is(err, errTowerRoundChanged) {
			historical = true
		} else if err != nil {
			return 0, err
		}
		if earthID != *r.roundID {
			return 0, errors.New("tower settlement round mismatch")
		}
	}
	result, err := tx.Exec(`INSERT INTO tower_settlements (character_id, run_id, earth_id, block, tr, trp, tsp, floors)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (character_id, run_id) DO NOTHING`,
		charID, runID, earthID, v.Block, v.TR, v.TRP, v.TSP, v.Floors)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		var old TowerSettlement
		var gain int32
		var oldEarthID int32
		err = tx.QueryRow(`SELECT block,tr,trp,tsp,floors,credited_floors,earth_id FROM tower_settlements WHERE character_id=$1 AND run_id=$2`, charID, runID).
			Scan(&old.Block, &old.TR, &old.TRP, &old.TSP, &old.Floors, &gain, &oldEarthID)
		if err != nil {
			return 0, err
		}
		if old != v || oldEarthID != earthID {
			return 0, errors.New("tower settlement retry differs")
		}
		return gain, tx.Commit()
	}
	var b1, b2 int32
	// Serialize against other writers before computing the clipped floor delta.
	if err = tx.QueryRow(`SELECT COALESCE(block1,0),COALESCE(block2,0) FROM tower WHERE char_id=$1 FOR UPDATE`, charID).Scan(&b1, &b2); err != nil {
		return 0, err
	}
	currentB1, currentB2 := b1, b2
	if historical {
		if err = tx.QueryRow(`SELECT block1,block2 FROM tower_round_character_state WHERE earth_id=$1 AND character_id=$2 FOR UPDATE`, earthID, charID).Scan(&b1, &b2); err != nil {
			return 0, err
		}
	}
	gain := int32(0)
	if v.Block == 1 {
		gain = towerFloorGain(b1, v.Floors)
		b1 += gain
	}
	if v.Block == 2 {
		gain = towerFloorGain(b2, v.Floors)
		b2 += gain
	}
	if historical {
		if _, err = tx.Exec(`UPDATE tower_round_character_state SET block1=$3,block2=$4 WHERE earth_id=$1 AND character_id=$2`, earthID, charID, b1, b2); err != nil {
			return 0, err
		}
		b1, b2 = currentB1, currentB2
	}
	result, err = tx.Exec(`UPDATE tower SET tr=GREATEST(COALESCE(tr,0),$2),
 trp=LEAST(COALESCE(trp,0)::bigint+$3,99999999),tsp=LEAST(COALESCE(tsp,0)::bigint+$4,99999999),
 block1=$5,block2=$6 WHERE char_id=$1`, charID, v.TR, v.TRP, v.TSP, b1, b2)
	if err != nil {
		return 0, err
	}
	if n, err = result.RowsAffected(); err != nil || n != 1 {
		return 0, errors.New("expected one tower progress row")
	}
	if gain > 0 || v.TRP > 0 {
		if err = recordTowerRunTx(tx, earthID, charID, v.Block, dayStart, TowerMissionStats{Floors: uint16(gain), TRP: uint16(v.TRP)}); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(`UPDATE tower_settlements SET credited_floors=$3 WHERE character_id=$1 AND run_id=$2`, charID, runID, gain); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return gain, nil
}
