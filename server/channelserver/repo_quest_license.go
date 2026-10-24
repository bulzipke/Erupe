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
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// QuestLicensePurchaseStatus describes a business rejection separately from a
// database failure. These values are internal and are not packet ACK statuses.
type QuestLicensePurchaseStatus uint8

const (
	QuestLicensePurchaseOK QuestLicensePurchaseStatus = iota
	QuestLicensePurchaseUnavailable
	QuestLicensePurchasePriceChanged
	QuestLicensePurchaseConflict
	QuestLicensePurchaseAlreadyOwned
	QuestLicensePurchasePartialOwned
	QuestLicensePurchaseInsufficient
)

// QuestLicensePurchaseRequest contains authenticated identity and a trusted
// quote snapshot. LicenseKeys must not come directly from client-supplied data.
// This backend does not bind the old 16-byte campaign code to a UI preview;
// that integration requires a server-issued quote before exposing a handler.
type QuestLicensePurchaseRequest struct {
	UserID      uint32
	CharacterID uint32
	CampaignID  uint32
	RequestCode string
	QuotedPrice uint32
	LicenseKeys []string
}

// QuestLicenseSale is the current immutable catalog snapshot supplied by the
// service. A committed replay is checked against its original request before
// inspecting current sale availability, price or mapping.
type QuestLicenseSale struct {
	CampaignID  uint32
	Enabled     bool
	Price       uint32
	LicenseKeys []string
	Start       time.Time
	End         time.Time
}

type QuestLicensePurchaseResult struct {
	Status       QuestLicensePurchaseStatus
	PurchaseID   int64
	BalanceAfter uint32
	Replay       bool
}

// QuestLicenseRepository owns account locking, quote/receipt comparison and
// atomic debit plus permanent ownership. It does not grant items, complete
// quests, alter legacy campaign state, or promote character rights to accounts.
type QuestLicenseRepository struct {
	db  *sqlx.DB
	now func() time.Time
}

func NewQuestLicenseRepository(db *sqlx.DB) *QuestLicenseRepository {
	return &QuestLicenseRepository{db: db, now: time.Now}
}

func canonicalQuestLicenseKeys(keys []string) ([]string, string, error) {
	if len(keys) == 0 || len(keys) > 512 {
		return nil, "", fmt.Errorf("invalid quest license count")
	}
	canonical := slices.Clone(keys)
	for _, key := range canonical {
		if len(key) == 0 || len(key) > 96 {
			return nil, "", fmt.Errorf("invalid quest license key length")
		}
		for i := 0; i < len(key); i++ {
			c := key[i]
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
				continue
			}
			if i > 0 && (c == '.' || c == '_' || c == ':' || c == '-') {
				continue
			}
			return nil, "", fmt.Errorf("invalid quest license key")
		}
	}
	slices.Sort(canonical)
	for i := 1; i < len(canonical); i++ {
		if canonical[i] == canonical[i-1] {
			return nil, "", fmt.Errorf("duplicate quest license key")
		}
	}
	// JSON array encoding makes key boundaries unambiguous; input order is not
	// identity. Persist both the canonical array and its hash in the receipt.
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(encoded)
	return canonical, hex.EncodeToString(hash[:]), nil
}

func validQuestLicenseRequestCode(code string) bool {
	if len(code) == 0 || len(code) > 128 {
		return false
	}
	for i := range code {
		if code[i] < 0x21 || code[i] > 0x7e {
			return false
		}
	}
	return true
}

func (r *QuestLicenseRepository) Owned(ctx context.Context, userID uint32, keys []string) ([]string, error) {
	if userID == 0 || userID > math.MaxInt32 {
		return nil, fmt.Errorf("invalid quest license account")
	}
	if len(keys) == 0 {
		return []string{}, nil
	}
	canonical, _, err := canonicalQuestLicenseKeys(keys)
	if err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("quest license repository unavailable")
	}
	owned := []string{}
	err = r.db.SelectContext(ctx, &owned, `SELECT license_key FROM quest_licenses
		WHERE user_id=$1 AND license_key=ANY($2) ORDER BY license_key`, userID, pq.Array(canonical))
	return owned, err
}

