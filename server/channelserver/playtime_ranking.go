package channelserver

import (
	"encoding/binary"
	"fmt"

	cfg "erupe-ce/config"
	"erupe-ce/server/channelserver/compression/nullcomp"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

// extractPlaytimeFromSavedata reads the cumulative playtime maintained by the
// client. nullcomp transparently returns old uncompressed saves unchanged.
func extractPlaytimeFromSavedata(mode cfg.Mode, savedata []byte) (uint32, error) {
	decompressed, err := nullcomp.DecompressWithLimit(savedata, saveDataMaxDecompressedPayload)
	if err != nil {
		return 0, fmt.Errorf("decompress savedata: %w", err)
	}
	pointers := getPointers(mode)
	offset, ok := pointers[pPlaytime]
	if !ok || offset < 0 || offset+saveFieldPlaytime > len(decompressed) {
		return 0, fmt.Errorf("playtime offset unavailable for mode %s or save length %d", mode, len(decompressed))
	}
	return binary.LittleEndian.Uint32(decompressed[offset : offset+saveFieldPlaytime]), nil
}

// BackfillPlaytimeRankings copies cumulative playtime out of existing savedata
// exactly once. Successfully processed rows become non-NULL and are skipped on
// later startups; malformed saves stay NULL so they never publish a false rank.
func BackfillPlaytimeRankings(db *sqlx.DB, mode cfg.Mode, logger *zap.Logger) error {
	rows, err := db.Query(`
		SELECT id, savedata
		FROM characters
		WHERE playtime_seconds IS NULL
		  AND savedata IS NOT NULL
		  AND deleted = false
		  AND COALESCE(is_new_character, false) = false
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("query playtime backfill candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Close error is non-actionable here

	type playtimeUpdate struct {
		charID   uint32
		playtime uint32
	}
	updates := make([]playtimeUpdate, 0)
	skipped := 0
	for rows.Next() {
		var charID uint32
		var savedata []byte
		if err := rows.Scan(&charID, &savedata); err != nil {
			return fmt.Errorf("scan playtime backfill candidate: %w", err)
		}

		playtime, extractErr := extractPlaytimeFromSavedata(mode, savedata)
		if extractErr != nil {
			skipped++
			if skipped <= 5 {
				logger.Warn("Playtime ranking backfill skipped malformed savedata",
					zap.Uint32("charID", charID), zap.Error(extractErr))
			}
			continue
		}
		updates = append(updates, playtimeUpdate{charID: charID, playtime: playtime})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate playtime backfill candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close playtime backfill rows: %w", err)
	}

	if len(updates) > 0 {
		tx, err := db.Beginx()
		if err != nil {
			return fmt.Errorf("begin playtime backfill: %w", err)
		}
		defer tx.Rollback() //nolint:errcheck // rollback is a no-op after commit
		stmt, err := tx.Prepare(`UPDATE characters
			SET playtime_seconds=$1
			WHERE id=$2 AND playtime_seconds IS NULL`)
		if err != nil {
			return fmt.Errorf("prepare playtime backfill update: %w", err)
		}
		defer stmt.Close() //nolint:errcheck // statement close error is non-actionable
		for _, update := range updates {
			if _, err := stmt.Exec(update.playtime, update.charID); err != nil {
				return fmt.Errorf("update playtime for character %d: %w", update.charID, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit playtime backfill: %w", err)
		}
	}

	if len(updates) > 0 || skipped > 0 {
		logger.Info("Playtime ranking backfill completed",
			zap.Int("backfilled", len(updates)), zap.Int("skipped", skipped))
	}
	return nil
}
