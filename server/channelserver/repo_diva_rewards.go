package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// DivaRewardRepository is separate from DivaRepo so unsupported repository
// implementations cannot accidentally acknowledge unpersisted reward claims.
type DivaRewardRepository interface {
	GetDivaRewardProgress(charID, eventID uint32, start, end time.Time) (DivaRewardProgress, error)
	OfferDivaRewards(charID, eventID uint32, kind uint8, rewards []DivaRewardCatalogEntry) ([]DivaRewardOffer, error)
	PrepareDivaRewardClaims(charID uint32, kind uint8, ids []uint32) ([]DivaRewardOffer, error)
}

type DivaRewardRankRepository interface {
	GetDivaRewardRanks(charID uint32) (hr, gr uint16, err error)
}

func (r *DivaRepository) GetDivaRewardRanks(charID uint32) (hr, gr uint16, err error) {
	err = r.db.QueryRow(`SELECT COALESCE(hr,0),COALESCE(gr,0) FROM characters WHERE id=$1`, charID).Scan(&hr, &gr)
	return
}

type DivaRewardProgress struct {
	HR                uint16 `db:"hr"`
	GR                uint16 `db:"gr"`
	Points            int64  `db:"points"`
	ParticipationDays uint32 `db:"participation_days"`
	Rank              uint32 `db:"rank"`
	GuildRank         uint32 `db:"guild_rank"`
}

var errInvalidDivaReward = errors.New("invalid Diva reward request")

// GetDivaRewardProgress uses the contribution journal, not login dates or the
// unbounded aggregate. Both ranking and eligibility use the same event window.
func (r *DivaRepository) GetDivaRewardProgress(charID, eventID uint32, start, end time.Time) (DivaRewardProgress, error) {
	var result DivaRewardProgress
	if charID == 0 || eventID == 0 || !start.Before(end) {
		return result, errInvalidDivaReward
	}
	if err := r.ensureDivaGuildFinalRanks(eventID, end); err != nil {
		return result, err
	}
	err := r.db.Get(&result, `WITH days AS (
		SELECT char_id,day_start,SUM(quest_points+bonus_points)::bigint points
		FROM diva_song_records WHERE event_id=$2 AND submitted_at >= $3 AND submitted_at < $4
		GROUP BY char_id,day_start
	), scores AS (
		SELECT char_id,SUM(points)::bigint points,
			COUNT(*) FILTER (WHERE points>0) participation_days
		FROM days GROUP BY char_id
	), ranks AS (
		SELECT char_id,RANK() OVER (ORDER BY points DESC) rank FROM scores WHERE points>0
	)
	SELECT COALESCE(c.hr,0) hr,COALESCE(c.gr,0) gr,COALESCE(s.points,0) points,
		COALESCE(s.participation_days,0) participation_days,COALESCE(r.rank,0) rank,
		COALESCE((SELECT f.rank FROM diva_guild_final_ranks f
			JOIN diva_guild_membership_history h ON h.guild_id=f.guild_id AND h.char_id=c.id
			WHERE f.event_id=$2 AND h.valid_from < $3::timestamptz+INTERVAL '601200 seconds'
			AND (h.valid_until IS NULL OR h.valid_until >= $3::timestamptz+INTERVAL '601200 seconds')
			AND EXISTS(SELECT 1 FROM diva_song_records j WHERE j.char_id=c.id AND j.event_id=$2
				AND j.guild_id=f.guild_id AND j.quest_points+j.bonus_points>0
				AND j.submitted_at >= $3 AND j.submitted_at < $3::timestamptz+INTERVAL '601200 seconds')
			ORDER BY h.valid_from DESC LIMIT 1),0) guild_rank
	FROM characters c LEFT JOIN scores s ON s.char_id=c.id LEFT JOIN ranks r ON r.char_id=c.id
	WHERE c.id=$1`, charID, eventID, start, end)
	return result, err
}

func validateDivaRewardItem(kind uint8, id, quantity uint16) error {
	if quantity == 0 || (kind != 7 && kind != 26) ||
		(kind == 7 && id == 0) || (kind == 26 && id != 0) {
		return errInvalidDivaReward
	}
	return nil
}

