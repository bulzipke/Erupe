package channelserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"erupe-ce/common/questlicense"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// All servers bind the complete registry under this two-integer transaction
// advisory lock: namespace 0x514c4446 (ASCII QLDF), protocol version 1. It is
// independent of account currency locks and is released on commit/rollback.
const (
	questLicenseDefinitionLockNamespace = 0x514c4446
	questLicenseDefinitionLockVersion   = 1
)

type questLicenseDefinitionBinding struct {
	Key      string        `db:"license_key"`
	QuestIDs pq.Int64Array `db:"quest_ids"`
	Hash     string        `db:"mapping_hash"`
}

type questLicensePurchaseCoverage struct {
	registeredKeys int
	targetQuests   int
	coveredQuests  int
	exactOwnedKeys int
}

// loadQuestLicensePurchaseCoverage reads one statement snapshot while the
// caller holds this account's users row FOR UPDATE. Registered mappings are
// append-only, and account purchases serialize on that row. Include every
// registered key the account owns: an alias key grants no additional benefit
// when its target quests are already covered by other permanent rights.
// exactOwnedKeys preserves the internal trusted-key fallback only when none
// of the requested keys has a registered definition.
func loadQuestLicensePurchaseCoverage(ctx context.Context, tx *sqlx.Tx, userID uint32, keys []string) (questLicensePurchaseCoverage, error) {
	var coverage questLicensePurchaseCoverage
	err := tx.QueryRowContext(ctx, `WITH requested_definitions AS (
		SELECT license_key,quest_ids FROM quest_license_definitions WHERE license_key=ANY($2)
	), target_quests AS (
		SELECT DISTINCT unnest(quest_ids) AS quest_id FROM requested_definitions
	), owned_quests AS (
		SELECT DISTINCT unnest(d.quest_ids) AS quest_id
		FROM quest_licenses l
		JOIN quest_license_definitions d ON d.license_key=l.license_key
		WHERE l.user_id=$1
	)
	SELECT
		(SELECT COUNT(*) FROM requested_definitions),
		(SELECT COUNT(*) FROM target_quests),
		(SELECT COUNT(*) FROM target_quests t JOIN owned_quests o USING (quest_id)),
		(SELECT COUNT(*) FROM quest_licenses WHERE user_id=$1 AND license_key=ANY($2))`,
		userID, pq.Array(keys)).Scan(&coverage.registeredKeys, &coverage.targetQuests,
		&coverage.coveredQuests, &coverage.exactOwnedKeys)
	return coverage, err
}

func validQuestLicenseDefinitionKey(key string) bool {
	if len(key) == 0 || len(key) > 64 || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for i := 1; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func canonicalQuestLicenseDefinitions(definitions []questlicense.Definition) ([]questLicenseDefinitionBinding, error) {
	if len(definitions) > questlicense.MaxDefinitions {
		return nil, fmt.Errorf("too many quest license definitions")
	}
	bindings := make([]questLicenseDefinitionBinding, 0, len(definitions))
	seen := make(map[string]bool, len(definitions))
	count := 0
	for _, definition := range definitions {
		if !validQuestLicenseDefinitionKey(definition.Key) || seen[definition.Key] || len(definition.QuestIDs) == 0 {
			return nil, fmt.Errorf("invalid, duplicate or empty quest license definition %q", definition.Key)
		}
		seen[definition.Key] = true
		count += len(definition.QuestIDs)
		if count > questlicense.MaxQuestMappings {
			return nil, fmt.Errorf("too many quest license definition mappings")
		}
		ids := make(pq.Int64Array, len(definition.QuestIDs))
		for i, id := range definition.QuestIDs {
			if id < 1 || id > 65535 {
				return nil, fmt.Errorf("invalid quest ID in license %q", definition.Key)
			}
			ids[i] = int64(id)
		}
		slices.Sort(ids)
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return nil, fmt.Errorf("duplicate quest ID in license %q", definition.Key)
			}
		}
		encoded, err := json.Marshal(struct {
			Key      string  `json:"key"`
			QuestIDs []int64 `json:"quest_ids"`
		}{definition.Key, ids})
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(encoded)
		bindings = append(bindings, questLicenseDefinitionBinding{Key: definition.Key, QuestIDs: ids, Hash: hex.EncodeToString(hash[:])})
	}
	slices.SortFunc(bindings, func(a, b questLicenseDefinitionBinding) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		default:
			return 0
		}
	})
	return bindings, nil
}

// BindDefinitions binds exact permanent-right mappings before the service may
// sell or resolve ownership. Every previously bound key must stay present with
// the same quest IDs; only new keys may be appended. Product deletion and quest
// publication are separate, so archiving a quest never requires deleting its
// historical right definition. It does not grant account ownership or infer
// missing historical mappings from receipts, legacy state or similar names.
func (r *QuestLicenseRepository) BindDefinitions(ctx context.Context, definitions []questlicense.Definition) error {
	bindings, err := canonicalQuestLicenseDefinitions(definitions)
	if err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("quest license repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,$2::integer)`,
		questLicenseDefinitionLockNamespace, questLicenseDefinitionLockVersion); err != nil {
		return err
	}
	var registered []questLicenseDefinitionBinding
	if err := tx.SelectContext(ctx, &registered, `SELECT license_key,quest_ids,mapping_hash
		FROM quest_license_definitions ORDER BY license_key`); err != nil {
		return err
	}
	current := make(map[string]questLicenseDefinitionBinding, len(bindings))
	for _, binding := range bindings {
		current[binding.Key] = binding
	}
	known := make(map[string]bool, len(registered))
	for _, previous := range registered {
		candidate, exists := current[previous.Key]
		if !exists {
			return fmt.Errorf("previously bound quest license %q is missing", previous.Key)
		}
		if previous.Hash != candidate.Hash || !slices.Equal(previous.QuestIDs, candidate.QuestIDs) {
			return fmt.Errorf("previously bound quest license %q changed", previous.Key)
		}
		known[previous.Key] = true
	}
	for _, binding := range bindings {
		if known[binding.Key] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO quest_license_definitions(license_key,quest_ids,mapping_hash)
			VALUES($1,$2,$3)`, binding.Key, binding.QuestIDs, binding.Hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}
