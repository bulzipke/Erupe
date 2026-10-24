package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

func (s *APIServer) registerQuestScheduleRoutes(r *mux.Router) {
	r.HandleFunc("/v2/quests/schedule", s.QuestSchedule).Methods(http.MethodGet)
}

// QuestSchedule exposes only the validated manifest snapshot also used by the
// channel servers. It neither queries account data nor writes schedule state.
func (s *APIServer) QuestSchedule(w http.ResponseWriter, r *http.Request) {
	if s.erupeConfig == nil || !s.erupeConfig.QuestDelivery.Enabled || s.erupeConfig.QuestDeliveryCatalog == nil {
		http.NotFound(w, r)
		return
	}
	publication := s.erupeConfig.QuestDeliveryPublication
	if !publication.BoundTo(s.erupeConfig.QuestDeliveryCatalog) {
		http.Error(w, "common publication content is not prepared", http.StatusServiceUnavailable)
		return
	}
	days := 30
	if value := r.URL.Query().Get("days"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 31 {
			http.Error(w, "days must be between 1 and 31", http.StatusBadRequest)
			return
		}
		days = n
	}
	zone := time.FixedZone("KST", 9*60*60)
	now := time.Now().In(zone)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	if value := r.URL.Query().Get("from"); value != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, value, zone)
		if err != nil {
			http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		from = parsed
	}
	document, err := publication.PublicDocument(from, from.AddDate(0, 0, days))
	if err != nil {
		http.Error(w, "schedule could not be generated for this range", http.StatusUnprocessableEntity)
		return
	}
	data, err := json.Marshal(document)
	if err != nil {
		http.Error(w, "schedule encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Quest-Schedule-Version", document.Version)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
