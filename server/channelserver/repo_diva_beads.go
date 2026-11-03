package channelserver

import (
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Older in-memory test repositories may continue to provide GetBeads. All
// production reads, including battle-song activation, use the event snapshot.
type DivaEventBeadRepository interface {
	GetEventBeads(eventID uint32) ([]int, error)
}

type divaBeadRound struct {
	Mode    int           `db:"mode"`
	Effects pq.Int64Array `db:"effects"`
	Absence pq.Int64Array `db:"support_absence"`
	Same    int           `db:"same_count"`
	Version int           `db:"rotation_version"`
}

func loadDivaBeadRound(q sqlx.Queryer, eventID uint32) (divaBeadRound, error) {
	var row divaBeadRound
	err := sqlx.Get(q, &row, `SELECT mode,effects,support_absence,same_count,rotation_version
		FROM diva_bead_rounds WHERE event_id=$1`, eventID)
	return row, err
}

func (r divaBeadRound) state() divaBeadRotationState {
	s := divaBeadRotationState{Same: r.Same}
	for _, id := range r.Effects {
		s.Slots = append(s.Slots, int(id))
	}
	for i, age := range r.Absence {
		s.Absence[i] = int(age)
	}
	return s
}

// Existing/manual events are frozen once, without drawing or changing slots.
// INSERT-on-conflict plus a subsequent SELECT also handles concurrent lazy
// readers under READ COMMITTED. No lifecycle/character lock inversion.
func freezeDivaBeadRoundTx(tx *sqlx.Tx, eventID uint32, mode int) (divaBeadRound, error) {
	row, err := loadDivaBeadRound(tx, eventID)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return row, err
	}
	var ids []int
	if err = tx.Select(&ids, `SELECT type FROM diva_beads ORDER BY id LIMIT 4`); err != nil {
		return row, err
	}
	if len(ids) == 0 {
		ids = defaultBeadTypes
	}
	var effects pq.Int64Array
	var mask uint32
	for _, id := range ids {
		if _, valid := divaBeadDefinitionFor(id); !valid || mask&(1<<id) != 0 {
			return row, fmt.Errorf("invalid legacy Diva bead configuration: %v", ids)
		}
		mask |= 1 << id
		effects = append(effects, int64(id))
	}
	_, err = tx.Exec(`INSERT INTO diva_bead_rounds(event_id,mode,effects,support_absence,same_count,rotation_version)
		SELECT id,$2,$3,$4,1,0 FROM events WHERE id=$1 AND event_type='diva'
		ON CONFLICT(event_id) DO NOTHING`, eventID, mode, effects, make(pq.Int64Array, 13))
	if err != nil {
		return row, err
	}
	return loadDivaBeadRound(tx, eventID)
}

// Called in the SAME transaction and lifecycle lock as event creation. The
// active event pointer cannot become visible before its selection is durable.
func ensureDivaBeadRotationTx(tx *sqlx.Tx, eventID, previousID uint32, mode int) error {
	if _, err := loadDivaBeadRound(tx, eventID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previousID == 0 {
		_, err := freezeDivaBeadRoundTx(tx, eventID, mode)
		return err
	}
	previous, err := freezeDivaBeadRoundTx(tx, previousID, mode)
	if err != nil {
		return err
	}
	if previous.Mode != mode {
		return fmt.Errorf("Diva bead rotation mode mismatch: previous=%d next=%d", previous.Mode, mode)
	}
	var entropy [8]byte
	if _, err = cryptorand.Read(entropy[:]); err != nil {
		return err
	}
	seed := int64(binary.LittleEndian.Uint64(entropy[:]) & 0x7fffffffffffffff)
	next := nextDivaBeadRotation(previous.state(), seed)
	var effects, absence pq.Int64Array
	for _, id := range next.Slots {
		effects = append(effects, int64(id))
	}
	for _, age := range next.Absence {
		absence = append(absence, int64(age))
	}
	_, err = tx.Exec(`INSERT INTO diva_bead_rounds
		(event_id,mode,effects,support_absence,same_count,rotation_version,seed,previous_event_id)
		VALUES($1,$2,$3,$4,$5,1,$6,$7)`, eventID, mode, effects, absence, next.Same, seed, previousID)
	return err
}

func (r *DivaRepository) GetEventBeads(eventID uint32) ([]int, error) {
	if eventID == 0 {
		return nil, nil
	}
	row, err := loadDivaBeadRound(r.db, eventID)
	if err == nil {
		return row.state().Slots, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err = divaEventBeadsTx(tx, eventID)
	if err != nil {
		return nil, err
	}
	return row.state().Slots, tx.Commit()
}

func divaEventBeadsTx(tx *sqlx.Tx, eventID uint32) (divaBeadRound, error) {
	row, err := loadDivaBeadRound(tx, eventID)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return row, err
	}
	var mode int
	err = tx.Get(&mode, `SELECT CASE WHEN EXISTS(SELECT 1 FROM diva_debug_song_event d WHERE d.event_id=e.id)
		THEN 1 ELSE COALESCE((SELECT l.mode FROM diva_event_lifecycle l WHERE l.event_id=e.id
		ORDER BY l.mode LIMIT 1),-1) END FROM events e WHERE e.id=$1 AND e.event_type='diva'`, eventID)
	if err != nil {
		return row, err
	}
	return freezeDivaBeadRoundTx(tx, eventID, mode)
}

func (s *Session) divaBeadsForEvent(event DivaEvent) ([]int, error) {
	if repo, ok := s.server.divaRepo.(DivaEventBeadRepository); ok {
		return repo.GetEventBeads(event.ID)
	}
	beads, err := s.server.divaRepo.GetBeads()
	if err == nil && len(beads) == 0 {
		beads = defaultBeadTypes
	}
	return beads, err
}
