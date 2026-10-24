package channelserver

import (
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
	raw, _ := json.Marshal(map[string]any{"version": "configured-world-runtime-test", "profiles": []any{map[string]any{"id": "common", "worlds": []string{"Common"}}}, "schedules": []any{map[string]any{"id": "always"}}, "quests": quests, "collab_presentation_mode": "configured_world"})
	catalog, err := questdelivery.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := PrepareCommonCollabRuntime(catalog, []questdelivery.PublicationLocation{{World: "Common", Channel: 1}}, nil, configuredWorldQuestSource{})
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
			s := &Session{server: &Server{worldName: "Common", channelNumber: 1, worldType: test.world, collabEvent: test.event, erupeConfig: config, questDelivery: catalog, commonCollab: cohort}, collabEvent: test.selected}
			wantOriginal := s.appendCollabTuneValues(nil)
			if !reflect.DeepEqual(wantOriginal, test.want) {
				t.Fatal("bad original-behavior fixture")
			}
			view, err := s.commonCollabRuntimeViewAt(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if len(view.activeIDs) != 13 || !reflect.DeepEqual(view.tunes, wantOriginal) {
				t.Fatal("NPC selection changed or common quests filtered")
			}
			for _, definition := range questdelivery.CommonCollabDefinitions() {
				if !view.activeIDs[definition.QuestID] {
					t.Fatal("common quest missing")
				}
			}
		})
	}
}
