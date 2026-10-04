package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

const towerLifecycleLock int64 = 1414485829

var errTowerRoundChanged = errors.New("Tower round has changed")

type TowerEvent struct {
	ID         int32     `db:"earth_id"`
	Start      time.Time `db:"starts_at"`
	End        time.Time `db:"ends_at"`
	ClaimUntil time.Time `db:"claim_until"`
}

func (e TowerEvent) Active(now time.Time) bool { return !now.Before(e.Start) && now.Before(e.End) }
func (e TowerEvent) Claimable(now time.Time) bool {
	return !now.Before(e.Start) && now.Before(e.ClaimUntil)
}

type TowerEventLifecycleRepository interface {
	EnsureTowerEvent(time.Time, cfg.TowerRotationOptions, int32) (TowerEvent, error)
	ForTowerRound(int32) TowerRepo
}

type TowerDailyExtrasRepository interface {
	RecordTowerDailyExtrasWithTRP(int32, uint32, time.Time, TowerMissionStats, bool) error
}

func towerRotationDurations(o cfg.TowerRotationOptions) (time.Duration, time.Duration, error) {
	if o.ActiveDays <= 0 || o.CycleDays <= o.ActiveDays || o.CycleDays > 365 {
		return 0, 0, errors.New("TowerRotation requires 0 < ActiveDays < CycleDays <= 365")
	}
	return time.Duration(o.ActiveDays) * 24 * time.Hour, time.Duration(o.CycleDays) * 24 * time.Hour, nil
}
func towerInitialStart(now time.Time, o cfg.TowerRotationOptions) (time.Time, error) {
	_, cycle, err := towerRotationDurations(o)
	if err != nil {
		return time.Time{}, err
	}
	anchor := towerDailyStart(now.In(divaLocation))
	if o.StartAt != "" {
		anchor, err = time.Parse(time.RFC3339, o.StartAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("TowerRotation.StartAt: %w", err)
		}
	}
	if !now.Before(anchor) {
		anchor = anchor.Add(time.Duration(now.Sub(anchor)/cycle) * cycle)
	}
	if anchor.Unix() < 0 || anchor.Add(cycle).Unix() > 1<<32-1 {
		return time.Time{}, errors.New("Tower schedule outside protocol time range")
	}
	return anchor.UTC(), nil
}
func (r *TowerRepository) EnsureTowerEvent(now time.Time, o cfg.TowerRotationOptions, seed int32) (TowerEvent, error) {
	active, cycle, err := towerRotationDurations(o)
	if err != nil {
		return TowerEvent{}, err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return TowerEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1)", towerLifecycleLock); err != nil {
		return TowerEvent{}, err
	}
	var event TowerEvent
	var anchor time.Time
	var oldActive, oldCycle int
	err = tx.QueryRow(`SELECT e.earth_id,e.starts_at,e.ends_at,e.claim_until,l.anchor,l.active_days,l.cycle_days
		FROM tower_event_lifecycle l JOIN tower_events e USING(earth_id) WHERE l.singleton`).Scan(
		&event.ID, &event.Start, &event.End, &event.ClaimUntil, &anchor, &oldActive, &oldCycle)
	if errors.Is(err, sql.ErrNoRows) {
		start, e := towerInitialStart(now, o)
		if e != nil {
			return event, e
		}
		// Adopt the configured manual round once, without resetting progress.
		var existing int32
		if err = tx.Get(&existing, `SELECT COALESCE(MAX(earth_id),0) FROM (
			SELECT earth_id FROM tower_event_progress UNION ALL SELECT earth_id FROM tower_reward_claims
			UNION ALL SELECT earth_id FROM tower_guardian_kills UNION ALL SELECT earth_id FROM tower_daily_progress) q`); err != nil {
			return event, err
		}
		source := max(seed, 0)
		if seed < 0 || source > 65535 || existing > 65535 {
			return event, errors.New("manual EarthID outside Tower protocol range")
		}
		baseline := int32(towerSurveyRound(source))
		id := max(baseline, existing+1)
		if source == existing {
			id = baseline
		}
		event = TowerEvent{ID: id, Start: start, End: start.Add(active), ClaimUntil: start.Add(cycle)}
		if event.ID > 65535 {
			return event, errors.New("existing EarthID exceeds Tower reward namespace")
		}
		if err = insertTowerEvent(tx, event); err != nil {
			return event, err
		}
		if err = adoptTowerManualRound(tx, source, event.ID); err != nil {
			return event, err
		}
		if _, err = tx.Exec(`INSERT INTO tower_event_lifecycle(singleton,earth_id,anchor,active_days,cycle_days) VALUES(true,$1,$2,$3,$4)`, event.ID, start, o.ActiveDays, o.CycleDays); err != nil {
			return event, err
		}
	} else if err != nil {
		return event, err
	} else {
		if oldActive != o.ActiveDays || oldCycle != o.CycleDays {
			return event, errors.New("persisted Tower durations differ; change schedule explicitly while stopped")
		}
		if o.StartAt != "" {
			configured, e := time.Parse(time.RFC3339, o.StartAt)
			if e != nil {
				return event, e
			}
			if configured.After(anchor) || anchor.Sub(configured)%cycle != 0 {
				return event, errors.New("persisted Tower anchor differs")
			}
		}
		if !now.Before(event.ClaimUntil) {
			steps := int64(now.Sub(event.Start) / cycle)
			if int64(event.ID)+steps > 65535 {
				return event, errors.New("Tower round ID exhausted")
			}
			if err = archiveAndResetTowerRound(tx, event.ID); err != nil {
				return event, err
			}
			start := event.Start.Add(time.Duration(steps) * cycle)
			event = TowerEvent{ID: event.ID + int32(steps), Start: start, End: start.Add(active), ClaimUntil: start.Add(cycle)}
			if event.ClaimUntil.Unix() > 1<<32-1 {
				return event, errors.New("Tower schedule outside protocol time range")
			}
			if err = insertTowerEvent(tx, event); err != nil {
				return event, err
			}
			if _, err = tx.Exec(`UPDATE tower_event_lifecycle SET earth_id=$1 WHERE singleton`, event.ID); err != nil {
				return event, err
			}
		}
	}
	return event, tx.Commit()
}
func insertTowerEvent(tx *sqlx.Tx, e TowerEvent) error {
	_, err := tx.Exec(`INSERT INTO tower_events(earth_id,starts_at,ends_at,claim_until) VALUES($1,$2,$3,$4)`, e.ID, e.Start, e.End, e.ClaimUntil)
	return err
}

