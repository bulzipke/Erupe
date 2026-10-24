package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
	cfg "erupe-ce/config"
	"github.com/gorilla/mux"
)

func scheduleTestServer(t *testing.T) *APIServer {
	t.Helper()
	c, err := questdelivery.Parse([]byte(`{"version":"calendar-v1","profiles":[{"id":"a","worlds":["Brave"],"channels":[1]}],"schedules":[{"id":"rotation","starts_at":"2026-10-20T12:00:00+09:00","active_hours":72,"repeat_hours":96}],"quests":[{"quest_id":53140,"profiles":["a"],"schedule":"rotation","requirements":["original ticket and rank rules apply"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := questdelivery.BuildCommonPublicationCohort(c, []questdelivery.PublicationLocation{{World: "Brave", Channel: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return &APIServer{erupeConfig: &cfg.Config{QuestDelivery: cfg.QuestDeliveryOptions{Enabled: true}, QuestDeliveryCatalog: c, QuestDeliveryPublication: publication}}
}

func TestQuestScheduleUsesSharedManifestAndKST(t *testing.T) {
	s := scheduleTestServer(t)
	router := mux.NewRouter()
	s.registerQuestScheduleRoutes(router)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/quests/schedule?from=2026-10-23&days=30", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got questdelivery.ScheduleDocument
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "calendar-v1" || got.Timezone != "Asia/Seoul" || !got.OriginalEntryConditionsApply {
		t.Fatalf("wrong envelope: %+v", got)
	}
	if len(got.Occurrences) == 0 || !got.Occurrences[0].StartsAt.Before(got.From) {
		t.Fatal("window already open before today was omitted")
	}
	if len(got.Occurrences[0].Requirements) != 1 || got.Occurrences[0].Requirements[0] != "original ticket and rank rules apply" {
		t.Fatal("original conditions lost")
	}
	_, offset := got.From.Zone()
	if offset != 9*3600 {
		t.Fatal("date is not KST")
	}
	if !got.Until.Equal(got.From.AddDate(0, 0, 30)) || w.Header().Get("X-Quest-Schedule-Version") != got.Version {
		t.Fatal("range/version mismatch")
	}
	managed, allowed := s.erupeConfig.QuestDeliveryCatalog.Allows(53140, "Brave", 1, time.Date(2026, 10, 23, 2, 0, 0, 0, time.UTC))
	if !managed || !allowed {
		t.Fatal("HTTP calendar and runtime schedule diverged")
	}
}

func TestQuestScheduleBoundsAndOptIn(t *testing.T) {
	s := scheduleTestServer(t)
	for _, query := range []string{"days=0", "days=32", "days=abc", "from=2026-02-30", "from=not-a-date"} {
		w := httptest.NewRecorder()
		s.QuestSchedule(w, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", query, w.Code)
		}
	}
	s.erupeConfig.QuestDelivery.Enabled = false
	w := httptest.NewRecorder()
	s.QuestSchedule(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("disabled feature exposed an endpoint")
	}
	router := mux.NewRouter()
	s.registerQuestScheduleRoutes(router)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v2/quests/schedule", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("schedule must be read-only GET")
	}
}
