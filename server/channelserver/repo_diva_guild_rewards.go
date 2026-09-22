package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Guild rewards use map-area entitlements, not personal interception points.
// Callers cannot supply eligible rows or a guild ID: both are resolved under
// database locks, using the real departure/contribution journal.
type DivaGuildRewardRepository interface {
	OfferDivaGuildRewards(charID uint32, override int) ([]DivaRewardOffer, error)
	PrepareDivaGuildRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error)
}

type DivaTreasureRewardRepository interface {
	OfferDivaTreasureRewards(charID uint32, override int) ([]DivaRewardOffer, error)
	PrepareDivaTreasureRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error)
}

type DivaSpecialHallRepository interface {
	GetDivaSpecialHall(charID, guildID uint32, now time.Time) (bool, error)
}

// These are the twelve approved rows installed by migration 0045: six direct
// round-40 rows and six inferred from matching round-35/38 screens (85% accepted
// confidence, not a measured probability). Preserve that evidence distinction;
// a replacement map must not invent new prizes or repeat schedules.
func divaApprovedGuildRewards() []DivaRewardCatalogEntry {
	values := [][4]uint16{
		{2, 7, 1026, 5}, {3, 7, 1026, 20}, {5, 7, 7456, 3},
		{6, 7, 1026, 20}, {8, 7, 7457, 3}, {10, 7, 1026, 20},
		{20, 7, 7458, 3}, {22, 7, 1026, 40}, {22, 7, 13692, 7},
		{22, 7, 13693, 7}, {24, 7, 7463, 3}, {26, 26, 0, 3000},
	}
	rows := make([]DivaRewardCatalogEntry, 0, len(values))
	for _, v := range values {
		basis := "round40-direct"
		if v[0] >= 20 {
			basis = "round35-38-inferred-85"
		}
		rows = append(rows, DivaRewardCatalogEntry{
			Key:        fmt.Sprintf("tactics-guild-%d-%d-%d", v[0], v[1], v[2]),
			RewardType: 7, Threshold: uint32(v[0]), GR: true,
			ItemType: uint8(v[1]), ItemID: v[2], Quantity: v[3], Basis: basis,
		})
	}
	return rows
}

func eligibleDivaGuildRewards(prizes []DivaPrize, gr uint16, areas uint32) ([]DivaRewardCatalogEntry, error) {
	approved := divaApprovedGuildRewards()
	seen := make(map[string]bool, len(prizes))
	var result []DivaRewardCatalogEntry
	for _, prize := range prizes {
		var match *DivaRewardCatalogEntry
		for i := range approved {
			r := &approved[i]
			if prize.Type == "guild" && !prize.Repeatable && prize.GR &&
				prize.PointsReq == int(r.Threshold) && prize.ItemType == int(r.ItemType) &&
				prize.ItemID == int(r.ItemID) && prize.Quantity == int(r.Quantity) {
				match = r
				break
			}
		}
		if match == nil || seen[match.Key] {
			return nil, fmt.Errorf("unapproved or duplicate Diva guild prize %d", prize.ID)
		}
		seen[match.Key] = true
		if gr > 0 && areas >= match.Threshold {
			result = append(result, *match)
		}
	}
	return result, nil
}

type divaGuildRewardState struct {
	GuildID      uint32
	Areas        uint32
	Participated bool
}

