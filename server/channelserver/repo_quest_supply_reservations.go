package channelserver

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"time"

	"erupe-ce/common/questsupplies"
	"github.com/jmoiron/sqlx"
)

// This repository is deliberately not wired into runtime initialization or any
// game handler. It records an internal financial/delivery obligation only.
// It does not allocate native reward IDs, offer items, consume a reservation,
// update savedata/item_box, grant quest authority, or claim delivery success.
type QuestSupplyReservationRepository struct {
	db  *sqlx.DB
	now func() time.Time
}

var _ QuestSupplyReservationStore = (*QuestSupplyReservationRepository)(nil)

func NewQuestSupplyReservationRepository(db *sqlx.DB) *QuestSupplyReservationRepository {
	return &QuestSupplyReservationRepository{db: db, now: time.Now}
}

type QuestSupplyOccurrence struct {
	WindowID     string
	OccurrenceID string
}

type QuestSupplyReservationStatus uint8

const (
	QuestSupplyReservationUnavailable QuestSupplyReservationStatus = iota
	QuestSupplyReservationOK
	QuestSupplyReservationConflict
	QuestSupplyReservationClosed
	QuestSupplyReservationInsufficient
	QuestSupplyReservationPendingExists
)

// ID is an internal bigint reservation identity, NOT a native collectible ID.
// Every row remains pending: settlement needs a separately verified inventory
// adapter and an atomic save/obligation transition, which are not implemented.
type QuestSupplyReservation struct {
	ID           int64         `db:"id"`
	UserID       uint32        `db:"user_id"`
	CharacterID  uint32        `db:"character_id"`
	SupplyKey    string        `db:"supply_key"`
	WindowID     string        `db:"window_key"`
	Kind         string        `db:"grant_kind"`
	PolicyHash   string        `db:"policy_hash"`
	RequestHash  string        `db:"request_hash"`
	Nonce        []byte        `db:"nonce"`
	ItemType     uint8         `db:"item_type"`
	ItemID       uint16        `db:"item_id"`
	Quantity     uint16        `db:"quantity"`
	PriceGoldG   uint32        `db:"price_gold_g"`
	BalanceAfter sql.NullInt64 `db:"balance_after"`
	ReservedAt   time.Time     `db:"reserved_at"`
}

type QuestSupplyReservationResult struct {
	Status      QuestSupplyReservationStatus
	Reservation QuestSupplyReservation
	Replay      bool
}

var questSupplyOccurrencePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,95}$`)

func validQuestSupplyIdentity(userID, characterID uint32) bool {
	return userID > 0 && userID <= math.MaxInt32 && characterID > 0 && characterID <= math.MaxInt32
}

func validQuestSupplyTimes(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && start.Before(end) && start.UTC().Year() >= 1 && end.UTC().Year() <= 9999 &&
		start.Nanosecond()%1000 == 0 && end.Nanosecond()%1000 == 0
}

func approvedQuestSupplyHash(key string) (string, bool) {
	approved, err := questsupplies.NewApprovedSeasonalDraft()
	if err != nil {
		return "", false
	}
	hash, found := approved.DefinitionDigest(key)
	return hex.EncodeToString(hash[:]), found
}

// Bind is an explicit local/admin preparation operation, never a startup hook.
// Only the two already-approved exact semantic definitions can be registered.
// An occurrence binds to one stable window ID; date edits use ReviseWindow and
// cannot mint another free quota. Supplying a new catalog version has no quota
// effect. No native campaign/category/receipt IDs are allocated here.
func (r *QuestSupplyReservationRepository) Bind(ctx context.Context, catalog *questsupplies.Catalog, occurrences []QuestSupplyOccurrence) error {
	if r == nil || r.db == nil || catalog == nil {
		return fmt.Errorf("seasonal reservation binding unavailable")
	}
	doc := catalog.Document()
	byWindow := make(map[string]string, len(occurrences))
	for _, b := range occurrences {
		if !questSupplyOccurrencePattern.MatchString(b.OccurrenceID) || byWindow[b.WindowID] != "" {
			return fmt.Errorf("invalid or duplicate seasonal occurrence binding")
		}
		byWindow[b.WindowID] = b.OccurrenceID
	}
	if len(byWindow) != len(doc.Windows) {
		return fmt.Errorf("every seasonal window requires one occurrence binding")
	}
	for _, d := range doc.Definitions {
		expected, approved := approvedQuestSupplyHash(d.Key)
		actual, exists := catalog.DefinitionDigest(d.Key)
		if !approved || !exists || expected != hex.EncodeToString(actual[:]) {
			return fmt.Errorf("seasonal definition differs from approved policy")
		}
	}
	for _, w := range doc.Windows {
		if byWindow[w.ID] == "" || !validQuestSupplyTimes(w.StartsAt, w.EndsAt) {
			return fmt.Errorf("invalid seasonal window or missing occurrence identity")
		}
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('quest_supply_reservation_bind_v1',0))`); err != nil {
		return err
	}
	for _, d := range doc.Definitions {
		hash, _ := approvedQuestSupplyHash(d.Key)
		_, err = tx.ExecContext(ctx, `INSERT INTO quest_supply_definitions(supply_key,grant_kind,item_type,item_id,quantity,price_gold_g,policy_hash)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(supply_key) DO NOTHING`, d.Key, d.Kind, d.Delivery.ItemType, d.Delivery.ItemID, d.Delivery.Quantity, d.PriceGoldG, hash)
		if err != nil {
			return err
		}
		var existing string
		if err = tx.QueryRowContext(ctx, `SELECT policy_hash FROM quest_supply_definitions WHERE supply_key=$1`, d.Key).Scan(&existing); err != nil {
			return err
		}
		if existing != hash {
			return fmt.Errorf("immutable seasonal policy binding conflict")
		}
	}
	for _, w := range doc.Windows {
		_, err = tx.ExecContext(ctx, `INSERT INTO quest_supply_windows(window_key,supply_key,occurrence_key)
			VALUES($1,$2,$3) ON CONFLICT(window_key) DO NOTHING`, w.ID, w.SupplyKey, byWindow[w.ID])
		if err != nil {
			return err
		}
		var supply, occurrence string
		if err = tx.QueryRowContext(ctx, `SELECT supply_key,occurrence_key FROM quest_supply_windows WHERE window_key=$1`, w.ID).Scan(&supply, &occurrence); err != nil {
			return err
		}
		if supply != w.SupplyKey || occurrence != byWindow[w.ID] {
			return fmt.Errorf("immutable seasonal occurrence binding conflict")
		}
		var revision int
		var start, end time.Time
		err = tx.QueryRowContext(ctx, `SELECT a.revision,v.starts_at,v.ends_at FROM quest_supply_active_windows a
			JOIN quest_supply_window_revisions v USING(window_key,revision) WHERE a.window_key=$1`, w.ID).Scan(&revision, &start, &end)
		if err == nil {
			if !start.Equal(w.StartsAt) || !end.Equal(w.EndsAt) {
				return fmt.Errorf("date changes require an explicit seasonal window revision")
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if err = rejectOverlappingQuestSupplyWindow(ctx, tx, w.ID, w.SupplyKey, w.StartsAt, w.EndsAt); err != nil {
			return err
		}
		var insertedRevision int
		if err = tx.QueryRowContext(ctx, `INSERT INTO quest_supply_window_revisions(window_key,revision,starts_at,ends_at) VALUES($1,1,$2,$3) RETURNING revision`, w.ID, w.StartsAt, w.EndsAt).Scan(&insertedRevision); err != nil {
			return err
		}
		if insertedRevision != 1 {
			return fmt.Errorf("seasonal initial revision differs from requested revision")
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO quest_supply_active_windows(window_key,revision) VALUES($1,1) RETURNING revision`, w.ID).Scan(&insertedRevision); err != nil {
			return err
		}
		if insertedRevision != 1 {
			return fmt.Errorf("seasonal initial active revision differs from requested revision")
		}
	}
	return tx.Commit()
}

func rejectOverlappingQuestSupplyWindow(ctx context.Context, tx *sqlx.Tx, id, supply string, start, end time.Time) error {
	var overlap bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quest_supply_windows w
		JOIN quest_supply_active_windows a USING(window_key) JOIN quest_supply_window_revisions v USING(window_key,revision)
		WHERE w.supply_key=$1 AND w.window_key<>$2 AND v.starts_at<$4 AND $3<v.ends_at)`, supply, id, start, end).Scan(&overlap); err != nil {
		return err
	}
	if overlap {
		return fmt.Errorf("overlapping seasonal occurrence windows")
	}
	return nil
}

// ReviseWindow appends dates under the SAME occurrence/window identity. It
// neither deletes nor resets reservations. A stale expected revision is denied.
func (r *QuestSupplyReservationRepository) ReviseWindow(ctx context.Context, id string, expectedRevision int, start, end time.Time) error {
	if r == nil || r.db == nil || expectedRevision < 1 || expectedRevision == math.MaxInt32 || !validQuestSupplyTimes(start, end) {
		return fmt.Errorf("invalid seasonal window revision")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('quest_supply_reservation_bind_v1',0))`); err != nil {
		return err
	}
	var supply string
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT w.supply_key,a.revision FROM quest_supply_windows w
		JOIN quest_supply_active_windows a USING(window_key) WHERE w.window_key=$1 FOR UPDATE OF a`, id).Scan(&supply, &current); err != nil {
		return err
	}
	if current != expectedRevision {
		return fmt.Errorf("seasonal window revision conflict")
	}
	if err = rejectOverlappingQuestSupplyWindow(ctx, tx, id, supply, start, end); err != nil {
		return err
	}
	var actualRevision int
	if err = tx.QueryRowContext(ctx, `INSERT INTO quest_supply_window_revisions(window_key,revision,starts_at,ends_at) VALUES($1,$2,$3,$4) RETURNING revision`, id, current+1, start, end).Scan(&actualRevision); err != nil {
		return err
	}
	if actualRevision != current+1 {
		return fmt.Errorf("seasonal inserted revision differs from requested revision")
	}
	if err = tx.QueryRowContext(ctx, `UPDATE quest_supply_active_windows SET revision=$2 WHERE window_key=$1 RETURNING revision`, id, current+1).Scan(&actualRevision); err != nil {
		return err
	}
	if actualRevision != current+1 {
		return fmt.Errorf("seasonal active revision differs from requested revision")
	}
	return tx.Commit()
}

func lockQuestSupplyIdentity(ctx context.Context, tx *sqlx.Tx, userID, characterID uint32) (int64, bool, error) {
	var balance int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(gacha_premium,0) FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&balance); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	var id uint32
	if err := tx.QueryRowContext(ctx, `SELECT id FROM characters WHERE id=$1 AND user_id=$2 AND NOT deleted FOR UPDATE`, characterID, userID).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if balance < 0 || balance > math.MaxUint32 {
		return 0, false, fmt.Errorf("invalid premium currency balance")
	}
	return balance, true, nil
}

