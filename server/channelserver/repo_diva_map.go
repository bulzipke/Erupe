package channelserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

var (
	ErrDivaMapMembership = errors.New("diva map guild membership mismatch")
	ErrDivaMapDeparture  = errors.New("invalid diva map departure")
)

type divaMapEventWindow struct{ Start, End time.Time }
type divaStoredMap struct {
	GuildID uint32    `db:"guild_id"`
	Number  uint16    `db:"current_number"`
	Areas   uint32    `db:"acquired_areas"`
	Settled time.Time `db:"last_settled_at"`
	Data    []byte    `db:"map_data"`
}
type divaMapContribution struct {
	CharID     uint32    `db:"char_id"`
	RunKey     string    `db:"run_key"`
	MapNumber  uint16    `db:"map_number"`
	Route      uint16    `db:"route"`
	Points     uint64    `db:"points"`
	EligibleAt time.Time `db:"eligible_at"`
}

// No map operation locks character, membership or event rows. Existing reward
// and personal-point transactions may acquire those locks before this event
// advisory lock, but this layer never acquires them in the reverse order.
func lockDivaMapEvent(tx *sqlx.Tx, eventID uint32) error {
	if eventID == 0 || eventID > math.MaxInt32 {
		return ErrDivaMapDeparture
	}
	_, err := tx.Exec(`SELECT pg_advisory_xact_lock(1146507843,$1)`, eventID)
	return err
}

func divaMapClock(tx *sqlx.Tx, now time.Time) (time.Time, error) {
	if now.IsZero() {
		if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return time.Time{}, err
		}
	}
	return now.Truncate(time.Microsecond), nil
}

func ensureDivaMapEventTx(tx *sqlx.Tx, eventID uint32, now time.Time) (divaMapEventWindow, bool, error) {
	var window divaMapEventWindow
	var actualStart time.Time
	var enabled bool
	err := tx.QueryRow(`SELECT p.starts_at,GREATEST(p.starts_at,COALESCE(a.activated_at,p.starts_at)),p.ends_at,
		(p.starts_at>c.installed_at
		AND NOT EXISTS(SELECT 1 FROM diva_map_legacy_events l WHERE l.event_id=p.event_id)
		AND NOT EXISTS(SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=p.event_id))
		OR COALESCE(a.activated_at>=p.starts_at AND a.activated_at<p.ends_at,FALSE)
		FROM diva_interception_periods p CROSS JOIN diva_map_cutover c
		LEFT JOIN diva_map_activations a ON a.event_id=p.event_id WHERE p.event_id=$1`, eventID).
		Scan(&actualStart, &window.Start, &window.End, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return window, false, nil
	}
	if err != nil || !enabled || now.Before(window.Start) {
		return window, false, err
	}
	if err = ensureDivaMapEventRulesTx(tx, eventID, actualStart, window); err != nil {
		return window, false, err
	}
	return window, true, nil
}

// The timestamp is the lower departure bound, not a replacement event anchor.
// In particular, callers must not make the old personal ledger reward-eligible.
func (r *DivaRepository) GetDivaMapActivation(eventID uint32) (time.Time, bool, error) {
	var activated time.Time
	err := r.db.QueryRow(`SELECT a.activated_at FROM diva_map_activations a
		JOIN diva_interception_periods p ON p.event_id=a.event_id
		WHERE a.event_id=$1 AND a.activated_at>=p.starts_at AND a.activated_at<p.ends_at`, eventID).Scan(&activated)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return activated, err == nil, err
}

func divaMapMemberName(tx *sqlx.Tx, charID, guildID uint32) (string, error) {
	var name string
	err := tx.QueryRow(`SELECT g.name FROM guild_characters c JOIN guilds g ON g.id=c.guild_id
		WHERE c.character_id=$1 AND c.guild_id=$2`, charID, guildID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDivaMapMembership
	}
	return name, err
}

