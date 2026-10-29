package channelserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var _ PalloneRewardRepository = (*PalloneRepository)(nil)

func (r *PalloneRepository) FinalizeEndedPalloneEvents(now time.Time) error {
	var ids []int32
	if err := r.db.Select(&ids, `SELECT e.id FROM pallone_events e WHERE e.ends_at<=$1 AND NOT EXISTS(SELECT 1 FROM pallone_event_finalizations f WHERE f.event_id=e.id) ORDER BY e.ends_at LIMIT 32`, now); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := r.FinalizePalloneEvent(id, now); err != nil {
			return err
		}
	}
	return nil
}

func palloneRewardProfileTx(tx *sqlx.Tx, eventID int32) (PalloneRewardCatalog, error) {
	var raw []byte
	err := tx.Get(&raw, `SELECT profile FROM pallone_reward_profiles WHERE event_id=$1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = loadPalloneRewardCatalog(palloneRewardJSON); err != nil {
			return PalloneRewardCatalog{}, err
		}
		if _, err = tx.Exec(`INSERT INTO pallone_reward_profiles(event_id,profile) VALUES($1,$2::jsonb) ON CONFLICT DO NOTHING`, eventID, string(palloneRewardJSON)); err != nil {
			return PalloneRewardCatalog{}, err
		}
		err = tx.Get(&raw, `SELECT profile FROM pallone_reward_profiles WHERE event_id=$1`, eventID)
	}
	if err != nil {
		return PalloneRewardCatalog{}, err
	}
	return loadPalloneRewardCatalog(raw)
}

func (r *PalloneRepository) PalloneRewardCatalog(eventID int32) (PalloneRewardCatalog, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return PalloneRewardCatalog{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	c, err := palloneRewardProfileTx(tx, eventID)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

// Freeze at the deadline without a timed grace period. Already accepted complete
// reports are applied eagerly before the snapshot, including after a restart.
// Retry outside the event lock: settlement acquires character then event locks.
func (r *PalloneRepository) FinalizePalloneEvent(eventID int32, now time.Time) (bool, error) {
	for attempt := 0; attempt < 32; attempt++ {
		done, pending, err := r.finalizePalloneEventAttempt(eventID, now)
		if err != nil || done || !pending {
			return done, err
		}
		ready, err := r.readyPalloneRuns(eventID, now)
		if err != nil {
			return false, err
		}
		if len(ready) == 0 {
			return false, nil
		}
		for _, v := range ready {
			if _, err = r.SettlePalloneResult(v.Receipt, v.Submission, now); err != nil {
				if !permanentPalloneRunError(err) {
					return false, err
				}
				if err = r.RejectPalloneRunReport(v.Receipt.CharID, v.Receipt.RunID, err.Error(), now); err != nil {
					return false, err
				}
			}
		}
	}
	return false, nil
}

func (r *PalloneRepository) finalizePalloneEventAttempt(eventID int32, now time.Time) (bool, bool, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback() //nolint:errcheck
	var e PalloneEvent
	if err = tx.Get(&e, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1 FOR UPDATE`, eventID); err != nil {
		return false, false, err
	}
	var done bool
	if err = tx.Get(&done, `SELECT EXISTS(SELECT 1 FROM pallone_event_finalizations WHERE event_id=$1)`, eventID); err != nil {
		return false, false, err
	}
	if done {
		return true, false, tx.Commit()
	}
	if now.Before(e.End) {
		return false, false, nil
	}
	var pending bool
	if err = tx.Get(&pending, `SELECT EXISTS(SELECT 1 FROM pallone_run_reports p WHERE event_id=$1 AND rejected_at IS NULL AND completed_at IS NOT NULL AND base_score IS NOT NULL AND completed_at<$2 AND score_reported_at<$2 AND result_reported_at<$2
		AND NOT EXISTS(SELECT 1 FROM pallone_results r WHERE r.character_id=p.character_id AND r.run_id=p.run_id))`, eventID, e.End); err != nil {
		return false, false, err
	}
	if pending {
		return false, true, nil
	}
	if _, err = tx.Exec(`SELECT character_id FROM pallone_results WHERE event_id=$1 FOR SHARE`, eventID); err != nil {
		return false, false, err
	}
	c, err := palloneRewardProfileTx(tx, eventID)
	if err != nil {
		return false, false, err
	}
	profile, err := json.Marshal(c)
	if err != nil {
		return false, false, err
	}
	if _, err = tx.Exec(`INSERT INTO pallone_event_finalizations(event_id,finalized_at,reward_profile) VALUES($1,$2,$3::jsonb)`, eventID, now, string(profile)); err != nil {
		return false, false, err
	}
	// Rank ties keep SQL RANK semantics; a stable display sort is not a tie breaker.
	if _, err = tx.Exec(`WITH points AS (
		SELECT 1::smallint kind,0 route_key,character_id::bigint subject_id,SUM(total_score)::integer score FROM pallone_results WHERE event_id=$1 GROUP BY character_id
		UNION ALL SELECT 2,route_key,character_id,MAX(base_score) FROM pallone_results WHERE event_id=$1 GROUP BY route_key,character_id
		UNION ALL SELECT 3,0,guild_id,SUM(total_score)::integer FROM pallone_results WHERE event_id=$1 AND guild_id IS NOT NULL GROUP BY guild_id
		UNION ALL SELECT 4,route_key,guild_id,SUM(total_score)::integer FROM pallone_results WHERE event_id=$1 AND guild_id IS NOT NULL GROUP BY route_key,guild_id
	) INSERT INTO pallone_final_ranks(event_id,kind,route_key,subject_id,score,rank)
	SELECT $1,kind,route_key,subject_id,score,RANK() OVER(PARTITION BY kind,route_key ORDER BY score DESC) FROM points`, eventID); err != nil {
		return false, false, err
	}
	if _, err = tx.Exec(`INSERT INTO pallone_final_guild_members(event_id,character_id,guild_id,contribution,guild_points)
		SELECT $1,m.character_id,m.guild_id,COALESCE(SUM(c.points),0)::integer,
		(SELECT COALESCE(SUM(points),0) FROM pallone_guild_contributions WHERE event_id=$1 AND guild_id=m.guild_id) FROM guild_characters m
		LEFT JOIN pallone_guild_contributions c ON c.event_id=$1 AND c.guild_id=m.guild_id AND c.character_id=m.character_id
		WHERE EXISTS(SELECT 1 FROM pallone_results p WHERE p.event_id=$1 AND p.guild_id=m.guild_id)
		GROUP BY m.character_id,m.guild_id`, eventID); err != nil {
		return false, false, err
	}
	var members []struct {
		CharID       uint32 `db:"character_id"`
		Contribution int32  `db:"contribution"`
		Total        int64  `db:"total"`
	}
	if err = tx.Select(&members, `SELECT m.character_id,m.contribution,m.guild_points total
		FROM pallone_final_guild_members m WHERE m.event_id=$1`, eventID); err != nil {
		return false, false, err
	}
	for _, m := range members {
		var amount int32
		for _, rule := range c.Rules {
			if rule.Kind == "guild-achievement" && rule.ItemType == palloneRP && m.Total >= int64(rule.Threshold) && m.Contribution >= rule.MinimumContribution {
				amount += rule.Quantity
			}
		}
		if amount != 0 {
			if _, err = tx.Exec(`INSERT INTO pallone_auto_rp_claims(event_id,character_id,quantity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, eventID, m.CharID, amount); err != nil {
				return false, false, err
			}
		}
	}
	return true, false, tx.Commit()
}

func palloneRewardProgressTx(tx *sqlx.Tx, eventID int32, charID uint32, current bool) (PalloneRewardProgress, error) {
	p := PalloneRewardProgress{Cleared: make(map[int32]bool)}
	err := tx.Get(&p.Score, `SELECT COALESCE(SUM(total_score::bigint+premium_bonus),0) FROM pallone_results WHERE event_id=$1 AND character_id=$2`, eventID, charID)
	if err != nil {
		return p, err
	}
	var keys []int32
	if err = tx.Select(&keys, `SELECT DISTINCT route_key FROM pallone_results WHERE event_id=$1 AND character_id=$2`, eventID, charID); err != nil {
		return p, err
	}
	for _, key := range keys {
		p.Cleared[key] = true
	}
	p.Routes, err = readPalloneRoutes(tx, eventID)
	if err != nil {
		return p, err
	}
	if err = tx.Get(&p.RepeatThrough, `SELECT COALESCE(MAX(threshold),0) FROM pallone_reward_offers WHERE event_id=$1 AND character_id=$2 AND reward_key LIKE 'repeat:%'`, eventID, charID); err != nil {
		return p, err
	}
	if current {
		return p, nil
	}
	if err = tx.Select(&p.Ranks, `SELECT kind,route_key,rank FROM pallone_final_ranks WHERE event_id=$1 AND kind IN(1,2) AND subject_id=$2 ORDER BY kind,route_key`, eventID, charID); err != nil {
		return p, err
	}
	var guildID int64
	err = tx.Get(&guildID, `SELECT guild_id FROM pallone_final_guild_members WHERE event_id=$1 AND character_id=$2`, eventID, charID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	// A guild award needs a clear credited to that guild, not a membership change
	// performed after the event. The finalized membership is used afterwards.
	var participated bool
	if err = tx.Get(&participated, `SELECT EXISTS(SELECT 1 FROM pallone_results WHERE event_id=$1 AND character_id=$2 AND guild_id=$3)`, eventID, charID, guildID); err != nil {
		return p, err
	}
	if !participated {
		return p, nil
	}
	var ranks []PalloneRewardRank
	if err = tx.Select(&ranks, `SELECT kind,route_key,rank FROM pallone_final_ranks WHERE event_id=$1 AND kind IN(3,4) AND subject_id=$2 ORDER BY kind,route_key`, eventID, guildID); err != nil {
		return p, err
	}
	p.Ranks = append(p.Ranks, ranks...)
	if err = tx.Get(&p.GuildPoints, `SELECT guild_points FROM pallone_final_guild_members WHERE event_id=$1 AND character_id=$2`, eventID, charID); err != nil {
		return p, err
	}
	err = tx.QueryRow(`WITH members AS (SELECT character_id,contribution,RANK() OVER(ORDER BY contribution DESC) rank
		FROM pallone_final_guild_members WHERE event_id=$1 AND guild_id=$2 AND contribution>0)
		SELECT contribution,rank FROM members WHERE character_id=$3`, eventID, guildID, charID).Scan(&p.Contribution, &p.ContributionRank)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return p, err
}

const palloneOfferColumns = `id,event_id,character_id,reward_key,present_type,item_type,item_id,quantity,threshold,route_key,expires_at,(claimed_at IS NOT NULL) AS claimed`

func (r *PalloneRepository) OfferPalloneRewards(eventID int32, charID uint32, current bool, types []uint32, now time.Time) ([]PalloneRewardOffer, error) {
	if charID == 0 || len(types) == 0 || len(types) > 7 {
		return nil, ErrPalloneInvalid
	}
	for _, typ := range types {
		if !pallonePresentType(typ) || current != (typ >= 3301) {
			return nil, ErrPalloneInvalid
		}
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, eventID^int32(0x50524c4c), int32(charID)); err != nil {
		return nil, err
	}
	var e PalloneEvent
	if err = tx.Get(&e, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1 FOR SHARE`, eventID); err != nil {
		return nil, err
	}
	if current && !e.Active(now) || !current && !e.Claimable(now) {
		return nil, nil
	}
	if !current {
		var done bool
		if err = tx.Get(&done, `SELECT EXISTS(SELECT 1 FROM pallone_event_finalizations WHERE event_id=$1)`, eventID); err != nil {
			return nil, err
		}
		if !done {
			return nil, nil
		}
	}
	c, err := palloneRewardProfileTx(tx, eventID)
	if err != nil {
		return nil, err
	}
	p, err := palloneRewardProgressTx(tx, eventID, charID, current)
	if err != nil {
		return nil, err
	}
	expires := e.ClaimUntil
	if current {
		expires = e.End
	}
	wanted := make(map[uint32]bool, len(types))
	for _, typ := range types {
		wanted[typ] = true
	}
	for _, offer := range palloneEligibleRewards(c, p, current) {
		if !wanted[offer.PresentType] || offer.ItemType == palloneRP {
			continue
		}
		if _, err = tx.Exec(`INSERT INTO pallone_reward_offers(event_id,character_id,reward_key,present_type,item_type,item_id,quantity,threshold,route_key,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(event_id,character_id,reward_key) DO NOTHING`, eventID, charID, offer.Key, offer.PresentType, offer.ItemType, offer.ItemID, offer.Quantity, offer.Threshold, offer.RouteKey, expires); err != nil {
			return nil, err
		}
	}
	ints := make([]int64, len(types))
	for i, v := range types {
		ints[i] = int64(v)
	}
	var out []PalloneRewardOffer
	if err = tx.Select(&out, `SELECT `+palloneOfferColumns+` FROM pallone_reward_offers WHERE event_id=$1 AND character_id=$2 AND present_type=ANY($3) AND claimed_at IS NULL AND expires_at>$4 ORDER BY id LIMIT 8192`, eventID, charID, pq.Array(ints), now); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (r *PalloneRepository) PreparePalloneRewardClaims(charID uint32, ids []uint32, now time.Time) ([]PalloneRewardOffer, error) {
	if charID == 0 || len(ids) == 0 || len(ids) > 256 {
		return nil, ErrPalloneInvalid
	}
	keys := make([]int64, len(ids))
	seen := make(map[uint32]bool)
	for i, id := range ids {
		if !palloneRewardID(id) || seen[id] {
			return nil, ErrPalloneInvalid
		}
		seen[id] = true
		keys[i] = int64(id)
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var out []PalloneRewardOffer
	if err = tx.Select(&out, `SELECT `+palloneOfferColumns+` FROM pallone_reward_offers WHERE id=ANY($1) AND character_id=$2
		AND (expires_at>$3 OR prepared_at IS NOT NULL) ORDER BY id FOR UPDATE`, pq.Array(keys), charID, now); err != nil {
		return nil, err
	}
	if len(out) != len(keys) {
		return nil, ErrPalloneIneligible
	}
	if _, err = tx.Exec(`UPDATE pallone_reward_offers SET prepared_at=COALESCE(prepared_at,$3) WHERE id=ANY($1) AND character_id=$2`, pq.Array(keys), charID, now); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// Runs inside the transaction writing the same client inventory or GP
// snapshot. No second copy is deposited into a server warehouse.
func commitPalloneRewardReceipts(tx *sqlx.Tx, charID uint32, ids []uint32, itemType int32) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 8192 || (itemType != palloneItem && itemType != palloneGP) {
		return ErrPalloneInvalid
	}
	keys := make([]int64, len(ids))
	seen := make(map[uint32]bool)
	for i, id := range ids {
		if !palloneRewardID(id) || seen[id] {
			return ErrPalloneInvalid
		}
		seen[id] = true
		keys[i] = int64(id)
	}
	var found int
	if err := tx.Get(&found, `SELECT COUNT(*) FROM pallone_reward_offers WHERE id=ANY($1) AND character_id=$2 AND item_type=$3 AND prepared_at IS NOT NULL`, pq.Array(keys), charID, itemType); err != nil {
		return err
	}
	if found != len(ids) {
		return ErrPalloneIneligible
	}
	_, err := tx.Exec(`UPDATE pallone_reward_offers SET claimed_at=COALESCE(claimed_at,NOW()) WHERE id=ANY($1) AND character_id=$2 AND item_type=$3`, pq.Array(keys), charID, itemType)
	return err
}

type PalloneRPAward struct {
	EventID  int32 `db:"event_id"`
	Quantity int32 `db:"quantity"`
}

func (r *PalloneRepository) PendingPalloneRP(charID uint32) ([]PalloneRPAward, error) {
	var rows []PalloneRPAward
	err := r.db.Select(&rows, `SELECT event_id,quantity FROM pallone_auto_rp_claims WHERE character_id=$1 AND applied_at IS NULL ORDER BY event_id`, charID)
	return rows, err
}

func commitPalloneRP(tx *sqlx.Tx, charID uint32, ids []int32) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE pallone_auto_rp_claims SET applied_at=NOW() WHERE event_id=$1 AND character_id=$2 AND applied_at IS NULL`, id, charID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("Pallone RP already applied or missing")
		}
	}
	return nil
}
