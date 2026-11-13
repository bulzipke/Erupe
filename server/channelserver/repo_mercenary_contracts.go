package channelserver

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

const (
	mercenaryContractLock = 7 * 24 * time.Hour
	mercenaryMaxLoans     = 3
)

var errMercenaryContract = errors.New("invalid mercenary contract change")

func (r *MercenaryRepository) ChangeMercenaryPact(owner, borrower, actor, rastaID uint32, op uint8, now time.Time) error {
	if owner == 0 || borrower == 0 || owner == borrower || rastaID == 0 {
		return errMercenaryContract
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Lock both characters in the same order for reciprocal and concurrent pacts.
	var ids []uint32
	if err := tx.Select(&ids, `SELECT id FROM characters WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, owner, borrower); err != nil {
		return err
	}
	if len(ids) != 2 {
		return errMercenaryContract
	}
	var ownerRasta, borrowerRasta, existing uint32
	var since sql.NullTime
	if err := tx.QueryRow(`SELECT COALESCE(o.rasta_id,0), COALESCE(b.rasta_id,0), COALESCE(b.pact_id,0), b.mercenary_contract_at
		FROM characters o, characters b WHERE o.id=$1 AND b.id=$2`, owner, borrower).
		Scan(&ownerRasta, &borrowerRasta, &existing, &since); err != nil {
		return err
	}
	if ownerRasta != rastaID {
		return errMercenaryContract
	}
	event := 0
	switch op {
	case 0:
		if actor != owner || borrowerRasta == 0 {
			return errMercenaryContract
		}
		if existing == rastaID && since.Valid {
			return nil
		}
		var loans int
		if err := tx.Get(&loans, `SELECT count(*) FROM characters WHERE pact_id=$1 AND id NOT IN ($2,$3)`, rastaID, owner, borrower); err != nil {
			return err
		}
		if existing != 0 || loans >= mercenaryMaxLoans {
			return errMercenaryContract
		}
		_, err = tx.Exec(`UPDATE characters SET pact_id=$1, mercenary_contract_at=$2, mercenary_escort=true WHERE id=$3`,
			rastaID, now, borrower)
	case 1, 2:
		if (op == 1 && actor != borrower) || (op == 2 && actor != owner) ||
			existing != rastaID || (since.Valid && now.Before(since.Time.Add(mercenaryContractLock))) {
			return errMercenaryContract
		}
		event = 3
		if op == 1 {
			event = 4
		}
		_, err = tx.Exec(`UPDATE characters SET pact_id=0, mercenary_contract_at=NULL, mercenary_escort=true WHERE id=$1`, borrower)
	default:
		return errMercenaryContract
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO mercenary_contract_logs(owner_id,borrower_id,actor_id,occurred_at,event)
		VALUES($1,$2,$3,$4,$5)`, owner, borrower, actor, now, event); err != nil {
		return err
	}
	return tx.Commit()
}

type MercenaryContractLog struct {
	Name      string
	When      time.Time
	Initiated bool
	Event     uint8
}

func (r *MercenaryRepository) GetMercenaryContractLogs(charID uint32) ([]MercenaryContractLog, error) {
	rows, err := r.db.Query(`SELECT COALESCE(c.name,''), l.occurred_at, l.actor_id=$1, l.event
		FROM mercenary_contract_logs l
		LEFT JOIN characters c ON c.id=CASE WHEN l.owner_id=$1 THEN l.borrower_id ELSE l.owner_id END
		WHERE l.owner_id=$1 OR l.borrower_id=$1 ORDER BY l.id DESC LIMIT 50`, charID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []MercenaryContractLog
	for rows.Next() {
		var entry MercenaryContractLog
		if err := rows.Scan(&entry.Name, &entry.When, &entry.Initiated, &entry.Event); err != nil {
			return nil, err
		}
		logs = append(logs, entry)
	}
	return logs, rows.Err()
}

const mercenaryHuntLimit = 5

type MercenaryHunt struct {
	ID      int32 `db:"hunt_id"`
	Monster int32 `db:"monster"`
}

func mercenaryHuntWindow(now time.Time) time.Time {
	// The game and server both use UTC+9, so the twelve-hour boundary is
	// calculated in wall time rather than with time.Truncate.
	hour := now.Hour() / 12 * 12
	return time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
}

func (r *MercenaryRepository) RecordMercenaryHunts(borrower, rastaID uint32, startedAt, endedAt time.Time, monsters []int32) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var owner uint32
	err = tx.Get(&owner, `SELECT o.id FROM characters b JOIN characters o ON o.rasta_id=b.pact_id
		WHERE b.id=$1 AND b.id<>o.id AND b.pact_id=$2 AND b.mercenary_contract_at<=$3
		ORDER BY o.id LIMIT 1 FOR UPDATE OF o`, borrower, rastaID, startedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
	INSERT INTO mercenary_hunts(owner_id, monster)
	SELECT $1, m FROM unnest($2::integer[]) m
	WHERE NOT EXISTS (SELECT 1 FROM mercenary_hunt_claims c WHERE c.owner_id=$1 AND c.claimed_at >= $3)
	ON CONFLICT (owner_id,monster) DO NOTHING`, owner, pq.Array(monsters), mercenaryHuntWindow(endedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *MercenaryRepository) PreviewMercenaryHunts(owner uint32, now time.Time) ([]MercenaryHunt, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var id uint32
	if err := tx.Get(&id, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, owner); err != nil {
		return nil, err
	}
	var ids, monsters pq.Int32Array
	var claimed sql.NullTime
	err = tx.QueryRow(`SELECT hunt_ids, monsters, claimed_at FROM mercenary_hunt_claims WHERE owner_id=$1`, owner).
		Scan(&ids, &monsters, &claimed)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if claimed.Valid && !claimed.Time.Before(mercenaryHuntWindow(now)) {
		return nil, nil
	}
	if len(ids) > 0 {
		hunts := make([]MercenaryHunt, len(ids))
		for i, id := range ids {
			hunts[i] = MercenaryHunt{ID: id, Monster: monsters[i]}
		}
		return hunts, nil
	}
	var hunts []MercenaryHunt
	if err := tx.Select(&hunts, `SELECT hunt_id,monster FROM mercenary_hunts WHERE owner_id=$1
		ORDER BY hunt_id DESC LIMIT $2`, owner, mercenaryHuntLimit); err != nil || len(hunts) == 0 {
		return nil, err
	}
	for _, hunt := range hunts {
		ids = append(ids, hunt.ID)
		monsters = append(monsters, hunt.Monster)
	}
	if _, err := tx.Exec(`INSERT INTO mercenary_hunt_claims(owner_id,hunt_ids,monsters)
		VALUES($1,$2,$3) ON CONFLICT(owner_id) DO UPDATE SET hunt_ids=EXCLUDED.hunt_ids, monsters=EXCLUDED.monsters`,
		owner, ids, monsters); err != nil {
		return nil, err
	}
	return hunts, tx.Commit()
}

func (r *MercenaryRepository) ClaimMercenaryHunts(owner uint32, now time.Time) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var id uint32
	if err := tx.Get(&id, `SELECT id FROM characters WHERE id=$1 FOR UPDATE`, owner); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE mercenary_hunt_claims SET hunt_ids='{}',monsters='{}',claimed_at=$2
		WHERE owner_id=$1 AND cardinality(hunt_ids)>0 AND (claimed_at IS NULL OR claimed_at<$3)`,
		owner, now, mercenaryHuntWindow(now))
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	// The native list holds five materials. Once collected, it starts fresh
	// at the next window.
	if _, err := tx.Exec(`DELETE FROM mercenary_hunts WHERE owner_id=$1`, owner); err != nil {
		return err
	}
	return tx.Commit()
}
