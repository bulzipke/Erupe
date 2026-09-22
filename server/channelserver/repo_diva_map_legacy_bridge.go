package channelserver

import (
	"time"

	"github.com/jmoiron/sqlx"
)

// The activation is a map-only exception, not removal of the personal legacy
// marker. Require a server-bound departure after activation, including an
// exact quest/guild/time match. An excluded branch may still keep its personal
// points; recordDivaMapContributionTx separately requires route eligibility.
func validateDivaLegacyMapDepartureTx(tx *sqlx.Tx, charID, eventID, guildID uint32,
	questID uint16, runKey string, startedAt, now time.Time) error {
	var allowed bool
	err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM diva_map_activations a
		JOIN diva_interception_periods p ON p.event_id=a.event_id
		JOIN diva_map_departures d ON d.event_id=a.event_id
		WHERE a.event_id=$1 AND d.char_id=$2 AND d.run_key=$3
		AND d.guild_id=$4 AND d.quest_id=$5 AND d.started_at=$6
		AND $6>=a.activated_at AND $6>=p.starts_at AND $6<p.ends_at
		AND $7>=a.activated_at AND $7<p.ends_at)`,
		eventID, charID, runKey, guildID, questID, startedAt.Truncate(time.Microsecond), now.Truncate(time.Microsecond)).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrDivaInterceptionLegacy
	}
	return nil
}
