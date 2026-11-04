package api

import (
	"context"
	"strings"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/server/channelserver"
	"github.com/lib/pq"
)

// DashboardDivaBeads describes a character's active battle-song activation.
// Effect names come from the activation, not the current selectable catalog.
type DashboardDivaBeads struct {
	Round     uint32    `json:"round"`
	Beads     []string  `json:"beads"`
	Levels    []int     `json:"levels,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type dashboardDivaBeadRow struct {
	CharID      uint32    `db:"char_id"`
	EventID     uint32    `db:"event_id"`
	StartTime   uint32    `db:"start_time"`
	Continuous  bool      `db:"diva_continuous"`
	ActivatedAt time.Time `db:"activated_at"`
	EffectID    int       `db:"effect_id"`
	Level       *int      `db:"-"`
}

// A single read-only query binds activation effects to the current lifecycle.
// Never call lazy event/snapshot creation from a dashboard refresh.
const dashboardDivaBeadsQuery = `
	SELECT b.char_id, e.id AS event_id, EXTRACT(epoch FROM e.start_time)::bigint AS start_time,
	       e.diva_continuous, b.activated_at, f.effect_id
	FROM diva_event_lifecycle l
	JOIN events e ON e.id=l.event_id AND e.event_type='diva'
	JOIN diva_battle_songs b ON b.event_id=e.id
	JOIN diva_battle_song_effects f ON f.char_id=b.char_id AND f.event_id=b.event_id
	LEFT JOIN diva_bead_rounds r ON r.event_id=e.id
	WHERE l.mode=$1 AND b.char_id=ANY($2) AND b.activation_id<>0
	  AND b.activated_at<=$3 AND b.activated_at>$3::timestamptz-INTERVAL '1 hour'
	ORDER BY b.char_id, array_position(r.effects,f.effect_id::bigint),f.effect_id
`

func dashboardDivaBeadsEnabled(options *cfg.Config) bool {
	return options != nil && options.RealClientMode == cfg.ZZ &&
		(options.DebugOptions.DivaOverride == -1 || options.DebugOptions.DivaOverride == 2) &&
		options.DebugOptions.InGameTimeOverrideHour == nil
}

func (s *APIServer) enrichDashboardDivaBeads(ctx context.Context, online []OnlineCharacter, now time.Time) error {
	if len(online) == 0 || !dashboardDivaBeadsEnabled(s.erupeConfig) {
		return nil
	}
	ids := make(pq.Int64Array, len(online))
	for i := range online {
		ids[i] = int64(online[i].CharID)
	}
	var rows []dashboardDivaBeadRow
	if err := s.db.SelectContext(ctx, &rows, dashboardDivaBeadsQuery,
		s.erupeConfig.DebugOptions.DivaOverride, ids, now); err != nil {
		return err
	}
	// Cache per event, not per character. The slot order maps winning colors
	// to effect IDs; activation effects alone cannot establish their levels.
	repo := channelserver.NewDivaRepository(s.db)
	eventLevels := make(map[uint32]map[int]int)
	for i := range rows {
		row := &rows[i]
		levels, loaded := eventLevels[row.EventID]
		if !loaded {
			event := channelserver.DivaEvent{ID: row.EventID, StartTime: row.StartTime, Continuous: row.Continuous}
			var err error
			levels, err = repo.GetDashboardDivaBeadLevels(ctx, event, now)
			if err != nil {
				return err
			}
			eventLevels[row.EventID] = levels
		}
		if level, known := levels[row.EffectID]; known {
			row.Level = &level
		}
	}
	active := dashboardDivaBeadsByCharacter(rows, now)
	for i := range online {
		online[i].DivaBeads = active[online[i].CharID]
	}
	return nil
}

func dashboardDivaBeadsByCharacter(rows []dashboardDivaBeadRow, now time.Time) map[uint32]*DashboardDivaBeads {
	result := make(map[uint32]*DashboardDivaBeads)
	invalid := make(map[uint32]bool)
	seen := make(map[uint32]map[int]bool)
	for _, row := range rows {
		event := channelserver.DivaEvent{ID: row.EventID, StartTime: row.StartTime, Continuous: row.Continuous}
		ts := event.ScheduleTimestamps()
		battleStart, battleEnd := time.Unix(int64(ts[2]), 0), time.Unix(int64(ts[3]), 0)
		expires := row.ActivatedAt.Add(time.Hour)
		if row.EventID == 0 || row.CharID == 0 || row.StartTime == 0 ||
			now.Before(battleStart) || !now.Before(battleEnd) || row.ActivatedAt.Before(battleStart) ||
			now.Before(row.ActivatedAt) || !now.Before(expires) {
			continue
		}
		if battleEnd.Before(expires) {
			expires = battleEnd
		}
		name := strings.TrimSuffix(channelserver.DivaBeadNameKO(row.EffectID), "의 기주")
		if name == "" {
			invalid[row.CharID] = true
			continue
		}
		beads := result[row.CharID]
		if beads == nil {
			beads = &DashboardDivaBeads{Round: row.EventID, ExpiresAt: expires}
			result[row.CharID] = beads
			seen[row.CharID] = make(map[int]bool)
		}
		if beads.Round != row.EventID || !beads.ExpiresAt.Equal(expires) ||
			seen[row.CharID][row.EffectID] || len(beads.Beads) == 4 {
			invalid[row.CharID] = true
			continue
		}
		seen[row.CharID][row.EffectID] = true
		beads.Beads = append(beads.Beads, name)
		if row.Level != nil {
			beads.Levels = append(beads.Levels, *row.Level)
		}
	}
	for _, beads := range result {
		if len(beads.Levels) != len(beads.Beads) {
			beads.Levels = nil
		}
	}
	for id := range invalid {
		delete(result, id)
	}
	return result
}
