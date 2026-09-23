package channelserver

import (
	"database/sql"
	"errors"
	"time"
)

var errDivaSpecialAdventureUnavailable = errors.New("diva special guild adventure unavailable")

// DivaSpecialAdventureRepository authorizes the one-hour special-hall perk
// and inserts it atomically. The authenticated character, not a packet guild
// or character ID, determines which guild receives the expedition.
type DivaSpecialAdventureRepository interface {
	RegisterDivaSpecialAdventure(charID, destination, charge uint32, override int) error
}

func (r *DivaRepository) RegisterDivaSpecialAdventure(charID, destination, charge uint32, override int) error {
	return r.registerDivaSpecialAdventureAt(charID, destination, charge, override, time.Time{})
}

func (r *DivaRepository) registerDivaSpecialAdventureAt(charID, destination, charge uint32, override int, now time.Time) error {
	if charID == 0 || (override != -1 && override != 3) {
		return errDivaSpecialAdventureUnavailable
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.QueryRow(`SELECT id FROM characters WHERE id=$1 FOR SHARE`, charID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errDivaSpecialAdventureUnavailable
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
		return errDivaSpecialAdventureUnavailable
	}
	// Resolve first, then lock guild before membership, matching disband and
	// guild missions. Rechecking under the membership lock rejects a transfer
	// that raced with this initial lookup; never insert into the old guild.
	var guildID uint32
	if err = tx.QueryRow(`SELECT guild_id FROM guild_characters
		WHERE character_id=$1 AND guild_id>0 AND joined_at IS NOT NULL`, charID).Scan(&guildID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errDivaSpecialAdventureUnavailable
		}
		return err
	}
	if err = tx.QueryRow(`SELECT id FROM guilds WHERE id=$1 FOR UPDATE`, guildID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errDivaSpecialAdventureUnavailable
		}
		return err
	}
	state, err := lockDivaGuildRewardState(tx, charID, event.ID, clock)
	if err != nil {
		return err
	}
	// Production always samples the DB clock after all waiting locks. A
	// request queued before closing cannot spend an expired hall entitlement.
	clock, err = divaMapClock(tx, now)
	if err != nil {
		return err
	}
	if state.GuildID != guildID || state.Areas < 1 || !divaSpecialHallPeriod(event, clock) {
		return errDivaSpecialAdventureUnavailable
	}
	// Retain the existing native destination/charge semantics. Do not invent
	// a destination whitelist, a charge multiplier or a new participation fee.
	if _, err = tx.Exec(`INSERT INTO guild_adventures(guild_id,destination,charge,depart,return)
		VALUES($1,$2,$3,$4,$5)`, guildID, destination, charge, clock.Unix(), clock.Add(time.Hour).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
