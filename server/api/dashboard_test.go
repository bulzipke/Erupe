package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cfg "erupe-ce/config"
)

// TestDashboardStatsJSON_NoDB verifies the stats endpoint returns valid JSON
// with safe zero values when no database is configured.
func TestDashboardStatsJSON_NoDB(t *testing.T) {
	logger := NewTestLogger(t)
	defer func() { _ = logger.Sync() }()

	server := &APIServer{
		logger:      logger,
		erupeConfig: NewTestConfig(),
		startTime:   time.Now().Add(-5 * time.Minute),
		// db intentionally nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	rec := httptest.NewRecorder()

	server.DashboardStatsJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Expected Content-Type application/json, got %q", ct)
	}

	var stats DashboardStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify required fields are present and have expected zero-DB values.
	if stats.ServerVersion == "" {
		t.Error("Expected non-empty ServerVersion")
	}
	if stats.Uptime == "" || stats.Uptime == "확인 불가" {
		// startTime is set so uptime should be computed.
		t.Errorf("Expected computed uptime, got %q", stats.Uptime)
	}
	if stats.TotalAccounts != 0 {
		t.Errorf("Expected TotalAccounts=0 without DB, got %d", stats.TotalAccounts)
	}
	if stats.TotalCharacters != 0 {
		t.Errorf("Expected TotalCharacters=0 without DB, got %d", stats.TotalCharacters)
	}
	if stats.OnlinePlayers != 0 {
		t.Errorf("Expected OnlinePlayers=0 without DB, got %d", stats.OnlinePlayers)
	}
	if stats.DatabaseOK {
		t.Error("Expected DatabaseOK=false without DB")
	}
	if len(stats.Channels) != 0 {
		t.Errorf("Expected empty Channels without DB, got %v", stats.Channels)
	}
	if len(stats.OnlineCharacters) != 0 {
		t.Errorf("Expected empty OnlineCharacters without DB, got %v", stats.OnlineCharacters)
	}
	if stats.Rankings.Hunters == nil || stats.Rankings.MonsterHunts == nil || stats.Rankings.Guilds == nil {
		t.Error("Expected ranking arrays to be initialized without DB")
	}
}

// TestDashboardStatsJSON_UptimeUnknown verifies the Korean unavailable label when startTime is zero.
func TestDashboardStatsJSON_UptimeUnknown(t *testing.T) {
	logger := NewTestLogger(t)
	defer func() { _ = logger.Sync() }()

	server := &APIServer{
		logger:      logger,
		erupeConfig: NewTestConfig(),
		// startTime is zero value
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	rec := httptest.NewRecorder()

	server.DashboardStatsJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var stats DashboardStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if stats.Uptime != "확인 불가" {
		t.Errorf("Expected Uptime='확인 불가' for zero startTime, got %q", stats.Uptime)
	}
}

// TestDashboardStatsJSON_JSONShape validates every field of the DashboardStats payload.
func TestDashboardStatsJSON_JSONShape(t *testing.T) {
	logger := NewTestLogger(t)
	defer func() { _ = logger.Sync() }()

	server := &APIServer{
		logger:      logger,
		erupeConfig: NewTestConfig(),
		startTime:   time.Now(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	rec := httptest.NewRecorder()

	server.DashboardStatsJSON(rec, req)

	// Decode into a raw map so we can check key presence independent of type.
	var raw map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode response as raw map: %v", err)
	}

	requiredKeys := []string{
		"uptime", "serverVersion", "clientMode",
		"onlinePlayers", "totalAccounts", "totalCharacters",
		"channels", "onlineCharacters", "rankings", "databaseOK",
	}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("Missing required JSON key %q", key)
		}
	}
	rankings, ok := raw["rankings"].(map[string]interface{})
	if !ok {
		t.Fatalf("rankings has unexpected JSON type %T", raw["rankings"])
	}
	if _, ok := rankings["playtime"]; !ok {
		t.Error("Missing rankings.playtime JSON key")
	}
}

func TestDashboardConfiguredChannelsExcludesDisabledAndStaleChannels(t *testing.T) {
	enabled := true
	disabled := false
	server := &APIServer{erupeConfig: &cfg.Config{
		Entrance: cfg.Entrance{Entries: []cfg.EntranceServerInfo{
			{
				Name: "Newbie",
				Channels: []cfg.EntranceChannelInfo{
					{Port: 53313, MaxPlayers: 100, Enabled: &enabled},
					{Port: 54002, MaxPlayers: 100, Enabled: &disabled},
				},
			},
		}},
	}}

	channels := server.dashboardConfiguredChannels()
	if len(channels) != 1 {
		t.Fatalf("configured channel count = %d, want 1", len(channels))
	}
	if got := channels[4112]; got.port != 53313 || got.maxPlayers != 100 {
		t.Fatalf("configured channel = %+v, want port 53313 and maxPlayers 100", got)
	}
	if _, ok := channels[4113]; ok {
		t.Error("disabled second Newbie channel should be excluded")
	}
	if _, ok := channels[4368]; ok {
		t.Error("stale Normal channel should not be synthesized from the database")
	}
}

