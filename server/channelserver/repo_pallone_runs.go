package channelserver

import (
	"database/sql"
	"github.com/jmoiron/sqlx"
	"time"
)

var _ PalloneRunRepository = (*PalloneRepository)(nil)

func palloneDurableReportTx(tx *sqlx.Tx, receipt PalloneRunReceipt, v PalloneSubmission) (bool, error) {
	var accepted bool
	err := tx.Get(&accepted, `SELECT EXISTS(SELECT 1 FROM pallone_run_reports p JOIN pallone_events e ON e.id=p.event_id
		WHERE p.character_id=$1 AND p.run_id=$2 AND p.event_id=$3 AND p.route_key=$4 AND p.started_at=$5 AND p.completed_at=$6 AND p.final_quest_id=$7
		AND p.base_score=$8 AND p.premium_bonus=$9 AND p.item_bonus=$10 AND p.guild_id=$11 AND p.rejected_at IS NULL
		AND p.completed_at<e.ends_at AND p.score_reported_at<e.ends_at AND p.result_reported_at<e.ends_at)`, receipt.CharID, receipt.RunID, receipt.EventID, receipt.RouteKey, receipt.StartedAt, receipt.CompletedAt, receipt.FinalQuestID, v.BaseScore, v.PremiumBonus, v.ItemBonus, v.GuildID)
	return accepted, err
}

