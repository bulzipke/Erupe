package questdelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const commonPublicationExample = `{"version":"research-not-live","profiles":[{"id":"left","worlds":["One"],"channels":[1]},{"id":"right","worlds":["Two"]}],"schedules":[{"id":"always"},{"id":"future","starts_at":"2099-01-01T00:00:00+09:00","ends_at":"2099-01-02T00:00:00+09:00"}],"quests":[{"quest_id":1,"profiles":["left","right"],"schedule":"always","requirements":["Original ticket, equipment and NPC chain remain required"]},{"quest_id":2,"profiles":["right","left"],"schedule":"future"},{"quest_id":3,"disabled":true}]}`

func commonPublicationCatalog(t *testing.T, raw string) *Catalog {
	t.Helper()
	c, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func commonPublicationLocations() []PublicationLocation {
	return []PublicationLocation{{World: "One", Channel: 1}, {World: "Two", Channel: 1}, {World: "Two", Channel: 2}}
}
func commonPublicationRange() (time.Time, time.Time) {
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, kst)
	return from, from.Add(30 * 24 * time.Hour)
}

func TestCommonPublicationUnionAndStaticInventory(t *testing.T) {
	c := commonPublicationCatalog(t, commonPublicationExample)
	cohort, err := BuildCommonPublicationCohort(c, commonPublicationLocations())
	if err != nil {
		t.Fatal(err)
	}
	from, until := commonPublicationRange()
	doc, err := cohort.PublicDocument(from, until)
	if err != nil {
		t.Fatal(err)
	}
	if doc.PublicationScope != CommonPublicationScope || doc.Coverage != "managed_quests_only" || !doc.OriginalEntryConditionsApply || !cohort.BoundTo(c) {
		t.Fatal("scope/identity lost")
	}
	if len(doc.Occurrences) != 1 || len(doc.CommonPublication.Inventory) != 2 || doc.CommonPublication.Inventory[1].QuestID != 2 {
		t.Fatal("static out-of-range enabled inventory lost or disabled included")
	}
	if !reflect.DeepEqual(doc.Occurrences[0].Requirements, []string{"Original ticket, equipment and NPC chain remain required"}) {
		t.Fatal("conditions changed")
	}
	clone := commonPublicationCatalog(t, commonPublicationExample)
	if cohort.BoundTo(clone) || cohort.PermitsLocation("Two", 3) {
		t.Fatal("identity/location inferred")
	}
	raw, err := c.PublicDocument(from, until)
	if err != nil || raw.PublicationScope != "" || raw.CommonPublication != nil {
		t.Fatal("unproven raw document mislabeled common")
	}
	encoded, _ := json.Marshal(raw)
	if strings.Contains(string(encoded), "publication_scope") || strings.Contains(string(encoded), "common_publication") {
		t.Fatal("legacy envelope changed")
	}
}
func TestCommonPublicationRejectsIncompleteAndFakeCommon(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		locations []PublicationLocation
	}{
		{"missing_real_channel", strings.ReplaceAll(commonPublicationExample, `"worlds":["Two"]`, `"worlds":["Two"],"channels":[1]`), commonPublicationLocations()},
		{"partial_ID_outside_range", strings.Replace(commonPublicationExample, `"profiles":["right","left"]`, `"profiles":["right"]`, 1), commonPublicationLocations()},
		{"unused_unknown_profile", strings.Replace(commonPublicationExample, `"profiles":[{"id":"left"`, `"profiles":[{"id":"unused","worlds":["Missing"]},{"id":"left"`, 1), commonPublicationLocations()},
		{"fake_common_name", strings.ReplaceAll(strings.ReplaceAll(commonPublicationExample, "left", "all-existing-worlds-and-channels"), `"profiles":["right","all-existing-worlds-and-channels"]`, `"profiles":["right"]`), commonPublicationLocations()},
		{"no_locations", commonPublicationExample, nil},
		{"duplicate_locations", commonPublicationExample, append(commonPublicationLocations(), PublicationLocation{World: "One", Channel: 1})},
		{"disabled_or_missing_world", commonPublicationExample, []PublicationLocation{{World: "One", Channel: 1}}},
		{"invalid_channel", commonPublicationExample, []PublicationLocation{{World: "One", Channel: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if cohort, err := BuildCommonPublicationCohort(commonPublicationCatalog(t, tc.raw), tc.locations); err == nil || cohort != nil {
				t.Fatal("invalid common coverage accepted")
			}
		})
	}
}
func TestCommonPublicationImmutableOutputsAndEmptyPeriod(t *testing.T) {
	locations := commonPublicationLocations()
	c := commonPublicationCatalog(t, commonPublicationExample)
	cohort, err := BuildCommonPublicationCohort(c, locations)
	if err != nil {
		t.Fatal(err)
	}
	locations[0].World = "changed"
	from, until := commonPublicationRange()
	first, _ := cohort.PublicDocument(from, until)
	want, _ := json.Marshal(first)
	first.CommonPublication.Locations[0].World = "changed"
	first.CommonPublication.Profiles[0].Worlds[0] = "changed"
	first.CommonPublication.Inventory[0].Profiles[0] = "changed"
	first.Occurrences[0].Requirements[0] = "changed"
	second, _ := cohort.PublicDocument(from, until)
	got, _ := json.Marshal(second)
	if string(want) != string(got) {
		t.Fatal("public data aliases cohort")
	}
	empty := commonPublicationCatalog(t, strings.ReplaceAll(commonPublicationExample, `"schedule":"always"`, `"schedule":"future"`))
	verified, err := BuildCommonPublicationCohort(empty, commonPublicationLocations())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := verified.PublicDocument(from, until)
	if err != nil || len(doc.Occurrences) != 0 || len(doc.CommonPublication.Inventory) != 2 {
		t.Fatal("empty range erased static scope or invented current publication")
	}
	if _, err := (*CommonPublicationCohort)(nil).PublicDocument(from, until); err == nil {
		t.Fatal("nil proof accepted")
	}
}
func commonPublicationLarge(t *testing.T, count int, title string) *Catalog {
	t.Helper()
	pid := strings.Repeat("p", 1024)
	rows := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		row, _ := json.Marshal(map[string]any{"quest_id": i, "profiles": []string{pid}, "schedule": "always", "title": title})
		rows = append(rows, string(row))
	}
	return commonPublicationCatalog(t, fmt.Sprintf(`{"version":"bounded-research","profiles":[{"id":%q,"worlds":["One"]}],"schedules":[{"id":"always"}],"quests":[%s]}`, pid, strings.Join(rows, ",")))
}
func TestCommonPublicationMetadataBudgetBeforeCopyAndCombinedWindows(t *testing.T) {
	locations := []PublicationLocation{{World: "One", Channel: 1}}
	if _, err := BuildCommonPublicationCohort(commonPublicationLarge(t, 3000, ""), locations); !errors.Is(err, ErrPublicDocumentTooLarge) {
		t.Fatal("metadata-only oversize not rejected", err)
	}
	c := commonPublicationLarge(t, 1000, strings.Repeat("x", 1000))
	cohort, err := BuildCommonPublicationCohort(c, locations)
	if err != nil {
		t.Fatal(err)
	}
	from, until := commonPublicationRange()
	if _, err := c.PublicDocument(from, until); err != nil {
		t.Fatal("ordinary document should fit independent budget", err)
	}
	if _, err := cohort.PublicDocument(from, until); !errors.Is(err, ErrPublicDocumentTooLarge) {
		t.Fatal("proof+occurrence combined budget exceeded without error", err)
	}
}
func TestCommonPublicationBoundsProfileLocationWorkingSet(t *testing.T) {
	profiles := make([]string, 4096)
	for i := range profiles {
		profiles[i] = fmt.Sprintf(`{"id":"p%d","worlds":["One"]}`, i)
	}
	c := commonPublicationCatalog(t, `{"version":"bounded","profiles":[`+strings.Join(profiles, ",")+`],"quests":[]}`)
	locations := make([]PublicationLocation, 65535)
	for i := range locations {
		locations[i] = PublicationLocation{World: "One", Channel: i + 1}
	}
	if _, err := BuildCommonPublicationCohort(c, locations); !errors.Is(err, ErrPublicDocumentTooLarge) {
		t.Fatal("large profile/location working set not bounded", err)
	}
}