// Caller has already locked the character and lifecycle. Membership is held
// through the grant decision, so concurrent transfer cannot invalidate it.
func lockDivaGuildRewardState(tx *sqlx.Tx, charID, eventID uint32, now time.Time) (divaGuildRewardState, error) {
	var state divaGuildRewardState
	err := tx.QueryRow(`SELECT guild_id FROM guild_characters
		WHERE character_id=$1 AND guild_id>0 AND joined_at IS NOT NULL FOR SHARE`, charID).Scan(&state.GuildID)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	enabled, err := settleDivaMapEventTx(tx, eventID, now)
	if err != nil || !enabled {
		return state, err
	}
	err = tx.QueryRow(`SELECT acquired_areas FROM diva_map_guilds
		WHERE event_id=$1 AND guild_id=$2 FOR UPDATE`, eventID, state.GuildID).Scan(&state.Areas)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	// A validated personal report retains the guild at the actual departure.
	// Map capping may assign zero credited points to the last party member in
	// the same bucket; that must not erase legitimate participation. Legacy
	// guild JSON and a newly joined guild's other members do not count.
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM diva_interception_runs
		WHERE char_id=$1 AND event_id=$2 AND guild_id=$3 AND points>0)`,
		charID, eventID, state.GuildID).Scan(&state.Participated)
	return state, err
}

func lockDivaGuildRewardClock(tx *sqlx.Tx, now time.Time) (time.Time, error) {
	if !now.IsZero() {
		return now.Truncate(time.Microsecond), nil
	}
	err := tx.QueryRow("SELECT clock_timestamp()").Scan(&now)
	return now, err
}

func (r *DivaRepository) OfferDivaGuildRewards(charID uint32, override int) ([]DivaRewardOffer, error) {
	return r.offerDivaGuildRewardsAt(charID, time.Time{}, override)
}

func (r *DivaRepository) offerDivaGuildRewardsAt(charID uint32, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
	return r.offerDivaMapRewardsAt(charID, 7, now, modes...)
}

func (r *DivaRepository) OfferDivaTreasureRewards(charID uint32, override int) ([]DivaRewardOffer, error) {
	return r.offerDivaMapRewardsAt(charID, 5, time.Time{}, override)
}

func (r *DivaRepository) offerDivaMapRewardsAt(charID uint32, kind uint8, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
	if charID == 0 || (kind != 5 && kind != 7) {
		return nil, errInvalidDivaReward
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var hr, gr uint16
	if err = tx.QueryRow("SELECT COALESCE(hr,0),COALESCE(gr,0) FROM characters WHERE id=$1 FOR UPDATE", charID).Scan(&hr, &gr); err != nil {
		return nil, err
	}
	event, err := lockDivaMapRewardWindow(tx, now, modes...)
	if err != nil {
		return nil, err
	}
	if event.ID == 0 {
		return nil, tx.Commit()
	}
	now, err = lockDivaGuildRewardClock(tx, now)
	if err != nil {
		return nil, err
	}
	state, err := lockDivaGuildRewardState(tx, charID, event.ID, now)
	if err != nil {
		return nil, err
	}
	if !state.Participated {
		return nil, tx.Commit()
	}
	var eligible []DivaRewardCatalogEntry
	if kind == 5 {
		eligible, err = eligibleDivaTreasureRewardsTx(tx, charID, event.ID, state.GuildID)
	} else {
		var prizes []DivaPrize
		if err = tx.Select(&prizes, `SELECT id,type,points_req AS pointsreq,item_type AS itemtype,item_id AS itemid,quantity,gr,repeatable
			FROM diva_prizes WHERE type='guild' ORDER BY points_req,id`); err != nil {
			return nil, err
		}
		eligible, err = eligibleDivaGuildRewards(prizes, gr, state.Areas)
		if err == nil {
			for _, reward := range divaHRGuildRewards() {
				if state.Areas >= reward.Threshold && divaRewardRankMatches(reward, hr, gr) {
					eligible = append(eligible, reward)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(eligible) > 0 {
		if _, err = tx.Exec(`INSERT INTO diva_guild_reward_memberships(char_id,event_id,guild_id)
			VALUES($1,$2,$3) ON CONFLICT(char_id,event_id) DO NOTHING`, charID, event.ID, state.GuildID); err != nil {
			return nil, err
		}
	}
	var pinnedGuild uint32
	err = tx.QueryRow(`SELECT guild_id FROM diva_guild_reward_memberships
		WHERE char_id=$1 AND event_id=$2`, charID, event.ID).Scan(&pinnedGuild)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && pinnedGuild != state.GuildID) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	for _, reward := range eligible {
		if kind == 7 {
			// Select one HR/GR bundle per area milestone BEFORE exposing IDs.
			// Existing pending snapshots remain collectable after promotion.
			selected, err := selectDivaGuildRewardVariantTx(tx, charID, event.ID, reward)
			if err != nil {
				return nil, err
			}
			if !selected {
				continue
			}
		}
		if _, err = tx.Exec(`INSERT INTO diva_reward_receipts
			(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT(char_id,event_id,reward_type,catalog_key) DO NOTHING`,
			charID, event.ID, kind, reward.Key, reward.ItemType, reward.ItemID, reward.Quantity); err != nil {
			return nil, err
		}
	}
	var offers []DivaRewardOffer
	if err = tx.Select(&offers, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity
		FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=$3
		AND claimed_at IS NULL ORDER BY id LIMIT 32`, charID, event.ID, kind); err != nil {
		return nil, err
	}
	return offers, tx.Commit()
}

func (r *DivaRepository) PrepareDivaGuildRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error) {
	return r.prepareDivaGuildRewardClaimsAt(charID, ids, time.Time{}, override)
}

func (r *DivaRepository) prepareDivaGuildRewardClaimsAt(charID uint32, ids []uint32, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
	return r.prepareDivaMapRewardClaimsAt(charID, 7, ids, now, modes...)
}

