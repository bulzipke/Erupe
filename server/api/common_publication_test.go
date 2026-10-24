package api

import (
	"encoding/json"
	"erupe-ce/common/questdelivery"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestCommonPublicationHTTPIsExactCohortDocument(t *testing.T) {
	s := scheduleTestServer(t)
	w := httptest.NewRecorder()
	s.QuestSchedule(w, httptest.NewRequest(http.MethodGet, "/?from=2026-10-24&days=30", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var got questdelivery.ScheduleDocument
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.FixedZone("KST", 9*3600))
	want, err := s.erupeConfig.QuestDeliveryPublication.PublicDocument(from, from.Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(want)
	var normalized questdelivery.ScheduleDocument
	_ = json.Unmarshal(raw, &normalized)
	if !reflect.DeepEqual(got, normalized) || got.PublicationScope != questdelivery.CommonPublicationScope {
		t.Fatal("API differs from common public method")
	}
	s.erupeConfig.QuestDeliveryPublication = nil
	w = httptest.NewRecorder()
	s.QuestSchedule(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("unprepared common content silently exposed legacy", w.Code)
	}
}
