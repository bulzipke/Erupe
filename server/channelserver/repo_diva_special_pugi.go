package channelserver

import (
	"database/sql"
	"errors"
	"time"
)

func (r *DivaRepository) ChangeDivaSpecialPugi(charID, guildID uint32, slot uint8, outfit uint32, override int) error {
	return r.changeDivaSpecialPugiAt(charID, guildID, slot, outfit, override, time.Time{})
}

func (r *DivaRepository) changeDivaSpecialPugiAt(charID, guildID uint32, slot uint8, outfit uint32, override int, now time.Time) error {
	if charID == 0 || guildID == 0 || !validDivaSpecialPugiClothing(slot, outfit) || (override != -1 && override != 3) {
		return errDivaSpecialPugiUnavailable
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.QueryRow(`SELECT id FROM characters WHERE id=$1 FOR SHARE`, charID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errDivaSpecialPugiUnavailable
		}
		return err
	}
	event, err := lockDivaMapRewardWindow(tx, now, override)
	if err != nil {
		return err
	}
	clock, err := divaMapClock(tx, now)
	if err != nil {
		return err
	}
	if !divaSpecialHallPeriod(event, clock) {
		return errDivaSpecialPugiUnavailable
	}
	// Match guild disband/mission order: guild before membership. The native
	// clothing menu requires INFO_GUILD role 1 (106f7110), i.e. leader only.
	var leader uint32
	if err = tx.QueryRow(`SELECT leader_id FROM guilds WHERE id=$1 FOR UPDATE`, guildID).Scan(&leader); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errDivaSpecialPugiUnavailable
		}
		return err
	}
	if leader != charID {
		return errDivaSpecialPugiUnavailable
	}
	state, err := lockDivaGuildRewardState(tx, charID, event.ID, clock)
	if err != nil {
		return err
	}
	// Re-sample after all waiting locks. The stored clothes survive expiry,
	// but a request queued before the closing boundary must not change them.
	clock, err = divaMapClock(tx, now)
	if err != nil {
		return err
	}
	if state.GuildID != guildID || state.Areas < 1 || !divaSpecialHallPeriod(event, clock) {
		return errDivaSpecialPugiUnavailable
	}
	queries := [...]string{
		`UPDATE guilds SET diva_pugi_outfit_1=$2 WHERE id=$1`,
		`UPDATE guilds SET diva_pugi_outfit_2=$2 WHERE id=$1`,
		`UPDATE guilds SET diva_pugi_outfit_3=$2 WHERE id=$1`,
	}
	if _, err = tx.Exec(queries[slot-1], guildID, outfit); err != nil {
		return err
	}
	return tx.Commit()
}
