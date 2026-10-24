package questdelivery_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
)

const example = `{
  "version":"draft-1",
  "profiles":[
    {"id":"free","worlds":["Free"]},
    {"id":"expert","worlds":["Expert"],"channels":[2,3]}
  ],
  "schedules":[
    {"id":"permanent"},
    {"id":"rotation","starts_at":"2026-10-23T00:00:00+09:00","active_hours":72,"repeat_hours":96},
    {"id":"holiday","starts_at":"2026-12-24T00:00:00+09:00","ends_at":"2026-12-27T00:00:00+09:00"}
  ],
  "quests":[
    {"quest_id":40001,"profiles":["expert"],"schedule":"holiday"},
    {"quest_id":40000,"profiles":["free"],"schedule":"rotation","title":"Materials"},
    {"quest_id":11000,"profiles":["free","expert"],"schedule":"permanent","requirements":["Specified equipment required","Original entry ticket required"]},
    {"quest_id":42000,"disabled":true},
    {"quest_id":65535,"disabled":true}
  ]
}`

func timestamp(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func parse(t *testing.T, value string) *questdelivery.Catalog {
	t.Helper()
	c, err := questdelivery.Parse([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func oneSchedule(schedule string) string {
	return `{"version":"test","profiles":[{"id":"p","worlds":["W"]}],"schedules":[` + schedule + `],"quests":[{"quest_id":1,"profiles":["p"],"schedule":"s"}]}`
}

func TestPublicationBoundariesAndProfileUnion(t *testing.T) {
	c := parse(t, example)
	anchor := timestamp(t, "2026-10-23T00:00:00+09:00")
	tests := []struct {
		name             string
		quest, channel   int
		world            string
		at               time.Time
		managed, allowed bool
	}{
		{"unmanaged", 999, 1, "Free", anchor, false, false},
		{"disabled", 42000, 1, "Free", anchor, true, false},
		{"permanent first profile", 11000, 1, "Free", anchor, true, true},
		{"permanent second profile", 11000, 3, "Expert", anchor, true, true},
		{"wrong channel", 11000, 1, "Expert", anchor, true, false},
		{"zero channel", 11000, 0, "Free", anchor, true, false},
		{"negative channel", 11000, -1, "Free", anchor, true, false},
		{"out of range channel", 11000, 65536, "Free", anchor, true, false},
		{"wrong world", 11000, 1, "Unknown", anchor, true, false},
		{"exact world names", 11000, 1, "free", anchor, true, false},
		{"before anchor", 40000, 1, "Free", anchor.Add(-time.Nanosecond), true, false},
		{"at anchor", 40000, 1, "Free", anchor, true, true},
		{"last active instant", 40000, 1, "Free", anchor.Add(72*time.Hour - time.Nanosecond), true, true},
		{"exclusive end", 40000, 1, "Free", anchor.Add(72 * time.Hour), true, false},
		{"last off instant", 40000, 1, "Free", anchor.Add(96*time.Hour - time.Nanosecond), true, false},
		{"second cycle", 40000, 1, "Free", anchor.Add(96 * time.Hour), true, true},
		{"same instant UTC", 40000, 1, "Free", anchor.UTC(), true, true},
		{"one-time starts", 40001, 2, "Expert", timestamp(t, "2026-12-24T00:00:00+09:00"), true, true},
		{"one-time ends", 40001, 2, "Expert", timestamp(t, "2026-12-27T00:00:00+09:00"), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managed, allowed := c.Allows(tt.quest, tt.world, tt.channel, tt.at)
			if managed != tt.managed || allowed != tt.allowed {
				t.Fatalf("Allows = (%v,%v), want (%v,%v)", managed, allowed, tt.managed, tt.allowed)
			}
		})
	}
	if got := c.AllowedQuestIDs("Free", 1, anchor); !reflect.DeepEqual(got, []int{11000, 40000}) {
		t.Fatalf("allowed IDs = %v", got)
	}
	if got := c.AllowedQuestIDs("Missing", 1, anchor); len(got) != 0 {
		t.Fatalf("unknown world allowed IDs = %v", got)
	}
}

func TestOldAnchorAndFractionalSecondPhase(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s","starts_at":"1600-01-01T00:00:00.5Z","active_hours":12,"repeat_hours":24}`))
	// Four centuries exceed time.Duration's maximum. A fixed daily phase must
	// still retain the original fractional-second boundary after a restart.
	for _, tc := range []struct {
		at      string
		allowed bool
	}{
		{"2000-01-01T00:00:00.499999999Z", false},
		{"2000-01-01T00:00:00.5Z", true},
		{"2000-01-01T12:00:00.499999999Z", true},
		{"2000-01-01T12:00:00.5Z", false},
	} {
		if _, got := c.Allows(1, "W", 1, timestamp(t, tc.at)); got != tc.allowed {
			t.Errorf("at %s: allowed %v, want %v", tc.at, got, tc.allowed)
		}
	}
	from := timestamp(t, "2000-01-01T06:00:00Z")
	rows, err := c.Windows(from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[0].StartsAt.Equal(timestamp(t, "2000-01-01T00:00:00.5Z")) || !rows[1].StartsAt.Equal(timestamp(t, "2000-01-02T00:00:00.5Z")) {
		t.Fatalf("old anchor windows = %+v", rows)
	}
}

func TestPeriodicGlobalEndClipsLastWindow(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s","starts_at":"2026-10-23T00:00:00+09:00","ends_at":"2026-10-27T05:00:00+09:00","active_hours":72,"repeat_hours":96}`))
	end := timestamp(t, "2026-10-27T05:00:00+09:00")
	if _, allowed := c.Allows(1, "W", 1, end.Add(-time.Nanosecond)); !allowed {
		t.Fatal("last instant before global cutoff must be active")
	}
	if _, allowed := c.Allows(1, "W", 1, end); allowed {
		t.Fatal("global end is exclusive")
	}
	rows, err := c.Windows(end.Add(-time.Hour), end.Add(30*24*time.Hour))
	if err != nil || len(rows) != 1 || !rows[0].EndsAt.Equal(end) {
		t.Fatalf("clipped rows = %+v, err = %v", rows, err)
	}
}

func TestZeroTimeIsNotAnAbsentEnd(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s","starts_at":"0001-01-01T00:00:00+09:00","ends_at":"0001-01-01T00:00:00Z"}`))
	if _, allowed := c.Allows(1, "W", 1, time.Time{}); allowed {
		t.Fatal("a year-one zero-valued time is still an explicit exclusive end")
	}
}

func TestThirtyDayCalendarIncludesAlreadyStartedWindow(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s","starts_at":"2026-10-23T00:00:00+09:00","active_hours":72,"repeat_hours":96}`))
	from := timestamp(t, "2026-10-24T00:00:00+09:00")
	to := timestamp(t, "2026-11-23T00:00:00+09:00")
	rows, err := c.Windows(from, to)
	if err != nil {
		t.Fatal(err)
	}
	starts := []string{
		"2026-10-23T00:00:00+09:00", "2026-10-27T00:00:00+09:00",
		"2026-10-31T00:00:00+09:00", "2026-11-04T00:00:00+09:00",
		"2026-11-08T00:00:00+09:00", "2026-11-12T00:00:00+09:00",
		"2026-11-16T00:00:00+09:00", "2026-11-20T00:00:00+09:00",
	}
	if len(rows) != len(starts) {
		t.Fatalf("got %d windows, want %d", len(rows), len(starts))
	}
	for i, want := range starts {
		if !rows[i].StartsAt.Equal(timestamp(t, want)) || rows[i].EndsAt.Sub(rows[i].StartsAt) != 72*time.Hour {
			t.Errorf("row %d = %+v", i, rows[i])
		}
	}
	// A window ending exactly at from and another starting exactly at to do
	// not overlap the requested half-open interval.
	gapStart := timestamp(t, "2026-10-26T00:00:00+09:00")
	gapEnd := timestamp(t, "2026-10-27T00:00:00+09:00")
	rows, err = c.Windows(gapStart, gapEnd)
	if err != nil || len(rows) != 0 {
		t.Fatalf("gap returned %+v, %v", rows, err)
	}
}

func TestCalendarPermanentAndOneTimeWindows(t *testing.T) {
	c := parse(t, example)
	from := timestamp(t, "2026-12-25T12:00:00+09:00")
	to := from.Add(6 * time.Hour)
	rows, err := c.Windows(from, to)
	if err != nil {
		t.Fatal(err)
	}
	var permanent, holiday *questdelivery.Occurrence
	for i := range rows {
		switch rows[i].QuestID {
		case 11000:
			permanent = &rows[i]
		case 40001:
			holiday = &rows[i]
		case 42000, 65535:
			t.Fatal("disabled quest appeared in public calendar")
		}
	}
	if permanent == nil || !permanent.StartsAt.Equal(from) || !permanent.EndsAt.Equal(to) {
		t.Fatalf("permanent export = %+v", permanent)
	}
	if holiday == nil || !holiday.StartsAt.Equal(timestamp(t, "2026-12-24T00:00:00+09:00")) || !holiday.EndsAt.Equal(timestamp(t, "2026-12-27T00:00:00+09:00")) {
		t.Fatalf("one-time export = %+v", holiday)
	}
	if _, err := json.Marshal(rows); err != nil {
		t.Fatalf("calendar must be JSON exportable: %v", err)
	}
}

func TestAccessorsCannotMutateCatalog(t *testing.T) {
	data := []byte(example)
	c, err := questdelivery.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = ' '
	}
	if c.Version() != "draft-1" {
		t.Fatal("version was changed by input mutation")
	}
	ids := c.QuestIDs()
	if !reflect.DeepEqual(ids, []int{11000, 40000, 40001, 42000, 65535}) {
		t.Fatalf("IDs are not sorted or disabled IDs were omitted: %v", ids)
	}
	ids[0] = 0
	profiles := c.Profiles()
	profiles[0].ID = "changed"
	profiles[0].Worlds[0] = "changed"
	profiles[0].Channels[0] = 99
	at := timestamp(t, "2026-10-23T00:00:00+09:00")
	rows, err := c.Windows(at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Profiles[0].Worlds[0] = "changed"
	rows[0].Profiles[1].Channels[0] = 99
	rows[0].Profiles[1].ID = "changed"
	rows[0].Requirements[0] = "external metadata mutation"
	if _, allowed := c.Allows(11000, "Expert", 2, at); !allowed {
		t.Fatal("returned slices mutated catalog's matching rules")
	}
	if got := c.QuestIDs()[0]; got != 11000 {
		t.Fatalf("returned ID slice mutated catalog: %d", got)
	}
	if got := c.Profiles()[0]; got.ID != "expert" || got.Worlds[0] != "Expert" || got.Channels[0] != 2 {
		t.Fatalf("profile copy mutated catalog: %+v", got)
	}
	rows2, err := c.Windows(at, at.Add(time.Hour))
	if err != nil || rows2[0].Profiles[0].Worlds[0] != "Free" || rows2[0].Profiles[1].Channels[0] != 2 {
		t.Fatalf("calendar profile copies were shared: %+v, %v", rows2, err)
	}
	if !reflect.DeepEqual(rows2[0].Requirements, []string{"Specified equipment required", "Original entry ticket required"}) {
		t.Fatalf("calendar requirements were not copied: %v", rows2[0].Requirements)
	}
}

func TestRequirementsAreDescriptiveMetadataOnly(t *testing.T) {
	value := strings.Replace(oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":1,"repeat_hours":2}`), `"quest_id":1`, `"quest_id":1,"requirements":["Original code entitlement required"]`, 1)
	c := parse(t, value)
	from := timestamp(t, "2026-01-01T00:00:00Z")
	if _, allowed := c.Allows(1, "W", 1, from); !allowed {
		t.Fatal("requirement descriptions must not be interpreted as publication rules")
	}
	rows, err := c.Windows(from, from.Add(4*time.Hour))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v, err %v", rows, err)
	}
	rows[0].Requirements[0] = "changed"
	if rows[1].Requirements[0] != "Original code entitlement required" {
		t.Fatal("sibling occurrences share a requirements slice")
	}
	for name, requirements := range map[string][]string{
		"too many": make([]string, 65),
		"too long": {strings.Repeat("x", 4097)},
		"empty":    {" "},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(requirements)
			if err != nil {
				t.Fatal(err)
			}
			invalid := strings.Replace(oneSchedule(`{"id":"s"}`), `"quest_id":1`, `"quest_id":1,"requirements":`+string(encoded), 1)
			if _, err := questdelivery.Parse([]byte(invalid)); err == nil {
				t.Fatal("invalid requirements metadata accepted")
			}
		})
	}
}

func TestRejectInvalidManifests(t *testing.T) {
	valid := oneSchedule(`{"id":"s"}`)
	tests := map[string]string{
		"empty":                  ``,
		"null document":          `null`,
		"missing version":        strings.Replace(valid, `"version":"test",`, "", 1),
		"blank version":          strings.Replace(valid, `"version":"test"`, `"version":" "`, 1),
		"unknown root":           strings.Replace(valid, `"version":"test"`, `"version":"test","extra":1`, 1),
		"unknown schedule":       oneSchedule(`{"id":"s","active_hour":72}`),
		"unknown profile":        strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W"],"extra":true`, 1),
		"unknown quest field":    strings.Replace(valid, `"quest_id":1`, `"quest_id":1,"extra":true`, 1),
		"trailing document":      valid + `{}`,
		"trailing garbage":       valid + `invalid`,
		"duplicate profile":      strings.Replace(valid, `"profiles":[{"id":"p","worlds":["W"]}]`, `"profiles":[{"id":"p","worlds":["W"]},{"id":"p","worlds":["V"]}]`, 1),
		"duplicate schedule":     strings.Replace(valid, `"schedules":[{"id":"s"}]`, `"schedules":[{"id":"s"},{"id":"s"}]`, 1),
		"duplicate quest":        strings.Replace(valid, `"quests":[`, `"quests":[{"quest_id":1,"disabled":true},`, 1),
		"empty world":            strings.Replace(valid, `"worlds":["W"]`, `"worlds":[]`, 1),
		"empty world name":       strings.Replace(valid, `"worlds":["W"]`, `"worlds":[""]`, 1),
		"padded world":           strings.Replace(valid, `"worlds":["W"]`, `"worlds":[" W"]`, 1),
		"duplicate world":        strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W","W"]`, 1),
		"duplicate channel":      strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W"],"channels":[1,1]`, 1),
		"zero channel":           strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W"],"channels":[0]`, 1),
		"negative channel":       strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W"],"channels":[-1]`, 1),
		"large channel":          strings.Replace(valid, `"worlds":["W"]`, `"worlds":["W"],"channels":[65536]`, 1),
		"unknown profile ref":    strings.Replace(valid, `"profiles":["p"]`, `"profiles":["missing"]`, 1),
		"unknown schedule ref":   strings.Replace(valid, `"schedule":"s"`, `"schedule":"missing"`, 1),
		"duplicate profile ref":  strings.Replace(valid, `"profiles":["p"]`, `"profiles":["p","p"]`, 1),
		"no quest profiles":      strings.Replace(valid, `"profiles":["p"]`, `"profiles":[]`, 1),
		"no quest schedule":      strings.Replace(valid, `,"schedule":"s"`, ``, 1),
		"disabled bad ref":       strings.Replace(valid, `"schedule":"s"`, `"schedule":"missing","disabled":true`, 1),
		"zero quest id":          strings.Replace(valid, `"quest_id":1`, `"quest_id":0`, 1),
		"negative quest id":      strings.Replace(valid, `"quest_id":1`, `"quest_id":-1`, 1),
		"large quest id":         strings.Replace(valid, `"quest_id":1`, `"quest_id":65536`, 1),
		"integer overflow":       strings.Replace(valid, `"quest_id":1`, `"quest_id":18446744073709551616`, 1),
		"negative hours":         oneSchedule(`{"id":"s","active_hours":-1}`),
		"duration overflow":      oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":2562048,"repeat_hours":2562048}`),
		"integer hours overflow": oneSchedule(`{"id":"s","active_hours":9223372036854775808}`),
		"overlapping cycle":      oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":97,"repeat_hours":96}`),
		"no anchor":              oneSchedule(`{"id":"s","active_hours":72,"repeat_hours":96}`),
		"no repeat":              oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":72}`),
		"no active":              oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","repeat_hours":96}`),
		"only start":             oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z"}`),
		"only end":               oneSchedule(`{"id":"s","ends_at":"2026-01-01T00:00:00Z"}`),
		"reversed window":        oneSchedule(`{"id":"s","starts_at":"2026-01-02T00:00:00Z","ends_at":"2026-01-01T00:00:00Z"}`),
		"zero window":            oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-01-01T00:00:00Z"}`),
		"reversed periodic":      oneSchedule(`{"id":"s","starts_at":"2026-01-02T00:00:00Z","ends_at":"2026-01-01T00:00:00Z","active_hours":1,"repeat_hours":2}`),
		"no offset":              oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00","active_hours":1,"repeat_hours":2}`),
		"comma fraction":         oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00,5Z","active_hours":1,"repeat_hours":2}`),
		"invalid offset":         oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00+24:00","active_hours":1,"repeat_hours":2}`),
		"invalid offset minutes": oneSchedule(`{"id":"s","starts_at":"2026-01-01T00:00:00+09:60","active_hours":1,"repeat_hours":2}`),
		"invalid date":           oneSchedule(`{"id":"s","starts_at":"2026-02-30T00:00:00Z","active_hours":1,"repeat_hours":2}`),
		"zero year":              oneSchedule(`{"id":"s","starts_at":"0000-01-01T00:00:00Z","active_hours":1,"repeat_hours":2}`),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if c, err := questdelivery.Parse([]byte(value)); err == nil || c != nil {
				t.Fatalf("invalid manifest accepted: catalog=%v, err=%v", c, err)
			}
		})
	}
	if _, err := questdelivery.Parse([]byte(valid + " \n\t")); err != nil {
		t.Fatalf("trailing whitespace should be accepted: %v", err)
	}
	if _, err := questdelivery.Parse([]byte(strings.Repeat(" ", questdelivery.MaxManifestBytes+1))); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

func TestVersionIsBoundedPrintableHeaderMetadata(t *testing.T) {
	valid := oneSchedule(`{"id":"s"}`)
	for _, version := range []string{strings.Repeat("v", 128), "2026-10-23 draft 1", "release!#$%&'*+-.^_`|~"} {
		encoded, err := json.Marshal(version)
		if err != nil {
			t.Fatal(err)
		}
		value := strings.Replace(valid, `"version":"test"`, `"version":`+string(encoded), 1)
		if got := parse(t, value).Version(); got != version {
			t.Errorf("version %q changed to %q", version, got)
		}
	}
	for name, version := range map[string]string{
		"too long":        strings.Repeat("v", 129),
		"line feed":       "v\n1",
		"carriage return": "v\r1",
		"tab":             "v\t1",
		"null byte":       "v\x001",
		"delete control":  "v\x7f1",
		"non-ASCII":       "v\u00e91",
		"leading space":   " v1",
		"trailing space":  "v1 ",
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(version)
			if err != nil {
				t.Fatal(err)
			}
			value := strings.Replace(valid, `"version":"test"`, `"version":`+string(encoded), 1)
			if _, err := questdelivery.Parse([]byte(value)); err == nil {
				t.Fatal("unsafe or oversized version metadata was accepted")
			}
		})
	}
}

