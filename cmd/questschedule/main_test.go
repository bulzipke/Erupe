package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
)

const testManifest = `{
  "version":"calendar-test-1",
  "profiles":[{"id":"p","worlds":["Free"],"channels":[2]}],
  "schedules":[
    {"id":"permanent"},
    {"id":"rotation","starts_at":"2026-10-23T00:00:00+09:00","active_hours":72,"repeat_hours":96},
    {"id":"future","starts_at":"2026-11-01T00:00:00+09:00","ends_at":"2026-11-02T00:00:00+09:00"}
  ],
  "quests":[
    {"quest_id":10,"profiles":["p"],"schedule":"permanent","requirements":["Required equipment and entry ticket remain necessary"]},
    {"quest_id":20,"profiles":["p"],"schedule":"rotation"},
    {"quest_id":30,"profiles":["p"],"schedule":"future"},
    {"quest_id":40,"disabled":true}
  ]
}`

func writeManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(testManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readDocument(t *testing.T, value []byte) questdelivery.ScheduleDocument {
	t.Helper()
	var doc questdelivery.ScheduleDocument
	if err := json.Unmarshal(value, &doc); err != nil {
		t.Fatalf("invalid exported document: %v", err)
	}
	return doc
}

func assertInputUnchanged(t *testing.T, path string, expected []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatal("the input file was modified")
	}
}

func TestRunExportsSamePublicDocumentInKST(t *testing.T) {
	input := writeManifest(t)
	var stdout bytes.Buffer
	if err := run([]string{"-input", input, "-from", "2026-10-24", "-days", "30"}, &stdout); err != nil {
		t.Fatal(err)
	}
	got := readDocument(t, stdout.Bytes())
	zone := time.FixedZone("KST", 9*60*60)
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, zone)
	catalog, err := questdelivery.Parse([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	want, err := catalog.PublicDocument(from, from.AddDate(0, 0, 30))
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip the expected document to compare identical JSON time-zone
	// locations rather than a named FixedZone versus an unnamed parsed one.
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, readDocument(t, encoded)) {
		t.Fatalf("CLI data differs from shared public export: got %+v, want %+v", got, want)
	}
	hasRequirements := false
	for _, row := range got.Occurrences {
		if row.QuestID == 10 && reflect.DeepEqual(row.Requirements, []string{"Required equipment and entry ticket remain necessary"}) {
			hasRequirements = true
		}
	}
	if got.Version != "calendar-test-1" || got.Timezone != "Asia/Seoul" || !got.OriginalEntryConditionsApply || !hasRequirements {
		t.Fatalf("public policy metadata is missing: %+v", got)
	}
	if !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) || !bytes.Contains(stdout.Bytes(), []byte("+09:00")) {
		t.Fatal("CLI export needs a terminating newline and explicit KST timestamps")
	}
	assertInputUnchanged(t, input, []byte(testManifest))
}

func TestRunCalendarRetainsPriorStartAndExcludesFutureWindows(t *testing.T) {
	input := writeManifest(t)
	for _, tc := range []struct {
		date string
		want []int
	}{
		{"2026-10-22", []int{10}},
		{"2026-10-24", []int{10, 20}},
		{"2026-10-26", []int{10}},
		{"2026-10-27", []int{10, 20}},
	} {
		t.Run(tc.date, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := run([]string{"-input", input, "-from", tc.date, "-days", "1"}, &stdout); err != nil {
				t.Fatal(err)
			}
			doc := readDocument(t, stdout.Bytes())
			var ids []int
			for _, row := range doc.Occurrences {
				ids = append(ids, row.QuestID)
				if tc.date == "2026-10-24" && row.QuestID == 20 && row.StartsAt.Format(time.RFC3339) != "2026-10-23T00:00:00+09:00" {
					t.Errorf("ongoing window lost its original start: %+v", row)
				}
			}
			// Publication order is by occurrence start, not quest ID. Compare
			// membership while preserving the exact prior-start assertion above.
			if len(ids) != len(tc.want) {
				t.Fatalf("exported quest IDs %v, want %v", ids, tc.want)
			}
			for _, want := range tc.want {
				found := false
				for _, got := range ids {
					found = found || got == want
				}
				if !found {
					t.Errorf("exported quest IDs %v omitted %d", ids, want)
				}
			}
		})
	}
}