func TestDashboardRendersWorldStatusPage(t *testing.T) {
	logger := NewTestLogger(t)
	defer func() { _ = logger.Sync() }()

	server := &APIServer{logger: logger}
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	server.Dashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("Expected HTML content type, got %q", contentType)
	}
	body := rec.Body.String()
	for _, marker := range []string{
		"Erupe 월드 현황",
		"접속 중인 헌터",
		"위치",
		"월드 채팅",
		"대형 몬스터 토벌",
		"플레이 타임",
		"/api/dashboard/stats",
		"/api/dashboard/chat",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("Dashboard HTML missing %q", marker)
		}
	}
	channelIndex := strings.Index(body, "채널 상태")
	onlineIndex := strings.Index(body, "접속 중인 헌터")
	chatIndex := strings.Index(body, "월드 채팅")
	if channelIndex == -1 || onlineIndex == -1 || chatIndex == -1 || !(channelIndex < onlineIndex && onlineIndex < chatIndex) {
		t.Errorf("Dashboard sections are not ordered channel, online, chat")
	}
	hunterRankIndex := strings.Index(body, "헌터 랭크")
	monsterRankIndex := strings.Index(body, "대형 몬스터 토벌")
	playtimeRankIndex := strings.Index(body, "플레이 타임")
	guildRankIndex := strings.Index(body, "수렵단 RP")
	if hunterRankIndex == -1 || monsterRankIndex == -1 || playtimeRankIndex == -1 || guildRankIndex == -1 ||
		!(hunterRankIndex < monsterRankIndex && monsterRankIndex < playtimeRankIndex && playtimeRankIndex < guildRankIndex) {
		t.Errorf("Dashboard rankings are not ordered hunter rank, monster hunts, playtime, guild RP")
	}
}

func TestEmptyDashboardRankingsIncludesPlaytime(t *testing.T) {
	rankings := emptyDashboardRankings()
	if rankings.Playtime == nil {
		t.Fatal("playtime ranking must encode as an empty array, not null")
	}
}

func TestDashboardLocationForStage(t *testing.T) {
	tests := []struct {
		stageID string
		want    string
	}{
		{stageID: "", want: "접속 중"},
		{stageID: "sl1Ns200p0a0u0", want: "메제포르타"},
		{stageID: "sl1Ns211p0a0u0", want: "라스타 주점"},
		{stageID: "sl1Ns260p0a0u0", want: "파로네 캐러밴"},
		{stageID: "sl2Ns379p0a0u0", want: "기도의 샘"},
		{stageID: "sl1Ns462p0a0u0", want: "메제페스"},
		{stageID: "sl2Ls210p0a0u0", want: "공개 주점"},
		{stageID: "sl2Qs999p0a0u42", want: "퀘스트 중"},
		{stageID: "sl2Gs999p0a0u42", want: "수렵단"},
		{stageID: "sl2Ms999p0a0u42", want: "마이하우스"},
		{stageID: "sl2Ls999p0a0u42", want: "로비"},
		{stageID: "sl2Ns999p0a0u42", want: "로비/시설"},
		{stageID: "invalid", want: "기타 장소"},
	}

	for _, tt := range tests {
		t.Run(tt.stageID, func(t *testing.T) {
			if got := dashboardLocationForStage(tt.stageID); got != tt.want {
				t.Errorf("dashboardLocationForStage(%q) = %q, want %q", tt.stageID, got, tt.want)
			}
		})
	}
}

func TestDashboardStageSnapshot(t *testing.T) {
	server := &APIServer{}
	server.SetDashboardStageProvider(func() map[uint32]string {
		return map[uint32]string{42: "sl2Qs999p0a0u42"}
	})

	got := server.dashboardStageSnapshot()
	if got[42] != "sl2Qs999p0a0u42" {
		t.Fatalf("dashboard stage snapshot = %v", got)
	}
}

// TestFormatDuration covers the human-readable duration formatter.
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "10초"},
		{90 * time.Second, "1분 30초"},
		{2*time.Hour + 15*time.Minute + 5*time.Second, "2시간 15분 5초"},
		{25*time.Hour + 3*time.Minute + 0*time.Second, "1일 1시간 3분 0초"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
