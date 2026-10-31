package api

import (
	"context"
	"fmt"
)

// ProductPopularityEntry exposes only aggregate counts, never purchaser IDs.
type ProductPopularityEntry struct {
	ID    int    `json:"id" db:"id"`
	Name  string `json:"name" db:"name"`
	Count int64  `json:"count" db:"count"`
}

const dashboardPopularGachasQuery = `
    SELECT t.gacha_id AS id,
        COALESCE(NULLIF(BTRIM(g.name),''),'뽑기 #'||t.gacha_id::text) AS name,
        t.draw_count AS count
    FROM gacha_play_totals t LEFT JOIN gacha_shop g ON g.id=t.gacha_id
    ORDER BY t.draw_count DESC,t.gacha_id ASC LIMIT 14`

// One committed receipt is one purchase, regardless of reward item count or
// later claims. The purchase flow already admits only paid kit categories 6/7.
// Do not re-check current catalog dates/categories: past purchases still count.
const dashboardPopularKitsQuery = `
    SELECT p.campaign_id AS id,
        COALESCE(NULLIF(BTRIM(c.title),''),'키트 #'||p.campaign_id::text) AS name,
        COUNT(*)::bigint AS count
    FROM campaign_coin_purchases p LEFT JOIN campaigns c ON c.id=p.campaign_id
    GROUP BY p.campaign_id,c.title
    ORDER BY count DESC,p.campaign_id ASC LIMIT 14`

func (s *APIServer) loadDashboardShopRankings(ctx context.Context, rankings *DashboardRankings) error {
	if err := s.db.SelectContext(ctx, &rankings.PopularGachas, dashboardPopularGachasQuery); err != nil {
		return fmt.Errorf("popular gacha rankings: %w", err)
	}
	if err := s.db.SelectContext(ctx, &rankings.PopularKits, dashboardPopularKitsQuery); err != nil {
		return fmt.Errorf("popular kit rankings: %w", err)
	}
	return nil
}
