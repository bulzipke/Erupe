package questdelivery_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
)

func TestPublicDocumentUsesKSTAndSameCatalogWindows(t *testing.T) {
	c := parse(t, example)
	from := timestamp(t, "2026-10-24T00:00:00Z")
	until := from.Add(30 * 24 * time.Hour)
	raw, err := c.Windows(from, until)
	if err != nil {
		t.Fatal(err)
	}
	public, err := c.PublicDocument(from, until)
	if err != nil {
		t.Fatal(err)
	}
	if public.Version != c.Version() || public.Coverage != "managed_quests_only" || public.Timezone != "Asia/Seoul" || !public.OriginalEntryConditionsApply {
		t.Fatalf("public metadata = %+v", public)
	}
	if !public.From.Equal(from) || !public.Until.Equal(until) || len(public.Occurrences) != len(raw) {
		t.Fatalf("public export did not use the requested catalog/range: %+v", public)
	}
	times := []time.Time{public.From, public.Until}
	for i, row := range public.Occurrences {
		want := raw[i]
		if row.QuestID != want.QuestID || row.Title != want.Title || row.ScheduleID != want.ScheduleID || !row.StartsAt.Equal(want.StartsAt) || !row.EndsAt.Equal(want.EndsAt) || !reflect.DeepEqual(row.Profiles, want.Profiles) || !reflect.DeepEqual(row.Requirements, want.Requirements) {
			t.Fatalf("public occurrence %d changed catalog data: got %+v, want %+v", i, row, want)
		}
		times = append(times, row.StartsAt, row.EndsAt)
	}
	for _, value := range times {
		if _, offset := value.Zone(); offset != 9*60*60 {
			t.Errorf("public time has offset %d: %s", offset, value)
		}
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Coverage    string `json:"coverage"`
		From        string `json:"from"`
		Until       string `json:"until"`
		Occurrences []struct {
			Start string `json:"starts_at"`
			End   string `json:"ends_at"`
		} `json:"occurrences"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Coverage != "managed_quests_only" {
		t.Fatalf("public JSON does not declare its managed-only scope: %s", encoded)
	}
	serializedTimes := []string{wire.From, wire.Until}
	for _, row := range wire.Occurrences {
		serializedTimes = append(serializedTimes, row.Start, row.End)
	}
	for _, value := range serializedTimes {
		if !strings.HasSuffix(value, "+09:00") {
			t.Errorf("public JSON timestamp lacks the explicit KST offset: %s", value)
		}
	}
}

func TestPublicDocumentDeclaresScopeWithoutInferringLegacyOrDisabledPublication(t *testing.T) {
	c := parse(t, `{"version":"scope-test","quests":[{"quest_id":61067,"disabled":true}]}`)
	from := timestamp(t, "2026-10-24T00:00:00+09:00")
	public, err := c.PublicDocument(from, from.Add(30*24*time.Hour))
	if err != nil || public.Coverage != "managed_quests_only" || len(public.Occurrences) != 0 {
		t.Fatalf("empty managed calendar lost its scope or invented a publication: %+v, %v", public, err)
	}
	if managed, allowed := c.Allows(55936, "Free", 1, from); managed || allowed {
		t.Fatal("an unmanaged legacy quest was promoted into the managed calendar")
	}
	if managed, allowed := c.Allows(61067, "Free", 1, from); !managed || allowed {
		t.Fatal("an excluded managed quest lost its runtime restriction")
	}
}

func TestPublicDocumentLabelsPermanentWindowWithoutInventingAnExpiry(t *testing.T) {
	c := parse(t, `{"version":"permanence-test","profiles":[{"id":"all","worlds":["Brave"]}],"schedules":[{"id":"permanent-arbitrary-name"},{"id":"always","starts_at":"2026-10-23T00:00:00+09:00","ends_at":"2026-10-25T00:00:00+09:00"}],"quests":[{"quest_id":1,"profiles":["all"],"schedule":"permanent-arbitrary-name"},{"quest_id":2,"profiles":["all"],"schedule":"always"}]}`)
	from := timestamp(t, "2026-10-23T00:00:00+09:00")
	doc, err := c.PublicDocument(from, from.Add(30*24*time.Hour))
	if err != nil || len(doc.Occurrences) != 2 {
		t.Fatalf("public document: %+v, %v", doc, err)
	}
	for _, row := range doc.Occurrences {
		if row.Permanent != (row.QuestID == 1) {
			t.Fatalf("permanence was inferred from an ID instead of schedule mode: %+v", row)
		}
	}
	data, err := json.Marshal(doc)
	if err != nil || !strings.Contains(string(data), `"permanent":true`) {
		t.Fatalf("explicit permanence missing in public JSON: %s %v", data, err)
	}
}

func TestPublicDocumentReturnsIndependentMetadata(t *testing.T) {
	c := parse(t, example)
	from := timestamp(t, "2026-10-23T00:00:00+09:00")
	until := from.Add(time.Hour)
	first, err := c.PublicDocument(from, until)
	if err != nil || len(first.Occurrences) != 2 {
		t.Fatalf("public export: %+v, %v", first, err)
	}
	first.Version = "changed"
	first.Occurrences[0].QuestID = 0
	first.Occurrences[0].Profiles[0].Worlds[0] = "changed"
	first.Occurrences[0].Profiles[1].Channels[0] = 99
	first.Occurrences[0].Requirements[0] = "changed"
	second, err := c.PublicDocument(from, until)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != "draft-1" || second.Occurrences[0].QuestID != 11000 || second.Occurrences[0].Profiles[0].Worlds[0] != "Free" || second.Occurrences[0].Profiles[1].Channels[0] != 2 || second.Occurrences[0].Requirements[0] != "Specified equipment required" {
		t.Fatalf("a prior document mutated the catalog or later document: %+v", second)
	}
	if _, allowed := c.Allows(11000, "Expert", 2, from); !allowed {
		t.Fatal("public document mutation changed catalog publication rules")
	}
}

func TestPublicDocumentRejectsInvalidRangeWithoutPartialData(t *testing.T) {
	c := parse(t, example)
	from := timestamp(t, "2026-10-23T00:00:00+09:00")
	for _, until := range []time.Time{from, from.Add(-time.Hour), from.Add(questdelivery.MaxWindowSpan + time.Nanosecond)} {
		got, err := c.PublicDocument(from, until)
		if err == nil || !reflect.DeepEqual(got, questdelivery.ScheduleDocument{}) {
			t.Fatalf("invalid range returned document %+v, error %v", got, err)
		}
	}
}

func TestPublicDocumentRejectsExpandedMetadataBeforeSerialization(t *testing.T) {
	for name, metadata := range map[string]map[string]any{
		"repeated title":        {"title": strings.Repeat("x", 32<<10)},
		"repeated requirements": {"requirements": repeatRequirement(64, strings.Repeat("y", 4096))},
	} {
		t.Run(name, func(t *testing.T) {
			metadata["quest_id"] = 1
			metadata["profiles"] = []string{"p"}
			metadata["schedule"] = "s"
			input, err := json.Marshal(map[string]any{
				"version":   "expansion-limit",
				"profiles":  []map[string]any{{"id": "p", "worlds": []string{"W"}}},
				"schedules": []map[string]any{{"id": "s", "starts_at": "2026-01-01T00:00:00Z", "active_hours": 1, "repeat_hours": 1}},
				"quests":    []map[string]any{metadata},
			})
			if err != nil {
				t.Fatal(err)
			}
			c, err := questdelivery.Parse(input)
			if err != nil {
				t.Fatalf("bounded manifest itself must remain valid: %v", err)
			}
			from := timestamp(t, "2026-01-01T00:00:00Z")
			until := from.Add(31 * 24 * time.Hour)
			if windows, err := c.Windows(from, until); err != nil || len(windows) != 744 {
				t.Fatalf("metadata test did not reach the public byte-budget guard: windows %d, err %v", len(windows), err)
			}
			got, err := c.PublicDocument(from, until)
			if !errors.Is(err, questdelivery.ErrPublicDocumentTooLarge) || !reflect.DeepEqual(got, questdelivery.ScheduleDocument{}) {
				t.Fatalf("expanded metadata returned document %+v, error %v", got, err)
			}
		})
	}
}

func repeatRequirement(count int, value string) []string {
	result := make([]string, count)
	for i := range result {
		result[i] = value
	}
	return result
}