func TestRunDefaultTodayAndDayBounds(t *testing.T) {
	input := writeManifest(t)
	zone := time.FixedZone("KST", 9*60*60)
	before := time.Now().In(zone)
	var stdout bytes.Buffer
	if err := run([]string{"-input", input}, &stdout); err != nil {
		t.Fatal(err)
	}
	after := time.Now().In(zone)
	doc := readDocument(t, stdout.Bytes())
	day := doc.From.Format(time.DateOnly)
	if day != before.Format(time.DateOnly) && day != after.Format(time.DateOnly) {
		t.Fatalf("default start %s is not today's KST date", day)
	}
	if doc.From.Hour() != 0 || doc.From.Minute() != 0 || doc.From.Second() != 0 || !doc.Until.Equal(doc.From.AddDate(0, 0, 30)) {
		t.Fatalf("default calendar range is not 30 KST calendar days: %+v", doc)
	}
	for _, days := range []int{1, 31} {
		stdout.Reset()
		if err := run([]string{"-input", input, "-from", "2026-10-24", "-days", fmt.Sprint(days)}, &stdout); err != nil {
			t.Fatal(err)
		}
		doc = readDocument(t, stdout.Bytes())
		if !doc.Until.Equal(doc.From.AddDate(0, 0, days)) {
			t.Errorf("days %d exported range %+v", days, doc)
		}
	}
}

func TestRunRejectsInvalidOptionsAndInputWithoutWriting(t *testing.T) {
	input := writeManifest(t)
	for name, args := range map[string][]string{
		"missing input":            nil,
		"positional argument":      {"-input", input, "extra"},
		"zero days":                {"-input", input, "-days", "0"},
		"negative days":            {"-input", input, "-days", "-1"},
		"too many days":            {"-input", input, "-days", "32"},
		"invalid date":             {"-input", input, "-from", "2026-02-30"},
		"nonpadded date":           {"-input", input, "-from", "2026-1-1"},
		"datetime instead of date": {"-input", input, "-from", "2026-10-24T00:00:00+09:00"},
		"zero year":                {"-input", input, "-from", "0000-01-01"},
		"year overflow":            {"-input", input, "-from", "9999-12-31", "-days", "31"},
		"missing file":             {"-input", filepath.Join(t.TempDir(), "missing.json")},
		"directory":                {"-input", t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := run(args, &stdout); err == nil {
				t.Fatal("invalid arguments/input were accepted")
			}
			if stdout.Len() != 0 {
				t.Fatal("failed export wrote partial output")
			}
			assertInputUnchanged(t, input, []byte(testManifest))
		})
	}
}

func TestRunRejectsServerConfigAndMalformedManifestReadOnly(t *testing.T) {
	for _, value := range []string{
		`{"Host":"127.0.0.1","QuestDelivery":{"Enabled":false}}`,
		strings.TrimSuffix(testManifest, "}"),
		`{"version":"bad","unexpected_field":true}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		if err := run([]string{"-input", path}, &stdout); err == nil || stdout.Len() != 0 {
			t.Fatalf("invalid manifest/config was accepted or produced output: %v", err)
		}
		assertInputUnchanged(t, path, []byte(value))
	}
}

func TestRunWritesSeparateOutputAndPropagatesWriterError(t *testing.T) {
	input := writeManifest(t)
	output := filepath.Join(t.TempDir(), "calendar.json")
	var stdout bytes.Buffer
	if err := run([]string{"-input", input, "-output", output, "-from", "2026-10-24"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatal("file export also wrote to stdout")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if doc := readDocument(t, data); doc.Version != "calendar-test-1" {
		t.Fatalf("wrong exported version: %s", doc.Version)
	}
	assertInputUnchanged(t, input, []byte(testManifest))
	sentinel := errors.New("test output failure")
	if err := run([]string{"-input", input, "-from", "2026-10-24"}, errorWriter{sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("writer failure was lost: %v", err)
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRunRejectsOutputAliasesOfInput(t *testing.T) {
	for _, alias := range []string{"identical", "dot directory", "hard link"} {
		t.Run(alias, func(t *testing.T) {
			input := writeManifest(t)
			output := input
			switch alias {
			case "dot directory":
				output = filepath.Dir(input) + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(input)
			case "hard link":
				output = filepath.Join(filepath.Dir(input), "manifest-hardlink.json")
				if err := os.Link(input, output); err != nil {
					t.Skipf("filesystem does not support a test hard link: %v", err)
				}
			}
			if err := run([]string{"-input", input, "-output", output, "-from", "2026-10-24"}, io.Discard); err == nil {
				t.Error("an output alias of the input manifest was accepted")
			}
			assertInputUnchanged(t, input, []byte(testManifest))
		})
	}
}
