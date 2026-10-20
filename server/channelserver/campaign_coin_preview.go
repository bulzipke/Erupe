package channelserver

import (
	"context"
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

// Priced-kit information only: the high bit cannot identify a PostgreSQL
// positive INT campaign. The ordinary enumerate/claim path never receives
// preview rows or receipt IDs. The native sender owns a 16 KiB reply buffer.
const campaignPreviewFlag uint32 = 0x80000000
const campaignPreviewMagic uint32 = 0x4b505631 // KPV1
const campaignPreviewMaxItems = (0x4000 - 8) / 4

type campaignPreviewItem struct {
	ItemID   uint16 `db:"item_id"`
	Quantity uint16 `db:"quantity"`
	Price    uint16 `db:"price"`
}

func campaignCoinPreview(ctx context.Context, db *sqlx.DB, userID, charID, campaignID uint32) ([]campaignPreviewItem, error) {
	if userID == 0 || charID == 0 || campaignID == 0 || campaignID >= campaignPreviewFlag {
		return nil, fmt.Errorf("invalid preview identity")
	}
	// One statement gives price and every item the same snapshot. No balances,
	// entitlements, claimed flags or native campaign item cache are modified.
	var items []campaignPreviewItem
	err := db.SelectContext(ctx, &items, `SELECT r.item_id,r.quantity,p.price
		FROM campaigns c JOIN campaign_coin_prices p ON p.campaign_id=c.id
		JOIN campaign_rewards r ON r.campaign_id=c.id
		WHERE c.id=$1 AND c.reward_type=1 AND c.receive_type=1 AND c.stamps=1
		AND c.start_time<=CURRENT_TIMESTAMP AND CURRENT_TIMESTAMP<c.end_time
		AND p.price BETWEEN 1 AND 65535
		AND EXISTS(SELECT 1 FROM characters ch WHERE ch.id=$2 AND ch.user_id=$3 AND NOT ch.deleted)
		AND EXISTS(SELECT 1 FROM campaign_category_links l JOIN campaign_categories cat ON cat.id=l.category_id
			WHERE l.campaign_id=c.id AND cat.type=1 AND cat.id IN(6,7))
		AND NOT EXISTS(SELECT 1 FROM campaign_rewards bad WHERE bad.campaign_id=c.id
			AND (bad.item_type<>7 OR bad.item_id<=0 OR bad.item_id>65535 OR bad.quantity<=0 OR bad.quantity>65535))
		ORDER BY r.id LIMIT $4`, campaignID, charID, userID, campaignPreviewMaxItems+1)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > campaignPreviewMaxItems {
		return nil, fmt.Errorf("unavailable preview catalog")
	}
	return items, nil
}

func writeCampaignPreview(bf *byteframe.ByteFrame, items []campaignPreviewItem) error {
	if len(items) == 0 || len(items) > campaignPreviewMaxItems {
		return fmt.Errorf("invalid preview item count")
	}
	for _, item := range items {
		if item.ItemID == 0 || item.Quantity == 0 || item.Price == 0 || item.Price != items[0].Price {
			return fmt.Errorf("invalid preview row")
		}
	}
	bf.WriteUint32(campaignPreviewMagic)
	bf.WriteUint16(items[0].Price)
	bf.WriteUint16(uint16(len(items)))
	for _, item := range items {
		bf.WriteUint16(item.ItemID)
		bf.WriteUint16(item.Quantity)
	}
	return nil
}

func handleCampaignCoinPreview(s *Session, pkt *mhfpacket.MsgMhfEnumerateItem) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	items, err := campaignCoinPreview(ctx, s.server.db, s.userID, s.charID, pkt.CampaignID&^campaignPreviewFlag)
	bf := byteframe.NewByteFrame()
	if err == nil {
		err = writeCampaignPreview(bf, items)
	}
	if err != nil {
		s.logger.Debug("Campaign preview unavailable", zap.Uint32("campaign_id", pkt.CampaignID&^campaignPreviewFlag), zap.Error(err))
		doAckBufFail(s, pkt.AckHandle, make([]byte, 4))
		return
	}
	doAckBufSucceed(s, pkt.AckHandle, bf.Data())
}
