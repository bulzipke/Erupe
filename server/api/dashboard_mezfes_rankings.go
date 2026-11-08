package api

import (
	"context"

	"erupe-ce/server/channelserver"
)

// MezFesRankEntry is one hunter's best real (ticketed) play of one MezFes
// minigame. BestScore is that play's score total from the result screen (nil
// if the client never sent it) and BestPoints the festival points it awarded;
// practice plays are never logged, and each hunter of a group game has their
// own entry.
type MezFesRankEntry struct {
	StageID    int    `db:"stage_id" json:"stageId"`
	GameName   string `json:"gameName"`
	Group      bool   `json:"group"`
	Name       string `db:"character_name" json:"name"`
	BestScore  *int64 `db:"best_score" json:"bestScore"`
	BestRank   *int64 `db:"best_rank" json:"bestRank"` // 0 C, 1 B, 2 A, 3 S
	BestPoints int64  `db:"best_points" json:"bestPoints"`
	Plays      int64  `db:"plays" json:"plays"`
}

// Top five hunters per minigame by their single best score total; plays the
// client sent no total for rank below by festival points. Ties go to whoever
// reached the score first.
const dashboardMezFesRankingsQuery = `
WITH scored AS (
    SELECT p.stage_id, p.character_id, p.raw_score, p.result_rank, p.points, p.scored_at,
        ROW_NUMBER() OVER (PARTITION BY p.stage_id, p.character_id
            ORDER BY p.raw_score DESC NULLS LAST, CASE WHEN p.raw_score IS NULL THEN p.points END DESC NULLS LAST,
                p.scored_at ASC, p.id ASC) AS pick,
        COUNT(*) OVER (PARTITION BY p.stage_id, p.character_id) AS plays
    FROM mezfes_plays p
    WHERE p.points IS NOT NULL AND NOT p.cancelled AND p.stage_id BETWEEN 463 AND 469
), ranked AS (
    SELECT s.stage_id, c.name AS character_name, s.raw_score::bigint AS best_score,
        s.result_rank::bigint AS best_rank,
        s.points::bigint AS best_points, s.plays::bigint AS plays,
        ROW_NUMBER() OVER (PARTITION BY s.stage_id
            ORDER BY s.raw_score DESC NULLS LAST, CASE WHEN s.raw_score IS NULL THEN s.points END DESC NULLS LAST,
                s.scored_at ASC, c.id ASC) AS place
    FROM scored s JOIN characters c ON c.id = s.character_id
    WHERE s.pick = 1 AND c.deleted = false AND COALESCE(c.is_new_character, false) = false
      AND c.name IS NOT NULL AND c.name <> ''
)
SELECT stage_id, character_name, best_score, best_rank, best_points, plays FROM ranked
WHERE place <= 5 ORDER BY stage_id, place`

func (s *APIServer) loadDashboardMezFesRankings(ctx context.Context, rankings *DashboardRankings) error {
	if err := s.db.SelectContext(ctx, &rankings.MezFesGames, dashboardMezFesRankingsQuery); err != nil {
		return err
	}
	for i := range rankings.MezFesGames {
		entry := &rankings.MezFesGames[i]
		entry.GameName = channelserver.MezFesGameNameKO(uint16(entry.StageID))
		entry.Group = channelserver.MezFesGameIsGroup(uint16(entry.StageID))
	}
	return nil
}
