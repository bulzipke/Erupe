package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

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