func loadDivaStoredMap(tx *sqlx.Tx, eventID, guildID uint32) (divaStoredMap, DivaInterceptionMap, error) {
	var stored divaStoredMap
	var m DivaInterceptionMap
	err := tx.Get(&stored, `SELECT guild_id,current_number,acquired_areas,last_settled_at,map_data
		FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2`, eventID, guildID)
	if err != nil {
		return stored, m, err
	}
	if err = json.Unmarshal(stored.Data, &m); err != nil {
		return stored, m, err
	}
	if err = validateDivaMapEventCatalogTx(tx, eventID, m); err != nil {
		return stored, m, err
	}
	if m.States[0].MapNumber != stored.Number || m.AcquiredAreas != stored.Areas {
		return stored, m, fmt.Errorf("diva map stored state disagrees with summary")
	}
	return stored, m, nil
}

func createDivaGuildMapTx(tx *sqlx.Tx, eventID, guildID uint32, name string, start, firstUse time.Time) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2)`, eventID, guildID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	m, err := initialDivaMapForEventTx(tx, eventID)
	if err != nil {
		return err
	}
	// A late first visit must not inherit days of attacks before this guild
	// even had a map. Subsequent downtime DOES advance the persisted page clock.
	if m.RulesVersion == divaProgressiveMapRules && firstUse.After(start) {
		start = firstUse
	}
	if err = validateDivaMapEventCatalogTx(tx, eventID, m); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO diva_map_guilds(event_id,guild_id,guild_name,current_number,acquired_areas,last_settled_at,map_data)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, eventID, guildID, name, m.States[0].MapNumber, m.AcquiredAreas, start, data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO diva_map_snapshots(event_id,guild_id,settled_at,map_number,map_data)
		VALUES($1,$2,$3,$4,$5)`, eventID, guildID, start, m.States[0].MapNumber, data)
	return err
}

func nextDivaMapBoundary(after, end time.Time) time.Time {
	next := after.Truncate(time.Hour).Add(time.Hour)
	if next.After(end) {
		return end
	}
	return next
}

