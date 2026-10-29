package channelserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func palloneRotationPostgres(t *testing.T) *PalloneRepository {
	t.Helper()
	db := pallonePostgres(t)
	ddl, err := os.ReadFile(filepath.Join("..", "migrations", "sql", "0100_pallone_continuous_rotation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return NewPalloneRepository(db)
}

func TestPallonePostgresAutomaticIDsHistoryAndConcurrentRollover(t *testing.T) {
	r := palloneRotationPostgres(t)
	o := automaticPalloneOptions()
	o.StartAt = "2026-10-26T12:00:00+09:00"
	start, _ := time.Parse(time.RFC3339, o.StartAt)
	routes := []PalloneRoute{{Key: 7, Star: 0, QuestIDs: []uint16{27438, 27479, 27348, 27365}}}
	digest := strings.Repeat("a", 64)
	// Existing manually registered records are never adopted or overwritten.
	manual, _ := palloneRotationEvent(77, start.AddDate(0, 0, -42), o)
	if err := r.RegisterPalloneEvent(manual, routes); err != nil {
		t.Fatal(err)
	}
	first, err := r.EnsurePalloneSchedule(start, o, digest, routes)
	if err != nil || first.Current.ID != 78 || first.Next.ID != 79 || first.Previous != nil {
		t.Fatal("automatic ID allocation", first, err)
	}
	receipt := PalloneRunReceipt{EventID: 78, CharID: 100, RouteKey: 7, RunID: strings.Repeat("1", 32),
		StartedAt: start, CompletedAt: start.Add(time.Hour), FinalQuestID: 27365}
	if _, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 123}, receipt.CompletedAt); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan PalloneSchedule, 8)
	errorsOut := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := r.EnsurePalloneSchedule(first.Current.End, o, digest, routes)
			results <- v
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	for v := range results {
		if v.Current.ID != 79 || v.Next.ID != 80 || v.Previous == nil || v.Previous.ID != 78 {
			t.Fatal("channels disagreed at the boundary", v)
		}
	}
	old, err := r.GetPalloneScores(78, 100)
	if err != nil || len(old) != 1 || old[0].Total != 123 {
		t.Fatal("previous score history was reset", old, err)
	}
	fresh, err := r.GetPalloneScores(79, 100)
	if err != nil || len(fresh) != 0 {
		t.Fatal("previous scores leaked into new round", fresh, err)
	}
	if _, err := r.SettlePalloneResult(receipt, PalloneSubmission{BaseScore: 123}, first.Current.End.Add(time.Hour)); err != nil {
		t.Fatal("old qualified receipt was not idempotent after rollover", err)
	}
	// A restart far after multiple boundaries resumes the original grid and
	// increases IDs; it must not invent a played previous round in the gap.
	later, err := r.EnsurePalloneSchedule(start.AddDate(0, 0, 84), o, digest, routes)
	if err != nil || later.Current.ID != 82 || later.Next.ID != 83 || later.Previous != nil || !later.Current.Start.Equal(start.AddDate(0, 0, 84)) {
		t.Fatal("downtime changed the anchor/IDs", later, err)
	}
	preserved, err := r.GetPalloneEvent(77)
	if err != nil || preserved == nil || !preserved.Start.Equal(manual.Start) {
		t.Fatal("manual history was changed")
	}
}

func TestPallonePostgresRotationRejectsChangedPolicyAndPublishedRoutes(t *testing.T) {
	r := palloneRotationPostgres(t)
	o := automaticPalloneOptions()
	o.StartAt = "2026-10-26T12:00:00+09:00"
	start, _ := time.Parse(time.RFC3339, o.StartAt)
	routes := []PalloneRoute{{Key: 7, Star: 0, QuestIDs: []uint16{27438}}}
	digest := strings.Repeat("b", 64)
	first, err := r.EnsurePalloneSchedule(start.Add(-time.Hour), o, digest, routes)
	if err != nil || !first.Current.Start.Equal(start) || first.Previous != nil {
		t.Fatal("future activation", first, err)
	}
	for _, kind := range []string{"durations", "anchor", "profile", "routes"} {
		changed, hash, selected := o, digest, routes
		switch kind {
		case "durations":
			changed.ClaimDays = 7
		case "anchor":
			changed.StartAt = start.AddDate(0, 0, 7).Format(time.RFC3339)
		case "profile":
			hash = strings.Repeat("c", 64)
		case "routes":
			selected = []PalloneRoute{{Key: 7, Star: 0, QuestIDs: []uint16{27479}}}
		}
		if _, err := r.EnsurePalloneSchedule(start, changed, hash, selected); err == nil {
			t.Fatal("silently changed existing policy", kind)
		}
	}
	// The prepublished next round is immutable even before its start.
	bad := first.Next
	bad.ClaimUntil = bad.ClaimUntil.Add(time.Hour)
	if err := r.RegisterPalloneEvent(bad, routes); !errors.Is(err, ErrPalloneConflict) {
		t.Fatal("changed a published future deadline", err)
	}
	var count int
	if err := r.db.Get(&count, `SELECT COUNT(*) FROM pallone_events`); err != nil || count != 2 {
		t.Fatal("failed policy checks partially created/reset rounds", count, err)
	}
}
