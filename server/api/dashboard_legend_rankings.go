package api

import (
	"context"
	"erupe-ce/common/legendrasta"
)

type LegendUsageRankEntry struct {
	ID   uint8  `json:"id" db:"legend_id"`
	Name string `json:"name"`
	Uses int64  `json:"uses" db:"usage_count"`
}

const dashboardLegendUsageQuery = `SELECT legend_id,usage_count FROM legend_rasta_usage_stats ORDER BY usage_count DESC,legend_id ASC LIMIT 14`

func (s *APIServer) loadDashboardLegendUsage(ctx context.Context, rankings *DashboardRankings) error {
	if err := s.db.SelectContext(ctx, &rankings.LegendRastas, dashboardLegendUsageQuery); err != nil {
		return err
	}
	for i := range rankings.LegendRastas {
		rankings.LegendRastas[i].Name = legendrasta.Name(rankings.LegendRastas[i].ID)
	}
	return nil
}
