package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	cfg "erupe-ce/config"
	"github.com/jmoiron/sqlx"
)

// ConfigureDivaMapSpecialTreasures is called before worker goroutines start.
// Identical settings preserve their effective time across channels/restarts.
// A changed option only affects rounds beginning AFTER that change, and never
// rewrites an existing event policy, stored map, departure, award or receipt.
func (r *DivaRepository) ConfigureDivaMapSpecialTreasures(mode string, now time.Time) error {
	r.mapSpecialPolicyReady.Store(false)
	if mode == "" {
		mode = cfg.DivaMapRedTreasureOff
	}
	if err := cfg.ValidateDivaMapRedTreasureMode(mode); err != nil {
		return err
	}
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now, err = divaMapClock(tx, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`WITH changed AS (INSERT INTO diva_map_special_policy(singleton,mode,effective_at)
		VALUES(TRUE,$1,$2) ON CONFLICT(singleton) DO UPDATE SET mode=EXCLUDED.mode,effective_at=EXCLUDED.effective_at
		WHERE diva_map_special_policy.mode IS DISTINCT FROM EXCLUDED.mode RETURNING mode,effective_at)
		INSERT INTO diva_map_special_policy_history(mode,effective_at) SELECT mode,effective_at FROM changed`, mode, now)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.mapSpecialPolicyReady.Store(true)
	return nil
}

// SQL errors must not leave the map transaction aborted. Only this optional
// read is rolled back; all prior authoritative map work remains intact.
func divaMapSpecialReadTx(tx *sqlx.Tx, read func() error) error {
	if _, err := tx.Exec(`SAVEPOINT diva_special_presentation`); err != nil {
		return err
	}
	err := read()
	if err != nil {
		if _, rollbackErr := tx.Exec(`ROLLBACK TO SAVEPOINT diva_special_presentation`); rollbackErr != nil {
			return fmt.Errorf("special treasure read: %v; rollback: %w", err, rollbackErr)
		}
	}
	if _, releaseErr := tx.Exec(`RELEASE SAVEPOINT diva_special_presentation`); releaseErr != nil {
		return releaseErr
	}
	return err
}

func divaMapSpecialModeForNewEventTx(tx *sqlx.Tx, version string, actualStart time.Time) (string, error) {
	if version != divaRandomMapRules {
		return cfg.DivaMapRedTreasureOff, nil
	}
	var mode string
	err := divaMapSpecialReadTx(tx, func() error {
		return tx.QueryRow(`SELECT mode FROM diva_map_special_policy_history WHERE effective_at<$1
			ORDER BY effective_at DESC,id DESC LIMIT 1`, actualStart).Scan(&mode)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return cfg.DivaMapRedTreasureOff, nil
	}
	if err != nil {
		return cfg.DivaMapRedTreasureOff, err
	}
	if err := cfg.ValidateDivaMapRedTreasureMode(mode); err != nil {
		return cfg.DivaMapRedTreasureOff, err
	}
	return mode, nil
}

func loadDivaMapSpecialSelectionsTx(tx *sqlx.Tx, eventID uint32, m DivaInterceptionMap) ([]DivaMapSpecialTreasureSelection, error) {
	var mode string
	err := divaMapSpecialReadTx(tx, func() error {
		return tx.QueryRow(`SELECT red_treasure_mode FROM diva_map_events WHERE event_id=$1`, eventID).Scan(&mode)
	})
	if err != nil {
		return nil, err
	}
	return selectDivaMapSpecialTreasures(m, mode)
}