func TestCalendarCapsRejectWithoutPartialResult(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s"}`))
	from := timestamp(t, "2026-01-01T00:00:00Z")
	if rows, err := c.Windows(from, from.Add(questdelivery.MaxWindowSpan)); err != nil || len(rows) != 1 {
		t.Fatalf("exact range limit: rows %d, err %v", len(rows), err)
	}
	for _, to := range []time.Time{from, from.Add(-time.Hour), from.Add(questdelivery.MaxWindowSpan + time.Nanosecond), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if rows, err := c.Windows(from, to); err == nil || rows != nil {
			t.Fatalf("invalid/oversized range accepted: rows %d, err %v", len(rows), err)
		}
	}
	// Twelve quests each contribute 8,784 hourly windows in a leap-year span.
	// This exceeds the combined cap, though each individual schedule fits it.
	var quests []string
	for id := 1; id <= 12; id++ {
		quests = append(quests, fmt.Sprintf(`{"quest_id":%d,"profiles":["p"],"schedule":"s"}`, id))
	}
	value := `{"version":"cap","profiles":[{"id":"p","worlds":["W"]}],"schedules":[{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":1,"repeat_hours":1}],"quests":[` + strings.Join(quests, ",") + `]}`
	c = parse(t, value)
	if rows, err := c.Windows(from, from.Add(questdelivery.MaxWindowSpan)); err == nil || rows != nil {
		t.Fatalf("combined occurrence cap returned partial data: rows %d, err %v", len(rows), err)
	}
}

