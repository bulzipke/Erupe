package channelserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
)

type configuredWorldQuestSource struct{}

func (configuredWorldQuestSource) GetEventQuests() ([]EventQuest, error) { return nil, nil }

func TestConfiguredWorldCollabPublicationPreservesNPCSelection(t *testing.T) {
	quests := []any{}
	for _, definition := range questdelivery.CommonCollabDefinitions() {
		quests = append(quests, map[string]any{"quest_id": definition.QuestID, "profiles": []string{"common"}, "schedule": "always"})
	}
	raw, _ := json.Marshal(map[string]any{"version": "configured-world-runtime-test", "profiles": []any{map[string]any{"id": "common", "worlds": []string{"Common", "Ordinary"}}}, "schedules": []any{map[string]any{"id": "always"}}, "quests": quests, "collab_presentation_mode": "configured_world"})
	catalog, err := questdelivery.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := PrepareCommonCollabRuntime(catalog, []questdelivery.PublicationLocation{{World: "Common", Channel: 1}, {World: "Ordinary", Channel: 1}}, nil, configuredWorldQuestSource{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name            string
		world           uint8
		event, selected string
		want            []tuneValue
	}{
		{"selected", 1, collabNier, "", []tuneValue{{1153, 1}}},
		{"random-session", 1, collabRandom, collabHiganjima, []tuneValue{{1144, 1}}},
		{"none", 1, collabNone, "", nil},
		{"original-world-filter", 5, collabNier, "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := &cfg.Config{}
			config.QuestDelivery.Enabled = true
			config.GameplayOptions.EnableKaijiEvent = true
			s := &Session{server: &Server{worldName: "Common", channelNumber: 1, worldType: test.world, collabEvent: test.event, erupeConfig: config, questDelivery: catalog, commonCollab: cohort, collabOnlyEvents: true}, collabEvent: test.selected}
			wantOriginal := s.appendCollabTuneValues(nil)
			if !reflect.DeepEqual(wantOriginal, test.want) {
				t.Fatal("bad original-behavior fixture")
			}
			at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			view, err := s.commonCollabRuntimeViewAt(at)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.activeIDs) != 13 || !reflect.DeepEqual(view.tunes, wantOriginal) {
				t.Fatal("NPC selection changed or common quests filtered")
			}
			rows := []EventQuest{{ID: 1, QuestID: 50118, QuestType: 18}, {ID: 2, QuestID: 50143, QuestType: 43}}
			snapshot, err := s.encodeQuestDeliverySnapshotWithReturn(rows, at, false, deliveryTestEncode)
			if err != nil || len(snapshot.entries) != 14 {
				t.Fatalf("expected 13 common collab entries and the dedicated quest, got %v", err)
			}
			for _, entry := range snapshot.entries {
				if binary.LittleEndian.Uint32(entry) == 50118 {
					t.Fatal("collaboration-only filter kept an ordinary event quest")
				}
			}
			for _, definition := range questdelivery.CommonCollabDefinitions() {
				if !view.activeIDs[definition.QuestID] {
					t.Fatal("common quest missing")
				}
			}
		})
	}

	t.Run("common manifest in an ordinary world", func(t *testing.T) {
		config := &cfg.Config{QuestDeliveryCatalog: catalog}
		config.QuestDelivery.Enabled = true
		config.Entrance.Entries = []cfg.EntranceServerInfo{
			{Name: "Common", CollabOnlyEventQuests: true, Channels: []cfg.EntranceChannelInfo{{}}},
			{Name: "Ordinary", Channels: []cfg.EntranceChannelInfo{{}}},
		}
		server := &Server{erupeConfig: config}
		server.configureChannelContent(&Config{
			ErupeConfig: config, WorldName: "Ordinary", WorldType: 1, ChannelNumber: 1,
			CollabEvent: collabKaiji, CommonCollab: cohort,
		})
		s := &Session{server: server}
		at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		if !catalog.AllowsCommonCollab(40215, "Ordinary", 1, at) {
			t.Fatal("fixture must publish collab quests to both worlds")
		}
		rows := []EventQuest{{ID: 1, QuestID: 50118, QuestType: 18}}
		snapshot, err := s.encodeQuestDeliverySnapshotWithReturn(rows, at, false, deliveryTestEncode)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.entries) != 1 || binary.LittleEndian.Uint32(snapshot.entries[0]) != 50118 {
			t.Fatal("ordinary world must only receive the ordinary event quest")
		}
		if allowed, err := s.questFileAllowedInWorld(context.Background(), "40215d0"); err != nil || allowed {
			t.Fatalf("common collab file allowed=%t, err=%v", allowed, err)
		}
	})
}
