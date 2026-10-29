package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/server/channelserver"
	"github.com/gorilla/mux"
)

type eventScheduleFixture struct {
	*mockAPIEventRepo
	snapshot recurringEventSnapshot
	err      error
}

func (r *eventScheduleFixture) RecurringEventSnapshot(context.Context) (recurringEventSnapshot, error) {
	return r.snapshot, r.err
}

func eventScheduleTestServer() (*APIServer, *eventScheduleFixture) {
	t := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	r := &eventScheduleFixture{mockAPIEventRepo: &mockAPIEventRepo{}, snapshot: recurringEventSnapshot{
		Festa: channelserver.FestaEvent{ID: 12, StartTime: uint32(t("2026-10-19T12:00:00+09:00").Unix()), Continuous: true},
		Diva:  channelserver.DivaEvent{ID: 13, StartTime: uint32(t("2026-10-26T12:00:00+09:00").Unix()), Continuous: true},
		Tower: channelserver.TowerEvent{ID: 1, Start: t("2026-10-26T12:00:00+09:00"), End: t("2026-11-16T12:00:00+09:00"), ClaimUntil: t("2026-12-07T12:00:00+09:00")},
	}}
	c := &cfg.Config{}
	c.DebugOptions.FestaOverride, c.DebugOptions.DivaOverride = -1, -1
	c.TowerRotation.Enabled = true
	return &APIServer{eventRepo: r, erupeConfig: c}, r
}

func TestEventScheduleUsesPersistedIDsAndGameBoundaries(t *testing.T) {
	s, _ := eventScheduleTestServer()
	w := httptest.NewRecorder()
	s.EventSchedule(w, httptest.NewRequest(http.MethodGet, "/v2/events/schedule", nil))
	var d eventScheduleDocument
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil {
		t.Fatalf("bad schedule: %d %s", w.Code, w.Body.String())
	}
	if d.Timezone != "Asia/Seoul" || d.Version != "event-schedule-v1" || len(d.Events) != 3 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad envelope: %+v", d)
	}
	if d.Events[0].Round != 12 || d.Events[1].Round != 1 || d.Events[2].Round != 13 {
		t.Fatal("rounds were inferred instead of using lifecycle IDs")
	}
	if got := d.Events[0].Phases[3].Start.Format(time.RFC3339); got != "2026-11-02T13:00:00+09:00" {
		t.Fatal(got)
	}
	if got := d.Events[0].Phases[3].End.Format(time.RFC3339); got != "2026-11-09T12:00:00+09:00" {
		t.Fatal(got)
	}
	if got := d.Events[2].Phases[1].Start.Format(time.RFC3339); got != "2026-11-02T12:00:00+09:00" {
		t.Fatal(got)
	}
	if got := d.Events[1].ClaimUntil.Format(time.RFC3339); got != "2026-12-07T12:00:00+09:00" {
		t.Fatal(got)
	}
}

func TestEventScheduleRejectsUnavailableAndForcedSchedules(t *testing.T) {
	s, r := eventScheduleTestServer()
	r.err = errors.New("database failure")
	w := httptest.NewRecorder()
	s.EventSchedule(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	r.err = nil
	s.erupeConfig.DebugOptions.FestaOverride = 2
	s.erupeConfig.TowerRotation.Enabled = false
	w = httptest.NewRecorder()
	s.EventSchedule(w, httptest.NewRequest("GET", "/", nil))
	var d eventScheduleDocument
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Events[0].Round != 0 || len(d.Events[0].Phases) != 0 || d.Events[1].Round != 0 {
		t.Fatal("old normal dates exposed as a forced schedule")
	}
	router := mux.NewRouter()
	s.registerEventScheduleRoutes(router)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/v2/events/schedule", nil))
	if w.Code != 405 {
		t.Fatal("public schedule must be read-only GET")
	}
}