func TestNestedProfileExportCap(t *testing.T) {
	var worlds, quests []string
	for id := 0; id < 100; id++ {
		worlds = append(worlds, fmt.Sprintf(`"W%d"`, id))
	}
	for id := 1; id <= 100; id++ {
		quests = append(quests, fmt.Sprintf(`{"quest_id":%d,"profiles":["p"],"schedule":"s"}`, id))
	}
	value := `{"version":"cap","profiles":[{"id":"p","worlds":[` + strings.Join(worlds, ",") + `]}],"schedules":[{"id":"s","starts_at":"2026-01-01T00:00:00Z","active_hours":1,"repeat_hours":1}],"quests":[` + strings.Join(quests, ",") + `]}`
	c := parse(t, value)
	from := timestamp(t, "2026-01-01T00:00:00Z")
	if rows, err := c.Windows(from, from.Add(100*time.Hour)); err == nil || rows != nil {
		t.Fatalf("nested export cap returned partial data: rows %d, err %v", len(rows), err)
	}
}

func TestUnrepresentableOccurrenceFailsExport(t *testing.T) {
	c := parse(t, oneSchedule(`{"id":"s","starts_at":"9999-12-31T00:00:00Z","active_hours":48,"repeat_hours":48}`))
	from := timestamp(t, "9999-12-31T00:00:00Z")
	if rows, err := c.Windows(from, from.Add(time.Hour)); err == nil || rows != nil {
		t.Fatalf("unrepresentable occurrence: rows %v, err %v", rows, err)
	}
	c = parse(t, oneSchedule(`{"id":"s","starts_at":"9999-12-31T00:00:00Z","ends_at":"9999-12-31T12:00:00Z","active_hours":48,"repeat_hours":48}`))
	if rows, err := c.Windows(from, from.Add(time.Hour)); err != nil || len(rows) != 1 || rows[0].EndsAt.Year() != 9999 {
		t.Fatalf("explicit end should clip unrepresentable extension: rows %v, err %v", rows, err)
	}
}

func TestEmptyCatalogAndConcurrentReadIsolation(t *testing.T) {
	empty := parse(t, `{"version":"empty"}`)
	at := timestamp(t, "2026-10-23T00:00:00+09:00")
	if ids := empty.QuestIDs(); len(ids) != 0 {
		t.Fatalf("empty IDs = %v", ids)
	}
	if rows, err := empty.Windows(at, at.Add(time.Hour)); err != nil || len(rows) != 0 {
		t.Fatalf("empty calendar = %v, %v", rows, err)
	}
	c := parse(t, example)
	var wg sync.WaitGroup
	errors := make(chan string, 8)
	for n := 0; n < cap(errors); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rows, err := c.Windows(at, at.Add(time.Hour))
				if err != nil || len(rows) != 2 {
					errors <- fmt.Sprintf("rows %d, err %v", len(rows), err)
					return
				}
				rows[0].Profiles[0].Worlds[0] = "external mutation"
				if _, allowed := c.Allows(11000, "Free", 1, at); !allowed {
					errors <- "external mutation changed catalog"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
