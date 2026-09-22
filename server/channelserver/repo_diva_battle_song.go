package channelserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

func (r *DivaRepository) GetDivaBattleSong(charID, eventID uint32) (DivaBattleSongState, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaBattleSongState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.Get(&id, `SELECT id FROM characters WHERE id=$1 FOR SHARE`, charID); err != nil {
		return DivaBattleSongState{}, err
	}
	state, err := loadDivaBattleSong(tx, charID, eventID)
	if err != nil {
		return DivaBattleSongState{}, err
	}
	return state, tx.Commit()
}

func loadDivaBattleSong(tx *sqlx.Tx, charID, eventID uint32) (DivaBattleSongState, error) {
	var state DivaBattleSongState
	err := tx.Get(&state, `SELECT used_count,activation_id,
		COALESCE(EXTRACT(epoch FROM activated_at)::bigint,0) started_at
		FROM diva_battle_songs WHERE char_id=$1 AND event_id=$2`, charID, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return DivaBattleSongState{}, nil
	}
	if err == nil {
		err = tx.Select(&state.Effects, `SELECT effect_id,used_count FROM diva_battle_song_effects
			WHERE char_id=$1 AND event_id=$2 ORDER BY effect_id`, charID, eventID)
	}
	return state, err
}

// Character row locking serializes activations across channels. A repeated
// request during the active hour succeeds without consuming another use.
func (r *DivaRepository) UseDivaBattleSong(charID, eventID uint32) (DivaBattleSongState, error) {
	return r.useDivaBattleSongAt(charID, eventID, time.Time{})
}

