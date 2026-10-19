package channelserver

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

const campaignCoinRewardBase uint32 = 0x40000000
const (
	campaignCoinOK           uint32 = 0
	campaignCoinInsufficient uint32 = 0x434f0001
	campaignCoinPriceChanged uint32 = 0x434f0002
	campaignCoinPending      uint32 = 0x434f0003
	campaignCoinUnavailable  uint32 = 0x434f0004
)

// Existing 16-byte code field: ! + four hex price digits + eleven URL-safe
// base64 nonce characters (64 random bits). No new opcode or packet length.
func campaignCoinQuote(code string) (int, bool) {
	if len(code) != 16 || code[0] != '!' {
		return 0, false
	}
	n, err := strconv.ParseUint(code[1:5], 16, 16)
	if err != nil || n == 0 {
		return 0, false
	}
	for _, c := range code[5:] {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return 0, false
		}
	}
	return int(n), true
}

func purchaseCampaignCoins(ctx context.Context, db *sqlx.DB, userID, charID, campaignID uint32, code string) (uint32, error) {
	quote, valid := campaignCoinQuote(code)
	if !valid || userID == 0 || charID == 0 {
		return campaignCoinUnavailable, nil
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return campaignCoinUnavailable, err
	}
	defer tx.Rollback()
	var balance int
	// Same account lock as login awards and ordinary coin spending. Verify
	// ownership from the authenticated Session, never from request data.
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(u.gacha_premium,0) FROM users u
        WHERE u.id=$1 AND EXISTS(SELECT 1 FROM characters c WHERE c.id=$2 AND c.user_id=u.id AND NOT c.deleted)
        FOR UPDATE OF u`, userID, charID).Scan(&balance)
	if err != nil {
		return campaignCoinUnavailable, err
	}
	var priorCampaign, priorCharacter uint32
	err = tx.QueryRowContext(ctx, `SELECT campaign_id,character_id FROM campaign_coin_purchases
        WHERE user_id=$1 AND request_code=$2`, userID, code).Scan(&priorCampaign, &priorCharacter)
	if err == nil {
		if priorCampaign != campaignID || priorCharacter != charID {
			return campaignCoinUnavailable, nil
		}
		return campaignCoinOK, nil // Committed replay, even after price/date changes.
	}
	if err != sql.ErrNoRows {
		return campaignCoinUnavailable, err
	}
	var price int
	var start, end time.Time
	err = tx.QueryRowContext(ctx, `SELECT p.price,c.start_time,c.end_time FROM campaign_coin_prices p
        JOIN campaigns c ON c.id=p.campaign_id WHERE c.id=$1 AND c.reward_type=1 AND c.receive_type=1 AND c.stamps=1
        AND EXISTS(SELECT 1 FROM campaign_category_links l JOIN campaign_categories cat ON cat.id=l.category_id
          WHERE l.campaign_id=c.id AND cat.type=1 AND cat.id IN(6,7))
        AND EXISTS(SELECT 1 FROM campaign_rewards r WHERE r.campaign_id=c.id)
        AND NOT EXISTS(SELECT 1 FROM campaign_rewards r WHERE r.campaign_id=c.id AND (r.item_type<>7 OR r.quantity<=0 OR r.quantity>65535 OR r.item_id<=0 OR r.item_id>65535))
        FOR SHARE OF p,c`, campaignID).Scan(&price, &start, &end)
	if err == sql.ErrNoRows {
		return campaignCoinUnavailable, nil
	}
	if err != nil {
		return campaignCoinUnavailable, err
	}
	if now := time.Now(); now.Before(start) || !now.Before(end) {
		return campaignCoinUnavailable, nil
	}
	if price != quote {
		return campaignCoinPriceChanged, nil
	}
	var pending bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM campaign_coin_purchases p
        JOIN campaign_coin_rewards r ON r.purchase_id=p.id
        WHERE p.character_id=$1 AND p.campaign_id=$2 AND NOT r.claimed)
        OR (EXISTS(SELECT 1 FROM campaign_state WHERE character_id=$1 AND campaign_id=$2)
        AND EXISTS(SELECT 1 FROM campaign_rewards r WHERE r.campaign_id=$2 AND NOT EXISTS
          (SELECT 1 FROM campaign_rewards_claimed rc WHERE rc.reward_id=r.id AND rc.character_id=$1)))`, charID, campaignID).Scan(&pending)
	if err != nil {
		return campaignCoinUnavailable, err
	}
	if pending {
		return campaignCoinPending, nil
	}
	if balance < price {
		return campaignCoinInsufficient, nil
	}
	balance -= price
	if _, err = tx.ExecContext(ctx, `UPDATE users SET gacha_premium=$2 WHERE id=$1`, userID, balance); err != nil {
		return campaignCoinUnavailable, err
	}
	var purchaseID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO campaign_coin_purchases(user_id,character_id,campaign_id,request_code,price,balance_after)
        VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, userID, charID, campaignID, code, price, balance).Scan(&purchaseID)
	if err != nil {
		return campaignCoinUnavailable, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO campaign_coin_rewards(purchase_id,item_type,quantity,item_id)
        SELECT $1,item_type,quantity,item_id FROM campaign_rewards WHERE campaign_id=$2 ORDER BY id`, purchaseID, campaignID)
	if err != nil {
		return campaignCoinUnavailable, err
	}
	if n, e := result.RowsAffected(); e != nil || n == 0 {
		return campaignCoinUnavailable, fmt.Errorf("empty campaign purchase: %d, %v", n, e)
	}
	if err = tx.Commit(); err != nil {
		return campaignCoinUnavailable, err
	}
	return campaignCoinOK, nil
}

func handleCampaignCoinPurchase(s *Session, pkt *mhfpacket.MsgMhfApplyCampaign) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := purchaseCampaignCoins(ctx, s.server.db, s.userID, s.charID, pkt.CampaignID, pkt.Code)
	if err != nil {
		s.logger.Warn("Campaign coin purchase failed", zap.Uint32("campaign_id", pkt.CampaignID), zap.Error(err))
	}
	bf := byteframe.NewByteFrame()
	bf.WriteUint32(status)
	if status == campaignCoinOK {
		doAckSimpleSucceed(s, pkt.AckHandle, bf.Data())
	} else {
		doAckSimpleFail(s, pkt.AckHandle, bf.Data())
	}
}

func campaignCoinItems(db *sqlx.DB, charID, campaignID uint32) ([]CampaignReward, error) {
	var items []CampaignReward
	err := db.Select(&items, `SELECT r.id,r.item_type,r.quantity,r.item_id,TO_TIMESTAMP(0) AS deadline
        FROM campaign_coin_rewards r JOIN campaign_coin_purchases p ON p.id=r.purchase_id
        WHERE p.character_id=$1 AND p.campaign_id=$2 AND NOT r.claimed ORDER BY r.id`, charID, campaignID)
	return items, err
}

func writeCampaignItems(bf *byteframe.ByteFrame, items []CampaignReward) {
	bf.WriteUint16(uint16(len(items)))
	for _, item := range items {
		bf.WriteUint32(item.ID)
		bf.WriteUint16(item.ItemType)
		bf.WriteUint16(item.Quantity)
		bf.WriteUint16(item.ItemID)
		bf.WriteUint16(0)
		bf.WriteUint32(0)
		bf.WriteUint32(uint32(item.Deadline.Unix()))
	}
}

func acquireCampaignCoins(ctx context.Context, db *sqlx.DB, userID, charID uint32, ids []uint32) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner uint32
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND EXISTS
        (SELECT 1 FROM characters WHERE id=$2 AND user_id=$1 AND NOT deleted) FOR UPDATE`, userID, charID).Scan(&owner); err != nil {
		return err
	}
	for _, id := range ids {
		if id < campaignCoinRewardBase {
			return fmt.Errorf("mixed campaign reward batch")
		}
		// Claimed receipts remain valid for acknowledgement retries, but can
		// never identify a different (later) purchase's rewards.
		result, e := tx.ExecContext(ctx, `UPDATE campaign_coin_rewards r SET claimed=true
            FROM campaign_coin_purchases p WHERE r.id=$1 AND r.purchase_id=p.id AND p.character_id=$2 AND p.user_id=$3`, id, charID, userID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return fmt.Errorf("unknown campaign purchase receipt")
		}
	}
	return tx.Commit()
}
