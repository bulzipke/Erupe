package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// Debug phase 1 stays active, but no longer changes the day-zero anchor at
// every midnight. Renewal creates a new event without erasing old records.
func (r *DivaRepository) EnsureDivaSongEvent(now time.Time) (DivaEvent, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return DivaEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(1146507841)"); err != nil {
		return DivaEvent{}, err
	}
	var event DivaEvent
	err = tx.Get(&event, `SELECT e.id,EXTRACT(epoch FROM e.start_time)::bigint AS start_time
		FROM diva_debug_song_event d JOIN events e ON e.id=d.event_id WHERE d.singleton`)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DivaEvent{}, err
	}
	if event.ID == 0 || now.Unix() >= int64(event.StartTime)+divaPhaseDuration {
		t := now.In(divaLocation)
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, divaLocation)
		if err = tx.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", start.UTC()).Scan(&event.ID); err != nil {
			return DivaEvent{}, err
		}
		event.StartTime = uint32(start.Unix())
		if _, err = tx.Exec(`INSERT INTO diva_debug_song_event(singleton,event_id) VALUES(TRUE,$1)
			ON CONFLICT(singleton) DO UPDATE SET event_id=EXCLUDED.event_id`, event.ID); err != nil {
			return DivaEvent{}, err
		}
	}
	return event, tx.Commit()
}

type DivaDay struct {
	Day         time.Time `db:"day_start"`
	Color       int       `db:"bead_index"`
	Quest       int64     `db:"quest_points"`
	Bonus       int64     `db:"bonus_points"`
	SecondColor int       `db:"second_color"`
	SecondQuest int64     `db:"second_quest"`
	SecondBonus int64     `db:"second_bonus"`
}

type divaChoice struct {
	First  int `db:"first_color"`
	Second int `db:"second_color"`
}

func (c divaChoice) active() int {
	if c.Second != 0 {
		return c.Second
	}
	return c.First
}

func (c *divaChoice) selectColor(color int) error {
	if color < 1 || color > 4 {
		return errDivaBeadLocked
	}
	if c.active() == color {
		return nil
	} // Retry does not spend another right.
	if c.First == 0 {
		c.First = color
		return nil
	}
	if c.Second != 0 {
		return errDivaBeadLocked
	}
	c.Second = color
	return nil
}

// Called under the character row lock by selection and contribution writes.
func loadDivaChoice(tx *sqlx.Tx, charID, eventID uint32, day time.Time) (divaChoice, error) {
	var row struct {
		divaChoice
		Day time.Time `db:"day_start"`
	}
	err := tx.Get(&row, `SELECT first_color,second_color,day_start FROM diva_song_choices
		WHERE char_id=$1 AND event_id=$2 AND day_start<=$3 ORDER BY day_start DESC LIMIT 1`, charID, eventID, day)
	if errors.Is(err, sql.ErrNoRows) {
		return divaChoice{}, nil
	}
	if err != nil {
		return divaChoice{}, err
	}
	if row.Day.Before(day) {
		return divaChoice{First: row.active()}, nil
	}
	return row.divaChoice, nil
}

type DivaRank struct {
	CharID  uint32 `db:"char_id"`
	GuildID uint32 `db:"guild_id"`
	IsOwn   bool   `db:"is_own"`
	Rank    uint32 `db:"rank"`
	Name    string `db:"name"`
	Points  int64  `db:"points"`
}

