package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const (
	divaOrdinarySongLimit       = 20
	divaFinalSongLimit          = 25
	divaSongTargetUnit          = 1000
	divaSongPointsPerAccountMin = 12000
	divaSongPointsPerAccountMax = 32000
)

// Repeating the last prayer week's effort earns the ordinary retail limit.
// The final five uses require extra contribution. The per-account bounds keep
// a mostly empty week or a few very high scorers from setting the next bar.
func divaSongTargets(accounts, points int64) []int64 {
	accounts = max(accounts, 1)
	ordinaryTarget := max(accounts*divaSongPointsPerAccountMin,
		min(points, accounts*divaSongPointsPerAccountMax))
	targets := make([]int64, divaFinalSongLimit)
	for i := range targets {
		uses := int64(i + 1)
		unrounded := ordinaryTarget * uses / divaOrdinarySongLimit
		// Keep even the smallest round's goals positive and distinct.
		targets[i] = max(unrounded/divaSongTargetUnit, uses) * divaSongTargetUnit
	}
	return targets
}

// Called under the lifecycle lock. Opt in the first time an active normal
// round is resolved, including an in-progress Battle Song chapter with no
// prior activations. Read the prior round only when creating the snapshot.
func ensureDivaSongGoalsTx(tx *sqlx.Tx, event DivaEvent, now time.Time) error {
	if !event.Continuous || now.Before(event.phaseTime(0)) || !now.Before(event.phaseTime(3)) {
		return nil
	}
	var pinned bool
	if err := tx.Get(&pinned, `SELECT EXISTS(SELECT 1 FROM diva_song_goal_rounds WHERE event_id=$1)
		OR EXISTS(SELECT 1 FROM diva_battle_songs WHERE event_id=$1)`, event.ID); err != nil || pinned {
		return err
	}

	var previous struct {
		ID       sql.NullInt64 `db:"event_id"`
		Accounts int64         `db:"accounts"`
		Points   int64         `db:"points"`
	}
	if err := tx.Get(&previous, `WITH previous AS (
		SELECT e.id,e.start_time,e.diva_continuous FROM events e
		JOIN diva_bead_rounds b ON b.event_id=e.id AND b.mode=-1
		WHERE e.start_time+CASE WHEN e.diva_continuous THEN interval '168 hours'
			ELSE interval '601200 seconds' END <= $1
		ORDER BY e.start_time DESC,e.id DESC LIMIT 1
	)
	SELECT (SELECT id FROM previous) event_id,COUNT(DISTINCT c.user_id) accounts,
		COALESCE(SUM(r.quest_points),0) points
	FROM previous p JOIN diva_song_records r ON r.event_id=p.id
	JOIN characters c ON c.id=r.char_id
	WHERE r.bead_index BETWEEN 1 AND 4 AND r.quest_points>0
		AND r.submitted_at>=p.start_time AND r.submitted_at<p.start_time+
		CASE WHEN p.diva_continuous THEN interval '168 hours' ELSE interval '601200 seconds' END`,
		event.phaseTime(0)); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO diva_song_goal_rounds(event_id,previous_event_id,previous_accounts,previous_points,targets)
		VALUES($1,$2,$3,$4,$5)`, event.ID, previous.ID, previous.Accounts, previous.Points,
		pq.Array(divaSongTargets(previous.Accounts, previous.Points)))
	return err
}

type divaSongProgress struct {
	Total   int64
	Targets []int64
}

type divaSongProgressRepository interface {
	GetDivaSongProgress(event DivaEvent) (divaSongProgress, error)
}

func (r *DivaRepository) GetDivaSongProgress(event DivaEvent) (divaSongProgress, error) {
	return loadDivaSongProgress(r.db, event)
}

// Use the same qualified base-point total and frozen targets for the NPC
// progress packet and actual activation. Historical and forced rounds keep
// the aggregate and thresholds they were originally served with.
func loadDivaSongProgress(q sqlx.Queryer, event DivaEvent) (divaSongProgress, error) {
	var progress divaSongProgress
	var targets pq.Int64Array
	err := sqlx.Get(q, &targets, `SELECT targets FROM diva_song_goal_rounds WHERE event_id=$1`, event.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		progress.Targets = udMilestones
		err = sqlx.Get(q, &progress.Total, `SELECT COALESCE(SUM(quest_points::bigint+bonus_points),0)
			FROM diva_points WHERE event_id=$1`, event.ID)
	case err == nil:
		progress.Targets = targets
		err = sqlx.Get(q, &progress.Total, `SELECT COALESCE(SUM(quest_points),0)
			FROM diva_song_records WHERE event_id=$1 AND bead_index BETWEEN 1 AND 4
			AND submitted_at>=$2 AND submitted_at<$3`, event.ID, event.phaseTime(0), event.phaseTime(1))
	}
	return progress, err
}
