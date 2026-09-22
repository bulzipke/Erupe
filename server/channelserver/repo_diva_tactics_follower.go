package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type divaTacticsFollowerContract struct {
	DivaTacticsFollowerState
	EventID uint32    `db:"event_id"`
	GuildID uint32    `db:"guild_id"`
	HiredAt time.Time `db:"hired_at"`
}

func (r *DivaRepository) GetDivaTacticsFollower(charID, eventID uint32, override int) (DivaTacticsFollowerState, error) {
	return r.getDivaTacticsFollowerAt(charID, eventID, override, time.Time{})
}

func (r *DivaRepository) getDivaTacticsFollowerAt(charID, eventID uint32, override int, now time.Time) (DivaTacticsFollowerState, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	guildID, now, err := lockDivaTacticsFollowerContext(tx, charID, eventID, override, now, false)
	if errors.Is(err, errDivaTacticsFollowerUnavailable) {
		return DivaTacticsFollowerState{}, tx.Commit()
	}
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	contract, err := loadDivaTacticsFollower(tx, charID)
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	state := DivaTacticsFollowerState{}
	// AvailableAt is the earliest CHANGE time, not an expiry. Native NPC
	// construction only checks that this value is nonzero (FUN_101ca120).
	if contract.EventID == eventID && contract.GuildID == guildID && !now.Before(contract.HiredAt) && contract.AvailableAt != 0 {
		state = contract.DivaTacticsFollowerState
	}
	return state, tx.Commit()
}

func (r *DivaRepository) SetDivaTacticsFollower(charID, eventID uint32, choice DivaTacticsFollowerChoice, override int) (DivaTacticsFollowerState, error) {
	return r.setDivaTacticsFollowerAt(charID, eventID, choice, override, time.Time{})
}

func (r *DivaRepository) setDivaTacticsFollowerAt(charID, eventID uint32, choice DivaTacticsFollowerChoice, override int, now time.Time) (DivaTacticsFollowerState, error) {
	cost, valid := divaTacticsFollowerCost(choice)
	if !valid {
		return DivaTacticsFollowerState{}, errDivaTacticsFollowerUnavailable
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	guildID, now, err := lockDivaTacticsFollowerContext(tx, charID, eventID, override, now, true)
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	contract, err := loadDivaTacticsFollower(tx, charID)
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	// Character-scoped locking survives guild/event changes. Even an identical
	// request fails: another success ACK would deduct client-side GP once more.
	if now.Unix() < int64(contract.AvailableAt) {
		return DivaTacticsFollowerState{}, errDivaTacticsFollowerLocked
	}
	available := now.Unix() + int64(divaTacticsFollowerChangeCooldown/time.Second)
	if available > 0xFFFFFFFE {
		return DivaTacticsFollowerState{}, errDivaTacticsFollowerUnavailable
	}
	result, err := tx.Exec(`UPDATE characters SET gcp=gcp-$2 WHERE id=$1 AND gcp >= $2`, charID, cost)
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	if changed != 1 {
		return DivaTacticsFollowerState{}, errDivaTacticsFollowerFunds
	}
	_, err = tx.Exec(`INSERT INTO diva_tactics_followers
		(char_id,event_id,guild_id,name_index,voice,weapon,strength,cost,hired_at,available_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,to_timestamp($9),to_timestamp($10))
		ON CONFLICT(char_id) DO UPDATE SET event_id=EXCLUDED.event_id,guild_id=EXCLUDED.guild_id,
		name_index=EXCLUDED.name_index,voice=EXCLUDED.voice,weapon=EXCLUDED.weapon,
		strength=EXCLUDED.strength,cost=EXCLUDED.cost,hired_at=EXCLUDED.hired_at,available_at=EXCLUDED.available_at`,
		charID, eventID, guildID, choice.NameIndex, choice.Voice, choice.Weapon, choice.Strength, cost, now.Unix(), available)
	if err != nil {
		return DivaTacticsFollowerState{}, err
	}
	return DivaTacticsFollowerState{DivaTacticsFollowerChoice: choice, AvailableAt: uint32(available)}, tx.Commit()
}

// All transaction paths use character -> lifecycle -> membership ordering.
// Production samples time after waiting, independently of the session clock.
func lockDivaTacticsFollowerContext(tx *sqlx.Tx, charID, eventID uint32, override int, now time.Time, write bool) (uint32, time.Time, error) {
	if charID == 0 || eventID == 0 || (override != -1 && override != 2) {
		return 0, now, errDivaTacticsFollowerUnavailable
	}
	query := `SELECT COALESCE(gr,0) FROM characters WHERE id=$1 FOR SHARE`
	if write {
		query = `SELECT COALESCE(gr,0) FROM characters WHERE id=$1 FOR UPDATE`
	}
	var gr uint16
	if err := tx.QueryRow(query, charID).Scan(&gr); err != nil {
		return 0, now, err
	}
	if gr == 0 {
		return 0, now, errDivaTacticsFollowerUnavailable
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(1146507841)`); err != nil {
		return 0, now, err
	}
	injected := !now.IsZero()
	if !injected {
		if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return 0, now, err
		}
	}
	event, err := ensureDivaEventTx(tx, now, override)
	if err != nil {
		return 0, now, err
	}
	if event.ID != eventID || !divaBattleSongPhase(event, now) {
		return 0, now, errDivaTacticsFollowerUnavailable
	}
	var guildID uint32
	err = tx.QueryRow(`SELECT guild_id FROM guild_characters WHERE character_id=$1 AND guild_id>0 FOR SHARE`, charID).Scan(&guildID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, now, errDivaTacticsFollowerUnavailable
	}
	if err != nil {
		return 0, now, err
	}
	if !injected {
		if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return 0, now, err
		}
	}
	if now.Unix() <= 0 || now.Unix() > 0xFFFFFFFE || !divaBattleSongPhase(event, now) {
		return 0, now, errDivaTacticsFollowerUnavailable
	}
	return guildID, now, nil
}

func loadDivaTacticsFollower(tx *sqlx.Tx, charID uint32) (divaTacticsFollowerContract, error) {
	var state divaTacticsFollowerContract
	err := tx.Get(&state, `SELECT event_id,guild_id,name_index,voice,weapon,strength,hired_at,
		EXTRACT(epoch FROM available_at)::bigint AS available_at FROM diva_tactics_followers WHERE char_id=$1`, charID)
	if errors.Is(err, sql.ErrNoRows) {
		return divaTacticsFollowerContract{}, nil
	}
	return state, err
}
