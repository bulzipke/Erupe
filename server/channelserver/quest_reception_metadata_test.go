package channelserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The review proposes a server header cap; it does not recover original event
// metadata or implement the original solo/sex/partner eligibility checks.
func TestQuestReceptionProposedPartyCapWire(t *testing.T) {
	dir := os.Getenv("ERUPE_RECEPTION_REHEARSAL_DIR")
	if dir == "" {
		t.Skip("explicit conservative reception-header proposals required")
	}
	base, binPath := os.Getenv("ERUPE_QUEST_REHEARSAL_DIR"), os.Getenv("ERUPE_QUEST_REHEARSAL_BIN")
	if base == "" || binPath == "" {
		t.Fatal("explicit unchanged base fixture and NAS binary input required")
	}
	var proposal struct {
		Status  string `json:"status"`
		Changes []struct {
			QuestID  int   `json:"quest_id"`
			OldCap   uint8 `json:"old_max_players"`
			Proposed uint8 `json:"proposed_max_players"`
		} `json:"changes"`
	}
	rehearsalJSON(t, filepath.Join(dir, "reception-header-proposals.json"), &proposal)
	want := map[int]uint8{62007: 1, 62020: 1, 62793: 1, 61194: 2, 61245: 2, 61575: 2, 53238: 2, 53240: 2, 55944: 2}
	if proposal.Status != "REVIEW_ONLY_NOT_ORIGINAL_HEADER_RECOVERY" || len(proposal.Changes) != len(want) {
		t.Fatal("unexpected reviewed party-cap proposal scope")
	}
	var input rehearsalInput
	rehearsalJSON(t, filepath.Join(base, "rehearsal-scenarios.json"), &input)
	var events rehearsalEventInput
	rehearsalJSON(t, filepath.Join(base, "annual-event-rows.json"), &events)
	rows := append(append(append([]EventQuest(nil), events.Rows...), events.Support...), events.Extra15...)
	byID := make(map[int]EventQuest)
	for _, row := range rows {
		if _, duplicate := byID[row.QuestID]; duplicate {
			t.Fatal("duplicate base event row")
		}
		byID[row.QuestID] = row
	}
	input.Config.BinPath = binPath
	if len(input.Profiles) == 0 || len(input.Towers) == 0 {
		t.Fatal("missing base profile/tower fixture")
	}
	s := rehearsalSession(input.Config, input.Profiles[0], input.Towers[0], nil, NewQuestCache(3600), rows)
	now := time.Unix(1, 0)
	seen := make(map[int]bool)
	for _, change := range proposal.Changes {
		original, exists := byID[change.QuestID]
		cap, known := want[change.QuestID]
		if !known || !exists || seen[change.QuestID] || change.OldCap != 4 || original.MaxPlayers != change.OldCap || cap != change.Proposed {
			t.Fatal("proposal does not match the reviewed original fixture", change.QuestID)
		}
		seen[change.QuestID] = true
		before, err := makeEventQuestWithAccess(s, original, s.questAccessSnapshot.Load(), now)
		if err != nil || len(before) < questDataMinLen || before[9] != 4 || rehearsalWireID(t, before) != change.QuestID {
			t.Fatal("failed to serialize original reviewed quest", change.QuestID, err)
		}
		revised := original
		revised.MaxPlayers = change.Proposed
		after, err := makeEventQuestWithAccess(s, revised, s.questAccessSnapshot.Load(), now)
		if err != nil || len(before) != len(after) || after[9] != change.Proposed {
			t.Fatal("failed to serialize proposed party cap", change.QuestID, err)
		}
		beforeCopy, afterCopy := append([]byte(nil), before...), append([]byte(nil), after...)
		beforeCopy[9], afterCopy[9] = 0, 0
		if !bytes.Equal(beforeCopy, afterCopy) {
			t.Fatal("party-cap proposal changed quest metadata, body or original restrictions", change.QuestID)
		}
	}
	report := map[string]interface{}{
		"schema": "quest-reception-party-cap-wire-validation-v1", "verified_at_utc": time.Now().UTC(), "passed": true,
		"reviewed_quests": len(seen), "serialized_frames": len(seen) * 2,
		"only_changed_wire_byte": 9, "original_body_and_other_header_bytes_preserved": true,
		"original_event_headers_recovered": false, "original_gender_or_partner_checks_replaced": false,
		"base_fixture_changed": false, "production_changes": false, "native_client_or_gameplay_test": false,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reception-party-cap-wire-validation.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
