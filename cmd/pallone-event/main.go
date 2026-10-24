// pallone-event previews an explicit maintenance plan offline. Only -register
// connects to PostgreSQL; it calls the existing immutable-event repository and
// never edits configuration, installs files, restarts services or grants points.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"time"

	"erupe-ce/common/mhfpallone"
	"erupe-ce/server/channelserver"
	"github.com/jmoiron/sqlx"
)

type eventPlan struct {
	Kind          string    `json:"kind"`
	ID            int32     `json:"event_id"`
	Start         time.Time `json:"starts_at"`
	End           time.Time `json:"ends_at"`
	ClaimUntil    time.Time `json:"claim_until"`
	ProfileSHA256 string    `json:"profile_sha256"`
}

type publicationProfile struct {
	Kind    string                     `json:"kind"`
	Key     int32                      `json:"native_route_key"`
	Overall int32                      `json:"overall_ranking_key"`
	Route   mhfpallone.Route           `json:"route"`
	Rows    []mhfpallone.RouteDelivery `json:"delivery_rows"`
}

type registration func(context.Context, channelserver.PalloneEvent, []channelserver.PalloneRoute) error

func main() {
	if err := run(os.Args[1:], os.Stdout, registerEvent); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readInput(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, fmt.Errorf("input must be a regular file no larger than 16 MiB")
	}
	return os.ReadFile(path)
}

func run(args []string, stdout io.Writer, register registration) error {
	flags := flag.NewFlagSet("pallone-event", flag.ContinueOnError)
	planPath := flags.String("plan", "", "filled maintenance event plan JSON (RFC3339 times with explicit offset)")
	profilePath := flags.String("profile", "", "matching candidate/runtime pallone-client-profile.json")
	apply := flags.Bool("register", false, "explicitly register the plan using PGHOST/PGPORT/PGDATABASE/PGUSER; otherwise offline preview only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *planPath == "" || *profilePath == "" {
		return fmt.Errorf("-plan and -profile are required; default is offline preview")
	}
	planBytes, err := readInput(*planPath)
	if err != nil {
		return err
	}
	var p eventPlan
	decoder := json.NewDecoder(bytes.NewReader(planBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("event plan must contain one JSON object")
	}
	profileBytes, err := readInput(*profilePath)
	if err != nil {
		return err
	}
	if digest, err := hex.DecodeString(p.ProfileSHA256); err != nil || len(digest) != 32 ||
		p.ProfileSHA256 != fmt.Sprintf("%x", sha256.Sum256(profileBytes)) {
		return fmt.Errorf("profile fingerprint differs from the reviewed event plan")
	}
	var profile publicationProfile
	if err := json.Unmarshal(profileBytes, &profile); err != nil {
		return err
	}
	ids := []uint16{27438, 27479, 27348, 27365}
	if profile.Kind != "offline_route_001_client_candidate" || profile.Key <= 0 || profile.Key >= mhfpallone.MaxRoutes || profile.Overall != 0 ||
		profile.Route.Index != int(profile.Key) || profile.Route.Star != 0 || profile.Route.Number != 1 || profile.Route.NativeCode != 10001 ||
		!reflect.DeepEqual(profile.Route.StageQuestIDs, ids) || !reflect.DeepEqual(profile.Rows, []mhfpallone.RouteDelivery{{Star: 0, Ordinal: uint16(profile.Key)}}) {
		return fmt.Errorf("unsupported route-001 publication profile")
	}
	// Match repository precision before validating/previewing the window.
	p.Start = p.Start.Truncate(time.Microsecond)
	p.End = p.End.Truncate(time.Microsecond)
	p.ClaimUntil = p.ClaimUntil.Truncate(time.Microsecond)
	if p.Kind != "pallone_maintenance_event_plan" || p.ID <= 0 || p.Start.IsZero() || p.End.IsZero() || p.ClaimUntil.IsZero() ||
		!p.Start.Before(p.End) || !p.End.Before(p.ClaimUntil) || p.Start.Unix() < 0 || p.ClaimUntil.Unix() > math.MaxUint32 {
		return fmt.Errorf("fill a positive event_id and explicit starts_at < ends_at < claim_until; no schedule is inferred")
	}
	event := channelserver.PalloneEvent{ID: p.ID, Start: p.Start, End: p.End, ClaimUntil: p.ClaimUntil}
	routes := []channelserver.PalloneRoute{{Key: profile.Key, Star: 0, QuestIDs: ids}}
	if *apply {
		if register == nil {
			return fmt.Errorf("registration unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := register(ctx, event, routes); err != nil {
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"kind": "pallone_event_plan_result", "event_id": p.ID, "starts_at": p.Start.Format(time.RFC3339Nano),
		"ends_at": p.End.Format(time.RFC3339Nano), "claim_until": p.ClaimUntil.Format(time.RFC3339Nano),
		"route_key": profile.Key, "stage_quest_ids": ids, "database_accessed": *apply, "registered": *apply,
		"earth_configuration_modified": false, "assets_installed": false, "completion_receipt_capture_armed": false,
	})
}

func registerEvent(ctx context.Context, event channelserver.PalloneEvent, routes []channelserver.PalloneRoute) error {
	// Never infer the production database from OS-user defaults or a project
	// configuration. lib/pq reads credentials from the operator's environment
	// (PGPASSWORD/PGPASSFILE); the command never prints their values.
	for _, name := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER"} {
		if os.Getenv(name) == "" {
			return fmt.Errorf("explicit %s is required for -register", name)
		}
	}
	db, err := sqlx.Open("postgres", "connect_timeout=10")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	return channelserver.NewPalloneRepository(db).RegisterPalloneEvent(event, routes)
}
