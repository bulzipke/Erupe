package channelserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"erupe-ce/common/questlicense"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// QLST/1 serializes full immutable starter-policy binding across servers. It
// does not overlap QLDF/1 (quest definitions) or account currency row locks.
const questLicenseStarterLockNamespace = 0x514c5354

type QuestLicenseStarterRepository struct{ db *sqlx.DB }

func NewQuestLicenseStarterRepository(db *sqlx.DB) *QuestLicenseStarterRepository {
	return &QuestLicenseStarterRepository{db: db}
}

// Every purchasing service binds the complete starter registry, including an
// empty one. Otherwise removing all starters could silently change a product's
// previously promised benefit without changing its permanent quest keys.
func (r *QuestLicenseRepository) BindStarterPolicies(ctx context.Context, policies []QuestLicenseStarterPolicy) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("quest license repository unavailable")
	}
	return NewQuestLicenseStarterRepository(r.db).BindPolicies(ctx, policies)
}

type questLicenseStarterBinding struct {
	Key       string         `db:"starter_key"`
	Keys      pq.StringArray `db:"license_keys"`
	ItemID    uint16         `db:"item_id"`
	Quantity  uint16         `db:"quantity"`
	Campaigns pq.Int64Array  `db:"legacy_campaign_ids"`
	Hash      string         `db:"policy_hash"`
}

func canonicalQuestLicenseStarter(policy QuestLicenseStarterPolicy) (questLicenseStarterBinding, error) {
	var b questLicenseStarterBinding
	if !validQuestLicenseDefinitionKey(policy.Key) || policy.ItemID == 0 || policy.Quantity == 0 ||
		len(policy.LicenseKeys) == 0 || len(policy.LicenseKeys) > questlicense.MaxProductKeys || len(policy.LegacyCampaignIDs) > questlicense.MaxStarterLegacyCampaigns {
		return b, fmt.Errorf("invalid quest license starter policy")
	}
	b = questLicenseStarterBinding{Key: policy.Key, Keys: append(pq.StringArray{}, policy.LicenseKeys...),
		ItemID: policy.ItemID, Quantity: policy.Quantity, Campaigns: make(pq.Int64Array, len(policy.LegacyCampaignIDs))}
	slices.Sort(b.Keys)
	for i, key := range b.Keys {
		if !validQuestLicenseDefinitionKey(key) || i > 0 && key == b.Keys[i-1] {
			return b, fmt.Errorf("invalid or duplicate starter license key")
		}
	}
	for i, id := range policy.LegacyCampaignIDs {
		if id == 0 || id > math.MaxInt32 {
			return b, fmt.Errorf("invalid starter legacy campaign")
		}
		b.Campaigns[i] = int64(id)
	}
	slices.Sort(b.Campaigns)
	for i := 1; i < len(b.Campaigns); i++ {
		if b.Campaigns[i] == b.Campaigns[i-1] {
			return b, fmt.Errorf("duplicate starter legacy campaign")
		}
	}
	encoded, err := json.Marshal(struct {
		Key       string
		Keys      []string
		ItemID    uint16
		Quantity  uint16
		Campaigns []int64
	}{b.Key, b.Keys, b.ItemID, b.Quantity, b.Campaigns})
	if err != nil {
		return b, err
	}
	hash := sha256.Sum256(encoded)
	b.Hash = hex.EncodeToString(hash[:])
	return b, nil
}

func sameQuestLicenseStarter(a, b questLicenseStarterBinding) bool {
	return a.Key == b.Key && a.Hash == b.Hash && a.ItemID == b.ItemID && a.Quantity == b.Quantity &&
		slices.Equal(a.Keys, b.Keys) && slices.Equal(a.Campaigns, b.Campaigns)
}