// Replay reads only a previously committed paid obligation using a persisted
// request contract. Unlike Prepare, it needs no current catalog or open season,
// so a new process can recover the exact request after expiry. The authenticated
// identity must come from the session, not this request's account field. found
// also reports conflicting reuse of an existing nonce; it never books a debit.
func (r *QuestSupplyReservationRepository) Replay(ctx context.Context, userID, characterID uint32, contract questsupplies.PaidReplayContract) (QuestSupplyReservationResult, bool, error) {
	result := QuestSupplyReservationResult{Status: QuestSupplyReservationUnavailable}
	if !validQuestSupplyIdentity(userID, characterID) || contract.Scope.AccountID != userID || questsupplies.ComparePaidReplay(contract, contract) != questsupplies.ReplayExactRequest {
		return result, false, nil
	}
	if r == nil || r.db == nil {
		return result, false, fmt.Errorf("seasonal reservation repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return result, false, err
	}
	defer func() { _ = tx.Rollback() }()
	_, active, err := lockQuestSupplyIdentity(ctx, tx, userID, characterID)
	if err != nil || !active {
		return result, false, err
	}
	var prior QuestSupplyReservation
	err = tx.GetContext(ctx, &prior, `SELECT * FROM quest_supply_reservations WHERE user_id=$1 AND nonce=$2`, userID, contract.Scope.Nonce[:])
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if prior.CharacterID != characterID || prior.RequestHash != hex.EncodeToString(contract.ContentDigest[:]) {
		result.Status = QuestSupplyReservationConflict
		return result, true, nil
	}
	return QuestSupplyReservationResult{Status: QuestSupplyReservationOK, Reservation: prior, Replay: true}, true, nil
}

// Reserve atomically books an approved pending obligation. A committed exact
// paid retry is resolved BEFORE fresh-window checks, including after expiry or
// date revision. This accepts only trusted Prepared values, not wire fields.
func (r *QuestSupplyReservationRepository) Reserve(ctx context.Context, prepared questsupplies.Prepared) (QuestSupplyReservationResult, error) {
	result := QuestSupplyReservationResult{Status: QuestSupplyReservationUnavailable}
	p := prepared.Preview()
	hash, approved := approvedQuestSupplyHash(p.Definition.Key)
	if !approved || !validQuestSupplyIdentity(p.Identity.AccountID, p.Identity.CharacterID) || hash != hex.EncodeToString(p.DefinitionDigest[:]) || p.LiveEnabled || p.CampaignID != 0 {
		return result, nil
	}
	free, isFree := prepared.FreeClaimScope()
	paid, isPaid := prepared.PaidReplayContract()
	if (!isFree && !isPaid) || (isFree && (free.CharacterID == 0 || free.WindowID == "")) ||
		(isPaid && questsupplies.ComparePaidReplay(paid, paid) != questsupplies.ReplayExactRequest) {
		return result, nil
	}
	if r == nil || r.db == nil || r.now == nil {
		return result, fmt.Errorf("seasonal reservation repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	balance, active, err := lockQuestSupplyIdentity(ctx, tx, p.Identity.AccountID, p.Identity.CharacterID)
	if err != nil || !active {
		return result, err
	}
	var prior QuestSupplyReservation
	if isPaid {
		err = tx.GetContext(ctx, &prior, `SELECT * FROM quest_supply_reservations WHERE user_id=$1 AND nonce=$2`, p.Identity.AccountID, paid.Scope.Nonce[:])
		if err == nil {
			if prior.CharacterID != p.Identity.CharacterID || prior.RequestHash != hex.EncodeToString(p.RequestDigest[:]) {
				result.Status = QuestSupplyReservationConflict
				return result, nil
			}
			return QuestSupplyReservationResult{Status: QuestSupplyReservationOK, Reservation: prior, Replay: true}, nil
		}
	} else {
		err = tx.GetContext(ctx, &prior, `SELECT * FROM quest_supply_reservations
			WHERE character_id=$1 AND supply_key=$2 AND window_key=$3 AND grant_kind=$4`, p.Identity.CharacterID, p.Definition.Key, p.Window.ID, questsupplies.FreeCharacterWindow)
		if err == nil {
			if prior.UserID != p.Identity.AccountID {
				result.Status = QuestSupplyReservationConflict
				return result, nil
			}
			return QuestSupplyReservationResult{Status: QuestSupplyReservationOK, Reservation: prior, Replay: true}, nil
		}
	}
	if err != sql.ErrNoRows {
		return result, err
	}
	var activeHash string
	var start, end time.Time
	err = tx.QueryRowContext(ctx, `SELECT d.policy_hash,v.starts_at,v.ends_at FROM quest_supply_windows w
		JOIN quest_supply_definitions d USING(supply_key) JOIN quest_supply_active_windows a USING(window_key)
		JOIN quest_supply_window_revisions v USING(window_key,revision) WHERE w.window_key=$1 AND w.supply_key=$2 FOR SHARE OF a`, p.Window.ID, p.Definition.Key).Scan(&activeHash, &start, &end)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if activeHash != hash || !start.Equal(p.Window.StartsAt) || !end.Equal(p.Window.EndsAt) {
		return result, nil
	}
	now := r.now()
	if now.IsZero() || now.Before(start) || !now.Before(end) {
		result.Status = QuestSupplyReservationClosed
		return result, nil
	}
	var paidBalanceAfter int64
	if isPaid {
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quest_supply_pending_holds WHERE character_id=$1 AND supply_key=$2)`, p.Identity.CharacterID, p.Definition.Key).Scan(&pending); err != nil {
			return result, err
		}
		if pending {
			result.Status = QuestSupplyReservationPendingExists
			return result, nil
		}
		if balance < int64(p.Definition.PriceGoldG) {
			result.Status = QuestSupplyReservationInsufficient
			return result, nil
		}
		// A successful Exec alone can mean zero affected rows (for example a
		// BEFORE trigger returning NULL). Require the locked account's actual
		// returned balance before recording a paid obligation. The primary key
		// bounds this UPDATE to one row; no row or an altered debit fails closed.
		if err = tx.QueryRowContext(ctx, `UPDATE users SET gacha_premium=COALESCE(gacha_premium,0)-$2 WHERE id=$1
			RETURNING COALESCE(gacha_premium,0)`, p.Identity.AccountID, p.Definition.PriceGoldG).Scan(&paidBalanceAfter); err != nil {
			return result, err
		}
		if paidBalanceAfter != balance-int64(p.Definition.PriceGoldG) {
			return result, fmt.Errorf("seasonal premium debit differs from approved price")
		}
	}
	var nonce []byte
	var after sql.NullInt64
	if isPaid {
		nonce = paid.Scope.Nonce[:]
		after = sql.NullInt64{Int64: paidBalanceAfter, Valid: true}
	}
	var row QuestSupplyReservation
	err = tx.GetContext(ctx, &row, `INSERT INTO quest_supply_reservations(user_id,character_id,supply_key,window_key,grant_kind,policy_hash,request_hash,nonce,item_type,item_id,quantity,price_gold_g,balance_after)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING *`, p.Identity.AccountID, p.Identity.CharacterID,
		p.Definition.Key, p.Window.ID, p.Definition.Kind, hash, hex.EncodeToString(p.RequestDigest[:]), nonce,
		p.Definition.Delivery.ItemType, p.Definition.Delivery.ItemID, p.Definition.Delivery.Quantity, p.Definition.PriceGoldG, after)
	if err != nil {
		return result, err
	}
	var heldReservationID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO quest_supply_pending_holds(reservation_id,character_id,supply_key,grant_kind) VALUES($1,$2,$3,$4)
		RETURNING reservation_id`, row.ID, row.CharacterID, row.SupplyKey, row.Kind).Scan(&heldReservationID); err != nil {
		return result, err
	}
	if heldReservationID != row.ID {
		return result, fmt.Errorf("seasonal pending hold differs from booked obligation")
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return QuestSupplyReservationResult{Status: QuestSupplyReservationOK, Reservation: row}, nil
}

// Pending recovers existing obligations across reconnect/restart/catalog expiry
// without constructing another reservation or charging currency. No native
// item or receipt is returned. A bounded whole result is required, never a trim.
func (r *QuestSupplyReservationRepository) Pending(ctx context.Context, userID, characterID uint32) ([]QuestSupplyReservation, error) {
	if !validQuestSupplyIdentity(userID, characterID) {
		return nil, nil
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("seasonal reservation repository unavailable")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, active, err := lockQuestSupplyIdentity(ctx, tx, userID, characterID)
	if err != nil || !active {
		return nil, err
	}
	var rows []QuestSupplyReservation
	if err = tx.SelectContext(ctx, &rows, `SELECT r.* FROM quest_supply_reservations r JOIN quest_supply_pending_holds h ON h.reservation_id=r.id
		WHERE r.user_id=$1 AND r.character_id=$2 ORDER BY r.id LIMIT 513`, userID, characterID); err != nil {
		return nil, err
	}
	if len(rows) > 512 {
		return nil, fmt.Errorf("seasonal pending obligations exceed recovery capacity")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}