func (r *PalloneRepository) SavePalloneRunReport(receipt PalloneRunReceipt, v *PalloneSubmission, qualified bool, now time.Time) (*PalloneReadyRun, error) {
	now = now.UTC().Truncate(time.Microsecond)
	probe := receipt
	if probe.CompletedAt.IsZero() {
		probe.CompletedAt = now
	}
	probe, err := normalizePalloneReceipt(probe)
	if err != nil || now.Before(probe.StartedAt) || !now.Before(probe.StartedAt.Add(palloneMaxRunDuration)) ||
		(qualified && (receipt.CompletedAt.IsZero() || now.Before(probe.CompletedAt))) || (v != nil && !validPalloneSubmission(*v)) {
		return nil, ErrPalloneInvalid
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var e PalloneEvent
	if err = tx.Get(&e, `SELECT id,starts_at,ends_at,claim_until FROM pallone_events WHERE id=$1 FOR SHARE`, probe.EventID); err != nil {
		return nil, err
	}
	if !e.Active(probe.StartedAt) || !e.Active(now) || (qualified && !probe.CompletedAt.Before(e.End)) {
		return nil, ErrPalloneIneligible
	}
	var frozen bool
	if err = tx.Get(&frozen, `SELECT EXISTS(SELECT 1 FROM pallone_event_finalizations WHERE event_id=$1)`, e.ID); err != nil || frozen {
		if err == nil {
			err = ErrPalloneIneligible
		}
		return nil, err
	}
	var finalID uint16
	if err = tx.Get(&finalID, `SELECT stage_quest_ids[cardinality(stage_quest_ids)] FROM pallone_event_routes WHERE event_id=$1 AND route_key=$2`, probe.EventID, probe.RouteKey); err != nil || finalID != probe.FinalQuestID {
		if err == nil {
			err = ErrPalloneInvalid
		}
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO pallone_run_reports(character_id,run_id,event_id,route_key,started_at,final_quest_id)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, probe.CharID, probe.RunID, probe.EventID, probe.RouteKey, probe.StartedAt, probe.FinalQuestID); err != nil {
		return nil, err
	}
	var stored struct {
		EventID   int32         `db:"event_id"`
		RouteKey  int32         `db:"route_key"`
		Started   time.Time     `db:"started_at"`
		Completed sql.NullTime  `db:"completed_at"`
		Final     uint16        `db:"final_quest_id"`
		Base      sql.NullInt64 `db:"base_score"`
		Premium   sql.NullInt64 `db:"premium_bonus"`
		Item      sql.NullInt64 `db:"item_bonus"`
		Guild     sql.NullInt64 `db:"guild_id"`
		Rejected  sql.NullTime  `db:"rejected_at"`
	}
	if err = tx.Get(&stored, `SELECT event_id,route_key,started_at,completed_at,final_quest_id,base_score,premium_bonus,item_bonus,guild_id,rejected_at
		FROM pallone_run_reports WHERE character_id=$1 AND run_id=$2 FOR UPDATE`, probe.CharID, probe.RunID); err != nil {
		return nil, err
	}
	if stored.Rejected.Valid {
		return nil, ErrPalloneIneligible
	}
	if stored.EventID != probe.EventID || stored.RouteKey != probe.RouteKey || !stored.Started.Equal(probe.StartedAt) || stored.Final != probe.FinalQuestID ||
		(qualified && stored.Completed.Valid && !stored.Completed.Time.Equal(probe.CompletedAt)) ||
		(v != nil && stored.Base.Valid && (stored.Base.Int64 != int64(v.BaseScore) || stored.Premium.Int64 != int64(v.PremiumBonus) || stored.Item.Int64 != int64(v.ItemBonus) || stored.Guild.Int64 != int64(v.GuildID))) {
		return nil, ErrPalloneConflict
	}
	if qualified && !stored.Completed.Valid {
		stored.Completed = sql.NullTime{Time: probe.CompletedAt, Valid: true}
		if _, err = tx.Exec(`UPDATE pallone_run_reports SET completed_at=$3,result_reported_at=$4 WHERE character_id=$1 AND run_id=$2`, probe.CharID, probe.RunID, probe.CompletedAt, now); err != nil {
			return nil, err
		}
	}
	if v != nil && !stored.Base.Valid {
		if v.GuildID != 0 {
			var exists bool
			if err = tx.Get(&exists, `SELECT EXISTS(SELECT 1 FROM guild_characters WHERE character_id=$1 AND guild_id=$2)`, probe.CharID, v.GuildID); err != nil {
				return nil, err
			}
			if !exists {
				return nil, ErrPalloneGuild
			}
		}
		stored.Base = sql.NullInt64{Int64: int64(v.BaseScore), Valid: true}
		stored.Premium = sql.NullInt64{Int64: int64(v.PremiumBonus), Valid: true}
		stored.Item = sql.NullInt64{Int64: int64(v.ItemBonus), Valid: true}
		stored.Guild = sql.NullInt64{Int64: int64(v.GuildID), Valid: true}
		if _, err = tx.Exec(`UPDATE pallone_run_reports SET base_score=$3,premium_bonus=$4,item_bonus=$5,guild_id=$6,score_reported_at=$7
			WHERE character_id=$1 AND run_id=$2`, probe.CharID, probe.RunID, v.BaseScore, v.PremiumBonus, v.ItemBonus, v.GuildID, now); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if !stored.Completed.Valid || !stored.Base.Valid {
		return nil, nil
	}
	probe.CompletedAt = stored.Completed.Time
	return &PalloneReadyRun{Receipt: probe, Submission: PalloneSubmission{BaseScore: int32(stored.Base.Int64), PremiumBonus: int32(stored.Premium.Int64), ItemBonus: int32(stored.Item.Int64), GuildID: uint32(stored.Guild.Int64)}}, nil
}

func (r *PalloneRepository) ReadyPalloneRuns(now time.Time) ([]PalloneReadyRun, error) {
	return r.readyPalloneRuns(0, now)
}

func (r *PalloneRepository) readyPalloneRuns(eventID int32, now time.Time) ([]PalloneReadyRun, error) {
	rows, err := r.db.Query(`SELECT p.event_id,p.character_id,p.route_key,p.run_id,p.started_at,p.completed_at,p.final_quest_id,
		p.base_score,p.premium_bonus,p.item_bonus,p.guild_id FROM pallone_run_reports p JOIN pallone_events e ON e.id=p.event_id
		WHERE p.rejected_at IS NULL AND p.completed_at IS NOT NULL AND p.base_score IS NOT NULL
		AND p.completed_at<e.ends_at AND p.score_reported_at<e.ends_at AND p.result_reported_at<e.ends_at AND p.result_reported_at<=$1 AND ($2::integer=0 OR p.event_id=$2)
		AND NOT EXISTS(SELECT 1 FROM pallone_event_finalizations f WHERE f.event_id=p.event_id)
		AND NOT EXISTS(SELECT 1 FROM pallone_results r WHERE r.character_id=p.character_id AND r.run_id=p.run_id)
		ORDER BY p.started_at,p.character_id,p.run_id LIMIT 256`, now, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PalloneReadyRun
	for rows.Next() {
		var v PalloneReadyRun
		if err = rows.Scan(&v.Receipt.EventID, &v.Receipt.CharID, &v.Receipt.RouteKey, &v.Receipt.RunID, &v.Receipt.StartedAt, &v.Receipt.CompletedAt, &v.Receipt.FinalQuestID,
			&v.Submission.BaseScore, &v.Submission.PremiumBonus, &v.Submission.ItemBonus, &v.Submission.GuildID); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (r *PalloneRepository) RejectPalloneRunReport(charID uint32, runID, reason string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE pallone_run_reports SET rejected_at=COALESCE(rejected_at,$3),rejection_reason=COALESCE(rejection_reason,$4) WHERE character_id=$1 AND run_id=$2`, charID, runID, now, reason)
	return err
}