// Replay resolves a committed server-issued request token without needing the
// current catalog or trusting a client's price/license mapping. The caller must
// issue and bind tokens on the server before enabling a network purchase flow.
// It never creates a receipt, charges currency, or restores removed licenses.
// found is true for an account's existing receipt, including identity conflicts;
// invalid/unauthenticated identities and absent receipts return found=false.
func (r *QuestLicenseRepository) Replay(ctx context.Context, userID, characterID, campaignID uint32, requestCode string) (QuestLicensePurchaseResult, bool, error) {
	result := QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}
	if userID == 0 || userID > math.MaxInt32 || characterID == 0 || characterID > math.MaxInt32 ||
		campaignID == 0 || campaignID > math.MaxInt32 || !validQuestLicenseRequestCode(requestCode) {
		return result, false, nil
	}
	if r == nil || r.db == nil {
		return result, false, fmt.Errorf("quest license repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return result, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize receipt lookup with purchases on this account, then validate
	// its active character under a shared row lock. No price or sale is read.
	var authenticatedUser, authenticatedCharacter uint32
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&authenticatedUser)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM characters
		WHERE id=$1 AND user_id=$2 AND NOT deleted FOR SHARE`, characterID, userID).Scan(&authenticatedCharacter)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}

	var priorID int64
	var priorCharacter, priorCampaign, priorBalance uint32
	err = tx.QueryRowContext(ctx, `SELECT id,character_id,campaign_id,balance_after
		FROM quest_license_purchases WHERE user_id=$1 AND request_code=$2`, userID, requestCode).
		Scan(&priorID, &priorCharacter, &priorCampaign, &priorBalance)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if priorCharacter != characterID || priorCampaign != campaignID {
		result.Status = QuestLicensePurchaseConflict
		return result, true, nil
	}
	return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: priorID, BalanceAfter: priorBalance, Replay: true}, true, nil
}

func (r *QuestLicenseRepository) Purchase(ctx context.Context, request QuestLicensePurchaseRequest, sale QuestLicenseSale) (QuestLicensePurchaseResult, error) {
	result := QuestLicensePurchaseResult{Status: QuestLicensePurchaseUnavailable}
	keys, hash, err := canonicalQuestLicenseKeys(request.LicenseKeys)
	if err != nil || request.UserID == 0 || request.UserID > math.MaxInt32 ||
		request.CharacterID == 0 || request.CharacterID > math.MaxInt32 ||
		request.CampaignID == 0 || request.CampaignID > math.MaxInt32 ||
		request.QuotedPrice == 0 || request.QuotedPrice > math.MaxUint16 ||
		!validQuestLicenseRequestCode(request.RequestCode) {
		return result, nil
	}
	if r == nil || r.db == nil {
		return result, fmt.Errorf("quest license repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()

	var balance uint32
	// Use the same users row lock as existing premium currency spending. Every
	// character and every product for this account serialize on this one row.
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(gacha_premium,0) FROM users
		WHERE id=$1 FOR UPDATE`, request.UserID).Scan(&balance)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var authenticatedCharacter uint32
	err = tx.QueryRowContext(ctx, `SELECT id FROM characters
		WHERE id=$1 AND user_id=$2 AND NOT deleted FOR SHARE`, request.CharacterID, request.UserID).Scan(&authenticatedCharacter)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}

	var priorID int64
	var priorCharacter, priorCampaign, priorPrice, priorBalance uint32
	var priorHash string
	var priorKeys pq.StringArray
	err = tx.QueryRowContext(ctx, `SELECT id,character_id,campaign_id,price,license_keys,license_hash,balance_after
		FROM quest_license_purchases WHERE user_id=$1 AND request_code=$2`, request.UserID, request.RequestCode).
		Scan(&priorID, &priorCharacter, &priorCampaign, &priorPrice, &priorKeys, &priorHash, &priorBalance)
	if err == nil {
		if priorCharacter != request.CharacterID || priorCampaign != request.CampaignID ||
			priorPrice != request.QuotedPrice || priorHash != hash || !slices.Equal([]string(priorKeys), keys) {
			result.Status = QuestLicensePurchaseConflict
			return result, nil
		}
		return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: priorID, BalanceAfter: priorBalance, Replay: true}, nil
	}
	if err != sql.ErrNoRows {
		return result, err
	}

	currentKeys, currentHash, err := canonicalQuestLicenseKeys(sale.LicenseKeys)
	if err != nil || !sale.Enabled || sale.CampaignID != request.CampaignID ||
		sale.Price == 0 || sale.Price > math.MaxUint16 || sale.Start.IsZero() || sale.End.IsZero() || !sale.End.After(sale.Start) {
		return result, nil
	}
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	if now.Before(sale.Start) || !now.Before(sale.End) {
		return result, nil
	}
	if sale.Price != request.QuotedPrice {
		result.Status = QuestLicensePurchasePriceChanged
		return result, nil
	}
	if currentHash != hash || !slices.Equal(currentKeys, keys) {
		result.Status = QuestLicensePurchaseConflict
		return result, nil
	}

	coverage, err := loadQuestLicensePurchaseCoverage(ctx, tx, request.UserID, keys)
	if err != nil {
		return result, err
	}
	var owned, target int
	switch coverage.registeredKeys {
	case 0:
		// Compatibility for internal callers with wholly unregistered trusted
		// keys. Service purchases bind all definitions before reaching here.
		owned, target = coverage.exactOwnedKeys, len(keys)
	case len(keys):
		if coverage.targetQuests == 0 {
			return result, nil
		}
		owned, target = coverage.coveredQuests, coverage.targetQuests
	default:
		// A partially registered sale has no complete rights contract.
		return result, nil
	}
	if owned == target {
		result.Status = QuestLicensePurchaseAlreadyOwned
		return result, nil
	}
	if owned > 0 {
		// No implicit full-price charge or ad hoc discount for partial bundles.
		result.Status = QuestLicensePurchasePartialOwned
		return result, nil
	}
	if balance < request.QuotedPrice {
		result.Status = QuestLicensePurchaseInsufficient
		return result, nil
	}
	debit, err := tx.ExecContext(ctx, `UPDATE users SET gacha_premium=COALESCE(gacha_premium,0)-$2
		WHERE id=$1 AND COALESCE(gacha_premium,0)>=$2`, request.UserID, request.QuotedPrice)
	if err != nil {
		return result, err
	}
	if changed, e := debit.RowsAffected(); e != nil || changed != 1 {
		return result, fmt.Errorf("quest license debit affected %d accounts: %v", changed, e)
	}
	balance -= request.QuotedPrice
	var purchaseID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO quest_license_purchases
		(user_id,character_id,campaign_id,request_code,price,license_keys,license_hash,balance_after)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, request.UserID, request.CharacterID,
		request.CampaignID, request.RequestCode, request.QuotedPrice, pq.Array(keys), hash, balance).Scan(&purchaseID)
	if err != nil {
		return result, err
	}
	for _, key := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO quest_licenses(user_id,license_key,purchase_id)
			VALUES($1,$2,$3)`, request.UserID, key, purchaseID); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return QuestLicensePurchaseResult{Status: QuestLicensePurchaseOK, PurchaseID: purchaseID, BalanceAfter: balance}, nil
}
