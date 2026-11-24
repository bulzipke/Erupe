package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

func divaMelodyUsesSharedHR(q sqlx.Queryer, event DivaEvent) (bool, error) {
	if event.ID == 0 {
		return true, nil
	}
	var sharedHR bool
	err := sqlx.Get(q, &sharedHR, `SELECT EXISTS(SELECT 1 FROM diva_hr_melody_cutover
		WHERE singleton AND installed_at < $1)`, event.phaseTime(0))
	return sharedHR, err
}

func (r *DivaRepository) GetDivaMelodyPrizes(now time.Time, override int) ([]DivaPrize, error) {
	var scheduled DivaEvent
	if override == -1 || override == 2 {
		var err error
		scheduled, err = r.EnsureDivaEvent(now, override)
		if err != nil {
			return nil, err
		}
	}
	event, err := r.GetDivaInterceptionRewardEvent(now)
	if err != nil {
		return nil, err
	}
	// While an actual interception owns the reward page, show its rows.
	// Before the first one, preview the scheduled round when available.
	if event.ID == 0 {
		event = scheduled
	}
	sharedHR, err := divaMelodyUsesSharedHR(r.db, event)
	if err != nil {
		return nil, err
	}
	return divaMelodyDisplay(sharedHR), nil
}

// Caller holds the character and lifecycle locks. Offering a cap is not a
// credit: only an explicit type-29 receipt claim changes the shared wallet.
func offerDivaMelodyTx(tx *sqlx.Tx, charID uint32, event DivaEvent, now time.Time) error {
	clock, err := divaMapClock(tx, now)
	if err != nil || !divaMelodyUnexpired(event, clock) {
		return err
	}
	var gr uint16
	var points int64
	if err = tx.QueryRow(`SELECT COALESCE(gr,0),COALESCE((SELECT SUM(points) FROM diva_interception_runs
		WHERE char_id=$1 AND event_id=$2),0) FROM characters WHERE id=$1`, charID, event.ID).Scan(&gr, &points); err != nil {
		return err
	}
	sharedHR, err := divaMelodyUsesSharedHR(tx, event)
	if err != nil {
		return err
	}
	for _, r := range divaMelodyRewards(sharedHR) {
		if r.GR != (gr > 0) || points < int64(r.Threshold) {
			continue
		}
		if _, err = tx.Exec(`INSERT INTO diva_reward_receipts(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
			VALUES($1,$2,6,$3,29,0,$4) ON CONFLICT(char_id,event_id,reward_type,catalog_key) DO NOTHING`, charID, event.ID, r.Key, r.Quantity); err != nil {
			return err
		}
	}
	return nil
}

func validDivaMelodyReceipt(r DivaRewardOffer) bool {
	if r.RewardType != 6 || r.ItemType != divaMelodyItemType || r.ItemID != 0 {
		return false
	}
	// The legacy catalog includes every receipt key and quantity ever issued.
	for _, rule := range divaMelodyRewards(false) {
		if rule.Key == r.CatalogKey && rule.Quantity == r.Quantity {
			return true
		}
	}
	return false
}