func (r *QuestLicenseStarterRepository) BindPolicies(ctx context.Context, policies []QuestLicenseStarterPolicy) error {
	if len(policies) > questlicense.MaxStarterDefinitions {
		return fmt.Errorf("too many quest license starter policies")
	}
	bindings := make(map[string]questLicenseStarterBinding, len(policies))
	for _, policy := range policies {
		b, err := canonicalQuestLicenseStarter(policy)
		if err != nil {
			return err
		}
		if _, duplicate := bindings[b.Key]; duplicate {
			return fmt.Errorf("duplicate starter policy")
		}
		bindings[b.Key] = b
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("starter repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,1)`, questLicenseStarterLockNamespace); err != nil {
		return err
	}
	var existing []questLicenseStarterBinding
	if err := tx.SelectContext(ctx, &existing, `SELECT starter_key,license_keys,item_id,quantity,legacy_campaign_ids,policy_hash
		FROM quest_license_starter_policies ORDER BY starter_key`); err != nil {
		return err
	}
	known := make(map[string]bool, len(existing))
	for _, old := range existing {
		current, exists := bindings[old.Key]
		if !exists || !sameQuestLicenseStarter(old, current) {
			return fmt.Errorf("previously bound starter policy %q missing or changed", old.Key)
		}
		known[old.Key] = true
	}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		b := bindings[key]
		var registered int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM quest_license_definitions WHERE license_key=ANY($1)`, b.Keys).Scan(&registered); err != nil {
			return err
		}
		if registered != len(b.Keys) {
			return fmt.Errorf("starter policy %q refers to an unbound license", key)
		}
		if !known[key] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO quest_license_starter_policies
				(starter_key,license_keys,item_id,quantity,legacy_campaign_ids,policy_hash) VALUES($1,$2,$3,$4,$5,$6)`,
				b.Key, b.Keys, b.ItemID, b.Quantity, b.Campaigns, b.Hash); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func lockQuestLicenseStarterCharacter(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32) (bool, error) {
	if userID == 0 || userID > math.MaxInt32 || characterID == 0 || characterID > math.MaxInt32 {
		return false, nil
	}
	var id uint32
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM characters WHERE id=$1 AND user_id=$2 AND NOT deleted FOR SHARE`, characterID, userID).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func legacyQuestLicenseStarterState(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32, b questLicenseStarterBinding) (claimed, pending bool, err error) {
	// Match only explicit legacy campaigns and this standard item. Unrelated
	// rewards, current inventory, GM grants and quest drops are not inferred.
	err = tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM campaign_rewards_claimed rc JOIN campaign_rewards r ON r.id=rc.reward_id
		 WHERE rc.character_id=$1 AND r.campaign_id=ANY($3) AND r.item_type=7 AND r.item_id=$4 AND r.quantity>0)
		OR EXISTS(SELECT 1 FROM campaign_coin_rewards r JOIN campaign_coin_purchases p ON p.id=r.purchase_id
		 WHERE p.character_id=$1 AND p.user_id=$2 AND p.campaign_id=ANY($3) AND r.item_type=7 AND r.item_id=$4 AND r.quantity>0 AND r.claimed),
		EXISTS(SELECT 1 FROM campaign_rewards r JOIN campaigns c ON c.id=r.campaign_id
		 WHERE r.campaign_id=ANY($3) AND r.item_type=7 AND r.item_id=$4 AND r.quantity>0
		 AND (SELECT count(*) FROM campaign_state s WHERE s.campaign_id=c.id AND s.character_id=$1)>=GREATEST(COALESCE(c.stamps,0),1)
		 AND (c.reward_type<>2 OR EXISTS(SELECT 1 FROM campaign_quest cq WHERE cq.campaign_id=c.id AND cq.character_id=$1))
		 AND NOT EXISTS(SELECT 1 FROM campaign_rewards_claimed rc WHERE rc.reward_id=r.id AND rc.character_id=$1))
		OR EXISTS(SELECT 1 FROM campaign_coin_rewards r JOIN campaign_coin_purchases p ON p.id=r.purchase_id
		 WHERE p.character_id=$1 AND p.user_id=$2 AND p.campaign_id=ANY($3) AND r.item_type=7 AND r.item_id=$4 AND r.quantity>0 AND NOT r.claimed)`,
		characterID, userID, b.Campaigns, b.ItemID).Scan(&claimed, &pending)
	return
}