func validateDivaRewardCatalog(kind uint8, rewards []DivaRewardCatalogEntry) error {
	if kind > 7 {
		return errInvalidDivaReward
	}
	keys := make(map[string]struct{}, len(rewards))
	for i, reward := range rewards {
		if reward.NormaRepeat || reward.RewardType != kind || reward.Key == "" || strings.TrimSpace(reward.Key) != reward.Key ||
			strings.ContainsRune(reward.Key, '\x00') {
			return fmt.Errorf("%w: catalog row %d", errInvalidDivaReward, i)
		}
		if _, exists := keys[reward.Key]; exists {
			return fmt.Errorf("%w: duplicate catalog key", errInvalidDivaReward)
		}
		if err := validateDivaRewardItem(reward.ItemType, reward.ItemID, reward.Quantity); err != nil {
			return fmt.Errorf("%w: catalog row %d item", err, i)
		}
		keys[reward.Key] = struct{}{}
	}
	return nil
}

// OfferDivaRewards receives the caller's currently eligible catalog subset.
// Persisted item snapshots never change when a catalog is edited or reordered.
// Querying does not consume anything; the matching save transaction does that.
func (r *DivaRepository) OfferDivaRewards(charID, eventID uint32, kind uint8, rewards []DivaRewardCatalogEntry) ([]DivaRewardOffer, error) {
	if kind == 6 {
		return nil, errInvalidDivaReward
	} // Interception requires authoritative mode context.
	return r.offerDivaRewardsAt(charID, eventID, kind, rewards, time.Time{})
}

func (r *DivaRepository) offerDivaRewardsAt(charID, eventID uint32, kind uint8, rewards []DivaRewardCatalogEntry, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
	return r.offerDivaRewardsWithRotationAt(charID, eventID, kind, rewards, now, nil, modes...)
}