// Unlike materials/GP, type 29 has no client inventory mutation or savedata
// field. Credit its immutable cap and consume the receipt in this transaction.
func claimDivaMelodyTx(tx *sqlx.Tx, charID uint32, r DivaRewardOffer, now time.Time) error {
	if !validDivaMelodyReceipt(r) {
		return errInvalidDivaReward
	}
	var event DivaEvent
	var owner uint32
	if err := tx.QueryRow(`SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint,e.diva_continuous,c.user_id
		FROM events e CROSS JOIN characters c WHERE e.id=$1 AND e.event_type='diva' AND c.id=$2`, r.EventID, charID).
		Scan(&event.ID, &event.StartTime, &event.Continuous, &owner); err != nil {
		return err
	}
	clock, err := divaMapClock(tx, now)
	if err != nil {
		return err
	}
	if owner == 0 || !divaMelodyUnexpired(event, clock) {
		return errDivaMelodyUnavailable
	}
	_, err = tx.Exec(`INSERT INTO diva_melody_wallets(user_id,event_id,earned) VALUES($1,$2,$3)
		ON CONFLICT(user_id,event_id) DO UPDATE SET earned=GREATEST(diva_melody_wallets.earned,EXCLUDED.earned),updated_at=clock_timestamp()`, owner, r.EventID, r.Quantity)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE diva_reward_receipts SET claimed_at=COALESCE(claimed_at,clock_timestamp())
		WHERE id=$1 AND char_id=$2 AND event_id=$3 AND item_type=29`, r.ID, charID, r.EventID)
	return err
}

// Character -> lifecycle -> membership/map -> account-wallet is the only
// order used here. No user-row lock or other character lock is taken later.
func lockDivaMelodyContext(tx *sqlx.Tx, userID, charID uint32, override int, now time.Time, shop bool) (DivaEvent, time.Time, error) {
	if userID == 0 || charID == 0 || override < -1 || override == 0 || override > 3 {
		return DivaEvent{}, now, errDivaMelodyUnavailable
	}
	var owner uint32
	if err := tx.QueryRow(`SELECT user_id FROM characters WHERE id=$1 FOR SHARE`, charID).Scan(&owner); err != nil {
		return DivaEvent{}, now, err
	}
	if owner != userID {
		return DivaEvent{}, now, errDivaMelodyUnavailable
	}
	event, err := lockDivaInterceptionRewardWindow(tx, now, override)
	if err != nil {
		return DivaEvent{}, now, err
	}
	clock, err := divaMapClock(tx, now)
	if err != nil {
		return DivaEvent{}, clock, err
	}
	if !divaMelodyUnexpired(event, clock) {
		return DivaEvent{}, clock, errDivaMelodyUnavailable
	}
	if shop {
		if !divaSpecialHallPeriod(event, clock) {
			return DivaEvent{}, clock, errDivaMelodyUnavailable
		}
		state, err := lockDivaGuildRewardState(tx, charID, event.ID, clock)
		if err != nil {
			return DivaEvent{}, clock, err
		}
		if state.GuildID == 0 || state.Areas < 1 {
			return DivaEvent{}, clock, errDivaMelodyUnavailable
		}
	}
	return event, clock, nil
}

func (r *DivaRepository) GetDivaMelody(userID, charID uint32, override int) (uint8, error) {
	return r.getDivaMelodyAt(userID, charID, override, time.Time{})
}

func (r *DivaRepository) getDivaMelodyAt(userID, charID uint32, override int, now time.Time) (uint8, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	event, _, err := lockDivaMelodyContext(tx, userID, charID, override, now, false)
	if errors.Is(err, errDivaMelodyUnavailable) {
		return 0, tx.Commit()
	}
	if err != nil {
		return 0, err
	}
	var balance uint8
	err = tx.QueryRow(`SELECT earned-spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2`, userID, event.ID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return 0, err
	}
	return balance, tx.Commit()
}

func (r *DivaRepository) CanUseDivaMelodyShop(userID, charID uint32, override int) (bool, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	_, _, err = lockDivaMelodyContext(tx, userID, charID, override, time.Time{}, true)
	if errors.Is(err, errDivaMelodyUnavailable) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *DivaRepository) UseDivaMelody(userID, charID uint32, cost uint8, override int) (uint8, error) {
	return r.useDivaMelodyAt(userID, charID, cost, override, time.Time{})
}

func (r *DivaRepository) useDivaMelodyAt(userID, charID uint32, cost uint8, override int, now time.Time) (uint8, error) {
	if !divaMelodyAllowedCost(cost) {
		return 0, errDivaMelodyUnavailable
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	event, _, err := lockDivaMelodyContext(tx, userID, charID, override, now, true)
	if err != nil {
		return 0, err
	}
	var balance uint8
	// The wallet row is shared by every character/channel. Lock first, then
	// resample production time: waiting must not authorize an expired purchase.
	err = tx.QueryRow(`SELECT earned-spent FROM diva_melody_wallets WHERE user_id=$1 AND event_id=$2 FOR UPDATE`, userID, event.ID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errDivaMelodyUnavailable
	}
	if err != nil {
		return 0, err
	}
	clock, err := divaMapClock(tx, now)
	if err != nil {
		return 0, err
	}
	if !divaSpecialHallPeriod(event, clock) || balance < cost {
		return 0, errDivaMelodyUnavailable
	}
	if _, err = tx.Exec(`UPDATE diva_melody_wallets SET spent=spent+$3,updated_at=clock_timestamp()
		WHERE user_id=$1 AND event_id=$2`, userID, event.ID, cost); err != nil {
		return 0, err
	}
	return balance - cost, tx.Commit()
}
