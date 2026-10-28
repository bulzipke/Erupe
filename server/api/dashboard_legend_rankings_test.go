package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDashboardLegendUsageJSONAndPlacement(t *testing.T) {
	r := emptyDashboardRankings()
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"legendRastas":[]`) {
		t.Fatal(string(b))
	}
	r.LegendRastas = append(r.LegendRastas, LegendUsageRankEntry{ID: 1, Name: "플로라", Uses: 3})
	b, _ = json.Marshal(r)
	if !strings.Contains(string(b), `"uses":3`) {
		t.Fatal(string(b))
	}
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if strings.Index(s, `id="legend-rasta-ranking"`) < strings.Index(s, `id="weapon-most-ranking"`) {
		t.Fatal("legend ranking must follow weapons")
	}
	for _, text := range []string{"가장 많이 사용한 레전드 라스터", `renderLegendRanking("legend-rasta-ranking", rankings.legendRastas || [])`} {
		if !strings.Contains(s, text) {
			t.Error(text)
		}
	}
	if !strings.Contains(dashboardLegendUsageQuery, "usage_count DESC,legend_id ASC") {
		t.Fatal("unstable ordering")
	}
}
