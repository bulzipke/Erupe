package main

import (
	"bytes"
	"encoding/json"
	"erupe-ce/common/questdelivery"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCommonPublicationCLIExactMethodAndLocationsAreReadOnly(t *testing.T) {
	input := writeManifest(t)
	locations := filepath.Join(t.TempDir(), "locations.json")
	raw := []byte(`[{"world":"Free","channel":2}]`)
	if err := os.WriteFile(locations, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"-input", input, "-locations", locations, "-from", "2026-10-24"}, &output); err != nil {
		t.Fatal(err)
	}
	c, err := questdelivery.Parse([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := questdelivery.BuildCommonPublicationCohort(c, []questdelivery.PublicationLocation{{World: "Free", Channel: 2}})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.FixedZone("KST", 9*3600))
	want, _ := cohort.PublicDocument(from, from.Add(30*24*time.Hour))
	encoded, _ := json.Marshal(want)
	if !reflect.DeepEqual(readDocument(t, output.Bytes()), readDocument(t, encoded)) {
		t.Fatal("offline and HTTP common method differ")
	}
	if err := run([]string{"-input", input, "-locations", locations, "-output", locations}, &bytes.Buffer{}); err == nil {
		t.Fatal("locations overwritten")
	}
	assertInputUnchanged(t, locations, raw)
	if err := os.WriteFile(locations, []byte(`[{"world":"Free","channel":2},{"world":"Free","channel":3}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-input", input, "-locations", locations}, &bytes.Buffer{}); err == nil {
		t.Fatal("fake common extra location accepted")
	}
}
