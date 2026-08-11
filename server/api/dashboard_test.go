package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/mhfmon"
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
	authorizeDashboard(req, server)
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
	if stats.Rankings.MonsterHunts == nil || stats.Rankings.Guilds == nil ||
		stats.Rankings.Playtime == nil || stats.Rankings.MonsterTimes == nil {
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
	authorizeDashboard(req, server)
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
	authorizeDashboard(req, server)
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
	if _, ok := rankings["monsterTimes"]; !ok {
		t.Error("Missing rankings.monsterTimes JSON key")
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
		"접속 중인 헌터",
		"위치",
		"월드 채팅",
		"대형 몬스터 토벌",
		"대형 몬스터별 최단 토벌",
		"토벌 시간은 개인 기록을 원칙으로 하며, 라비엔테 맹광기는 파티 기록도 집계합니다.",
		"초난관·무쌍·극한 · 상급 지천 · 천이종은 기본 표시",
		"기타 기록은 접어서 표시합니다.",
		"기타 (펼침)",
		"기타 (접기)",
		"몬스터별 상위 10명 중 3위 이후는 카드 안에서 세로로 스크롤",
		"id=\"monster-time-zenith\"",
		"id=\"monster-time-challenge\"",
		"id=\"monster-time-upper-shiten\"",
		"id=\"monster-time-other-details\"",
		"id=\"monster-time-other\"",
		"detailsOpen",
		"scrollState",
		"캐릭터이름",
		"퀘스트명",
		"플레이 타임",
		"/api/dashboard/stats",
		"/api/dashboard/chat",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("Dashboard HTML missing %q", marker)
		}
	}
	for _, removed := range []string{
		`class="masthead"`,
		`class="metrics"`,
		`id="version"`,
		`id="status-dot"`,
		`id="status-text"`,
		`id="online-players"`,
		`id="channel-count"`,
		`id="total-characters"`,
		`id="uptime"`,
	} {
		if strings.Contains(body, removed) {
			t.Errorf("Dashboard still contains removed summary element %q", removed)
		}
	}
	if strings.Contains(body, "monster-time-select") {
		t.Error("Monster time ranking should render every monster without a selector")
	}
	for _, marker := range []string{
		".monster-time-table-scroll{width:100%;overflow-x:auto;",
		".monster-time-table thead tr,.monster-time-table tbody tr{display:grid;",
		".monster-time-table tbody{display:block;max-height:102px;overflow-x:hidden;overflow-y:auto;",
		`var groupNames = ["challenge", "upper_shiten", "zenith", "other"];`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("Monster time table scrolling CSS missing %q", marker)
		}
	}
	if strings.Contains(body, "<details class=\"monster-time-details\" id=\"monster-time-other-details\" open") {
		t.Error("Other G-rank records should be collapsed by default")
	}
	zenithIndex := strings.Index(body, "id=\"monster-time-zenith\"")
	challengeIndex := strings.Index(body, "id=\"monster-time-challenge\"")
	upperShitenIndex := strings.Index(body, "id=\"monster-time-upper-shiten\"")
	otherIndex := strings.Index(body, "id=\"monster-time-other-details\"")
	if zenithIndex == -1 || challengeIndex == -1 || upperShitenIndex == -1 || otherIndex == -1 ||
		!(challengeIndex < upperShitenIndex && upperShitenIndex < zenithIndex && zenithIndex < otherIndex) {
		t.Error("Monster time groups are not ordered challenge, upper Shiten, Zenith, other")
	}
	if strings.Contains(body, "monsterIconById") {
		t.Error("Dashboard still contains the removed legacy icon mapping")
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

func TestDashboardMonsterIcon(t *testing.T) {
	server := &APIServer{}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/assets/namu_ad1ae05706f8dc9d.webp", nil)
	rec := httptest.NewRecorder()

	server.DashboardMonsterIcon(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "image/webp" {
		t.Fatalf("Expected WebP content type, got %q", contentType)
	}
	want, err := dashboardMonsterIconFS.ReadFile("dashboard_assets/namu_ad1ae05706f8dc9d.webp")
	if err != nil {
		t.Fatalf("Read embedded icon: %v", err)
	}
	if rec.Body.Len() != len(want) {
		t.Fatalf("Icon body length = %d, want %d", rec.Body.Len(), len(want))
	}
	if cacheControl := rec.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "immutable") {
		t.Fatalf("Expected immutable cache header, got %q", cacheControl)
	}
}

func TestDashboardMonsterIconRejectsUnknownAsset(t *testing.T) {
	server := &APIServer{}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/assets/namu_missing.webp", nil)
	rec := httptest.NewRecorder()

	server.DashboardMonsterIcon(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestDashboardMonsterIconMappingUsesVariantAndPlaceholderArtwork(t *testing.T) {
	tests := []struct {
		name        string
		monsterID   int
		variantKind string
		want        string
	}{
		{name: "normal Khezu", monsterID: 15, variantKind: "normal", want: "namu_78af28ce436f64c9.webp"},
		{name: "Zenith Khezu", monsterID: 15, variantKind: "zenith", want: "namu_14e64396175bd611.webp"},
		{name: "intentional question mark", monsterID: 2, variantKind: "normal", want: "namu_3e900d35cbd184fb.webp"},
		{name: "phantom form reuses base icon", monsterID: 53, variantKind: "phantom_red_rajang", want: dashboardMonsterNormalIcons[53]},
		{name: "violent Raviente uses distinct icon", monsterID: 93, variantKind: "violent_raviente", want: "namu_517a8fb942ea0258.webp"},
		{name: "extreme Zinogre reuses base icon", monsterID: 146, variantKind: "extreme_zinogre", want: dashboardMonsterNormalIcons[146]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dashboardMonsterIcon(tt.monsterID, tt.variantKind); got != tt.want {
				t.Fatalf("dashboardMonsterIcon(%d, %q) = %q, want %q", tt.monsterID, tt.variantKind, got, tt.want)
			}
		})
	}
}

func TestDashboardMonsterNormalIconsCoverAllRankedLargeMonsters(t *testing.T) {
	for monsterID, monster := range mhfmon.Monsters {
		if !monster.Large {
			continue
		}
		icon := dashboardMonsterIcon(monsterID, "normal")
		if icon == "" {
			t.Errorf("large monster %d (%s) has no normal icon", monsterID, monster.Name)
			continue
		}
		if _, err := dashboardMonsterIconFS.ReadFile("dashboard_assets/" + icon); err != nil {
			t.Errorf("large monster %d (%s) icon %q is not embedded: %v", monsterID, monster.Name, icon, err)
		}
	}
}

func TestDashboardMonsterDisplayNameSeparatesForms(t *testing.T) {
	if got := dashboardMonsterDisplayName(15, "zenith"); got != "천이종 푸루푸루" {
		t.Fatalf("Zenith display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(27, "hardcore"); got != "특이개체 도스람포스" {
		t.Fatalf("Hardcore display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(95, "phantom_doragyurosu"); got != "환상의 드라규로스" {
		t.Fatalf("Phantom display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(93, "violent_raviente"); got != "라비엔테 광폭기" {
		t.Fatalf("Violent Raviente display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(146, "extreme_zinogre"); got != "극도로 울부짖는 진오우거" {
		t.Fatalf("Extreme Zinogre display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(167, "hardcore"); got != "극도로 교만하는 두레무디라" {
		t.Fatalf("Raw-ID extreme Duremudira display name = %q", got)
	}
	if got := dashboardMonsterDisplayName(172, "normal"); got != "극도로 엄습하는 보가바도름" {
		t.Fatalf("Raw-ID extreme Bogabadorumu display name = %q", got)
	}
	for _, tt := range []struct {
		name        string
		monsterID   int
		rankKind    string
		variantKind string
		want        string
	}{
		{name: "Senyu", monsterID: 7, rankKind: "hr", variantKind: "senyu", want: "천유종 라오샨룽"},
		{name: "Zenith", monsterID: 15, rankKind: "g", variantKind: "zenith", want: "천이종 푸루푸루"},
		{name: "Conquest", monsterID: 107, rankKind: "hr", variantKind: "conquest", want: "극정(레벨 미확정) 디스피로아"},
		{name: "Shiten", monsterID: 100, rankKind: "hr", variantKind: "shiten", want: "지천 안노운"},
		{name: "Upper Shiten", monsterID: 100, rankKind: "hr", variantKind: "upper_shiten", want: "상급 지천 안노운"},
		{name: "Challenge", monsterID: 53, rankKind: "hr", variantKind: "challenge", want: "초난관 라잔"},
		{name: "G normal", monsterID: 6, rankKind: "g", variantKind: "normal", want: "G급 얀쿡크"},
		{name: "G hardcore", monsterID: 6, rankKind: "g", variantKind: "hardcore", want: "G급 특이개체 얀쿡크"},
		{name: "G Berserk Raviente", monsterID: mhfmon.BerserkRaviente, rankKind: "g", variantKind: "normal", want: "G급 라비엔테 맹광기"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := dashboardMonsterDisplayName(tt.monsterID, tt.variantKind, tt.rankKind); got != tt.want {
				t.Fatalf("display name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDashboardMonsterFeaturedGroup(t *testing.T) {
	for _, tt := range []struct {
		name        string
		monsterID   int
		rankKind    string
		variantKind string
		want        string
	}{
		{name: "Zenith", monsterID: 15, rankKind: "g", variantKind: "zenith", want: "zenith"},
		{name: "Challenge", monsterID: 53, rankKind: "hr", variantKind: "challenge", want: "challenge"},
		{name: "Extreme", monsterID: 146, rankKind: "unknown", variantKind: "extreme_zinogre", want: "challenge"},
		{name: "Upper Shiten", monsterID: 100, rankKind: "hr", variantKind: "upper_shiten", want: "upper_shiten"},
		{name: "G rank", monsterID: 6, rankKind: "g", variantKind: "normal", want: ""},
		{name: "Exception raw ID", monsterID: 7, rankKind: "hr", variantKind: "normal", want: ""},
		{name: "Hidden HR", monsterID: 6, rankKind: "hr", variantKind: "normal", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := dashboardMonsterFeaturedGroup(tt.monsterID, tt.rankKind, tt.variantKind); got != tt.want {
				t.Fatalf("featured group = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDashboardMonsterTimeRankingQuerySelectsPersonalBestBeforeTopTen(t *testing.T) {
	query := strings.Join(strings.Fields(dashboardMonsterTimesQuery), " ")
	for _, marker := range []string{
		"PARTITION BY r.character_id, r.monster_id, COALESCE(r.rank_kind, 'unknown'), COALESCE(r.variant_kind, 'unknown')",
		"WHERE personal_position = 1",
		"PARTITION BY monster_id, rank_kind, variant_kind",
		"WHERE position <= 10",
		"COALESCE(r.variant_kind, '') = 'zenith'",
		"COALESCE(r.variant_kind, '') = 'challenge'",
		`COALESCE(r.variant_kind, '') LIKE 'extreme\_%' ESCAPE '\'`,
		"COALESCE(r.variant_kind, '') = 'upper_shiten'",
		"COALESCE(r.rank_kind, '') = 'g'",
		`WHEN variant_kind = 'challenge' OR variant_kind LIKE 'extreme\_%' ESCAPE '\' THEN 0`,
		"WHEN variant_kind = 'upper_shiten' THEN 1",
		"WHEN variant_kind = 'zenith' THEN 2",
		"r.monster_id <> 93",
		"(r.monster_id <> 149 OR COALESCE(r.rank_kind, '') = 'g')",
		"r.monster_id IN (7, 50, 55, 58, 60, 119, 120)",
	} {
		if !strings.Contains(query, marker) {
			t.Errorf("monster time ranking query missing %q", marker)
		}
	}
}

func TestDashboardMonsterExceptionRecordsExcludeRaviente(t *testing.T) {
	for _, monsterID := range []int{mhfmon.Raviente, mhfmon.BerserkRaviente} {
		if dashboardMonsterExceptionRecord(monsterID) {
			t.Errorf("Raviente monster %d must rely on the G-rank filter", monsterID)
		}
	}
}

func TestDashboardBerserkRavienteAlwaysUsesOtherGroup(t *testing.T) {
	for _, variantKind := range []string{"normal", "challenge", "extreme_raviente", "upper_shiten"} {
		if got := dashboardMonsterFeaturedGroup(mhfmon.BerserkRaviente, "g", variantKind); got != "other" {
			t.Errorf("Berserk Raviente variant %q group = %q, want other", variantKind, got)
		}
	}
}

func TestMonsterTimeRankEntryJSONIncludesClassification(t *testing.T) {
	data, err := json.Marshal(MonsterTimeRankEntry{RankKind: "g", VariantKind: "normal", FeaturedGroup: ""})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["rankKind"] != "g" || raw["variantKind"] != "normal" || raw["featuredGroup"] != "" {
		t.Fatalf("classification fields missing from JSON: %s", data)
	}
}

func TestEmptyDashboardRankingsIncludesOptionalRankings(t *testing.T) {
	rankings := emptyDashboardRankings()
	if rankings.Playtime == nil {
		t.Fatal("playtime ranking must encode as an empty array, not null")
	}
	if rankings.MonsterTimes == nil {
		t.Fatal("monster time ranking must encode as an empty array, not null")
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

func TestDashboardStatsHidesOperatorDataWithoutKey(t *testing.T) {
	// Rankings stay public; anything naming who is online must not.
	server := &APIServer{
		logger:      NewTestLogger(t),
		erupeConfig: &cfg.Config{API: cfg.API{DashboardOperatorSequence: "up,down,up,up"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	rec := httptest.NewRecorder()
	server.DashboardStatsJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var stats DashboardStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Operator {
		t.Error("operator = true without a key")
	}
	if len(stats.Channels) != 0 || len(stats.OnlineCharacters) != 0 || stats.OnlinePlayers != 0 {
		t.Errorf("leaked live server state: channels=%d online=%d players=%d",
			len(stats.Channels), len(stats.OnlineCharacters), stats.OnlinePlayers)
	}
	if stats.TotalAccounts != 0 || stats.TotalCharacters != 0 || stats.ClientMode != "" {
		t.Errorf("leaked server metadata: accounts=%d chars=%d mode=%q",
			stats.TotalAccounts, stats.TotalCharacters, stats.ClientMode)
	}
}

func TestDashboardStatsMarksOperatorWithKey(t *testing.T) {
	server := &APIServer{
		logger:      NewTestLogger(t),
		erupeConfig: &cfg.Config{API: cfg.API{DashboardOperatorSequence: "up,down,up,up"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	authorizeDashboard(req, server)
	rec := httptest.NewRecorder()
	server.DashboardStatsJSON(rec, req)

	var stats DashboardStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !stats.Operator {
		t.Fatal("operator = false with the correct key")
	}
	if stats.ClientMode == "" && stats.ServerVersion == "" {
		t.Error("operator payload was still stripped")
	}
}

func TestDashboardStatsRejectsWrongKey(t *testing.T) {
	server := &APIServer{
		logger:      NewTestLogger(t),
		erupeConfig: &cfg.Config{API: cfg.API{DashboardOperatorSequence: "up,down,up,up"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
	req.AddCookie(&http.Cookie{Name: dashboardOperatorCookie, Value: server.operatorToken()[:8]})
	rec := httptest.NewRecorder()
	server.DashboardStatsJSON(rec, req)

	var stats DashboardStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Operator {
		t.Fatal("a prefix of the key was accepted")
	}
}

func TestDashboardUnlockSequence(t *testing.T) {
	newServer := func() *APIServer {
		return &APIServer{
			logger:      NewTestLogger(t),
			erupeConfig: &cfg.Config{API: cfg.API{DashboardOperatorSequence: "up,down,up,up"}},
		}
	}

	post := func(server *APIServer, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/dashboard/unlock", strings.NewReader(body))
		rec := httptest.NewRecorder()
		server.DashboardUnlock(rec, req)
		return rec
	}

	status := func(rec *httptest.ResponseRecorder) string {
		var resp dashboardUnlockResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.Status
	}

	t.Run("prefix is pending", func(t *testing.T) {
		server := newServer()
		rec := post(server, `{"gestures":["up","down"]}`)
		if got := status(rec); got != "pending" {
			t.Fatalf("status = %q, want pending", got)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("a partial sequence issued a cookie")
		}
	})

	t.Run("full sequence issues the cookie", func(t *testing.T) {
		server := newServer()
		rec := post(server, `{"gestures":["up","down","up","up"]}`)
		if got := status(rec); got != "matched" {
			t.Fatalf("status = %q, want matched", got)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != dashboardOperatorCookie {
			t.Fatalf("cookies = %+v", cookies)
		}
		if cookies[0].Value != server.operatorToken() {
			t.Fatal("cookie does not carry this process's token")
		}
		if !cookies[0].HttpOnly {
			t.Error("operator cookie must be HttpOnly so page scripts cannot read it")
		}
		// Not remembered: a session cookie must not carry an expiry.
		if cookies[0].MaxAge != 0 {
			t.Errorf("MaxAge = %d, want 0 without remember", cookies[0].MaxAge)
		}
	})

	t.Run("remember extends the cookie", func(t *testing.T) {
		server := newServer()
		rec := post(server, `{"gestures":["up","down","up","up"],"remember":true}`)
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].MaxAge <= 0 {
			t.Fatalf("remember did not set a persistent cookie: %+v", cookies)
		}
	})

	t.Run("wrong gesture is rejected", func(t *testing.T) {
		server := newServer()
		rec := post(server, `{"gestures":["down"]}`)
		if got := status(rec); got != "rejected" {
			t.Fatalf("status = %q, want rejected", got)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("a wrong gesture issued a cookie")
		}
	})

	t.Run("overlong sequence is rejected", func(t *testing.T) {
		server := newServer()
		rec := post(server, `{"gestures":["up","down","up","up","up"]}`)
		if got := status(rec); got != "rejected" {
			t.Fatalf("status = %q, want rejected", got)
		}
	})

	t.Run("unset sequence keeps the panels closed", func(t *testing.T) {
		server := &APIServer{logger: NewTestLogger(t), erupeConfig: &cfg.Config{}}
		rec := post(server, `{"gestures":["up","down","up","up"]}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("an unconfigured server issued a cookie")
		}
	})
}
