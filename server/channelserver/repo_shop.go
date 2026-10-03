package channelserver

import (
	"github.com/jmoiron/sqlx"
)

// ShopRepository centralizes all database access for shop-related tables.
type ShopRepository struct {
	db *sqlx.DB
}

// NewShopRepository creates a new ShopRepository.
func NewShopRepository(db *sqlx.DB) *ShopRepository {
	return &ShopRepository{db: db}
}

// GetShopItems returns shop items with per-character purchase counts.
func (r *ShopRepository) GetShopItems(shopType uint8, shopID uint32, charID uint32) ([]ShopItem, error) {
	var result []ShopItem
	err := r.db.Select(&result, `SELECT si.id, item_id, cost, quantity, min_hr, min_sr, min_gr, store_level, max_quantity,
        CASE WHEN si.road_weekly_limit AND si.shop_type=10 AND si.shop_id IN (7,8)
                  AND (b.week_start IS NULL OR b.week_start < $4::timestamptz)
             THEN 0 ELSE COALESCE(b.bought, 0) END AS used_quantity,
        road_floors, road_fatalis
        FROM shop_items si LEFT JOIN shop_items_bought b
          ON b.shop_item_id=si.id AND b.character_id=$3
        WHERE shop_type=$1 AND shop_id=$2 ORDER BY si.id
        `, shopType, shopID, charID, TimeWeekStart())
	return result, err
}

// RecordPurchase keeps ordinary shop counts cumulative. Opt-in Road limits use
// the server's Monday 00:00 UTC+9 week. An expired receipt starts a fresh count;
// the upsert is atomic, including simultaneous purchases after a rollover.
// A delayed pre-rollover request must not rewind a newer receipt's week.
func (r *ShopRepository) RecordPurchase(charID, shopItemID, quantity uint32) error {
	_, err := r.db.Exec(`INSERT INTO shop_items_bought (character_id, shop_item_id, bought, week_start)
        VALUES ($1,$2,$3,CASE WHEN COALESCE((SELECT road_weekly_limit
            FROM shop_items WHERE id=$2 AND shop_type=10 AND shop_id IN (7,8)), false)
            THEN $4::timestamptz ELSE NULL END)
        ON CONFLICT (character_id, shop_item_id) DO UPDATE SET
          bought = CASE WHEN EXCLUDED.week_start IS NOT NULL
                          AND (shop_items_bought.week_start IS NULL
                               OR shop_items_bought.week_start < EXCLUDED.week_start)
                        THEN EXCLUDED.bought ELSE shop_items_bought.bought + EXCLUDED.bought END,
          week_start = CASE WHEN EXCLUDED.week_start IS NULL THEN NULL
                            ELSE GREATEST(shop_items_bought.week_start, EXCLUDED.week_start) END
        `, charID, shopItemID, quantity, TimeWeekStart())
	return err
}

// GetFpointItem returns the quantity and fpoints cost for a frontier point item.
func (r *ShopRepository) GetFpointItem(tradeID uint32) (quantity, fpoints int, err error) {
	err = r.db.QueryRow("SELECT quantity, fpoints FROM fpoint_items WHERE id=$1", tradeID).Scan(&quantity, &fpoints)
	return
}

// GetFpointExchangeList returns all frontier point exchange items ordered by buyable status.
func (r *ShopRepository) GetFpointExchangeList() ([]FPointExchange, error) {
	var result []FPointExchange
	err := r.db.Select(&result, `SELECT id, item_type, item_id, quantity, fpoints, buyable FROM fpoint_items ORDER BY buyable DESC`)
	return result, err
}
