package channelserver

import (
	"errors"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var errDivaShopUnavailable = errors.New("diva shop round or exchange allowance unavailable")

type exchangeShopPurchase struct {
	ItemHash, Quantity uint32
}

// Optional extension: in-memory repositories for unrelated shops keep their
// existing interface. The production repository handles the whole batch in one
// transaction so an exhausted item cannot leave partial purchase receipts.
type divaShopContextRepository interface {
	GetDivaShopItems(shopID, charID uint32, mode int) ([]ShopItem, error)
	RecordExchangeShopPurchases(charID uint32, purchases []exchangeShopPurchase, mode int) error
}

func divaShopRoundTx(tx *sqlx.Tx, mode int, now time.Time) (DivaEvent, time.Time, error) {
	if mode != -1 && mode != 2 && mode != 3 {
		return DivaEvent{}, now, nil
	}
	if _, err := tx.Exec("SELECT pg_advisory_xact_lock(1146507841)"); err != nil {
		return DivaEvent{}, now, err
	}
	if now.IsZero() {
		if err := tx.QueryRow("SELECT clock_timestamp()").Scan(&now); err != nil {
			return DivaEvent{}, now, err
		}
	}
	event, err := ensureDivaEventTx(tx, now, mode)
	return event, now, err
}

func (r *ShopRepository) GetDivaShopItems(shopID, charID uint32, mode int) ([]ShopItem, error) {
	return r.getDivaShopItemsAt(shopID, charID, mode, time.Time{})
}

func (r *ShopRepository) getDivaShopItemsAt(shopID, charID uint32, mode int, now time.Time) ([]ShopItem, error) {
	items, err := r.getShopItems(8, shopID, charID)
	if err != nil || shopID != 7 || len(items) == 0 {
		return items, err
	}
	var flagged []uint32
	if err = r.db.Select(&flagged, `SELECT id FROM shop_items
		WHERE shop_type=8 AND shop_id=7 AND diva_round_limit`); err != nil {
		return nil, err
	}
	if len(flagged) == 0 {
		return items, nil
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	event, _, err := divaShopRoundTx(tx, mode, now)
	if err != nil {
		return nil, err
	}
	counts := make(map[uint32]uint16, len(flagged))
	for _, id := range flagged {
		counts[id] = 0
	}
	if event.ID != 0 {
		var receipts []struct {
			ID    uint32 `db:"shop_item_id"`
			Count uint16 `db:"bought"`
		}
		if err = tx.Select(&receipts, `SELECT shop_item_id,bought FROM diva_shop_purchases
			WHERE event_id=$1 AND char_id=$2`, event.ID, charID); err != nil {
			return nil, err
		}
		for _, p := range receipts {
			if _, ok := counts[p.ID]; ok {
				counts[p.ID] = p.Count
			}
		}
	}
	for i := range items {
		if count, ok := counts[items[i].ID]; ok {
			items[i].UsedQuantity = count
		}
	}
	return items, tx.Commit()
}

func (r *ShopRepository) RecordExchangeShopPurchases(charID uint32, purchases []exchangeShopPurchase, mode int) error {
	return r.recordExchangeShopPurchasesAt(charID, purchases, mode, time.Time{})
}

func (r *ShopRepository) recordExchangeShopPurchasesAt(charID uint32, purchases []exchangeShopPurchase, mode int, now time.Time) error {
	if len(purchases) == 0 {
		return nil
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	purchases = append([]exchangeShopPurchase(nil), purchases...)
	sort.SliceStable(purchases, func(i, j int) bool { return purchases[i].ItemHash < purchases[j].ItemHash })
	ids := make([]uint32, 0, len(purchases))
	for _, p := range purchases {
		ids = append(ids, p.ItemHash)
	}
	var limits []struct {
		ID  uint32 `db:"id"`
		Max uint16 `db:"max_quantity"`
	}
	if err = tx.Select(&limits, `SELECT id,max_quantity FROM shop_items
		WHERE id=ANY($1) AND shop_type=8 AND shop_id=7 AND diva_round_limit
		ORDER BY id FOR SHARE`, pq.Array(ids)); err != nil {
		return err
	}
	limited := make(map[uint32]uint16, len(limits))
	for _, l := range limits {
		limited[l.ID] = l.Max
	}
	var event DivaEvent
	if len(limited) != 0 {
		var character uint32
		if err = tx.Get(&character, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID); err != nil {
			return err
		}
		event, now, err = divaShopRoundTx(tx, mode, now)
		if err != nil {
			return err
		}
		if !divaBattleSongPhase(event, now) {
			return errDivaShopUnavailable
		}
	}
	for _, p := range purchases {
		if p.ItemHash == 0 || p.Quantity == 0 {
			continue
		}
		if maximum, ok := limited[p.ItemHash]; ok {
			if p.Quantity > uint32(maximum) {
				return errDivaShopUnavailable
			}
			result, err := tx.Exec(`INSERT INTO diva_shop_purchases(event_id,char_id,shop_item_id,bought)
				VALUES($1,$2,$3,$4) ON CONFLICT(event_id,char_id,shop_item_id) DO UPDATE SET
				bought=diva_shop_purchases.bought+EXCLUDED.bought
				WHERE diva_shop_purchases.bought+EXCLUDED.bought<=$5`,
				event.ID, charID, p.ItemHash, p.Quantity, maximum)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return errDivaShopUnavailable
			}
		} else if err = recordShopPurchase(tx, charID, p.ItemHash, p.Quantity, TimeWeekStart()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
