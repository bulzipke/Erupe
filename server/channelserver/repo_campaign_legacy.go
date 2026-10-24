package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

var errLegacyCampaignUnavailable = errors.New("legacy campaign request unavailable")

// LegacyCampaignRepository serializes legacy authentication, reward receipts
// and quest completion with starter eligibility/save decisions for the same
// account. It does not grant inventory or change legacy dates/rank policies.
type LegacyCampaignRepository struct{ db *sqlx.DB }

func NewLegacyCampaignRepository(db *sqlx.DB) *LegacyCampaignRepository {
	return &LegacyCampaignRepository{db: db}
}

func (r *LegacyCampaignRepository) begin(ctx context.Context, userID, characterID uint32) (*sqlx.Tx, error) {
	if r == nil || r.db == nil {
		return nil, errLegacyCampaignUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil || !active {
		_ = tx.Rollback()
		if err != nil {
			return nil, err
		}
		return nil, errLegacyCampaignUnavailable
	}
	return tx, nil
}

func (r *LegacyCampaignRepository) Apply(ctx context.Context, userID, characterID, campaignID uint32, code string) error {
	tx, err := r.begin(ctx, userID, characterID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var priced bool
	if err := tx.GetContext(ctx, &priced, `SELECT EXISTS(SELECT 1 FROM campaign_coin_prices WHERE campaign_id=$1)`, campaignID); err != nil {
		return err
	}
	if priced {
		return errLegacyCampaignUnavailable
	}
	var multi bool
	// Account locks alone cannot serialize a globally single-use code across
	// different users. Lock the existing code row before checking its use.
	if err := tx.GetContext(ctx, &multi, `SELECT multi FROM campaign_codes WHERE code=$1 AND campaign_id=$2 FOR UPDATE`, code, campaignID); err != nil {
		return err
	}
	var used bool
	if multi {
		err = tx.GetContext(ctx, &used, `SELECT EXISTS(SELECT 1 FROM campaign_state WHERE code=$1 AND character_id=$2)`, code, characterID)
	} else {
		err = tx.GetContext(ctx, &used, `SELECT EXISTS(SELECT 1 FROM campaign_state WHERE code=$1)`, code)
	}
	if err != nil {
		return err
	}
	if used {
		return errLegacyCampaignUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO campaign_state(code,campaign_id,character_id) VALUES($1,$2,$3)`, code, campaignID, characterID); err != nil {
		return err
	}
	return tx.Commit()
}

func validateLegacyCampaignRewardIDs(ids []uint32) error {
	if len(ids) > 512 {
		return errLegacyCampaignUnavailable
	}
	seen := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		if id == 0 || id >= campaignCoinRewardBase || seen[id] {
			return errLegacyCampaignUnavailable
		}
		seen[id] = true
	}
	return nil
}

func (r *LegacyCampaignRepository) Acquire(ctx context.Context, userID, characterID uint32, ids []uint32) error {
	if err := validateLegacyCampaignRewardIDs(ids); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil // Preserve the ordinary empty native acknowledgement.
	}
	tx, err := r.begin(ctx, userID, characterID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		var eligible bool
		// Match the existing EnumerateItem branch, including its deliberate
		// lack of date/rank checks. Quest-completion rewards retain the native
		// Type9-before-completion / material-after-completion distinction.
		err := tx.GetContext(ctx, &eligible, `SELECT
			(SELECT count(*) FROM campaign_state s WHERE s.campaign_id=c.id AND s.character_id=$2)>=GREATEST(COALESCE(c.stamps,1),1)
			AND (c.reward_type IS DISTINCT FROM 2 OR
				((r.item_type=9) <> EXISTS(SELECT 1 FROM campaign_quest q WHERE q.campaign_id=c.id AND q.character_id=$2)))
			AND NOT EXISTS(SELECT 1 FROM campaign_rewards_claimed x WHERE x.reward_id=r.id AND x.character_id=$2)
			FROM campaign_rewards r JOIN campaigns c ON c.id=r.campaign_id WHERE r.id=$1 FOR SHARE OF r,c`, id, characterID)
		if err != nil {
			return err
		}
		if !eligible {
			return errLegacyCampaignUnavailable
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO campaign_rewards_claimed(reward_id,character_id) VALUES($1,$2)`, id, characterID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompleteQuest receives the event_quests row ID used by TRANSFER_ITEM. The
// existing no-match success is preserved. Completion only records progression;
// it neither authenticates a code nor claims any reward inventory.
func (r *LegacyCampaignRepository) CompleteQuest(ctx context.Context, userID, characterID, questRecordID uint32) error {
	tx, err := r.begin(ctx, userID, characterID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var campaignID uint32
	err = tx.GetContext(ctx, &campaignID, `SELECT r.campaign_id FROM campaign_rewards r
		JOIN event_quests q ON r.item_id=q.quest_id WHERE q.id=$1`, questRecordID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy quest completion: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO campaign_quest(campaign_id,character_id) VALUES($1,$2)`, campaignID, characterID); err != nil {
		return err
	}
	return tx.Commit()
}