// An awarded area is timestamped at the settlement boundary, not at request
// time. This lets catch-up reproduce past publications without backfilling them.
func settleDivaGuildMapTx(tx *sqlx.Tx, eventID, guildID uint32, window divaMapEventWindow, now time.Time) error {
	stored, m, err := loadDivaStoredMap(tx, eventID, guildID)
	if err != nil {
		return err
	}
	until := now
	if until.After(window.End) {
		until = window.End
	}
	if !stored.Settled.Before(until) {
		return nil
	}
	var pending []divaMapContribution
	if err = tx.Select(&pending, `SELECT char_id,run_key,map_number,route,points,eligible_at
		FROM diva_map_contributions WHERE event_id=$1 AND guild_id=$2 AND settled_at IS NULL
		ORDER BY eligible_at,char_id,run_key`, eventID, guildID); err != nil {
		return err
	}
	position := 0
	last := stored.Settled
	for last.Before(until) {
		boundary := nextDivaMapBoundary(last, window.End)
		if boundary.After(until) {
			break
		}
		begin := position
		mapBefore := m.States[0].MapNumber
		changed := false
		points := make(map[uint16]uint64)
		for position < len(pending) && pending[position].EligibleAt.Before(boundary) {
			row := pending[position]
			if row.MapNumber == m.States[0].MapNumber {
				if math.MaxUint64-points[row.Route] < row.Points {
					return fmt.Errorf("diva map point sum overflow")
				}
				points[row.Route] += row.Points
			}
			position++
		}
		if position > begin {
			changed = true
			before := m
			progress, err := advanceDivaCustomMap(m, points)
			if err != nil {
				return err
			}
			m = progress.Map
			credited, err := divaMapCreditedByRoute(before, m, points)
			if err != nil {
				return err
			}
			branches := make(map[uint16]bool, len(progress.CompletedBranches))
			for _, coordinate := range progress.CompletedBranches {
				branches[coordinate] = true
			}
			for _, coordinate := range progress.Awarded {
				if _, err = tx.Exec(`INSERT INTO diva_map_area_awards(event_id,guild_id,map_number,coordinate,is_branch,awarded_at)
					VALUES($1,$2,$3,$4,$5,$6)`, eventID, guildID, before.States[0].MapNumber, coordinate, branches[coordinate], boundary); err != nil {
					return err
				}
			}
			for _, row := range pending[begin:position] {
				var used uint64
				participated := false
				if row.MapNumber == before.States[0].MapNumber {
					used = min(row.Points, credited[row.Route])
					credited[row.Route] -= used
					// Every legitimate contribution in the completion bucket is a
					// participant, even when earlier party rows consume the final
					// needed point. Do not award only the lowest character ID.
					if row.Route > 0 {
						for _, node := range before.States[0].Nodes {
							if node.Coordinate == row.Route && node.BranchQuests[0] != 0 && node.EarnedPoints < node.RequiredPoints {
								participated = true
								break
							}
						}
					}
				}
				if _, err = tx.Exec(`UPDATE diva_map_contributions SET credited_points=$4,settled_at=$5,treasure_participation=$6
					WHERE char_id=$1 AND event_id=$2 AND run_key=$3 AND settled_at IS NULL`, row.CharID, eventID, row.RunKey, used, boundary, participated); err != nil {
					return err
				}
			}
			// Apply every bound branch in the bucket before replacing the map.
			// Unused points are intentionally never carried into the new instance.
			if progress.GoalCompleted && boundary.Before(window.End) {
				m, err = divaCustomMapNext(m)
				if err != nil {
					return err
				}
			}
		}
		// Resolve this bucket's captures before invasion. Never attack a newly
		// opened page in its birth bucket, or at/after the event's final cutoff.
		if m.RulesVersion == divaProgressiveMapRules && m.States[0].MapNumber == mapBefore && boundary.Before(window.End) {
			var attacked []uint16
			m, attacked, err = applyDivaProgressiveInvasion(m, m.States[0].InvasionTick+1)
			if err != nil {
				return err
			}
			changed = true // Persist the tick even when every attack roll fails.
			if len(attacked) > 0 {
				if _, err = tx.Exec(`INSERT INTO diva_map_invasions(event_id,guild_id,happened_at,map_number,page_tick,changed_nodes)
					VALUES($1,$2,$3,$4,$5,$6)`, eventID, guildID, boundary, mapBefore, m.States[0].InvasionTick, len(attacked)); err != nil {
					return err
				}
			}
		}
		if changed {
			if err = validateDivaMapEventCatalogTx(tx, eventID, m); err != nil {
				return err
			}
			data, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO diva_map_snapshots(event_id,guild_id,settled_at,map_number,map_data)
				VALUES($1,$2,$3,$4,$5)`, eventID, guildID, boundary, m.States[0].MapNumber, data); err != nil {
				return err
			}
		}
		last = boundary
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE diva_map_guilds SET current_number=$3,acquired_areas=$4,last_settled_at=$5,map_data=$6
		WHERE event_id=$1 AND guild_id=$2`, eventID, guildID, m.States[0].MapNumber, m.AcquiredAreas, last, data)
	return err
}

func divaMapCreditedByRoute(before, after DivaInterceptionMap, points map[uint16]uint64) (map[uint16]uint64, error) {
	old := make(map[uint16]uint32, len(before.States[0].Nodes))
	for _, node := range before.States[0].Nodes {
		old[node.Coordinate] = node.EarnedPoints
	}
	used := make(map[uint16]uint64, len(points))
	for _, node := range after.States[0].Nodes {
		if node.EarnedPoints < old[node.Coordinate] {
			return nil, fmt.Errorf("diva map progress regressed")
		}
		delta := uint64(node.EarnedPoints - old[node.Coordinate])
		if _, branch := points[node.Coordinate]; branch && node.Coordinate != 0 {
			used[node.Coordinate] += delta
		} else {
			used[0] += delta
		}
	}
	for route, n := range used {
		if n > points[route] {
			return nil, fmt.Errorf("diva map credited more points than submitted")
		}
	}
	return used, nil
}

func publishDivaMapRanksTx(tx *sqlx.Tx, eventID uint32, window divaMapEventWindow, now time.Time) error {
	for boundary := nextDivaMapBoundary(window.Start, window.End); !boundary.After(now); boundary = nextDivaMapBoundary(boundary, window.End) {
		final := boundary.Equal(window.End)
		hour := boundary.In(divaLocation).Hour()
		if final || hour == 4 || hour == 12 || hour == 20 {
			result, err := tx.Exec(`INSERT INTO diva_map_publications(event_id,published_at,is_final)
				VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, eventID, boundary, final)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				_, err = tx.Exec(`INSERT INTO diva_map_ranks(event_id,published_at,guild_id,name,areas,rank)
					SELECT $1,$2,a.guild_id,g.guild_name,a.areas,RANK() OVER(ORDER BY a.areas DESC)
					FROM (SELECT guild_id,COUNT(*)::integer areas FROM diva_map_area_awards
					WHERE event_id=$1 AND awarded_at<=$2 GROUP BY guild_id) a
					JOIN diva_map_guilds g ON g.event_id=$1 AND g.guild_id=a.guild_id`, eventID, boundary)
				if err != nil {
					return err
				}
			}
		}
		if final {
			break
		}
	}
	return nil
}

// Safe to call inside reward transactions after their character/lifecycle locks.
// Explicit now is for deterministic scheduling/tests; zero samples the DB clock
// after the event lock. Nothing here locks a character or membership row.
func settleDivaMapEventTx(tx *sqlx.Tx, eventID uint32, now time.Time) (bool, error) {
	if err := lockDivaMapEvent(tx, eventID); err != nil {
		return false, err
	}
	now, err := divaMapClock(tx, now)
	if err != nil {
		return false, err
	}
	window, enabled, err := ensureDivaMapEventTx(tx, eventID, now)
	if err != nil || !enabled {
		return enabled, err
	}
	var guilds []uint32
	if err = tx.Select(&guilds, `SELECT guild_id FROM diva_map_guilds WHERE event_id=$1 ORDER BY guild_id`, eventID); err != nil {
		return false, err
	}
	for _, guildID := range guilds {
		if err = settleDivaGuildMapTx(tx, eventID, guildID, window, now); err != nil {
			return false, err
		}
	}
	return true, publishDivaMapRanksTx(tx, eventID, window, now)
}

func (r *DivaRepository) GetDivaMap(charID, guildID, eventID uint32, now time.Time) (DivaMapView, error) {
	var view DivaMapView
	tx, err := r.db.Beginx()
	if err != nil {
		return view, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockDivaMapEvent(tx, eventID); err != nil {
		return view, err
	}
	now, err = divaMapClock(tx, now)
	if err != nil {
		return view, err
	}
	window, enabled, err := ensureDivaMapEventTx(tx, eventID, now)
	if err != nil || !enabled {
		return view, err
	}
	name, err := divaMapMemberName(tx, charID, guildID)
	if err != nil {
		return view, err
	}
	if !now.Before(window.End) {
		var exists bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM diva_map_guilds WHERE event_id=$1 AND guild_id=$2)`, eventID, guildID).Scan(&exists); err != nil {
			return view, err
		}
		// After the event, preserve participants' existing maps but do not
		// manufacture a historical map for a guild that never opened one.
		if !exists {
			return view, nil
		}
	}
	if err = createDivaGuildMapTx(tx, eventID, guildID, name, window.Start, now); err != nil {
		return view, err
	}
	if _, err = settleDivaMapEventTx(tx, eventID, now); err != nil {
		return view, err
	}
	_, view.Map, err = loadDivaStoredMap(tx, eventID, guildID)
	if err != nil {
		return view, err
	}
	view.Enabled = true
	if view.Map.RulesVersion == divaProgressiveMapRules {
		view.SpecialTreasures, view.SpecialTreasureError = selectDivaProgressiveTreasures(view.Map)
	} else if r.mapSpecialPolicyReady.Load() {
		view.SpecialTreasures, view.SpecialTreasureError = loadDivaMapSpecialSelectionsTx(tx, eventID, view.Map)
	}
	return view, tx.Commit()
}

func (r *DivaRepository) BindDivaMapDeparture(charID, guildID, eventID uint32, questID uint16, runKey string, startedAt, now time.Time) (DivaMapDeparture, error) {
	var out DivaMapDeparture
	if charID == 0 || guildID == 0 || questID == 0 || !validDivaInterceptionRunKey(runKey) || startedAt.IsZero() {
		return out, ErrDivaMapDeparture
	}
	startedAt = startedAt.Truncate(time.Microsecond)
	tx, err := r.db.Beginx()
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockDivaMapEvent(tx, eventID); err != nil {
		return out, err
	}
	now, err = divaMapClock(tx, now)
	if err != nil {
		return out, err
	}
	window, enabled, err := ensureDivaMapEventTx(tx, eventID, now)
	if err != nil || !enabled {
		return out, err
	}
	var oldGuild uint32
	var oldQuest uint16
	var oldStart time.Time
	err = tx.QueryRow(`SELECT guild_id,quest_id,started_at,map_number,route,eligible FROM diva_map_departures
		WHERE char_id=$1 AND event_id=$2 AND run_key=$3`, charID, eventID, runKey).
		Scan(&oldGuild, &oldQuest, &oldStart, &out.MapNumber, &out.Route, &out.Enabled)
	if err == nil {
		if oldGuild != guildID || oldQuest != questID || !oldStart.Equal(startedAt) {
			return DivaMapDeparture{}, ErrDivaMapDeparture
		}
		return out, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if startedAt.Before(window.Start) || !startedAt.Before(window.End) || now.Before(startedAt) || !now.Before(window.End) {
		return out, ErrDivaMapDeparture
	}
	name, err := divaMapMemberName(tx, charID, guildID)
	if err != nil {
		return out, err
	}
	if err = createDivaGuildMapTx(tx, eventID, guildID, name, window.Start, startedAt); err != nil {
		return out, err
	}
	if _, err = settleDivaMapEventTx(tx, eventID, now); err != nil {
		return out, err
	}
	var data []byte
	if err = tx.QueryRow(`SELECT map_data FROM diva_map_snapshots WHERE event_id=$1 AND guild_id=$2 AND settled_at<=$3
		ORDER BY settled_at DESC LIMIT 1`, eventID, guildID, startedAt).Scan(&data); err != nil {
		return out, err
	}
	var m DivaInterceptionMap
	if err = json.Unmarshal(data, &m); err != nil {
		return out, err
	}
	if err = validateDivaMapEventCatalogTx(tx, eventID, m); err != nil {
		return out, err
	}
	out.MapNumber = m.States[0].MapNumber
	out.Route, out.Enabled = divaCustomMapQuestRoute(m, questID)
	_, err = tx.Exec(`INSERT INTO diva_map_departures(char_id,event_id,run_key,guild_id,map_number,quest_id,route,eligible,started_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, charID, eventID, runKey, guildID, out.MapNumber, questID, out.Route, out.Enabled, startedAt)
	if err != nil {
		return DivaMapDeparture{}, err
	}
	return out, tx.Commit()
}

// Call immediately after the personal run INSERT in the SAME transaction.
// Absence of a binding (legacy or excluded quest) never removes personal points.
func recordDivaMapContributionTx(tx *sqlx.Tx, charID, eventID uint32, runKey string, questID uint16, guildID, points uint32, startedAt, submittedAt time.Time) error {
	if err := lockDivaMapEvent(tx, eventID); err != nil {
		return err
	}
	var departure struct {
		Guild    uint32
		Quest    uint16
		Start    time.Time
		Map      uint16
		Route    uint16
		Eligible bool
	}
	err := tx.QueryRow(`SELECT guild_id,quest_id,started_at,map_number,route,eligible FROM diva_map_departures
		WHERE char_id=$1 AND event_id=$2 AND run_key=$3`, charID, eventID, runKey).
		Scan(&departure.Guild, &departure.Quest, &departure.Start, &departure.Map, &departure.Route, &departure.Eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if departure.Guild != guildID || departure.Quest != questID || !departure.Start.Equal(startedAt.Truncate(time.Microsecond)) {
		return ErrDivaMapDeparture
	}
	if !departure.Eligible {
		return nil
	}
	submittedAt = submittedAt.Truncate(time.Microsecond)
	if _, err = settleDivaMapEventTx(tx, eventID, submittedAt); err != nil {
		return err
	}
	stored, _, err := loadDivaStoredMap(tx, eventID, guildID)
	if err != nil {
		return err
	}
	// A report arriving after a publication cannot rewrite that snapshot, even
	// if it waited for this advisory lock with an earlier caller timestamp.
	eligibleAt := submittedAt
	if eligibleAt.Before(stored.Settled) {
		eligibleAt = stored.Settled
	}
	var settled any
	if departure.Map != stored.Number {
		settled = eligibleAt
	}
	var end time.Time
	if err = tx.QueryRow(`SELECT ends_at FROM diva_map_events WHERE event_id=$1`, eventID).Scan(&end); err != nil {
		return err
	}
	if !eligibleAt.Before(end) {
		settled = eligibleAt
	}
	_, err = tx.Exec(`INSERT INTO diva_map_contributions(char_id,event_id,run_key,guild_id,map_number,route,points,submitted_at,eligible_at,settled_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, charID, eventID, runKey, guildID, departure.Map, departure.Route, points, submittedAt, eligibleAt, settled)
	return err
}

func (r *DivaRepository) GetDivaMapRanking(charID, eventID uint32, now time.Time) (DivaMapRanking, error) {
	var result DivaMapRanking
	tx, err := r.db.Beginx()
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockDivaMapEvent(tx, eventID); err != nil {
		return result, err
	}
	now, err = divaMapClock(tx, now)
	if err != nil {
		return result, err
	}
	result.Enabled, err = settleDivaMapEventTx(tx, eventID, now)
	if err != nil || !result.Enabled {
		return result, err
	}
	var ownID uint32
	var ownName string
	err = tx.QueryRow(`SELECT c.guild_id,g.name FROM guild_characters c JOIN guilds g ON g.id=c.guild_id WHERE c.character_id=$1`, charID).Scan(&ownID, &ownName)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Own = &divaAreaRank{GuildID: ownID, Name: ownName}
	}
	var rows []struct {
		GuildID  uint32 `db:"guild_id"`
		Name     string `db:"name"`
		Areas    int64  `db:"areas"`
		Rank     uint32 `db:"rank"`
		Position int64  `db:"position"`
	}
	err = tx.Select(&rows, `WITH ranked AS (
		SELECT guild_id,name,areas,rank,ROW_NUMBER() OVER(ORDER BY rank,guild_id) position
		FROM diva_map_ranks WHERE event_id=$1 AND published_at=(SELECT MAX(published_at) FROM diva_map_publications WHERE event_id=$1 AND published_at<=$2)
	) SELECT * FROM ranked WHERE position<=100 OR guild_id=$3 ORDER BY position`, eventID, now, ownID)
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		rank := divaAreaRank{GuildID: row.GuildID, Name: row.Name, Areas: row.Areas, Rank: row.Rank}
		if row.Position <= 100 {
			result.Rows = append(result.Rows, rank)
		}
		if row.GuildID == ownID {
			result.Own = &rank
		}
	}
	return result, tx.Commit()
}

func (r *DivaRepository) SettleDivaMaps(now time.Time, override int) error {
	if now.IsZero() {
		if err := r.db.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
	}
	now = now.Truncate(time.Microsecond)
	switch override {
	case -1, 2, 3:
		if _, err := r.EnsureDivaEvent(now, override); err != nil {
			return err
		}
	case 1:
		if _, err := r.EnsureDivaSongEvent(now); err != nil {
			return err
		}
	}
	var events []uint32
	if err := r.db.Select(&events, `SELECT event_id FROM diva_map_events e WHERE starts_at<=$1
		AND NOT EXISTS(SELECT 1 FROM diva_map_publications p WHERE p.event_id=e.event_id AND p.is_final)
		ORDER BY event_id`, now); err != nil {
		return err
	}
	// Event ordering is stable; no transaction holds two event advisory locks.
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	for _, eventID := range events {
		tx, err := r.db.Beginx()
		if err != nil {
			return err
		}
		_, err = settleDivaMapEventTx(tx, eventID, now)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
