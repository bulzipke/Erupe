package channelserver

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// RavienteRunKind is the event_quests.quest_type identity retained for a
// communal Raviente run.  The dashboard may combine these later, but keeping
// the source type makes the stored history reclassifiable.
type RavienteRunKind string

const (
	RavienteRunKindUnknown RavienteRunKind = "unknown"
	RavienteRunKindBerserk RavienteRunKind = "berserk"
	RavienteRunKindExtreme RavienteRunKind = "extreme"
	RavienteRunKindSmall   RavienteRunKind = "small"
)

// RavienteRunParticipant is a historical snapshot.  CharacterName is not
// subsequently changed when the live character is renamed or deleted.
type RavienteRunParticipant struct {
	CharacterID   uint32
	CharacterName string
	FirstSeenAt   time.Time
}

// RavienteRunRepository persists one row per whole siege and its unique
// participant snapshots.
type RavienteRunRepository struct {
	db *sqlx.DB
}

func NewRavienteRunRepository(db *sqlx.DB) *RavienteRunRepository {
	return &RavienteRunRepository{db: db}
}

func (r *RavienteRunRepository) AbortActive(channelKey string, endedAt time.Time, reason string) error {
	_, err := r.db.Exec(`
		UPDATE raviente_runs
		SET status = 'aborted', ended_at = $2, duration_ms = NULL,
			end_reason = $3, updated_at = $2
		WHERE channel_key = $1 AND status = 'active'
	`, channelKey, endedAt, reason)
	return err
}

func (r *RavienteRunRepository) Start(channelKey string, generation uint16, startedAt time.Time) (int64, error) {
	var id int64
	err := r.db.QueryRow(`
		INSERT INTO raviente_runs
			(channel_key, raviente_generation, event_kind, status, started_at, updated_at)
		VALUES ($1, $2, 'unknown', 'active', $3, $3)
		RETURNING id
	`, channelKey, generation, startedAt).Scan(&id)
	return id, err
}

func (r *RavienteRunRepository) AddParticipant(runID int64, kind RavienteRunKind, participant RavienteRunParticipant) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(`
		UPDATE raviente_runs
		SET event_kind = CASE WHEN event_kind = 'unknown' THEN $2 ELSE event_kind END,
			updated_at = $3
		WHERE id = $1 AND status = 'active'
		  AND (event_kind = 'unknown' OR event_kind = $2)
	`, runID, string(kind), participant.FirstSeenAt)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		// The run already ended, or a conflicting quest type was presented.
		// In either case a late participant must not mutate its snapshot.
		return tx.Commit()
	}

	_, err = tx.Exec(`
		INSERT INTO raviente_run_participants
			(run_id, character_id_snapshot, character_name_snapshot, first_seen_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id, character_id_snapshot) DO NOTHING
	`, runID, participant.CharacterID, participant.CharacterName, participant.FirstSeenAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *RavienteRunRepository) Complete(
	runID int64,
	kind RavienteRunKind,
	endedAt time.Time,
	duration time.Duration,
	participants []RavienteRunParticipant,
) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, participant := range participants {
		_, err = tx.Exec(`
			INSERT INTO raviente_run_participants
				(run_id, character_id_snapshot, character_name_snapshot, first_seen_at)
			SELECT $1, $2, $3, $4
			WHERE EXISTS (
				SELECT 1 FROM raviente_runs WHERE id = $1 AND status = 'active'
			)
			ON CONFLICT (run_id, character_id_snapshot) DO NOTHING
		`, runID, participant.CharacterID, participant.CharacterName, participant.FirstSeenAt)
		if err != nil {
			return err
		}
	}

	durationMilliseconds := duration.Milliseconds()
	if durationMilliseconds < 1 {
		durationMilliseconds = 1
	}
	_, err = tx.Exec(`
		UPDATE raviente_runs
		SET event_kind = $2, status = 'completed', ended_at = $3,
			duration_ms = $4, end_reason = 'killed_time', updated_at = $3
		WHERE id = $1 AND status = 'active'
	`, runID, string(kind), endedAt, durationMilliseconds)
	if err != nil {
		return err
	}
	// An already-completed/aborted row affects zero rows.  Treat that as an
	// idempotent no-op; the status transition itself remains one-way.
	return tx.Commit()
}

func (r *RavienteRunRepository) Abort(runID int64, endedAt time.Time, reason string) error {
	_, err := r.db.Exec(`
		UPDATE raviente_runs
		SET status = 'aborted', ended_at = $2, duration_ms = NULL,
			end_reason = $3, updated_at = $2
		WHERE id = $1 AND status = 'active'
	`, runID, endedAt, reason)
	return err
}

func (r *RavienteRunRepository) ResolveQuestKind(questID uint16) (RavienteRunKind, bool, error) {
	var questTypes []int
	err := r.db.Select(&questTypes, `
		SELECT DISTINCT quest_type
		FROM event_quests
		WHERE quest_id = $1 AND quest_type IN ($2, $3, $4)
		ORDER BY quest_type
	`, questID, QuestTypeBerserkRaviente, QuestTypeExtremeRaviente, QuestTypeSmallBerserkRavi)
	if err != nil {
		return RavienteRunKindUnknown, false, err
	}
	if len(questTypes) == 0 {
		return RavienteRunKindUnknown, false, nil
	}
	if len(questTypes) != 1 {
		return RavienteRunKindUnknown, false, fmt.Errorf("quest %d has conflicting Raviente quest types %v", questID, questTypes)
	}
	switch questTypes[0] {
	case int(QuestTypeBerserkRaviente):
		return RavienteRunKindBerserk, true, nil
	case int(QuestTypeExtremeRaviente):
		return RavienteRunKindExtreme, true, nil
	case int(QuestTypeSmallBerserkRavi):
		return RavienteRunKindSmall, true, nil
	default:
		return RavienteRunKindUnknown, false, nil
	}
}
