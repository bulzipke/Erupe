package questdelivery

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func configuredWorldManifest() map[string]any {
	quests := []any{}
	for _, definition := range CommonCollabDefinitions() {
		quests = append(quests, map[string]any{"quest_id": definition.QuestID, "profiles": []string{"common"}, "schedule": "always"})
	}
	return map[string]any{"version": "configured-world-test", "profiles": []any{map[string]any{"id": "common", "worlds": []string{"One", "Two"}}}, "schedules": []any{map[string]any{"id": "always"}}, "quests": quests, "collab_presentation_mode": "configured_world"}
}

func TestConfiguredWorldCollabKeepsAllQuestsWithoutInventingNPC(t *testing.T) {
	raw, _ := json.Marshal(configuredWorldManifest())
	c, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.CommonCollabDeclared() || !c.UsesConfiguredWorldCollabPresentation() {
		t.Fatal("mode not retained")
	}
	locations := []PublicationLocation{{"One", 1}, {"One", 2}, {"Two", 1}}
	cohort, err := BuildCommonCollabCohort(c, locations, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)} {
		view := cohort.At(at)
		if !reflect.DeepEqual(view.QuestIDs(), expectedCollabIDs) || len(view.TuneValues()) != 0 || view.Presentation() != "" {
			t.Fatal("common roster changed or NPC layout was invented")
		}
		for _, location := range locations {
			for _, id := range expectedCollabIDs {
				if !c.AllowsCommonCollab(id, location.World, location.Channel, at) {
					t.Fatal("common quest hidden")
				}
			}
		}
	}
}

func TestConfiguredWorldCollabRejectsAmbiguousModes(t *testing.T) {
	for _, mode := range []any{nil, "", "unknown", true, 1, map[string]any{}} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			m := configuredWorldManifest()
			m["collab_presentation_mode"] = mode
			raw, _ := json.Marshal(m)
			if _, err := Parse(raw); err == nil {
				t.Fatal("invalid mode accepted")
			}
		})
	}
	for _, plan := range []any{nil, []any{}, []any{map[string]any{"event": "nier", "schedule": "always"}}} {
		m := configuredWorldManifest()
		m["collab_presentations"] = plan
		raw, _ := json.Marshal(m)
		if _, err := Parse(raw); err == nil {
			t.Fatal("two NPC policies accepted")
		}
	}
	m := configuredWorldManifest()
	raw, _ := json.Marshal(m)
	raw = append(raw[:len(raw)-1], []byte(`,"collab_presentation_mode":"scheduled"}`)...)
	if _, err := Parse(raw); err == nil {
		t.Fatal("repeated mode accepted")
	}
	m = configuredWorldManifest()
	m["quests"] = m["quests"].([]any)[1:]
	raw, _ = json.Marshal(m)
	if _, err := Parse(raw); err == nil {
		t.Fatal("incomplete common inventory accepted")
	}
}

func TestScheduledCollabModeRemainsExplicit(t *testing.T) {
	m := configuredWorldManifest()
	m["collab_presentation_mode"] = "scheduled"
	m["collab_presentations"] = []any{map[string]any{"event": "nier", "schedule": "always"}}
	raw, _ := json.Marshal(m)
	c, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.CommonCollabDeclared() || c.UsesConfiguredWorldCollabPresentation() {
		t.Fatal("scheduled mode changed")
	}
	cohort, err := BuildCommonCollabCohort(c, []PublicationLocation{{"One", 1}, {"Two", 1}}, []string{"nier"})
	if err != nil {
		t.Fatal(err)
	}
	if view := cohort.At(time.Now()); !reflect.DeepEqual(view.TuneValues(), []CollabTuneValue{{1153, 1}}) {
		t.Fatal("scheduled tune changed")
	}
}