func (r *QuestLicenseStarterRepository) Ensure(ctx context.Context, userID, characterID uint32, policy QuestLicenseStarterPolicy) (QuestLicenseStarterResult, error) {
	unavailable := QuestLicenseStarterResult{Status: StarterUnavailable}
	b, err := canonicalQuestLicenseStarter(policy)
	if err != nil {
		return unavailable, err
	}
	if r == nil || r.db == nil {
		return unavailable, fmt.Errorf("starter repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return unavailable, err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil || !active {
		return unavailable, err
	}
	var bound questLicenseStarterBinding
	err = tx.GetContext(ctx, &bound, `SELECT starter_key,license_keys,item_id,quantity,legacy_campaign_ids,policy_hash
		FROM quest_license_starter_policies WHERE starter_key=$1`, b.Key)
	if err != nil {
		return unavailable, fmt.Errorf("read bound starter policy: %w", err)
	}
	if !sameQuestLicenseStarter(bound, b) {
		return unavailable, fmt.Errorf("starter policy is unbound or changed")
	}
	var priorID uint32
	var state string
	err = tx.QueryRowContext(ctx, `SELECT id,state FROM quest_license_starters
		WHERE user_id=$1 AND character_id=$2 AND starter_key=$3`, userID, characterID, b.Key).Scan(&priorID, &state)
	if err == nil {
		switch state {
		case "pending":
			claimed, pending, err := legacyQuestLicenseStarterState(ctx, tx, userID, characterID, b)
			if err != nil {
				return unavailable, err
			}
			if claimed {
				updated, err := tx.ExecContext(ctx, `UPDATE quest_license_starters SET state='legacy_claimed'
					WHERE id=$1 AND user_id=$2 AND character_id=$3 AND state='pending'`, priorID, userID, characterID)
				if err != nil {
					return unavailable, err
				}
				if n, err := updated.RowsAffected(); err != nil || n != 1 {
					return unavailable, fmt.Errorf("starter suppression receipt changed")
				}
				if err := tx.Commit(); err != nil {
					return unavailable, err
				}
				return QuestLicenseStarterResult{Status: StarterLegacyClaimed}, nil
			}
			if pending {
				return QuestLicenseStarterResult{Status: StarterLegacyPending}, nil
			}
			return QuestLicenseStarterResult{Status: StarterPending, RewardID: priorID}, nil
		case "claimed":
			return QuestLicenseStarterResult{Status: StarterClaimed, RewardID: priorID}, nil
		case "legacy_claimed":
			return QuestLicenseStarterResult{Status: StarterLegacyClaimed}, nil
		}
		return unavailable, fmt.Errorf("invalid stored starter state")
	}
	if err != sql.ErrNoRows {
		return unavailable, err
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quest_licenses WHERE user_id=$1 AND license_key=ANY($2))`, userID, b.Keys).Scan(&owned); err != nil {
		return unavailable, err
	}
	if !owned {
		return QuestLicenseStarterResult{Status: StarterNotOwned}, nil
	}
	claimed, pending, err := legacyQuestLicenseStarterState(ctx, tx, userID, characterID, b)
	if err != nil {
		return unavailable, err
	}
	if !claimed && pending {
		// EnumerateItem's legacy item path uses stamps, not start/end dates.
		// Preserve that outstanding reward, even if its campaign has expired.
		// No permanent pending suppression is stored: recheck after collection.
		// Native UI reachability of expired campaigns remains an integration check.
		return QuestLicenseStarterResult{Status: StarterLegacyPending}, nil
	}
	state = "pending"
	if claimed {
		state = "legacy_claimed"
	}
	var id uint32
	err = tx.QueryRowContext(ctx, `INSERT INTO quest_license_starters(user_id,character_id,starter_key,item_id,quantity,state)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, userID, characterID, b.Key, b.ItemID, b.Quantity, state).Scan(&id)
	if err != nil {
		return unavailable, err
	}
	if err := tx.Commit(); err != nil {
		return unavailable, err
	}
	if claimed {
		return QuestLicenseStarterResult{Status: StarterLegacyClaimed}, nil
	}
	return QuestLicenseStarterResult{Status: StarterPending, RewardID: id}, nil
}

func (r *QuestLicenseStarterRepository) Pending(ctx context.Context, userID, characterID uint32, starterKey string) ([]CampaignReward, error) {
	if r == nil || r.db == nil || !validQuestLicenseDefinitionKey(starterKey) {
		return nil, fmt.Errorf("invalid starter pending query")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("starter character not authenticated")
	}
	items := []CampaignReward{}
	err = tx.SelectContext(ctx, &items, `SELECT id,7 AS item_type,quantity,item_id,TO_TIMESTAMP(0) AS deadline
		FROM quest_license_starters WHERE user_id=$1 AND character_id=$2 AND starter_key=$3 AND state='pending' ORDER BY id`, userID, characterID, starterKey)
	return items, err
}

func (r *QuestLicenseStarterRepository) Acquire(ctx context.Context, userID, characterID uint32, ids []uint32) error {
	if r == nil || r.db == nil || len(ids) == 0 || len(ids) > 512 {
		return fmt.Errorf("invalid starter acquire request")
	}
	for _, id := range ids {
		if id < campaignCoinRewardBase || id > math.MaxInt32 {
			return fmt.Errorf("invalid starter reward ID")
		}
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	active, err := lockQuestLicenseStarterCharacter(ctx, tx, userID, characterID)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("starter character not authenticated")
	}
	for _, id := range ids {
		updated, err := tx.ExecContext(ctx, `UPDATE quest_license_starters SET state='claimed',claimed_at=COALESCE(claimed_at,now())
			WHERE id=$1 AND user_id=$2 AND character_id=$3 AND state IN ('pending','claimed')`, id, userID, characterID)
		if err != nil {
			return err
		}
		if n, err := updated.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("unknown starter reward receipt")
		}
	}
	return tx.Commit()
}