func (r *DivaRepository) offerDivaRewardsWithRotationAt(charID, eventID uint32, kind uint8, rewards []DivaRewardCatalogEntry, now time.Time, rotationProgress *DivaRewardProgress, modes ...int) ([]DivaRewardOffer, error) {
	if charID == 0 || eventID == 0 {
		return nil, errInvalidDivaReward
	}
	if err := validateDivaRewardCatalog(kind, rewards); err != nil {
		return nil, err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID uint32
	if err := tx.Get(&lockedID, "SELECT id FROM characters WHERE id=$1 FOR UPDATE", charID); err != nil {
		return nil, err
	}
	if rotationProgress != nil {
		if kind != 1 {
			return nil, errInvalidDivaReward
		}
		progress := *rotationProgress
		if err := tx.QueryRow("SELECT COALESCE(hr,0),COALESCE(gr,0) FROM characters WHERE id=$1", charID).Scan(&progress.HR, &progress.GR); err != nil {
			return nil, err
		}
		// Eligibility uses the locked rank, including HR tier promotions. Already
		// issued receipts retain their immutable snapshots across rank changes.
		rotationProgress = &progress
		rewards = eligibleDivaSongRewards(1, progress)
	}
	if kind == 6 {
		current, err := lockDivaInterceptionRewardWindow(tx, now, modes...)
		if err != nil {
			return nil, err
		}
		if current.ID == 0 || current.ID != eventID {
			return nil, ErrDivaInterceptionRewardExpired
		}
	}
	keys := make([]string, 0, len(rewards))
	for _, reward := range rewards {
		group, variant := divaRewardGroup(reward)
		if group != "" {
			if _, err := tx.Exec(`INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
				VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, charID, eventID, kind, group, variant); err != nil {
				return nil, err
			}
			var selected string
			if err := tx.Get(&selected, `SELECT variant FROM diva_reward_groups WHERE char_id=$1 AND event_id=$2 AND reward_type=$3 AND group_key=$4`, charID, eventID, kind, group); err != nil {
				return nil, err
			}
			if selected != variant {
				continue
			}
		}
		_, err = tx.Exec(`INSERT INTO diva_reward_receipts
			(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT(char_id,event_id,reward_type,catalog_key) DO NOTHING`,
			charID, eventID, kind, reward.Key, reward.ItemType, reward.ItemID, reward.Quantity)
		if err != nil {
			return nil, err
		}
		keys = append(keys, reward.Key)
	}
	if rotationProgress != nil {
		if err := offerDivaPrayerRotation(tx, charID, eventID, *rotationProgress); err != nil {
			return nil, err
		}
	}
	var offers []DivaRewardOffer
	if kind == 6 {
		// Previously offered items retain their immutable snapshot, including
		// when a later catalog edit removed the original row from the UI list.
		err = tx.Select(&offers, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity
			FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=6
			AND claimed_at IS NULL ORDER BY id LIMIT 32`, charID, eventID)
		if err != nil {
			return nil, err
		}
	} else if kind == 0 || kind == 1 || kind == 3 {
		// Preserve the complete selected bundle after promotions/transfers,
		// including rows already offered but not yet committed by a save.
		err = tx.Select(&offers, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity
			FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=$3
			AND claimed_at IS NULL ORDER BY id LIMIT 32`, charID, eventID, kind)
		if err != nil {
			return nil, err
		}
	} else if len(keys) > 0 {
		err = tx.Select(&offers, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity
			FROM diva_reward_receipts WHERE char_id=$1 AND event_id=$2 AND reward_type=$3
			AND catalog_key=ANY($4::text[]) AND claimed_at IS NULL ORDER BY id LIMIT 32`,
			charID, eventID, kind, pq.Array(keys))
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return offers, nil
}

func validateDivaRewardClaimIDs(charID uint32, kind uint8, ids []uint32) error {
	if charID == 0 || kind > 7 || len(ids) > 32 {
		return errInvalidDivaReward
	}
	seen := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; id == 0 || duplicate {
			return errInvalidDivaReward
		}
		seen[id] = struct{}{}
	}
	return nil
}

// PrepareDivaRewardClaims validates the entire request before exposing pending
// rows. Already claimed owned IDs are harmless retries, not new entitlements.
// This method deliberately does not consume receipts or grant inventory/GP.
func (r *DivaRepository) PrepareDivaRewardClaims(charID uint32, kind uint8, ids []uint32) ([]DivaRewardOffer, error) {
	if kind == 6 {
		return nil, errInvalidDivaReward
	} // Use the interception-specific API.
	return r.prepareDivaRewardClaimsAt(charID, kind, ids, time.Time{})
}

func (r *DivaRepository) prepareDivaRewardClaimsAt(charID uint32, kind uint8, ids []uint32, now time.Time, modes ...int) ([]DivaRewardOffer, error) {
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
	if err := tx.Get(&lockedID, "SELECT id FROM characters WHERE id=$1 FOR UPDATE", charID); err != nil {
		return nil, err
	}
	var rows []struct {
		DivaRewardOffer
		ClaimedAt sql.NullTime `db:"claimed_at"`
	}
	if err := tx.Select(&rows, `SELECT id,event_id,reward_type,catalog_key,item_type,item_id,quantity,claimed_at
		FROM diva_reward_receipts WHERE char_id=$1 AND reward_type=$2 AND id=ANY($3::bigint[])
		ORDER BY id`, charID, kind, pq.Array(ids)); err != nil {
		return nil, err
	}
	// Do not distinguish foreign, wrong-kind, and missing IDs to the requester.
	if len(rows) != len(ids) {
		return nil, errInvalidDivaReward
	}
	var current DivaEvent
	if kind == 6 {
		for _, row := range rows {
			if !row.ClaimedAt.Valid {
				current, err = lockDivaInterceptionRewardWindow(tx, now, modes...)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	var offers []DivaRewardOffer
	for _, row := range rows {
		if err := validateDivaRewardItem(row.ItemType, row.ItemID, row.Quantity); err != nil {
			return nil, err
		}
		if !row.ClaimedAt.Valid {
			if kind == 6 && (current.ID == 0 || current.ID != row.EventID) {
				return nil, ErrDivaInterceptionRewardExpired
			}
			offers = append(offers, row.DivaRewardOffer)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return offers, nil
}
