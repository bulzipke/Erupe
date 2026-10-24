package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"erupe-ce/common/mhfpallone"
	"erupe-ce/server/channelserver"
)

func planFixture(t *testing.T) (string, string, eventPlan) {
	t.Helper()
	dir := t.TempDir()
	profile := publicationProfile{Kind: "offline_route_001_client_candidate", Key: 7,
		Route: mhfpallone.Route{Index: 7, Number: 1, NativeCode: 10001, StageQuestIDs: []uint16{27438, 27479, 27348, 27365}},
		Rows:  []mhfpallone.RouteDelivery{{Star: 0, Ordinal: 7}}}
	b, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	profilePath, planPath := filepath.Join(dir, "profile.json"), filepath.Join(dir, "plan.json")
	if err := os.WriteFile(profilePath, b, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 1, 1, 9, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	p := eventPlan{Kind: "pallone_maintenance_event_plan", ID: 77, Start: start, End: start.Add(time.Hour), ClaimUntil: start.Add(2 * time.Hour), ProfileSHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
	writePlan(t, planPath, p)
	return planPath, profilePath, p
}

func writePlan(t *testing.T, path string, p eventPlan) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEventPlanDefaultsToOfflineAndRegistersOnlyExplicitly(t *testing.T) {
	plan, profile, _ := planFixture(t)
	calls := 0
	register := func(_ context.Context, e channelserver.PalloneEvent, r []channelserver.PalloneRoute) error {
		calls++
		if e.ID != 77 || len(r) != 1 || r[0].Key != 7 || len(r[0].QuestIDs) != 4 {
			t.Fatal("changed reviewed event plan")
		}
		return nil
	}
	for _, apply := range []bool{false, true} {
		var stdout bytes.Buffer
		args := []string{"-plan", plan, "-profile", profile}
		if apply {
			args = append(args, "-register")
		}
		if err := run(args, &stdout, register); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["registered"] != apply || result["earth_configuration_modified"] != false || result["assets_installed"] != false {
			t.Fatal(result)
		}
		if !apply && calls != 0 {
			t.Fatal("preview connected to a database")
		}
	}
	if calls != 1 {
		t.Fatal("did not register exactly once")
	}
}

func TestEventPlanRefusesUnscheduledChangedOrInvalidPlansBeforeRegistration(t *testing.T) {
	for _, kind := range []string{"no schedule", "nonpositive ID", "inverted window", "no claim window", "submicrosecond claim", "changed fingerprint", "wrong profile"} {
		t.Run(kind, func(t *testing.T) {
			plan, profile, p := planFixture(t)
			switch kind {
			case "no schedule":
				p.Start = time.Time{}
			case "nonpositive ID":
				p.ID = 0
			case "inverted window":
				p.End = p.Start
			case "no claim window":
				p.ClaimUntil = p.End
			case "submicrosecond claim":
				p.ClaimUntil = p.End.Add(time.Nanosecond)
			case "changed fingerprint":
				p.ProfileSHA256 = "00"
			case "wrong profile":
				b := []byte(`{"kind":"wrong"}`)
				if err := os.WriteFile(profile, b, 0600); err != nil {
					t.Fatal(err)
				}
				p.ProfileSHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
			}
			writePlan(t, plan, p)
			called := false
			err := run([]string{"-plan", plan, "-profile", profile, "-register"}, &bytes.Buffer{}, func(context.Context, channelserver.PalloneEvent, []channelserver.PalloneRoute) error {
				called = true
				return nil
			})
			if err == nil || called {
				t.Fatal("registered invalid/unreviewed plan")
			}
		})
	}
}

func TestEventRegistrationRefusesImplicitDatabaseTarget(t *testing.T) {
	for _, name := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER"} {
		t.Setenv(name, "")
	}
	if err := registerEvent(context.Background(), channelserver.PalloneEvent{}, nil); err == nil {
		t.Fatal("connected using implicit database defaults")
	}
}