// The total, submission journal and color contribution commit together.
// Reading the selection from DB survives reconnects and channel changes.
func (r *DivaRepository) RecordDivaPoints(charID, eventID, questPoints, bonusPoints uint32, now time.Time) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id uint32
	if err = tx.Get(&id, "SELECT id FROM characters WHERE id=$1 FOR UPDATE", charID); err != nil {
		return err
	}
	// Attribute this contribution at submission time. Joining today's guild
	// membership when reading rankings would move old scores after a transfer.
	var guildID sql.NullInt64
	err = tx.QueryRow(`SELECT guild_id FROM guild_characters WHERE character_id=$1 FOR SHARE`, charID).Scan(&guildID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	day := divaNoon(now)
	choice, err := loadDivaChoice(tx, charID, eventID, day)
	if err != nil {
		return err
	}
	color := choice.active()
	if color != 0 {
		_, err = tx.Exec(`INSERT INTO diva_song_choices(char_id,event_id,day_start,first_color,second_color)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, charID, eventID, day, choice.First, choice.Second)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO diva_points(char_id,event_id,quest_points,bonus_points,updated_at)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(char_id,event_id) DO UPDATE SET
		quest_points=diva_points.quest_points+EXCLUDED.quest_points,
		bonus_points=diva_points.bonus_points+EXCLUDED.bonus_points,updated_at=EXCLUDED.updated_at`,
		charID, eventID, questPoints, bonusPoints, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at,guild_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, charID, eventID, divaNoon(now), color, questPoints, bonusPoints, now, guildID)
	if err != nil {
		return err
	}
	if color != 0 {
		_, err = tx.Exec("INSERT INTO diva_beads_points(character_id,bead_index,points,timestamp) VALUES($1,$2,$3,$4)",
			charID, color, int64(questPoints)+int64(bonusPoints), now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *DivaRepository) GetDivaDays(charID, eventID uint32, firstDay time.Time) ([]DivaDay, error) {
	var days []DivaDay
	err := r.db.Select(&days, `WITH days AS (
		SELECT day_start FROM diva_song_choices WHERE char_id=$1 AND event_id=$2
		UNION SELECT day_start FROM diva_song_records WHERE char_id=$1 AND event_id=$2
	)
	SELECT d.day_start,COALESCE(c.first_color,0) bead_index,COALESCE(c.second_color,0) second_color,
		COALESCE(SUM(r.quest_points) FILTER(WHERE c.second_color IS NULL OR c.second_color=0 OR r.bead_index<>c.second_color),0) quest_points,
		COALESCE(SUM(r.bonus_points) FILTER(WHERE c.second_color IS NULL OR c.second_color=0 OR r.bead_index<>c.second_color),0) bonus_points,
		COALESCE(SUM(r.quest_points) FILTER(WHERE c.second_color>0 AND r.bead_index=c.second_color),0) second_quest,
		COALESCE(SUM(r.bonus_points) FILTER(WHERE c.second_color>0 AND r.bead_index=c.second_color),0) second_bonus
	FROM days d LEFT JOIN diva_song_choices c ON c.day_start=d.day_start AND c.char_id=$1 AND c.event_id=$2
	LEFT JOIN diva_song_records r ON r.day_start=d.day_start AND r.char_id=$1 AND r.event_id=$2
	WHERE d.day_start >= $3 AND d.day_start < $3+interval '8 days'
	GROUP BY d.day_start,c.first_color,c.second_color ORDER BY d.day_start`, charID, eventID, firstDay)
	return days, err
}

// Current personal top 100, plus the requester even when outside the top 100.
func (r *DivaRepository) GetDivaRanking(eventID, charID uint32, cutoff time.Time) ([]DivaRank, error) {
	var ranks []DivaRank
	err := r.db.Select(&ranks, `WITH scores AS (
		SELECT r.char_id,SUM(r.quest_points+r.bonus_points) points FROM diva_song_records r
		JOIN events e ON e.id=r.event_id
		WHERE r.event_id=$1 AND r.submitted_at<$3 AND r.submitted_at>=e.start_time
		AND r.submitted_at<e.start_time+INTERVAL '601200 seconds'
		GROUP BY r.char_id HAVING SUM(r.quest_points+r.bonus_points)>0
	), ranked AS (
		SELECT s.char_id,COALESCE(c.name,'') name,s.points,
			RANK() OVER (ORDER BY s.points DESC) rank,
			ROW_NUMBER() OVER (ORDER BY s.points DESC,s.char_id) position
		FROM scores s JOIN characters c ON c.id=s.char_id
	) SELECT char_id,name,points,rank FROM ranked WHERE position<=100 OR char_id=$2
	ORDER BY points DESC,char_id`, eventID, charID, cutoff)
	return ranks, err
}

// Chronological, completed UTC+9 noon windows only. The native selection UI
// counts published winners before it enables a new day's change right.
func (r *DivaRepository) GetDivaWinningColors(eventID uint32, firstDay, now time.Time) ([]byte, error) {
	var rows []struct {
		Index int `db:"day_index"`
		Color int `db:"color"`
	}
	err := r.db.Select(&rows, `WITH days AS (
		SELECT n day_index,$2::timestamptz+n*interval '1 day' day_start FROM generate_series(0,7) n
	), scores AS (
		SELECT day_start,bead_index,SUM(quest_points) points FROM diva_song_records
		WHERE event_id=$1 AND bead_index BETWEEN 1 AND 4 GROUP BY day_start,bead_index
	)
	SELECT d.day_index,COALESCE((SELECT s.bead_index FROM scores s WHERE s.day_start=d.day_start
		ORDER BY s.points DESC,s.bead_index LIMIT 1),0) color
	FROM days d WHERE d.day_start+interval '1 day'<=$3`, eventID, firstDay, now)
	if err != nil {
		return nil, err
	}
	colors := make([]byte, 8)
	for _, r := range rows {
		colors[r.Index] = byte(r.Color)
	}
	return colors, nil
}