func adoptTowerManualRound(tx *sqlx.Tx, source, dest int32) error {
	if source != dest {
		// EarthID=0 was valid in manual mode. Copy its receipts/counters into the
		// first automatic round and retain the original rows as history.
		for _, q := range []string{
			`INSERT INTO tower_event_progress SELECT $2,character_id,block1_floors,block2_floors FROM tower_event_progress WHERE earth_id=$1`,
			`INSERT INTO tower_daily_progress SELECT $2,character_id,day_start,floors,antiques,chests,cats,trp,slays FROM tower_daily_progress WHERE earth_id=$1`,
			`INSERT INTO tower_guardian_kills(earth_id,block,run_id,character_id,before_casual) SELECT $2,block,run_id,character_id,before_casual FROM tower_guardian_kills WHERE earth_id=$1`,
			`INSERT INTO tower_guardian_participants SELECT $2,block,run_id,character_id,before_casual,recorded_at FROM tower_guardian_participants WHERE earth_id=$1`,
			`INSERT INTO tower_reward_claims SELECT $2,character_id,reward_kind,reward_index,item_id,quantity,claimed_at FROM tower_reward_claims WHERE earth_id=$1`,
		} {
			if _, err := tx.Exec(q, source, dest); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(`INSERT INTO tower_event_progress(earth_id,character_id,block1_floors,block2_floors)
		SELECT $1,char_id,COALESCE(block1,0),COALESCE(block2,0) FROM tower
		ON CONFLICT(earth_id,character_id) DO UPDATE SET
		block1_floors=GREATEST(tower_event_progress.block1_floors,EXCLUDED.block1_floors),
		block2_floors=GREATEST(tower_event_progress.block2_floors,EXCLUDED.block2_floors)`, dest)
	return err
}
func archiveAndResetTowerRound(tx *sqlx.Tx, id int32) error {
	queries := []string{
		`INSERT INTO tower_round_character_state SELECT $1,char_id,COALESCE(block1,0),COALESCE(block2,0),guardian1,guardian2,COALESCE(gems,$2) FROM tower`,
		`INSERT INTO tower_round_snapshots SELECT $1,'daily',character_id,to_jsonb(b) FROM tower_daily_bins b`,
		`INSERT INTO tower_round_snapshots SELECT $1,'guild',id,jsonb_build_object('page',tower_mission_page,'rp',tower_rp) FROM guilds`,
		`INSERT INTO tower_round_snapshots SELECT $1,'member',id,jsonb_build_object('guild_id',guild_id,'character_id',character_id,'mission1',tower_mission_1,'mission2',tower_mission_2,'mission3',tower_mission_3) FROM guild_characters`,
	}
	for i, q := range queries {
		args := []interface{}{id}
		if i == 0 {
			args = append(args, EmptyTowerCSV(30))
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE tower SET block1=0,block2=0,guardian1=0,guardian2=0,gems=$1`, EmptyTowerCSV(30)); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM tower_daily_bins`,
		`UPDATE guilds SET tower_mission_page=1,tower_rp=0`,
		`UPDATE guild_characters SET tower_mission_1=0,tower_mission_2=0,tower_mission_3=0`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Check the round in the SAME transaction as the write. Never hold a pooled
// connection while requesting a second connection: that can deadlock the pool.
func (r *TowerRepository) ForTowerRound(id int32) TowerRepo {
	return &TowerRepository{db: r.db, roundID: &id}
}
func (r *TowerRepository) checkTowerRound(tx *sqlx.Tx) error {
	if r.roundID == nil {
		return nil
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock_shared($1)`, towerLifecycleLock); err != nil {
		return err
	}
	var current int32
	if err := tx.Get(&current, `SELECT earth_id FROM tower_event_lifecycle WHERE singleton`); err != nil {
		return err
	}
	if current != *r.roundID {
		return errTowerRoundChanged
	}
	return nil
}
func (r *TowerRepository) execTowerRound(query string, args ...interface{}) (sql.Result, error) {
	if r.roundID == nil {
		return r.db.Exec(query, args...)
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = r.checkTowerRound(tx); err != nil {
		return nil, err
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}
func (s *Server) towerEvent(now time.Time) (TowerEvent, error) {
	if !s.erupeConfig.TowerRotation.Enabled {
		return TowerEvent{ID: s.erupeConfig.EarthID, Start: TimeWeekStart(), End: TimeWeekNext(), ClaimUntil: TimeWeekNext()}, nil
	}
	r, ok := s.towerRepo.(TowerEventLifecycleRepository)
	if !ok {
		return TowerEvent{}, errors.New("Tower repository has no lifecycle support")
	}
	return r.EnsureTowerEvent(now, s.erupeConfig.TowerRotation, s.erupeConfig.EarthID)
}
func (s *Server) rotateTowerEvents() {
	if !s.erupeConfig.TowerRotation.Enabled {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if _, err := s.towerEvent(TimeAdjusted()); err != nil {
				s.logger.Error("Tower rotation failed", zap.Error(err))
			}
		}
	}
}
func (s *Session) towerRoundRepo(id int32) TowerRepo {
	if s.server.erupeConfig.TowerRotation.Enabled {
		if r, ok := s.server.towerRepo.(TowerEventLifecycleRepository); ok {
			return r.ForTowerRound(id)
		}
	}
	return s.server.towerRepo
}

// Caller holds lifecycleMu. Admission belongs to departure, not result time.
func (s *Session) pinTowerDeparture(e TowerEvent, now time.Time) {
	s.towerDepartureEvent = e
	s.towerDepartureStarted = now
	s.towerDepartureGeneration = s.questWeaponGeneration
}
func (s *Session) towerResultEvent() (TowerEvent, bool) {
	if !s.server.erupeConfig.TowerRotation.Enabled {
		return TowerEvent{ID: s.server.erupeConfig.EarthID}, true
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	e := s.towerDepartureEvent
	return e, s.towerDepartureGeneration == s.questWeaponGeneration && s.questWeaponGeneration != 0 && e.Active(s.towerDepartureStarted)
}
func towerSetupIsTower(qid uint16, setup []byte) bool {
	switch qid {
	case towerQuestPrologue, towerQuestZone1, towerQuestZone2, towerQuestMilestone1, towerQuestMilestone2, towerQuestGuardian1, towerQuestGuardian2:
		return len(setup) > questRunStageQuestIDOffset+0x27 && setup[questRunStageQuestIDOffset+0x27]&2 == 0
	}
	return false
}
func doAckTowerSucceed(s *Session, handle uint32, id int32, data []*byteframe.ByteFrame) {
	bf := byteframe.NewByteFrame()
	bf.WriteInt32(id)
	bf.WriteUint32(0)
	bf.WriteUint32(0)
	bf.WriteUint32(uint32(len(data)))
	for _, frame := range data {
		bf.WriteBytes(frame.Data())
	}
	doAckBufSucceed(s, handle, bf.Data())
}
