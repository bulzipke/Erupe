package channelserver

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrDivaInterceptionLegacy     = errors.New("diva interception round predates the reward cutover")
	ErrDivaInterceptionInvalid    = errors.New("invalid diva interception report")
	ErrDivaInterceptionMembership = errors.New("diva interception guild membership changed")
)

type DivaInterceptionProgress struct {
	Enabled     bool
	HR          uint16
	GR          uint16
	Points      int64
	QuestPoints map[uint16]int64
}

type DivaInterceptionRepository interface {
	GetDivaInterceptionProgress(charID, eventID uint32) (DivaInterceptionProgress, error)
	AddDivaInterceptionPoints(charID, eventID uint32, questID uint16, points, guildID uint32, runKey string, startedAt, now time.Time) error
}

// Never import or sum the unversioned legacy JSON column here.
func (r *DivaRepository) GetDivaInterceptionProgress(charID, eventID uint32) (DivaInterceptionProgress, error) {
	progress := DivaInterceptionProgress{QuestPoints: make(map[uint16]int64)}
	err := r.db.QueryRow(`SELECT COALESCE(c.hr,0),COALESCE(c.gr,0),NOT EXISTS (
		SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=e.id)
		FROM characters c CROSS JOIN events e WHERE c.id=$1 AND e.id=$2 AND e.event_type='diva'`,
		charID, eventID).Scan(&progress.HR, &progress.GR, &progress.Enabled)
	if err != nil || !progress.Enabled {
		return progress, err
	}
	rows, err := r.db.Query(`SELECT quest_id,SUM(points) FROM diva_interception_runs
		WHERE char_id=$1 AND event_id=$2 GROUP BY quest_id ORDER BY quest_id`, charID, eventID)
	if err != nil {
		return progress, err
	}
	defer rows.Close()
	for rows.Next() {
		var questID uint16
		var points int64
		if err := rows.Scan(&questID, &points); err != nil {
			return progress, err
		}
		progress.QuestPoints[questID] = points
		progress.Points += points
	}
	return progress, rows.Err()
}

func validDivaInterceptionRunKey(key string) bool {
	if len(key) != 36 || key[8] != '-' || key[13] != '-' || key[18] != '-' || key[23] != '-' {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(key, "-", ""))
	if err != nil || len(b) != 16 {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return true
		}
	}
	return false
}

// The session supplies a validated quest and server-created departure snapshot;
// this layer independently enforces event, membership and replay limits.
func (r *DivaRepository) AddDivaInterceptionPoints(charID, eventID uint32, questID uint16, points, guildID uint32, runKey string, startedAt, now time.Time) error {
	if charID == 0 || eventID == 0 || questID < udTacticsQuestMin || questID > udTacticsQuestMax || points == 0 || guildID == 0 || !validDivaInterceptionRunKey(runKey) || startedAt.IsZero() || now.Before(startedAt) {
		return ErrDivaInterceptionInvalid
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Same first lock as reward offers/claims; concurrent duplicates serialize.
	var locked uint32
	if err = tx.QueryRow(`SELECT id FROM characters WHERE id=$1 FOR UPDATE`, charID).Scan(&locked); err != nil {
		return err
	}
	var event DivaEvent
	var legacy bool
	err = tx.QueryRow(`SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint,
		EXISTS(SELECT 1 FROM diva_interception_legacy_events l WHERE l.event_id=e.id)
		FROM events e WHERE e.id=$1 AND e.event_type='diva' FOR SHARE OF e`, eventID).Scan(&event.ID, &event.StartTime, &legacy)
	if err != nil {
		return err
	}
	// A successful report retried after leaving the guild or after phase end
	// creates no points. Check it before rejecting new out-of-window reports.
	var oldQuest uint16
	var oldPoints, oldGuild uint32
	var oldStart time.Time
	err = tx.QueryRow(`SELECT quest_id,points,guild_id,started_at FROM diva_interception_runs
		WHERE char_id=$1 AND event_id=$2 AND run_key=$3`, charID, eventID, runKey).Scan(&oldQuest, &oldPoints, &oldGuild, &oldStart)
	if err == nil {
		if oldQuest != questID || oldPoints != points || oldGuild != guildID || !oldStart.Equal(startedAt.Truncate(time.Microsecond)) {
			return ErrDivaInterceptionInvalid
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if legacy {
		// An explicit map activation may collect NEW validated departures in an
		// old personal round. It never imports old totals or unlocks old prizes.
		if err = validateDivaLegacyMapDepartureTx(tx, charID, eventID, guildID, questID, runKey, startedAt, now); err != nil {
			return err
		}
	}
	phaseStart, phaseEnd := divaInterceptionWindow(event)
	if startedAt.Before(phaseStart) || !startedAt.Before(phaseEnd) || now.Before(phaseStart) || !now.Before(phaseEnd) {
		return ErrDivaInterceptionInvalid
	}
	var memberGuild uint32
	err = tx.QueryRow(`SELECT guild_id FROM guild_characters WHERE character_id=$1 FOR SHARE`, charID).Scan(&memberGuild)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && memberGuild != guildID) {
		return ErrDivaInterceptionMembership
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO diva_interception_runs(char_id,event_id,run_key,quest_id,guild_id,points,started_at,submitted_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, charID, eventID, runKey, questID, guildID, points, startedAt.Truncate(time.Microsecond), now.Truncate(time.Microsecond))
	if err != nil {
		return err
	}
	if err := recordDivaMapContributionTx(tx, charID, eventID, runKey, questID, guildID, points, startedAt, now); err != nil {
		return err
	}
	if legacy {
		// Keep the pre-existing personal display and its completed-quest list.
		// The UUID journal makes this increment replay-safe; map progress and
		// the legacy total either commit together or both roll back.
		if _, err = tx.Exec(`UPDATE guild_characters
			SET interception_points = COALESCE(interception_points,'{}'::jsonb) || jsonb_build_object(
				$2::text,COALESCE((interception_points->>$2::text)::bigint,0)+$3::bigint)
			WHERE character_id=$1 AND guild_id=$4`, charID, questID, points, guildID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
