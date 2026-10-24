package questdelivery

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var expectedCollabIDs = []int{40211, 40212, 40213, 40214, 40215, 40217, 40221, 40223, 40224, 40225, 40226, 40227, 40239}

func researchFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/common590-plus13-research.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func encodeFixture(t *testing.T, value map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func candidateParse(t *testing.T, value map[string]any) *Catalog {
	t.Helper()
	c, err := Parse(encodeFixture(t, value))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func researchLocations(c *Catalog) []PublicationLocation {
	locations := []PublicationLocation{}
	for _, world := range c.Profiles()[0].Worlds {
		// Channel2 is a pure synthetic coverage probe, not an operating claim.
		locations = append(locations, PublicationLocation{world, 1}, PublicationLocation{world, 2})
	}
	return locations
}

func cohortForTest(t *testing.T, c *Catalog) *CommonCollabCohort {
	t.Helper()
	cohort, err := BuildCommonCollabCohort(c, researchLocations(c), []string{"higanjima", "nier"})
	if err != nil {
		t.Fatal(err)
	}
	return cohort
}

func researchTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestCollabDeclarationAbsentEmptyNullAndStrictness(t *testing.T) {
	t.Run("absent_does_not_convert_existing_managed13", func(t *testing.T) {
		fixture := researchFixture(t)
		delete(fixture, "collab_presentations")
		c := candidateParse(t, fixture)
		at := researchTime(t, "2099-01-02T00:00:00+09:00")
		world := c.Profiles()[0].Worlds[0]
		managed, allowed := c.Allows(40211, world, 1, at)
		if !managed || !allowed || c.CommonCollabDeclared() || c.AllowsCommonCollab(40211, world, 1, at) {
			t.Fatal("absence changed existing Catalog semantics or activated the new route")
		}
		if cohort, err := BuildCommonCollabCohort(c, researchLocations(c), nil); err == nil || cohort != nil {
			t.Fatal("absent declaration created a converted cohort")
		}
	})
	t.Run("explicit_empty_array", func(t *testing.T) {
		fixture := researchFixture(t)
		fixture["collab_presentations"] = []any{}
		c := candidateParse(t, fixture)
		cohort, err := BuildCommonCollabCohort(c, researchLocations(c), nil)
		if err != nil || !c.CommonCollabDeclared() {
			t.Fatal("empty declaration rejected", err)
		}
		view := cohort.At(researchTime(t, "2099-01-02T00:00:00+09:00"))
		if !reflect.DeepEqual(view.QuestIDs(), expectedCollabIDs) || len(view.TuneValues()) != 0 {
			t.Fatal("empty NPC plan changed common publication", view)
		}
	})
	for _, value := range []any{nil, "nier", true, float64(1), map[string]any{}, []any{nil},
		[]any{map[string]any{"event": "nier", "schedule": "research_nier_presentation", "unknown": true}},
		[]any{map[string]any{"event": "none", "schedule": "research_nier_presentation"}},
		[]any{map[string]any{"event": "nier", "schedule": "missing"}}} {
		t.Run(fmt.Sprintf("invalid_%v", value), func(t *testing.T) {
			fixture := researchFixture(t)
			fixture["collab_presentations"] = value
			if _, err := Parse(encodeFixture(t, fixture)); err == nil {
				t.Fatal("invalid declaration accepted", value)
			}
		})
	}
	t.Run("duplicate_declaration", func(t *testing.T) {
		raw := encodeFixture(t, researchFixture(t))
		raw = append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"collab_presentations":[]}`)...)
		if _, err := Parse(raw); err == nil {
			t.Fatal("repeated declaration was silently overwritten")
		}
	})
}

func TestCollabExactInventoryAndNoUnmanagedDefault(t *testing.T) {
	for _, missing := range expectedCollabIDs {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			fixture := researchFixture(t)
			rows := fixture["quests"].([]any)
			for i, raw := range rows {
				if int(raw.(map[string]any)["quest_id"].(float64)) == missing {
					fixture["quests"] = append(rows[:i], rows[i+1:]...)
					break
				}
			}
			if _, err := Parse(encodeFixture(t, fixture)); err == nil || !strings.Contains(err.Error(), fmt.Sprint(missing)) {
				t.Fatal("incomplete13 conversion did not fail explicitly", err)
			}
		})
	}
	var nilCatalog *Catalog
	at := researchTime(t, "2099-01-02T00:00:00+09:00")
	if nilCatalog.AllowsCommonCollab(40211, "W", 1, at) {
		t.Fatal("nil catalog granted publication")
	}
	if cohort, err := BuildCommonCollabCohort(nil, nil, nil); err == nil || cohort != nil {
		t.Fatal("nil catalog built a cohort")
	}
	fixture := researchFixture(t)
	for _, raw := range fixture["quests"].([]any) {
		row := raw.(map[string]any)
		if int(row["quest_id"].(float64)) == 40215 {
			for key := range row {
				delete(row, key)
			}
			row["quest_id"], row["disabled"] = 40215, true
		}
	}
	c := candidateParse(t, fixture)
	view := cohortForTest(t, c).At(at)
	want := append([]int(nil), expectedCollabIDs[:4]...)
	want = append(want, expectedCollabIDs[5:]...)
	world := c.Profiles()[0].Worlds[0]
	if c.AllowsCommonCollab(40215, world, 1, at) || c.AllowsCommonCollab(49999, world, 1, at) || !reflect.DeepEqual(view.QuestIDs(), want) {
		t.Fatal("disabled/unknown inventory inherited default allow", view)
	}
	var nilCohort *CommonCollabCohort
	if len(nilCohort.At(at).QuestIDs()) != 0 || len(nilCohort.At(at).TuneValues()) != 0 {
		t.Fatal("nil cohort synthesized content")
	}
}

func TestCollabCoverageRequiresEveryConfiguredLocation(t *testing.T) {
	c := candidateParse(t, researchFixture(t))
	locations := researchLocations(c)
	cohort := cohortForTest(t, c)
	at := researchTime(t, "2099-01-02T00:00:00+09:00")
	for _, location := range locations {
		for _, id := range expectedCollabIDs {
			if !c.AllowsCommonCollab(id, location.World, location.Channel, at) {
				t.Fatal("common profile differs by location", id, location)
			}
		}
	}
	if !reflect.DeepEqual(cohort.Locations(), locations) {
		t.Fatal("location projection changed")
	}
	for name, invalid := range map[string][]PublicationLocation{
		"empty": nil, "duplicate": {locations[0], locations[0]},
		"empty_world": {{"", 1}}, "whitespace_world": {{" W", 1}},
		"zero_channel": {{locations[0].World, 0}}, "large_channel": {{locations[0].World, 65536}},
		"missing_world":           append(append([]PublicationLocation(nil), locations...), PublicationLocation{"Uncovered", 1}),
		"not_all_existing_worlds": locations[:1],
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := BuildCommonCollabCohort(c, invalid, []string{"higanjima", "nier"}); err == nil || got != nil {
				t.Fatal("invalid configured coverage accepted", name)
			}
		})
	}
	t.Run("specific_channel_gap", func(t *testing.T) {
		fixture := researchFixture(t)
		fixture["profiles"].([]any)[0].(map[string]any)["channels"] = []int{1}
		partial := candidateParse(t, fixture)
		if got, err := BuildCommonCollabCohort(partial, locations, []string{"higanjima", "nier"}); err == nil || got != nil {
			t.Fatal("channel2 gap accepted")
		}
	})
	t.Run("unknown_explicit_channel", func(t *testing.T) {
		fixture := researchFixture(t)
		fixture["profiles"].([]any)[0].(map[string]any)["channels"] = []int{1, 2, 3}
		partial := candidateParse(t, fixture)
		if got, err := BuildCommonCollabCohort(partial, locations, []string{"higanjima", "nier"}); err == nil || got != nil {
			t.Fatal("unconfigured explicit channel accepted")
		}
	})
}

func TestCollabPresentationBoundaryAndWholeViewImmutability(t *testing.T) {
	c := candidateParse(t, researchFixture(t))
	locations := researchLocations(c)
	capabilities := []string{"higanjima", "nier"}
	cohort, err := BuildCommonCollabCohort(c, locations, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	boundary := researchTime(t, "2099-01-04T00:00:00+09:00")
	before := cohort.At(boundary.Add(-time.Nanosecond))
	after := cohort.At(boundary)
	if !reflect.DeepEqual(before.QuestIDs(), expectedCollabIDs) || !reflect.DeepEqual(after.QuestIDs(), expectedCollabIDs) ||
		before.Presentation() != "higanjima" || after.Presentation() != "nier" ||
		!reflect.DeepEqual(before.TuneValues(), []CollabTuneValue{{1144, 1}}) || !reflect.DeepEqual(after.TuneValues(), []CollabTuneValue{{1153, 1}}) {
		t.Fatal("atomic semantic boundary is wrong", before, after)
	}
	ids, tunes, copiedLocations := before.QuestIDs(), before.TuneValues(), cohort.Locations()
	ids[0], tunes[0].ID, copiedLocations[0].World = 1, 1, "Changed"
	locations[0].World, capabilities[0] = "Changed", "kaiji"
	defs := CommonCollabDefinitions()
	defs[0].QuestID = 1
	if !reflect.DeepEqual(before.QuestIDs(), expectedCollabIDs) || before.TuneValues()[0].ID != 1144 ||
		cohort.Locations()[0].World == "Changed" || !reflect.DeepEqual(cohort.At(boundary.Add(-time.Nanosecond)), before) || CommonCollabDefinitions()[0].QuestID != 40211 {
		t.Fatal("immutable slices or input configuration escaped")
	}
	if before.CatalogVersion() != c.Version() || !before.EvaluatedAt().Equal(boundary.Add(-time.Nanosecond)) {
		t.Fatal("view lost version/clock provenance")
	}
	end := researchTime(t, "2099-01-07T00:00:00+09:00")
	closed := cohort.At(end)
	if len(closed.QuestIDs()) != 0 || len(closed.TuneValues()) != 0 {
		t.Fatal("exclusive end stayed active", closed)
	}
	start := researchTime(t, "2099-01-01T00:00:00+09:00")
	if len(cohort.At(start.Add(-time.Nanosecond)).QuestIDs()) != 0 || len(cohort.At(start).QuestIDs()) != 13 {
		t.Fatal("inclusive start is wrong")
	}
}

func TestCollabCapabilitiesDoNotGrantOrFilterPublication(t *testing.T) {
	c := candidateParse(t, researchFixture(t))
	locations := researchLocations(c)
	at := researchTime(t, "2099-01-02T00:00:00+09:00")
	before := c.AllowedQuestIDs(locations[0].World, locations[0].Channel, at)
	for _, caps := range [][]string{nil, {"higanjima"}, {"nier"}, {"unknown"}, {"higanjima", "nier", "nier"}} {
		if got, err := BuildCommonCollabCohort(c, locations, caps); err == nil || got != nil {
			t.Fatal("missing/invalid static capability accepted", caps)
		}
		if !reflect.DeepEqual(c.AllowedQuestIDs(locations[0].World, locations[0].Channel, at), before) {
			t.Fatal("planning validation mutated publication")
		}
	}
	cohort, err := BuildCommonCollabCohort(c, locations, []string{"kaiji", "higanjima", "nier", "evangelion", "pso2"})
	if err != nil || !reflect.DeepEqual(cohort.At(at).TuneValues(), []CollabTuneValue{{1144, 1}}) {
		t.Fatal("all available contracts implied all NPC flags ON", err)
	}
}

func TestCollabPresentationOverlapAndBoundedMode(t *testing.T) {
	for _, test := range []struct {
		name       string
		start, end string
		wantError  bool
	}{
		{"adjacent", "2099-01-04T00:00:00+09:00", "2099-01-07T00:00:00+09:00", false},
		{"overlap1ns", "2099-01-03T23:59:59.999999999+09:00", "2099-01-07T00:00:00+09:00", true},
		{"nested", "2099-01-02T00:00:00+09:00", "2099-01-03T00:00:00+09:00", true},
		{"far_future_overlap", "2099-01-03T00:00:00+09:00", "9999-12-31T00:00:00+09:00", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := researchFixture(t)
			for _, raw := range fixture["schedules"].([]any) {
				row := raw.(map[string]any)
				if row["id"] == "research_nier_presentation" {
					row["starts_at"], row["ends_at"] = test.start, test.end
				}
			}
			_, err := Parse(encodeFixture(t, fixture))
			if (err != nil) != test.wantError {
				t.Fatal("overlap result", err)
			}
		})
	}
	t.Run("periodic_presentation_rejected_not_sampled", func(t *testing.T) {
		fixture := researchFixture(t)
		fixture["collab_presentations"] = []any{map[string]any{"event": "nier", "schedule": "research_collab_open"}}
		for _, raw := range fixture["schedules"].([]any) {
			row := raw.(map[string]any)
			if row["id"] == "research_collab_open" {
				row["active_hours"], row["repeat_hours"] = 72, 96
			}
		}
		if _, err := Parse(encodeFixture(t, fixture)); err == nil || !strings.Contains(err.Error(), "periodic collab") {
			t.Fatal("unsupported periodic NPC plan accepted", err)
		}
	})
	t.Run("single_always_and_no_extra_NPC", func(t *testing.T) {
		fixture := researchFixture(t)
		fixture["schedules"] = append(fixture["schedules"].([]any), map[string]any{"id": "research_npc_always"})
		fixture["collab_presentations"] = []any{map[string]any{"event": "pso2", "schedule": "research_npc_always"}}
		c := candidateParse(t, fixture)
		cohort, err := BuildCommonCollabCohort(c, researchLocations(c), []string{"pso2"})
		if err != nil {
			t.Fatal(err)
		}
		at := researchTime(t, "2098-01-01T00:00:00+09:00")
		if len(cohort.At(at).QuestIDs()) != 0 || !reflect.DeepEqual(cohort.At(at).TuneValues(), []CollabTuneValue{{1130, 1}}) {
			t.Fatal("NPC presence was confused with quest publication")
		}
		fixture["collab_presentations"] = append(fixture["collab_presentations"].([]any), map[string]any{"event": "nier", "schedule": "research_nier_presentation"})
		if _, err := Parse(encodeFixture(t, fixture)); err == nil {
			t.Fatal("always + another presentation accepted")
		}
	})
}

func TestCollabOrdinary590And19SchedulesUnchanged(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/common590-original.json")
	if err != nil {
		t.Fatal(err)
	}
	base, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if base.CommonCollabDeclared() || len(base.QuestIDs()) != 594 || len(base.schedules) != 19 {
		t.Fatal("absent baseline was converted")
	}
	fixture := researchFixture(t)
	candidate := candidateParse(t, fixture)
	if len(candidate.QuestIDs()) != 607 || len(candidate.schedules) != 22 {
		t.Fatal("composed fixture inventory differs")
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original["profiles"], fixture["profiles"]) ||
		!reflect.DeepEqual(original["quests"], fixture["quests"].([]any)[:594]) ||
		!reflect.DeepEqual(original["schedules"], fixture["schedules"].([]any)[:19]) {
		t.Fatal("base19 schedules or594 rows changed during fixture composition")
	}
	for _, date := range []string{"2026-10-24T00:00:00+09:00", "2027-08-01T00:00:00+09:00", "2099-01-01T00:00:00+09:00"} {
		from := researchTime(t, date)
		before, err := base.PublicDocument(from, from.Add(30*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		after, err := candidate.PublicDocument(from, from.Add(30*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		filtered := make([]Occurrence, 0)
		for _, occurrence := range after.Occurrences {
			if !commonCollabID(occurrence.QuestID) {
				filtered = append(filtered, occurrence)
			}
		}
		if !reflect.DeepEqual(before.Occurrences, filtered) {
			t.Fatal("ordinary publication/title/requirements/profiles/periodic occurrences changed", date)
		}
	}
}

func TestCollabCalendarAndSemanticViewUseSameCatalog(t *testing.T) {
	c := candidateParse(t, researchFixture(t))
	cohort := cohortForTest(t, c)
	from := researchTime(t, "2099-01-01T00:00:00+09:00")
	doc, err := cohort.PublicDocument(from, from.Add(30*24*time.Hour))
	if err != nil || doc.Version != c.Version() || doc.Coverage != "managed_quests_only" || !doc.OriginalEntryConditionsApply {
		t.Fatal("calendar lost source/conditions contract", err)
	}
	publication := make(map[int]Occurrence)
	for _, occurrence := range doc.Occurrences {
		if commonCollabID(occurrence.QuestID) {
			publication[occurrence.QuestID] = occurrence
		}
	}
	if len(publication) != 13 {
		t.Fatal("calendar missed managed13")
	}
	for _, at := range []time.Time{from.Add(-time.Nanosecond), from, from.Add(72 * time.Hour), from.Add(144 * time.Hour)} {
		view := cohort.At(at)
		want := make([]int, 0)
		for _, id := range expectedCollabIDs {
			o := publication[id]
			if !at.Before(o.StartsAt) && at.Before(o.EndsAt) {
				want = append(want, id)
			}
		}
		if !reflect.DeepEqual(view.QuestIDs(), want) && !(len(view.QuestIDs()) == 0 && len(want) == 0) {
			t.Fatal("calendar/runtime date mismatch", at, view.QuestIDs(), want)
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil || strings.Contains(string(encoded), "presentation") || strings.Contains(string(encoded), "capabilit") {
		t.Fatal("private NPC planning state leaked into public calendar", err)
	}
	var nilCohort *CommonCollabCohort
	if _, err := nilCohort.PublicDocument(from, from.Add(time.Hour)); err == nil {
		t.Fatal("nil cohort produced a successful calendar")
	}
}

func TestCollabConcurrentReadViewsRemainIndependent(t *testing.T) {
	cohort := cohortForTest(t, candidateParse(t, researchFixture(t)))
	boundary := researchTime(t, "2099-01-04T00:00:00+09:00")
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 100; n++ {
				before, after := cohort.At(boundary.Add(-time.Nanosecond)), cohort.At(boundary)
				if before.Presentation() != "higanjima" || after.Presentation() != "nier" || len(before.QuestIDs()) != 13 || len(after.QuestIDs()) != 13 {
					t.Error("shared view was mutated")
					return
				}
				before.QuestIDs()[0] = 0
				before.TuneValues()[0].ID = 0
			}
		}()
	}
	group.Wait()
}

func TestCollabExactScopeAndTuneContracts(t *testing.T) {
	wantScopes := []string{"evangelion", "evangelion", "evangelion", "evangelion", "kaiji", "higanjima", "nier", "nier", "nier", "nier", "nier", "nier", "pso2"}
	definitions := CommonCollabDefinitions()
	if len(definitions) != len(expectedCollabIDs) {
		t.Fatal("definition inventory changed")
	}
	for i, definition := range definitions {
		if definition.QuestID != expectedCollabIDs[i] || definition.Scope != wantScopes[i] {
			t.Fatal("scope or original ID changed", definition)
		}
	}
	for scope, expected := range map[string]uint16{"kaiji": 1106, "higanjima": 1144, "nier": 1153, "evangelion": 1156, "pso2": 1130} {
		t.Run(scope, func(t *testing.T) {
			fixture := researchFixture(t)
			fixture["schedules"] = append(fixture["schedules"].([]any), map[string]any{"id": "research_single_always"})
			fixture["collab_presentations"] = []any{map[string]any{"event": scope, "schedule": "research_single_always"}}
			c := candidateParse(t, fixture)
			cohort, err := BuildCommonCollabCohort(c, researchLocations(c), []string{scope})
			if err != nil {
				t.Fatal(err)
			}
			view := cohort.At(researchTime(t, "2099-01-02T00:00:00+09:00"))
			if view.Presentation() != scope || !reflect.DeepEqual(view.TuneValues(), []CollabTuneValue{{expected, 1}}) || !reflect.DeepEqual(view.QuestIDs(), expectedCollabIDs) {
				t.Fatal("static tuple changed or incorrectly filtered publication", view)
			}
		})
	}
}

func TestCollabCoverageCanUseEquivalentProfileUnions(t *testing.T) {
	fixture := researchFixture(t)
	worlds := fixture["profiles"].([]any)[0].(map[string]any)["worlds"]
	fixture["profiles"] = append(fixture["profiles"].([]any),
		map[string]any{"id": "research_c1", "worlds": worlds, "channels": []int{1}},
		map[string]any{"id": "research_c2", "worlds": worlds, "channels": []int{2}})
	for _, raw := range fixture["quests"].([]any) {
		row := raw.(map[string]any)
		if commonCollabID(int(row["quest_id"].(float64))) {
			row["profiles"] = []string{"research_c1", "research_c2"}
		}
	}
	c := candidateParse(t, fixture)
	cohort := cohortForTest(t, c)
	if !reflect.DeepEqual(cohort.At(researchTime(t, "2099-01-02T00:00:00+09:00")).QuestIDs(), expectedCollabIDs) {
		t.Fatal("equivalent profile unions were not common")
	}
}

func TestCollabQuestPeriodicPublicationRemainsSupported(t *testing.T) {
	fixture := researchFixture(t)
	fixture["collab_presentations"] = []any{}
	for _, raw := range fixture["schedules"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == "research_collab_open" {
			row["ends_at"], row["active_hours"], row["repeat_hours"] = "2099-01-20T00:00:00+09:00", 72, 96
		}
	}
	c := candidateParse(t, fixture)
	cohort, err := BuildCommonCollabCohort(c, researchLocations(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   string
		open bool
	}{
		{"2099-01-01T00:00:00+09:00", true},
		{"2099-01-03T23:59:59.999999999+09:00", true},
		{"2099-01-04T00:00:00+09:00", false},
		{"2099-01-05T00:00:00+09:00", true},
		{"2099-01-20T00:00:00+09:00", false},
	} {
		view := cohort.At(researchTime(t, test.at))
		if (len(view.QuestIDs()) == 13) != test.open || len(view.TuneValues()) != 0 {
			t.Fatal("publication periodicity inherited NPC-plan restriction", test, view)
		}
	}
	from := researchTime(t, "2099-01-01T00:00:00+09:00")
	doc, err := cohort.PublicDocument(from, from.Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int]int{}
	for _, occurrence := range doc.Occurrences {
		if commonCollabID(occurrence.QuestID) {
			counts[occurrence.QuestID]++
		}
	}
	for _, id := range expectedCollabIDs {
		if counts[id] != 5 {
			t.Fatal("periodic calendar count changed", id, counts[id])
		}
	}
}

func TestCollabAllDisabledRemainsAnExplicitEmptyInventory(t *testing.T) {
	fixture := researchFixture(t)
	fixture["collab_presentations"] = []any{}
	for i, raw := range fixture["quests"].([]any) {
		row := raw.(map[string]any)
		id := int(row["quest_id"].(float64))
		if commonCollabID(id) {
			fixture["quests"].([]any)[i] = map[string]any{"quest_id": id, "disabled": true}
		}
	}
	c := candidateParse(t, fixture)
	cohort, err := BuildCommonCollabCohort(c, researchLocations(c), nil)
	if err != nil || !c.CommonCollabDeclared() {
		t.Fatal("explicit disabled inventory rejected", err)
	}
	view := cohort.At(researchTime(t, "2099-01-02T00:00:00+09:00"))
	if len(view.QuestIDs()) != 0 || len(view.TuneValues()) != 0 {
		t.Fatal("disabled inventory auto-published", view)
	}
}
