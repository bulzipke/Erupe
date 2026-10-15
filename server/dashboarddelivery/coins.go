// Package dashboarddelivery shares durable currency settlement between the
// operator API and channel login/logout. It never writes character save blobs.
package dashboarddelivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

const MaxCoins = int64(1<<31 - 1)

// Online must be called while holding the users row FOR UPDATE. Bind and
// release of channel connections take that same lock.
func Online(ctx context.Context, tx *sqlx.Tx, userID uint32) (bool, error) {
	var online bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sign_sessions WHERE user_id=$1 AND (server_id IS NOT NULL OR char_id>0))
		OR EXISTS(SELECT 1 FROM dashboard_coin_sessions WHERE user_id=$1)`, userID).Scan(&online)
	return online, err
}

// SettleLocked credits pending receipts in submission order only if all
// account connections have finished saving. The caller owns the account row
// lock and transaction, so credit and receipt state commit together.
// A balance overflow leaves the remaining receipts pending, never discarded.
func SettleLocked(ctx context.Context, tx *sqlx.Tx, userID uint32) error {
	online, err := Online(ctx, tx, userID)
	if err != nil || online {
		return err
	}
	var balance int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(gacha_premium,0) FROM users WHERE id=$1`, userID).Scan(&balance); err != nil {
		return err
	}
	var grants []struct {
		ID       string `db:"request_id"`
		Quantity int64  `db:"quantity"`
	}
	if err = tx.SelectContext(ctx, &grants, `SELECT request_id::text,quantity FROM dashboard_item_deliveries
		WHERE user_id=$1 AND delivery_kind='gacha_premium' AND pending
		ORDER BY created_at,request_id FOR UPDATE`, userID); err != nil {
		return err
	}
	for _, grant := range grants {
		if balance < 0 || balance > MaxCoins-grant.Quantity {
			break
		}
		balance += grant.Quantity
		if _, err = tx.ExecContext(ctx, `UPDATE users SET gacha_premium=$2 WHERE id=$1`, userID, balance); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE dashboard_item_deliveries SET pending=false,coin_balance=$2,credited_at=NOW()
			WHERE request_id=$1::uuid AND pending`, grant.ID, balance); err != nil {
			return err
		}
	}
	return nil
}

func CoinNotice(quantity int) string {
	return fmt.Sprintf("보상으로 금빛 금화G가 %d개 지급되었습니다.", quantity)
}

// SendNextNotice locks one recipient receipt, sends one line, then consumes it.
// A failed send rolls back without consuming. Row locks prevent two channel
// connections from announcing the same grant. send must complete the socket
// write, not merely put a message in a droppable queue. The game has no chat
// render acknowledgement: a socket failure or ambiguous COMMIT can still replay
// a notice, but can never replay the currency credit.
func SendNextNotice(ctx context.Context, db *sqlx.DB, userID, charID uint32, send func(string) error) (bool, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id string
	var quantity int
	err = tx.QueryRowContext(ctx, `SELECT request_id::text,quantity FROM dashboard_item_deliveries
		WHERE user_id=$1 AND character_id=$2 AND delivery_kind='gacha_premium' AND NOT pending AND notified_at IS NULL
		ORDER BY created_at,request_id LIMIT 1 FOR UPDATE`, userID, charID).Scan(&id, &quantity)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = send(CoinNotice(quantity)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE dashboard_item_deliveries SET notified_at=NOW() WHERE request_id=$1::uuid`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
