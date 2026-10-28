package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"erupe-ce/server/channelserver"
	"github.com/gorilla/mux"
)

type recurringEventSnapshot struct {
	Festa channelserver.FestaEvent
	Diva  channelserver.DivaEvent
	Tower channelserver.TowerEvent
}

type recurringEventScheduleRepo interface {
	RecurringEventSnapshot(context.Context) (recurringEventSnapshot, error)
}

type eventSchedulePhase struct {
	Key   string    `json:"key"`
	Start time.Time `json:"starts_at"`
	End   time.Time `json:"ends_at"`
}

type eventScheduleEntry struct {
	Kind       string               `json:"kind"`
	Round      uint32               `json:"round,omitempty"`
	Mode       string               `json:"mode"`
	Phases     []eventSchedulePhase `json:"phases"`
	ClaimUntil *time.Time           `json:"claim_until,omitempty"`
}

type eventScheduleDocument struct {
	Version     string               `json:"version"`
	Timezone    string               `json:"timezone"`
	GeneratedAt time.Time            `json:"generated_at"`
	Events      []eventScheduleEntry `json:"events"`
}

func (s *APIServer) registerEventScheduleRoutes(r *mux.Router) {
	r.HandleFunc("/v2/events/schedule", s.EventSchedule).Methods(http.MethodGet)
}

// EventSchedule publishes only existing lifecycle IDs and dates. It never
// creates a round or predicts an event ID from the calendar after downtime.
func (s *APIServer) EventSchedule(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.eventRepo.(recurringEventScheduleRepo)
	if !ok || s.erupeConfig == nil {
		http.Error(w, "event schedule unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := repo.RecurringEventSnapshot(ctx)
	if err != nil {
		http.Error(w, "event schedule unavailable", http.StatusServiceUnavailable)
		return
	}
	zone := time.FixedZone("KST", 9*60*60)
	stamp := func(t uint32) time.Time { return time.Unix(int64(t), 0).In(zone) }
	phase := func(key string, a, b uint32) eventSchedulePhase { return eventSchedulePhase{key, stamp(a), stamp(b)} }
	festa := eventScheduleEntry{Kind: "festa", Mode: "unavailable", Phases: []eventSchedulePhase{}}
	diva := eventScheduleEntry{Kind: "diva", Mode: "unavailable", Phases: []eventSchedulePhase{}}
	tower := eventScheduleEntry{Kind: "tower", Mode: "unavailable", Phases: []eventSchedulePhase{}}
	if s.erupeConfig.DebugOptions.FestaOverride == -1 && snapshot.Festa.ID != 0 {
		ts := snapshot.Festa.ScheduleTimestamps()
		festa.Round, festa.Mode = snapshot.Festa.ID, "automatic"
		festa.Phases = []eventSchedulePhase{phase("registration", ts[0], ts[1]), phase("souls", ts[1], ts[2]), phase("tally", ts[2], ts[3]), phase("rewards", ts[3], ts[4])}
	}
	if s.erupeConfig.DebugOptions.DivaOverride == -1 && snapshot.Diva.ID != 0 {
		ts := snapshot.Diva.ScheduleTimestamps()
		diva.Round, diva.Mode = snapshot.Diva.ID, "automatic"
		diva.Phases = []eventSchedulePhase{phase("prayer", ts[0], ts[1]), phase("battle", ts[2], ts[3]), phase("welcome", ts[4], ts[5])}
	}
	if s.erupeConfig.TowerRotation.Enabled && snapshot.Tower.ID > 0 {
		e := snapshot.Tower
		tower.Round, tower.Mode = uint32(e.ID), "automatic"
		tower.Phases = []eventSchedulePhase{{"exploration", e.Start.In(zone), e.End.In(zone)}}
		claim := e.ClaimUntil.In(zone)
		tower.ClaimUntil = &claim
	}
	// A forced phase is not an automatic schedule, even when an old normal
	// lifecycle row still exists. Do not display that row as the active event.
	if s.erupeConfig.DebugOptions.FestaOverride >= 0 {
		festa.Mode = "manual"
	}
	if s.erupeConfig.DebugOptions.DivaOverride >= 0 {
		diva.Mode = "manual"
	}
	if !s.erupeConfig.TowerRotation.Enabled {
		tower.Mode = "manual"
	}
	document := eventScheduleDocument{"event-schedule-v1", "Asia/Seoul", time.Now().In(zone), []eventScheduleEntry{festa, tower, diva}}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(document)
}
