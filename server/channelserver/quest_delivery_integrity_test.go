package channelserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erupe-ce/network/mhfpacket"
)

func TestDeliveryManagedFrameFailureRejectsSnapshot(t *testing.T) {
	wantErr := errors.New("unreadable captured body")
	for _, test := range []struct {
		name string
		size int
		err  error
	}{{"error", questDataMinLen, wantErr}, {"empty", 0, nil}, {"short", questDataMinLen - 1, nil}, {"oversized", questDataMaxLen + 1, nil}} {
		t.Run(test.name, func(t *testing.T) {
			s := deliveryTestSession(t, `[{"quest_id":53140,"profiles":["normal"],"schedule":"always"}]`)
			rows := []EventQuest{{QuestID: 53142}, {QuestID: 53140}, {QuestID: 53143}}
			got, err := s.encodeQuestDeliverySnapshot(rows, deliveryTestTime(), func(row EventQuest) ([]byte, error) {
				if row.QuestID == 53140 {
					return make([]byte, test.size), test.err
				}
				return deliveryTestEncode(row)
			})
			if err == nil || got != nil || !strings.Contains(err.Error(), "53140") || !strings.Contains(err.Error(), "Brave") {
				t.Fatalf("managed quest failure returned a partial successful roster: snapshot=%v err=%v", got, err)
			}
			if test.err != nil && !errors.Is(err, wantErr) {
				t.Fatal("serializer error cause was lost", err)
			}
		})
	}
}

func TestDeliveryFrameIntegrityKeepsLegacyAndFilters(t *testing.T) {
	for _, test := range []struct {
		name, quests, world string
		now                 time.Time
	}{{"unmanaged", `[]`, "Brave", deliveryTestTime()},
		{"disabled", `[{"quest_id":53140,"disabled":true}]`, "Brave", deliveryTestTime()},
		{"wrong_world", `[{"quest_id":53140,"profiles":["normal"],"schedule":"always"}]`, "Grace", deliveryTestTime()},
		{"before_open", `[{"quest_id":53140,"profiles":["normal"],"schedule":"once"}]`, "Brave", deliveryTestTime().Add(-48 * time.Hour)},
		{"after_close", `[{"quest_id":53140,"profiles":["normal"],"schedule":"once"}]`, "Brave", deliveryTestTime().Add(96 * time.Hour)}} {
		t.Run(test.name, func(t *testing.T) {
			s := deliveryTestSession(t, test.quests)
			s.server.worldName = test.world
			calls := make(map[int]int)
			got, err := s.encodeQuestDeliverySnapshot([]EventQuest{{QuestID: 53140}, {QuestID: 53142}}, test.now, func(row EventQuest) ([]byte, error) {
				calls[row.QuestID]++
				if row.QuestID == 53140 {
					return nil, fmt.Errorf("legacy body is absent")
				}
				return deliveryTestEncode(row)
			})
			if err != nil || got == nil || len(got.entries) != 1 || calls[53142] != 1 {
				t.Fatal("unavailable or unmanaged quest prevented the legacy visible entry", err)
			}
			if test.name != "unmanaged" && calls[53140] != 0 {
				t.Fatal("ineligible quest was encoded before eligibility filtering")
			}
		})
	}
}

func TestDeliveryManagedFrameFailureACKClearsAndRecovers(t *testing.T) {
	s := createTestSession(&MockCryptConn{})
	s.server = deliveryTestSession(t, `[{"quest_id":53140,"profiles":["normal"],"schedule":"always"}]`).server
	s.clientLang = "en"
	s.server.erupeConfig.BinPath = t.TempDir()
	repo := &deliveryCountingRepo{mockEventRepo: mockEventRepo{eventQuests: []EventQuest{{QuestID: 53140, Flags: -1}, {QuestID: 53142, Flags: -1}}}}
	s.server.eventRepo = repo
	for _, id := range []int{53140, 53142} {
		s.server.questCache.Put(id, "en", make([]byte, 400))
	}
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 100})
	if ack := licenseHandlerAck(t, s); ack.ErrorCode != 0 || s.questDeliveryList == nil || len(s.questDeliveryList.entries) != 2 {
		t.Fatal("initial full managed roster failed")
	}
	s.server.questCache.Put(53140, "en", nil)
	for _, offset := range []uint16{0, 1} {
		handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 101, Offset: offset})
		ack := licenseHandlerAck(t, s)
		if ack.AckHandle != 101 || ack.ErrorCode == 0 || len(ack.AckData) != 0 || s.questDeliveryList != nil {
			t.Fatalf("offset%d reused stale data or returned a partial success: %#v", offset, ack)
		}
	}
	s.server.questCache.Put(53140, "en", make([]byte, 400))
	handleMsgMhfEnumerateQuest(s, &mhfpacket.MsgMhfEnumerateQuest{AckHandle: 102})
	if ack := licenseHandlerAck(t, s); ack.ErrorCode != 0 || s.questDeliveryList == nil || len(s.questDeliveryList.entries) != 2 {
		t.Fatal("repaired managed body did not restore the full roster")
	}
	if repo.reads != 4 || repo.writes != 0 {
		t.Fatal("managed failure recovery rewrote scheduling or skipped a fresh read", repo.reads, repo.writes)
	}
}

