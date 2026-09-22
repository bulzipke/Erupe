package channelserver

import (
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Current and previous UI pages are not the complete reward history. Each
// created map has a durable snapshot, so even much older branch rewards are
// checked against their original geometry, never against today's coordinates.
func loadDivaTreasureMapTx(tx *sqlx.Tx, eventID, guildID uint32, number uint16) (DivaInterceptionMap, error) {
	var m DivaInterceptionMap
	var data []byte
	if number == 0 {
		return m, fmt.Errorf("diva treasure: zero map number")
	}
	if err := tx.QueryRow(`SELECT map_data FROM diva_map_snapshots
		WHERE event_id=$1 AND guild_id=$2 AND map_number=$3
		ORDER BY settled_at DESC LIMIT 1`, eventID, guildID, number).Scan(&data); err != nil {
		return m, fmt.Errorf("diva treasure: historical map %d: %w", number, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if err := validateDivaMapEventCatalogTx(tx, eventID, m); err != nil {
		return m, err
	}
	if m.States[0].MapNumber != number {
		return m, fmt.Errorf("diva treasure: snapshot map number mismatch")
	}
	return m, nil
}

// The caller has checked contribution/award ownership and validated the
// historical catalog. This check deliberately does not require the snapshot
// itself to show a completed branch: its final award can coincide with a main
// goal settlement whose snapshot already contains the following map.
func divaMapBranchTreasureRewards(m DivaInterceptionMap, coordinate uint16) []DivaRewardCatalogEntry {
	if len(m.States) == 0 || m.States[0].MapNumber == 0 {
		return nil
	}
	for _, node := range m.States[0].Nodes {
		if node.Coordinate == coordinate && node.BranchQuests[0] != 0 {
			return divaApprovedBranchTreasureRewards(m.States[0].MapNumber, coordinate)
		}
	}
	return nil
}
