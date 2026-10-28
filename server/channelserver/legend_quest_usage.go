package channelserver

import (
	"bytes"
	"context"
	"erupe-ce/common/legendrasta"
	"erupe-ce/network/mhfpacket"
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"go.uber.org/zap"
	"time"
)

// One native final roster, including other hunters' selected companions.
// Repeated IDs represent distinct NPCs and count separately, up to three.
func decodeLegendQuestRoster(data []byte) ([]int64, bool) {
	if len(data) != 8 || !bytes.Equal(data[:4], []byte("LRQ1")) || data[4] < 1 || data[4] > 3 {
		return nil, false
	}
	ids := make([]int64, int(data[4]))
	for i := range ids {
		id := data[5+i]
		if legendrasta.Name(id) == "" {
			return nil, false
		}
		ids[i] = int64(id)
	}
	for _, padding := range data[5+len(ids):] {
		if padding != 0 {
			return nil, false
		}
	}
	return ids, true
}

type LegendQuestUsageRepo interface {
	RecordRoster(runID string, ids []int64) error
}
type LegendQuestUsageRepository struct{ db *sqlx.DB }

func NewLegendQuestUsageRepository(db *sqlx.DB) *LegendQuestUsageRepository {
	return &LegendQuestUsageRepository{db: db}
}

// A single statement claims the run and increments all entries atomically.
// Concurrent reports from multiple hunters or a new host cannot double count.
const recordLegendQuestRosterSQL = `
WITH receipt AS (
    INSERT INTO legend_rasta_quest_receipts(run_id) VALUES($1)
    ON CONFLICT DO NOTHING RETURNING run_id
), counts AS (
    SELECT id, count(*)::bigint AS uses FROM unnest($2::smallint[]) AS id
    WHERE EXISTS(SELECT 1 FROM receipt) GROUP BY id
)
INSERT INTO legend_rasta_usage_stats(legend_id,usage_count)
SELECT id,uses FROM counts
ON CONFLICT(legend_id) DO UPDATE
SET usage_count=legend_rasta_usage_stats.usage_count+EXCLUDED.usage_count`

func (r *LegendQuestUsageRepository) RecordRoster(runID string, ids []int64) error {
	if r == nil || r.db == nil {
		return nil
	}
	if len(runID) != 32 || len(ids) < 1 || len(ids) > 3 {
		return fmt.Errorf("invalid legend roster receipt")
	}
	for _, id := range ids {
		if id < 1 || id > 21 || legendrasta.Name(uint8(id)) == "" {
			return fmt.Errorf("invalid legend ID")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := r.db.ExecContext(ctx, recordLegendQuestRosterSQL, runID, pq.Array(ids))
	return err
}

func (s *Session) recordLegendQuestRoster(pkt *mhfpacket.MsgSysSetStageBinary) {
	ids, ok := decodeLegendQuestRoster(pkt.RawDataPayload)
	if !ok || s.server == nil || s.server.legendQuestUsageRepo == nil || stageKind(pkt.StageID) != "Qs" {
		return
	}
	stage, exists := s.server.stages.Get(pkt.StageID)
	if !exists {
		return
	}
	stage.RLock()
	_, member := stage.clients[s]
	setup := stage.rawBinaryData[stageBinaryKey{1, 3}]
	_, _, _, quest := decodeQuestRunSetupFromStageBinary(stage.id, 1, 3, setup)
	runID := stage.towerGuardianRunID
	humans := len(stage.clients)
	stage.RUnlock()
	if !member || !quest || runID == "" || humans < 1 || len(ids)+humans > 4 {
		return
	}
	if err := s.server.legendQuestUsageRepo.RecordRoster(runID, ids); err != nil {
		s.logger.Warn("Failed to update legend companion usage", zap.Error(err))
	}
}