func (r *DivaRepository) PrepareDivaTreasureRewardClaims(charID uint32, ids []uint32, override int) ([]DivaRewardOffer, error) {
	return r.prepareDivaMapRewardClaimsAt(charID, 5, ids, time.Time{}, override)
}

func (r *DivaRepository) prepareDivaMapRewardClaimsAt(charID uint32, kind uint8, ids []uint32, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
	if kind != 5 && kind != 7 {
		return nil, errInvalidDivaReward
	}
	if err := validateDivaRewardClaimIDs(charID, kind, ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID uint32
	if err = tx.QueryRow("SELECT id FROM characters WHERE id=$1 FOR UPDATE", charID).Scan(&lockedID); err != nil {
		return nil, err
	}
	var rows []struct {
		DivaRewardOffer
		ClaimedAt sql.NullTime `db:"claimed_at"`
	}
	if err = tx.Select(&rows, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity,claimed_at
		FROM diva_reward_receipts WHERE char_id=$1 AND reward_type=$2 AND id=ANY($3::bigint[])
		ORDER BY id FOR UPDATE`, charID, kind, pq.Array(ids)); err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, errInvalidDivaReward
	}
	var pending []DivaRewardOffer
	for _, row := range rows {
		if !row.ClaimedAt.Valid {
			pending = append(pending, row.DivaRewardOffer)
		}
	}
	// Completed saves retry harmlessly, even after a new round or guild transfer.
	if len(pending) == 0 {
		return nil, tx.Commit()
	}
	event, err := lockDivaMapRewardWindow(tx, now, modes...)
	if err != nil {
		return nil, err
	}
	for _, offer := range pending {
		if event.ID == 0 || offer.EventID != event.ID {
			return nil, ErrDivaInterceptionRewardExpired
		}
	}
	now, err = lockDivaGuildRewardClock(tx, now)
	if err != nil {
		return nil, err
	}
	state, err := lockDivaGuildRewardState(tx, charID, event.ID, now)
	if err != nil {
		return nil, err
	}
	var pinnedGuild uint32
	err = tx.QueryRow(`SELECT guild_id FROM diva_guild_reward_memberships
		WHERE char_id=$1 AND event_id=$2`, charID, event.ID).Scan(&pinnedGuild)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (pinnedGuild != state.GuildID || !state.Participated)) {
		return nil, ErrDivaInterceptionMembership
	}
	if err != nil {
		return nil, err
	}
	// Validate the immutable OFFERED rank variant, not the current rank. An HR
	// reward offered before promotion must not be replaced or become unclaimable.
	eligible := append(divaApprovedGuildRewards(), divaHRGuildRewards()...)
	if kind == 5 {
		eligible, err = eligibleDivaTreasureRewardsTx(tx, charID, event.ID, state.GuildID)
		if err != nil {
			return nil, err
		}
	}
	for _, offer := range pending {
		valid := false
		for _, reward := range eligible {
			if offer.CatalogKey == reward.Key && offer.ItemType == reward.ItemType &&
				offer.ItemID == reward.ItemID && offer.Quantity == reward.Quantity && state.Areas >= reward.Threshold {
				if kind == 7 {
					selected, err := divaGuildRewardVariantMatchesTx(tx, charID, event.ID, reward)
					if err != nil {
						return nil, err
					}
					if !selected {
						return nil, errInvalidDivaReward
					}
				}
				valid = true
				break
			}
		}
		if !valid {
			return nil, errInvalidDivaReward
		}
	}
	return pending, tx.Commit()
}

// The character row is already locked by the caller. Group and receipt writes
// commit together, and a multi-item GR milestone (e.g. 22 areas) shares one group.
func selectDivaGuildRewardVariantTx(tx *sqlx.Tx, charID, eventID uint32, reward DivaRewardCatalogEntry) (bool, error) {
	group, variant := divaRewardGroup(reward)
	if reward.RewardType != 7 || group == "" || variant == "" {
		return false, errInvalidDivaReward
	}
	if _, err := tx.Exec(`INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
		VALUES($1,$2,7,$3,$4) ON CONFLICT DO NOTHING`, charID, eventID, group, variant); err != nil {
		return false, err
	}
	return divaGuildRewardVariantMatchesTx(tx, charID, eventID, reward)
}

func divaGuildRewardVariantMatchesTx(tx *sqlx.Tx, charID, eventID uint32, reward DivaRewardCatalogEntry) (bool, error) {
	group, variant := divaRewardGroup(reward)
	if reward.RewardType != 7 || group == "" || variant == "" {
		return false, errInvalidDivaReward
	}
	var selected string
	err := tx.QueryRow(`SELECT variant FROM diva_reward_groups
		WHERE char_id=$1 AND event_id=$2 AND reward_type=7 AND group_key=$3`, charID, eventID, group).Scan(&selected)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && selected == variant, err
}

// Branch completion is guild-wide, but a treasure is available only to a
// character whose valid contribution actually targeted that exact branch.
// The key intentionally excludes guild ID: changing guild cannot recreate the
// same event/map/branch reward. The shared sticky membership also prevents
// cherry-picking another guild's different map progress after the first offer.
func eligibleDivaTreasureRewardsTx(tx *sqlx.Tx, charID, eventID, guildID uint32) ([]DivaRewardCatalogEntry, error) {
	var awards []struct {
		Map        uint16 `db:"map_number"`
		Coordinate uint16 `db:"coordinate"`
	}
	err := tx.Select(&awards, `SELECT a.map_number,a.coordinate FROM diva_map_area_awards a
		WHERE a.event_id=$1 AND a.guild_id=$2 AND a.is_branch
		AND EXISTS(SELECT 1 FROM diva_map_contributions c JOIN diva_map_departures d USING(char_id,event_id,run_key)
		WHERE c.char_id=$3 AND c.event_id=a.event_id AND c.guild_id=a.guild_id
		AND c.map_number=a.map_number AND c.route=a.coordinate AND c.treasure_participation
		AND d.eligible AND d.guild_id=c.guild_id AND d.map_number=c.map_number AND d.route=c.route)
		ORDER BY a.map_number,a.coordinate`, eventID, guildID, charID)
	if err != nil {
		return nil, err
	}
	var result []DivaRewardCatalogEntry
	maps := make(map[uint16]DivaInterceptionMap)
	for _, award := range awards {
		m, cached := maps[award.Map]
		if !cached {
			m, err = loadDivaTreasureMapTx(tx, eventID, guildID, award.Map)
			if err != nil {
				return nil, err
			}
			maps[award.Map] = m
		}
		result = append(result, divaMapBranchTreasureRewards(m, award.Coordinate)...)
	}
	return result, nil
}

func divaCustomBranchTreasureRewards(mapNumber, coordinate uint16) []DivaRewardCatalogEntry {
	if mapNumber == 0 || (coordinate != 407 && coordinate != 106) {
		return nil
	}
	return divaApprovedBranchTreasureRewards(mapNumber, coordinate)
}

// Call only after membership in the immutable map's approved branches has
// been established. Keep receipt keys and quantities identical for v1/v2.
func divaApprovedBranchTreasureRewards(mapNumber, coordinate uint16) []DivaRewardCatalogEntry {
	base := fmt.Sprintf("custom-treasure-map-%d-branch-%d", mapNumber, coordinate)
	return []DivaRewardCatalogEntry{
		{Key: base + "-ticket", RewardType: 5, ItemType: 7, ItemID: 1026, Quantity: 5, Basis: "operator-approved-2026-09-23"},
		{Key: base + "-gp", RewardType: 5, ItemType: 26, Quantity: 500, Basis: "operator-approved-2026-09-23"},
	}
}

func divaSpecialHallPeriod(event DivaEvent, now time.Time) bool {
	if event.ID == 0 {
		return false
	}
	start := int64(event.StartTime) + divaPhaseDuration + divaWeekDuration + divaInterlude
	end := int64(event.StartTime) + divaPhaseDuration + 2*divaWeekDuration
	return now.Unix() >= start && now.Unix() < end
}

// The earned-area flag is not itself permission to enter early: only the
// actual round's welcome-song week opens the special hall. It is a guild perk,
// so current accepted members need not individually have earned a reward.
func (r *DivaRepository) GetDivaSpecialHall(charID, guildID uint32, now time.Time) (bool, error) {
	if charID == 0 || guildID == 0 {
		return false, nil
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.QueryRow("SELECT id FROM characters WHERE id=$1 FOR SHARE", charID).Scan(&id); err != nil {
		return false, err
	}
	event, err := lockDivaMapRewardWindow(tx, now)
	if err != nil {
		return false, err
	}
	now, err = lockDivaGuildRewardClock(tx, now)
	if err != nil || !divaSpecialHallPeriod(event, now) {
		return false, err
	}
	state, err := lockDivaGuildRewardState(tx, charID, event.ID, now)
	if err != nil {
		return false, err
	}
	return state.GuildID == guildID && state.Areas >= 1, tx.Commit()
}
