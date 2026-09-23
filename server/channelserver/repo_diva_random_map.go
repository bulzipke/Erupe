package channelserver

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/jmoiron/sqlx"
)

type divaMapRules struct {
	Version string
	Seed    uint64
}

func validDivaMapRules(rules divaMapRules) bool {
	return (rules.Version == divaCustomMapRules && rules.Seed == 0) ||
		((rules.Version == divaRandomMapRules || rules.Version == divaProgressiveMapRules) && rules.Seed > 0 && rules.Seed <= math.MaxInt64)
}

func loadDivaMapRulesTx(tx *sqlx.Tx, eventID uint32) (divaMapRules, error) {
	var rules divaMapRules
	var seed int64
	err := tx.QueryRow(`SELECT rules_version,generation_seed FROM diva_map_events WHERE event_id=$1`, eventID).
		Scan(&rules.Version, &seed)
	if err != nil {
		return rules, err
	}
	if seed < 0 {
		return rules, fmt.Errorf("negative diva map generation seed")
	}
	rules.Seed = uint64(seed)
	if !validDivaMapRules(rules) {
		return rules, fmt.Errorf("invalid diva map event rules")
	}
	return rules, nil
}

// Callers hold the event advisory lock. Existing rows remain pinned even when
// policy changes; only a never-initialized round consults the permanent cutover.
// actualStart is the real period anchor, never the late activation timestamp.
func ensureDivaMapEventRulesTx(tx *sqlx.Tx, eventID uint32, actualStart time.Time, window divaMapEventWindow) error {
	var storedStart, storedEnd time.Time
	err := tx.QueryRow(`SELECT starts_at,ends_at FROM diva_map_events WHERE event_id=$1`, eventID).
		Scan(&storedStart, &storedEnd)
	if errors.Is(err, sql.ErrNoRows) {
		var cutover time.Time
		if err = tx.QueryRow(`SELECT installed_at FROM diva_random_map_cutover WHERE singleton=TRUE`).Scan(&cutover); err != nil {
			return err
		}
		rules := divaMapRules{Version: divaCustomMapRules}
		if actualStart.After(cutover) {
			seed, seedErr := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
			if seedErr != nil {
				return fmt.Errorf("create diva map seed: %w", seedErr)
			}
			rules = divaMapRules{Version: divaRandomMapRules, Seed: seed.Uint64() + 1}
			var progressiveCutover time.Time
			if err = tx.QueryRow(`SELECT installed_at FROM diva_progressive_map_cutover WHERE singleton=TRUE`).Scan(&progressiveCutover); err != nil {
				return err
			}
			if actualStart.After(progressiveCutover) {
				rules.Version = divaProgressiveMapRules
			}
		}
		// Optional presentation must not prevent normal map creation if its
		// configuration table is unavailable. The helper restores the savepoint.
		specialMode, _ := divaMapSpecialModeForNewEventTx(tx, rules.Version, actualStart)
		if rules.Version == divaProgressiveMapRules {
			specialMode = "progressive"
		}
		if _, err = tx.Exec(`INSERT INTO diva_map_events(event_id,rules_version,generation_seed,starts_at,ends_at,red_treasure_mode)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(event_id) DO NOTHING`,
			eventID, rules.Version, int64(rules.Seed), window.Start, window.End, specialMode); err != nil {
			return err
		}
		err = tx.QueryRow(`SELECT starts_at,ends_at FROM diva_map_events WHERE event_id=$1`, eventID).
			Scan(&storedStart, &storedEnd)
	}
	if err != nil {
		return err
	}
	if !storedStart.Equal(window.Start) || !storedEnd.Equal(window.End) {
		return fmt.Errorf("diva map event window changed")
	}
	_, err = loadDivaMapRulesTx(tx, eventID)
	return err
}

// Also used for old departure/treasure snapshots. The event seed binds all map
// numbers and guilds, while map-number-specific geometry is checked by catalog.
func validateDivaMapEventCatalogTx(tx *sqlx.Tx, eventID uint32, m DivaInterceptionMap) error {
	rules, err := loadDivaMapRulesTx(tx, eventID)
	if err != nil {
		return err
	}
	if err = validateDivaMapCatalogRules(rules, m); err != nil {
		return err
	}
	return validateDivaCustomMap(m)
}

func validateDivaMapCatalogRules(rules divaMapRules, m DivaInterceptionMap) error {
	if !validDivaMapRules(rules) {
		return fmt.Errorf("invalid diva map event rules")
	}
	if rules.Version == divaCustomMapRules {
		// v1 was serialized before metadata existed; do not rewrite it merely
		// to add a version tag. Empty metadata is its canonical representation.
		if m.RulesVersion != "" || m.GenerationSeed != 0 {
			return fmt.Errorf("diva v1 map contains unexpected generation metadata")
		}
	} else if m.RulesVersion != rules.Version || m.GenerationSeed != rules.Seed {
		return fmt.Errorf("diva map generation metadata disagrees with event")
	}
	return nil
}

func initialDivaMapForEventTx(tx *sqlx.Tx, eventID uint32) (DivaInterceptionMap, error) {
	rules, err := loadDivaMapRulesTx(tx, eventID)
	if err != nil {
		return DivaInterceptionMap{}, err
	}
	var m DivaInterceptionMap
	if rules.Version == divaProgressiveMapRules {
		m, err = divaProgressiveMapInitial(rules.Seed)
	} else if rules.Version == divaRandomMapRules {
		m, err = divaRandomMapInitial(rules.Seed)
	} else {
		m, err = divaCustomMapInitial()
	}
	if err == nil {
		err = validateDivaMapCatalogRules(rules, m)
	}
	if err == nil {
		err = validateDivaCustomMap(m)
	}
	return m, err
}
