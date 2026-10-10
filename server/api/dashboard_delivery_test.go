package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/mhfcid"
	"github.com/gorilla/mux"
)

func testDeliveryRequest() dashboardDeliveryRequest {
	return dashboardDeliveryRequest{RequestID: "de5a0000-1111-4111-8111-111111111111", CharacterID: 10,
		CharacterName: "테스트", CharacterCode: dashboardCharacterCode(10), ItemID: 7, ItemName: "회복약", Quantity: 199}
}

func TestDashboardDeliveryCharacterCodes(t *testing.T) {
	for _, id := range []uint32{1, 9, 10, 31, 32, 42, 9999, (1 << 30) - 1} {
		code := dashboardCharacterCode(id)
		if len(code) != 6 || mhfcid.ConvertCID(code) != id {
			t.Fatalf("ID %d -> %q -> %d", id, code, mhfcid.ConvertCID(code))
		}
	}
	if dashboardCharacterCode(0) != "" || dashboardCharacterCode(1<<30) != "" || dashboardCharacterCode(10) != "B11111" {
		t.Fatal("invalid or incorrectly formatted hunter code")
	}
}

func TestDashboardDeliveryCatalogParsingAndSearch(t *testing.T) {
	source := []byte(`"use strict"; var setItem = function (){return {
		"0007":["회복약",1,7,10,1,"0065","설명"],
		"0008":["회복약G",2,16,10,1,"0067","설명"],
		"0009":["숨김","-",0,0,0,"0000",""],
		"002A":["아름다운 꽃",5,0,99,1,"0001",""]
	};};`)
	items, err := parseDashboardDeliveryCatalog(source)
	if err != nil || len(items) != 3 || items[2].ID != 42 || items[2].Hex != "002A" {
		t.Fatalf("parse: %+v / %v", items, err)
	}
	for query, count := range map[string]int{"회복약": 2, "회 복 약": 2, "회복약 g": 1, "7": 1, "0x002a": 1, "002A": 1, "42": 1, "아름 다운 꽃": 1, "": 0, "숨김": 0} {
		if found := searchDashboardDeliveryItems(items, query); len(found) != count {
			t.Errorf("query %q: %+v, want %d", query, found, count)
		}
	}
	for _, source := range []string{`<html>Bad gateway</html>`, `var setItem=function(){return {"0007":["x",1]};window.evil();};`,
		`var setItem=function(){return {"0000":["x",1]};};`, `var setItem=function(){return {"0007":["x",alert(1)]};};`} {
		if _, err := parseDashboardDeliveryCatalog([]byte(source)); err == nil {
			t.Errorf("accepted unsafe/malformed source: %q", source)
		}
	}
}

func TestDashboardDeliveryCatalogRefreshAndFailure(t *testing.T) {
	calls := 0
	cache := dashboardDeliveryCatalog{fetch: func(context.Context) ([]dashboardDeliveryItem, error) {
		calls++
		if calls > 2 {
			return nil, errors.New("upstream down")
		}
		return []dashboardDeliveryItem{{ID: 7, Name: "회복약"}}, nil
	}}
	for _, refresh := range []bool{false, false, true} {
		if _, _, err := cache.load(context.Background(), refresh); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d", calls)
	}
	if _, _, err := cache.load(context.Background(), true); err == nil {
		t.Fatal("forced refresh should fail")
	}
	if _, _, err := cache.load(context.Background(), false); err == nil {
		t.Fatal("used previously fresh cache after forced refresh failed")
	}
	if calls != 4 {
		t.Fatalf("failed refresh did not invalidate cache: %d calls", calls)
	}
	cache.loadedAt = time.Now().Add(-3 * time.Minute)
	if _, _, err := cache.load(context.Background(), false); err == nil {
		t.Fatal("used expired cache after failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cache.load(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDashboardDeliveryRoutesRejectUnauthorized(t *testing.T) {
	server := newOperatorChatServer()
	server.deliveryCatalog.fetch = func(context.Context) ([]dashboardDeliveryItem, error) { t.Fatal("unauthorized fetch"); return nil, nil }
	router := mux.NewRouter()
	server.registerDashboardDeliveryRoutes(router)
	for _, route := range []struct{ method, path string }{{"GET", "/api/dashboard/delivery/characters?q=테스트"}, {"GET", "/api/dashboard/delivery/items?q=회복"}, {"POST", "/api/dashboard/delivery"}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
		if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", route.path, response.Code)
		}
	}
}

func TestDashboardDeliveryStrictRequests(t *testing.T) {
	server := newOperatorChatServer()
	requestJSON, _ := json.Marshal(testDeliveryRequest())
	for _, tc := range []struct {
		body, origin, contentType string
		status                    int
	}{
		{string(requestJSON), "", "application/json", 503},
		{string(requestJSON), "https://attacker.example", "application/json", 403},
		{string(requestJSON), "", "text/plain", 403},
		{`{}`, "", "application/json", 400},
		{string(requestJSON) + ` {}`, "", "application/json", 400},
		{strings.Replace(string(requestJSON), `"quantity":199`, `"quantity":-1`, 1), "", "application/json", 400},
		{strings.Replace(string(requestJSON), `"quantity":199`, `"quantity":1.5`, 1), "", "application/json", 400},
		{strings.Replace(string(requestJSON), `"itemId":7`, `"itemId":65536`, 1), "", "application/json", 400},
		{strings.Replace(string(requestJSON), `"quantity":199`, `"quantity":199,"unexpected":true`, 1), "", "application/json", 400},
	} {
		r := authorizeDashboard(httptest.NewRequest("POST", "/api/dashboard/delivery", strings.NewReader(tc.body)), server)
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set("Origin", tc.origin)
		response := httptest.NewRecorder()
		server.DashboardDelivery(response, r)
		if response.Code != tc.status {
			t.Errorf("%s -> %d, want %d: %s", tc.body, response.Code, tc.status, response.Body.String())
		}
	}
	invalid := testDeliveryRequest()
	invalid.CharacterID, invalid.CharacterCode = 1<<30, ""
	if validateDashboardDeliveryRequest(invalid) == nil {
		t.Fatal("unrepresentable character code accepted")
	}
}

func TestDashboardDeliveryItemsEndpoint(t *testing.T) {
	server := newOperatorChatServer()
	server.deliveryCatalog.fetch = func(context.Context) ([]dashboardDeliveryItem, error) {
		return []dashboardDeliveryItem{{ID: 7, Hex: "0007", Name: "회복약"}}, nil
	}
	r := authorizeDashboard(httptest.NewRequest("GET", "/api/dashboard/delivery/items?q=회복", nil), server)
	response := httptest.NewRecorder()
	server.DashboardDeliveryItems(response, r)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "회복약") || !strings.Contains(response.Body.String(), dashboardFeriasPageURL) {
		t.Fatal(response.Body.String())
	}
}

func TestDashboardDeliveryLiveFerias(t *testing.T) {
	if os.Getenv("DASHBOARD_DELIVERY_LIVE_FERIAS") != "1" {
		t.Skip("live Ferias check not requested")
	}
	items, err := fetchDashboardDeliveryCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(searchDashboardDeliveryItems(items, "회복약")) == 0 {
		t.Fatal("live Korean names were not decoded")
	}
	t.Logf("live Ferias items: %d", len(items))
}
