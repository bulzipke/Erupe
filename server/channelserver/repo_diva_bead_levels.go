package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// A completed day's wins can close a color to new choices and contributions.
func checkDivaBeadLevelTx(tx *sqlx.Tx, eventID uint32, color int, now time.Time) error {
	var event DivaEvent
	if err := tx.Get(&event, `SELECT EXTRACT(epoch FROM start_time)::bigint start_time,diva_continuous
		FROM events WHERE id=$1 AND event_type='diva'`, eventID); err != nil {
		return err
	}
	if !event.Continuous {
		return nil
	}
	round, err := divaEventBeadsTx(tx, eventID)
	if err != nil {
		return err
	}
	if color < 1 || color > len(round.Effects) {
		return errDivaBeadLocked
	}
	bead, valid := divaBeadDefinitionFor(int(round.Effects[color-1]))
	if !valid {
		return errDivaBeadLocked
	}
	firstDay := divaNoon(event.phaseTime(0))
	colors, err := getDivaWinningColors(context.Background(), tx, eventID, firstDay, divaNoon(now))
	if err != nil {
		return err
	}
	wins := 0
	for _, winner := range colors {
		if int(winner) == color {
			wins++
		}
	}
	if wins >= bead.MaxLevel {
		return errDivaBeadLocked
	}
	return nil
}

// GetDashboardDivaBeadLevels only reads the immutable slot selection and the
// same winners sent to the client. Dashboard requests never create snapshots.
func (r *DivaRepository) GetDashboardDivaBeadLevels(ctx context.Context, event DivaEvent, now time.Time) (map[int]int, error) {
	var stored pq.Int64Array
	if err := r.db.GetContext(ctx, &stored, `SELECT effects FROM diva_bead_rounds WHERE event_id=$1`, event.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Legacy activation without a slot snapshot: names only.
		}
		return nil, err
	}
	effects := make([]int, len(stored))
	for i, id := range stored {
		effects[i] = int(id)
	}
	colors, err := r.GetDivaWinningColorsContext(ctx, event.ID, divaNoon(time.Unix(int64(event.StartTime), 0)), now)
	if err != nil {
		return nil, err
	}
	return DivaBeadLevels(effects, colors), nil
}
