package channelserver

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
)

// DivaPrayerRotationRepository requires a persisted issued sequence alongside
// receipts before the server can advertise repeat rewards.
type DivaPrayerRotationRepository interface {
	OfferDivaPrayerRewards(charID, eventID uint32, progress DivaRewardProgress) ([]DivaRewardOffer, error)
}

var _ DivaPrayerRotationRepository = (*DivaRepository)(nil)

// Progress comes from the server's per-event journal, never client points or
// the all-time aggregate. Both finite rewards and repeats commit together.
func (r *DivaRepository) OfferDivaPrayerRewards(charID, eventID uint32, progress DivaRewardProgress) ([]DivaRewardOffer, error) {
	return r.offerDivaRewardsWithRotationAt(charID, eventID, 1,
		eligibleDivaSongRewards(1, progress), time.Time{}, &progress)
}

func divaPrayerRotationFingerprint(rotation DivaPrayerRotation) string {
	if rotation.track() == "hr" {
		// Both HR variants share a processed sequence. Promotion is not a
		// policy change; editing either tier within an event still is.
		return divaHRPrayerRotationFingerprint(divaPrayerRotationForRank(2, 0), divaPrayerRotationForRank(100, 0))
	}
	// Do not change this GR encoding: existing persisted fingerprints depend on it.
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d:%d", rotation.Start, rotation.Interval)
	for _, item := range rotation.Items {
		_, _ = fmt.Fprintf(hash, ":%d:%d:%d:%d:%t", item.RewardType, item.ItemType, item.ItemID, item.Quantity, item.GR)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func divaHRPrayerRotationFingerprint(low, high DivaPrayerRotation) string {
	hash := sha256.New()
	_, _ = fmt.Fprint(hash, "hr-rank-variants-v1")
	for _, variant := range []DivaPrayerRotation{low, high} {
		_, _ = fmt.Fprintf(hash, ":%s:%d:%d", variant.Key, variant.Start, variant.Interval)
		for _, item := range variant.Items {
			_, _ = fmt.Fprintf(hash, ":%d:%d:%d:%d:%t:%d:%d", item.RewardType, item.ItemType, item.ItemID, item.Quantity, item.GR, item.MinHR, item.MaxHR)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

type divaPrayerRotationCursor struct {
	Key         string `db:"schedule_key"`
	Fingerprint string `db:"schedule_fingerprint"`
	Next        uint32 `db:"next_index"`
}

func validateDivaPrayerRotationCursor(cursor divaPrayerRotationCursor, rotation DivaPrayerRotation, eventID uint32) error {
	if cursor.Key != rotation.Key || cursor.Fingerprint != divaPrayerRotationFingerprint(rotation) ||
		cursor.Next > rotation.dueCount(math.MaxUint32) {
		return fmt.Errorf("%w: prayer rotation changed or invalid cursor within event %d", errInvalidDivaReward, eventID)
	}
	return nil
}

// Jump only when the current score lies inside the other track's processed
// interval. A prior GR sequence must not erase HR's earlier 21k..102k rights.
func divaPrayerRotationSkipProcessed(index, due uint32, rotation, other DivaPrayerRotation, otherNext uint32) uint32 {
	if otherNext == 0 || rotation.Interval == 0 || rotation.Interval != other.Interval ||
		rotation.Start%rotation.Interval != other.Start%other.Interval {
		return index
	}
	point := uint64(rotation.Start) + uint64(index)*uint64(rotation.Interval)
	otherStart := uint64(other.Start)
	otherEnd := otherStart + uint64(otherNext)*uint64(other.Interval)
	if point < otherStart || point >= otherEnd {
		return index
	}
	next := (otherEnd - uint64(rotation.Start)) / uint64(rotation.Interval)
	if next > uint64(due) {
		return due
	}
	return uint32(next)
}

// Caller holds the character row lock, also used by atomic saves. A cursor
// records processed (issued or already owned at another rank), not claimed,
// sequence entries. Groups, finite/repeat receipts and cursors commit together.
func offerDivaPrayerRotation(tx *sqlx.Tx, charID, eventID uint32, progress DivaRewardProgress) error {
	rotation := divaPrayerRotationForRank(progress.HR, progress.GR)
	due := rotation.dueCount(progress.Points)
	if due == 0 {
		return nil
	}
	fingerprint := divaPrayerRotationFingerprint(rotation)
	if _, err := tx.Exec(`INSERT INTO diva_prayer_rotation_progress
		(char_id,event_id,track,schedule_key,schedule_fingerprint) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(char_id,event_id,track) DO NOTHING`, charID, eventID, rotation.track(), rotation.Key, fingerprint); err != nil {
		return err
	}
	var cursor divaPrayerRotationCursor
	if err := tx.Get(&cursor, `SELECT schedule_key,schedule_fingerprint,next_index
		FROM diva_prayer_rotation_progress WHERE char_id=$1 AND event_id=$2 AND track=$3 FOR UPDATE`, charID, eventID, rotation.track()); err != nil {
		return err
	}
	if err := validateDivaPrayerRotationCursor(cursor, rotation, eventID); err != nil {
		return err
	}
	otherRotation := divaPrayerRotationForRank(2, 0)
	if rotation.track() == "hr" {
		otherRotation = divaPrayerRotation()
	}
	var otherCursor divaPrayerRotationCursor
	err := tx.Get(&otherCursor, `SELECT schedule_key,schedule_fingerprint,next_index
		FROM diva_prayer_rotation_progress WHERE char_id=$1 AND event_id=$2 AND track=$3 FOR UPDATE`, charID, eventID, otherRotation.track())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := validateDivaPrayerRotationCursor(otherCursor, otherRotation, eventID); err != nil {
			return err
		}
	}
	if cursor.Next >= due {
		return nil
	}
	var pending int
	if err := tx.Get(&pending, `SELECT COUNT(*) FROM diva_reward_receipts
		WHERE char_id=$1 AND event_id=$2 AND reward_type=1 AND claimed_at IS NULL`, charID, eventID); err != nil {
		return err
	}
	capacity := 32 - pending
	if capacity <= 0 {
		return nil
	}
	index := cursor.Next
	for index < due && capacity > 0 {
		if next := divaPrayerRotationSkipProcessed(index, due, rotation, otherRotation, otherCursor.Next); next > index {
			index = next
			continue
		}
		reward := rotation.reward(index)
		if err := validateDivaRewardCatalog(1, []DivaRewardCatalogEntry{reward}); err != nil {
			return err
		}
		group, variant := divaRewardGroup(reward)
		if _, err := tx.Exec(`INSERT INTO diva_reward_groups(char_id,event_id,reward_type,group_key,variant)
			VALUES($1,$2,1,$3,$4) ON CONFLICT DO NOTHING`, charID, eventID, group, variant); err != nil {
			return err
		}
		var selected string
		if err := tx.Get(&selected, `SELECT variant FROM diva_reward_groups
			WHERE char_id=$1 AND event_id=$2 AND reward_type=1 AND group_key=$3`, charID, eventID, group); err != nil {
			return err
		}
		index++
		if selected != variant {
			continue
		}
		result, err := tx.Exec(`INSERT INTO diva_reward_receipts
			(char_id,event_id,reward_type,catalog_key,item_type,item_id,quantity)
			VALUES($1,$2,1,$3,$4,$5,$6)
			ON CONFLICT(char_id,event_id,reward_type,catalog_key) DO NOTHING`,
			charID, eventID, reward.Key, reward.ItemType, reward.ItemID, reward.Quantity)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		capacity -= int(inserted)
	}
	_, err = tx.Exec(`UPDATE diva_prayer_rotation_progress SET next_index=$4
		WHERE char_id=$1 AND event_id=$2 AND track=$3`, charID, eventID, rotation.track(), index)
	return err
}