// This is a detached reception proposal, not a daily-point deduction test.
func TestDeliveryDailyReceptionProposalWire(t *testing.T) {
	dir := os.Getenv("ERUPE_ENTRY_REHEARSAL_DIR")
	if dir == "" {
		t.Skip("explicit daily reception proposal required")
	}
	base, binPath := os.Getenv("ERUPE_QUEST_REHEARSAL_DIR"), os.Getenv("ERUPE_QUEST_REHEARSAL_BIN")
	if base == "" || binPath == "" {
		t.Fatal("explicit unchanged annual fixture and NAS binary input required")
	}
	var proposal struct {
		Status       string `json:"status"`
		QuestID      int    `json:"quest_id"`
		OriginalType uint8  `json:"old_quest_type"`
		ProposedType uint8  `json:"proposed_quest_type"`
	}
	rehearsalJSON(t, filepath.Join(dir, "daily-reception-proposal.json"), &proposal)
	if proposal.Status != "REVIEW_ONLY_NOT_ORIGINAL_HEADER_RECOVERY" || proposal.QuestID != 56014 || proposal.OriginalType != 28 || proposal.ProposedType != 30 {
		t.Fatal("unexpected reviewed daily reception proposal")
	}
	var input rehearsalInput
	rehearsalJSON(t, filepath.Join(base, "rehearsal-scenarios.json"), &input)
	var events rehearsalEventInput
	rehearsalJSON(t, filepath.Join(base, "annual-event-rows.json"), &events)
	if len(events.Support) != 1 || events.Support[0].QuestID != 56014 || events.Support[0].QuestType != proposal.OriginalType || len(input.Profiles) == 0 || len(input.Towers) == 0 {
		t.Fatal("daily proposal does not match the unchanged original support fixture")
	}
	input.Config.BinPath = binPath
	s := rehearsalSession(input.Config, input.Profiles[0], input.Towers[0], nil, NewQuestCache(3600), events.Support)
	original := events.Support[0]
	proposed := original
	proposed.QuestType = proposal.ProposedType
	now := time.Unix(1, 0)
	before, err := makeEventQuestWithAccess(s, original, s.questAccessSnapshot.Load(), now)
	if err != nil || len(before) < questDataMinLen || rehearsalWireID(t, before) != 56014 || before[10] != 28 {
		t.Fatal("failed to serialize original optional support", err)
	}
	after, err := makeEventQuestWithAccess(s, proposed, s.questAccessSnapshot.Load(), now)
	if err != nil || len(before) != len(after) || after[10] != 30 {
		t.Fatal("failed to serialize proposed daily type", err)
	}
	before[10], after[10] = 0, 0
	if !bytes.Equal(before, after) {
		t.Fatal("daily reception proposal changed original body or other header bytes")
	}
	report := map[string]interface{}{
		"schema": "quest-daily-reception-proposal-wire-v1", "verified_at_utc": time.Now().UTC(), "passed": true,
		"quest_id": 56014, "serialized_frames": 2, "only_changed_wire_byte": 10,
		"old_quest_type": 28, "proposed_quest_type": 30, "original_body_and_other_header_bytes_preserved": true,
		"actual_daily_point_deduction_or_persistence_verified": false, "original_header_recovered": false,
		"original_fixture_changed": false, "operating_registration_or_policy_changed": false,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daily-reception-wire-validation.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