// Only tests inject a clock. Production reads the DB clock after obtaining the
// character lock, so waiting cannot activate a song using a stale phase/time.
func (r *DivaRepository) useDivaBattleSongAt(charID, eventID uint32, now time.Time) (DivaBattleSongState, error) {
	if charID == 0 || eventID == 0 {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaBattleSongState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var character uint32
	if err = tx.Get(&character, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID); err != nil {
		return DivaBattleSongState{}, err
	}
	if now.IsZero() {
		if err = tx.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return DivaBattleSongState{}, err
		}
	}
	if now.Unix() <= 0 || now.Unix() > 0xFFFFFFFE {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	var event DivaEvent
	if err = tx.Get(&event, `SELECT id,EXTRACT(epoch FROM start_time)::bigint start_time
		FROM events WHERE id=$1 AND event_type='diva'`, eventID); err != nil {
		return DivaBattleSongState{}, err
	}
	if !divaBattleSongPhase(event, now) {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	// The client earns eligibility from its prayer-phase journal, not the mere
	// existence of an account, guild membership or a debug phase override.
	var participated bool
	err = tx.Get(&participated, `SELECT EXISTS(SELECT 1 FROM diva_song_records
		WHERE char_id=$1 AND event_id=$2 AND quest_points+bonus_points>0
		AND bead_index BETWEEN 1 AND 4 AND submitted_at>=to_timestamp($3)
		AND submitted_at<to_timestamp($3)+interval '601200 seconds')`, charID, eventID, event.StartTime)
	if err != nil {
		return DivaBattleSongState{}, err
	}
	if !participated {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	state, err := loadDivaBattleSong(tx, charID, eventID)
	if err != nil {
		return DivaBattleSongState{}, err
	}
	if divaBattleSongActive(state, now) {
		return state, tx.Commit()
	}
	var total int64
	err = tx.Get(&total, `SELECT COALESCE(SUM(quest_points+bonus_points),0)
		FROM diva_points WHERE event_id=$1`, eventID)
	if err != nil {
		return DivaBattleSongState{}, err
	}
	if state.Used >= divaBattleSongEarned(total) {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	var effects []uint16
	if err = tx.Select(&effects, `SELECT type FROM diva_beads ORDER BY id LIMIT 4`); err != nil {
		return DivaBattleSongState{}, err
	}
	if len(effects) == 0 {
		for _, id := range defaultBeadTypes {
			effects = append(effects, uint16(id))
		}
	}
	effects, valid := divaBattleSongEffectIDs(effects)
	if !valid {
		return DivaBattleSongState{}, errDivaBattleSongUnavailable
	}
	err = tx.Get(&state, `INSERT INTO diva_battle_songs(char_id,event_id,used_count,activation_id,activated_at)
		VALUES($1,$2,1,nextval('diva_battle_song_activation_ids'),to_timestamp($3))
		ON CONFLICT(char_id,event_id) DO UPDATE SET used_count=diva_battle_songs.used_count+1,
		activation_id=nextval('diva_battle_song_activation_ids'),activated_at=EXCLUDED.activated_at
		RETURNING used_count,activation_id,EXTRACT(epoch FROM activated_at)::bigint started_at`, charID, eventID, now.Unix())
	if err != nil {
		return DivaBattleSongState{}, err
	}
	if _, err = tx.Exec(`DELETE FROM diva_battle_song_effects WHERE char_id=$1 AND event_id=$2`, charID, eventID); err != nil {
		return DivaBattleSongState{}, err
	}
	state.Effects = nil
	for _, id := range effects {
		if _, err = tx.Exec(`INSERT INTO diva_battle_song_effects(char_id,event_id,effect_id,used_count)
			VALUES($1,$2,$3,0)`, charID, eventID, id); err != nil {
			return DivaBattleSongState{}, err
		}
		state.Effects = append(state.Effects, DivaBattleSongEffect{ID: id})
	}
	return state, tx.Commit()
}

// ADD reports limited-effect consumption after a quest. The run key is server
// generated at a validated departure, never supplied by the reporting client.
func (r *DivaRepository) ConsumeDivaBattleSongEffects(charID uint32, run divaBattleSongRun, effectIDs []uint16, now time.Time) error {
	ids, valid := divaBattleSongEffectIDs(effectIDs)
	if !valid || charID == 0 || run.EventID == 0 || run.ActivationID == 0 || run.QuestID == 0 ||
		!validDivaInterceptionRunKey(run.Key) || run.StartedAt.IsZero() || now.Before(run.StartedAt) {
		return errDivaBattleSongUnavailable
	}
	payload, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var char uint32
	if err = tx.Get(&char, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID); err != nil {
		return err
	}
	var previous struct {
		Activation uint32 `db:"activation_id"`
		Effects    string `db:"effects"`
	}
	err = tx.Get(&previous, `SELECT activation_id,effects FROM diva_battle_song_receipts
		WHERE char_id=$1 AND event_id=$2 AND run_key=$3`, charID, run.EventID, run.Key)
	if err == nil {
		if previous.Activation != run.ActivationID || previous.Effects != string(payload) {
			return errDivaBattleSongUnavailable
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	state, err := loadDivaBattleSong(tx, charID, run.EventID)
	if err != nil {
		return err
	}
	if state.ActivationID != run.ActivationID || !divaBattleSongActive(state, run.StartedAt) {
		return errDivaBattleSongUnavailable
	}
	var event DivaEvent
	if err = tx.Get(&event, `SELECT id,EXTRACT(epoch FROM start_time)::bigint start_time FROM events
		WHERE id=$1 AND event_type='diva'`, run.EventID); err != nil {
		return err
	}
	if !divaBattleSongPhase(event, run.StartedAt) {
		return errDivaBattleSongUnavailable
	}
	for _, id := range ids {
		found := false
		for _, effect := range state.Effects {
			if effect.ID == id {
				found = true
				break
			}
		}
		if !found {
			return errDivaBattleSongUnavailable
		}
		if _, err = tx.Exec(`UPDATE diva_battle_song_effects SET used_count=LEAST(used_count+1,255)
			WHERE char_id=$1 AND event_id=$2 AND effect_id=$3`, charID, run.EventID, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO diva_battle_song_receipts(char_id,event_id,run_key,activation_id,effects,quest_id,started_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, charID, run.EventID, run.Key, run.ActivationID, string(payload), run.QuestID, run.StartedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}
