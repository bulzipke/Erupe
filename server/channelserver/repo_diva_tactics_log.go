package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The request has no guild selector. Resolve current accepted membership under
// the same lock order as guild rewards; previous membership and previous UI
// selections can never grant access to another guild's journal.
func (r *DivaRepository) GetDivaTacticsLog(charID uint32, now time.Time) ([]divaTacticsLogEntry, error) {
	if charID == 0 {
		return nil, nil
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.QueryRow(`SELECT id FROM characters WHERE id=$1 FOR SHARE`, charID).Scan(&id); err != nil {
		return nil, err
	}
	event, err := lockDivaMapRewardWindow(tx, now)
	if err != nil {
		return nil, err
	}
	if event.ID == 0 {
		return nil, tx.Commit()
	}
	now, err = lockDivaGuildRewardClock(tx, now)
	if err != nil {
		return nil, err
	}
	var guildID uint32
	err = tx.QueryRow(`SELECT guild_id FROM guild_characters WHERE character_id=$1 AND guild_id>0 AND joined_at IS NOT NULL FOR SHARE`, charID).Scan(&guildID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	enabled, err := settleDivaMapEventTx(tx, event.ID, now)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, tx.Commit()
	}
	var raw []struct {
		Kind   uint8     `db:"kind"`
		CharID uint32    `db:"char_id"`
		Name   string    `db:"name"`
		Value  uint32    `db:"value"`
		At     time.Time `db:"happened_at"`
		Map    uint16    `db:"map_number"`
		Route  uint16    `db:"route"`
	}
	// The accepted result journal is idempotent per departure. Submitted points
	// retain their original value (not a second bonus multiplication), while
	// map area/treasure events exist only after actual hourly settlement. Personal
	// milestones include the character's whole event total, but a crossing only
	// belongs to the guild on that particular accepted result. A transfer never
	// resets the total or exposes the previous guild's milestone rows.
	err = tx.Select(&raw, `WITH area_totals AS (
		SELECT awarded_at,SUM(COUNT(*)) OVER(ORDER BY awarded_at) AS value
		FROM diva_map_area_awards WHERE event_id=$1 AND guild_id=$2 AND awarded_at<=$3
		GROUP BY awarded_at
	), personal_runs AS (
		SELECT r.*,SUM(r.points) OVER(PARTITION BY r.char_id ORDER BY r.submitted_at,r.run_key
			ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS cumulative
		FROM diva_interception_runs r
		WHERE r.event_id=$1 AND r.submitted_at<=$3 AND r.char_id IN (
			SELECT char_id FROM diva_interception_runs WHERE event_id=$1 AND guild_id=$2 AND submitted_at<=$3)
	), milestone_bounds AS (
		SELECT char_id,run_key,submitted_at,
			FLOOR(LEAST(cumulative-points,2147483647)/$4::numeric)::bigint+1 AS first_step,
			FLOOR(LEAST(cumulative,2147483647)/$4::numeric)::bigint AS last_step
		FROM personal_runs WHERE guild_id=$2
	), milestone_runs AS (
		SELECT * FROM milestone_bounds WHERE first_step<=last_step
		ORDER BY submitted_at DESC,char_id,run_key DESC LIMIT $5
	), entries AS (
		SELECT CASE WHEN c.route=0 THEN 0 ELSE 6 END AS kind,c.char_id,COALESCE(ch.name,'') AS name,
		c.points AS value,c.submitted_at AS happened_at,c.map_number,c.route,c.run_key::text AS tie_key
		FROM diva_map_contributions c JOIN diva_map_departures d USING(char_id,event_id,run_key)
		JOIN characters ch ON ch.id=c.char_id
		WHERE c.event_id=$1 AND c.guild_id=$2 AND c.submitted_at<=$3 AND c.points BETWEEN 1 AND 2147483647
		AND d.eligible AND d.guild_id=c.guild_id AND d.map_number=c.map_number AND d.route=c.route
		UNION ALL
		SELECT 2,r.char_id,COALESCE(ch.name,''),n.step*$4,r.submitted_at,0,0,
			r.run_key::text||':'||n.step::text
		FROM milestone_runs r JOIN characters ch ON ch.id=r.char_id
		CROSS JOIN LATERAL generate_series(GREATEST(r.first_step,r.last_step-$5+1),r.last_step) AS n(step)
		UNION ALL
		SELECT 1,0,'',value,awarded_at,0,0,'' FROM area_totals
		UNION ALL
		SELECT 4,0,'',0,awarded_at,map_number,coordinate,map_number::text||':'||coordinate::text
		FROM diva_map_area_awards WHERE event_id=$1 AND guild_id=$2 AND is_branch AND awarded_at<=$3
		UNION ALL
		SELECT 3,0,'',0,happened_at,map_number,0,page_tick::text FROM diva_map_invasions
		WHERE event_id=$1 AND guild_id=$2 AND happened_at<=$3
	) SELECT kind,char_id,name,value,happened_at,map_number,route FROM entries
	ORDER BY happened_at DESC,kind DESC,char_id,CASE WHEN kind=2 THEN value ELSE 0 END DESC,tie_key
	LIMIT $5`, event.ID, guildID, now, divaTacticsLogPersonalStep, divaTacticsLogMaxRows)
	if err != nil {
		return nil, err
	}
	rows := make([]divaTacticsLogEntry, 0, len(raw))
	maps := make(map[uint16]DivaInterceptionMap)
	for _, item := range raw {
		row := divaTacticsLogEntry{Kind: item.Kind, CharID: item.CharID, Value: item.Value, At: item.At}
		if row.Kind == 0 || row.Kind == 2 || row.Kind == 6 {
			row.Name = divaTacticsLogName(item.Name)
		}
		if row.Kind == 6 || row.Kind == 4 {
			m, cached := maps[item.Map]
			if !cached {
				m, err = loadDivaTreasureMapTx(tx, event.ID, guildID, item.Map)
				if err != nil {
					return nil, err
				}
				maps[item.Map] = m
			}
			var number uint8
			for _, node := range m.States[0].Nodes {
				if node.BranchQuests[0] == 0 {
					continue
				}
				number++
				if node.Coordinate == item.Route {
					row.Branch = number
					break
				}
			}
			if row.Branch == 0 {
				return nil, fmt.Errorf("diva log route is absent from immutable map")
			}
			if row.Kind == 4 {
				row.Branch = 0
			}
		}
		rows = append(rows, row)
	}
	rows = divaTacticsLogWithDates(rows)
	if _, err = divaTacticsLogPayload(rows); err != nil {
		return nil, err
	}
	return rows, tx.Commit()
}
