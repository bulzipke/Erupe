package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"

	"erupe-ce/common/mhfmon"
	"erupe-ce/common/mhfquest"

	"go.uber.org/zap"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed dashboard_assets/namu_*
var dashboardMonsterIconFS embed.FS

var dashboardTmpl = template.Must(template.New("dashboard").Parse(dashboardHTML))

// DashboardStats is the JSON payload returned by GET /api/dashboard/stats.
type DashboardStats struct {
	Uptime           string            `json:"uptime"`
	ServerVersion    string            `json:"serverVersion"`
	ClientMode       string            `json:"clientMode"`
	OnlinePlayers    int               `json:"onlinePlayers"`
	TotalAccounts    int               `json:"totalAccounts"`
	TotalCharacters  int               `json:"totalCharacters"`
	Channels         []ChannelInfo     `json:"channels"`
	OnlineCharacters []OnlineCharacter `json:"onlineCharacters"`
	Rankings         DashboardRankings `json:"rankings"`
	DatabaseOK       bool              `json:"databaseOK"`
}

// ChannelInfo describes a single channel server entry from the servers table.
type ChannelInfo struct {
	Name       string `json:"name"`
	Land       int    `json:"land"`
	Port       int    `json:"port"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"maxPlayers"`
}

type dashboardConfiguredChannel struct {
	port       uint16
	maxPlayers uint16
}

// dashboardConfiguredChannels returns only channel servers that this process
// is configured to run. Rows for channels from an older configuration can
// remain in the shared servers table and must not reappear on the dashboard.
func (s *APIServer) dashboardConfiguredChannels() map[int]dashboardConfiguredChannel {
	channels := make(map[int]dashboardConfiguredChannel)
	if s.erupeConfig == nil {
		return channels
	}

	for si, entry := range s.erupeConfig.Entrance.Entries {
		for ci, channel := range entry.Channels {
			if !channel.IsEnabled() {
				continue
			}
			serverID := (4096 + si*256) + (16 + ci)
			channels[serverID] = dashboardConfiguredChannel{
				port:       channel.Port,
				maxPlayers: channel.MaxPlayers,
			}
		}
	}
	return channels
}

// OnlineCharacter is a currently connected character from sign_sessions,
// enriched with its live in-process channel stage when available.
type OnlineCharacter struct {
	CharID   uint32 `db:"character_id" json:"-"`
	Name     string `db:"character_name" json:"name"`
	HR       int    `db:"hr" json:"hr"`
	GR       int    `db:"gr" json:"gr"`
	Channel  string `db:"channel_name" json:"channel"`
	Land     int    `db:"land" json:"land"`
	Location string `json:"location"`
}

// SetDashboardStageProvider connects the API dashboard to the channel
// registry without making the API package depend on channelserver internals.
func (s *APIServer) SetDashboardStageProvider(provider func() map[uint32]string) {
	s.dashboardStageMu.Lock()
	s.dashboardStageIDs = provider
	s.dashboardStageMu.Unlock()
}

func (s *APIServer) dashboardStageSnapshot() map[uint32]string {
	s.dashboardStageMu.RLock()
	provider := s.dashboardStageIDs
	s.dashboardStageMu.RUnlock()
	if provider == nil {
		return nil
	}
	return provider()
}

func dashboardLocationForStage(stageID string) string {
	if stageID == "" {
		return "접속 중"
	}

	fixedLocations := []struct {
		prefix string
		label  string
	}{
		{prefix: "sl1Ns200", label: "메제포르타"},
		{prefix: "sl1Ns211", label: "라스타 주점"},
		{prefix: "sl1Ns260", label: "파로네 캐러밴"},
		{prefix: "sl1Ns262", label: "캐러밴 객실 1층"},
		{prefix: "sl1Ns263", label: "캐러밴 객실 2층"},
		{prefix: "sl2Ns379", label: "기도의 샘"},
		{prefix: "sl1Ns462", label: "메제페스"},
		{prefix: "sl2Ls210", label: "공개 주점"},
		{prefix: "sl2Ls286", label: "수렵 기술 대회"},
		{prefix: "sl2Ls463", label: "토코토코 파트냐"},
		{prefix: "sl2Ls465", label: "볼파쿤"},
	}
	for _, location := range fixedLocations {
		if strings.HasPrefix(stageID, location.prefix) {
			return location.label
		}
	}

	if len(stageID) >= 5 {
		switch stageID[3:5] {
		case "Qs":
			return "퀘스트 중"
		case "Gs":
			return "수렵단"
		case "Ms":
			return "마이하우스"
		case "Ls":
			return "로비"
		case "Ns":
			return "로비/시설"
		}
	}
	return "기타 장소"
}

// HunterRankEntry describes one character in the HR/GR leaderboard.
type HunterRankEntry struct {
	Name string `db:"character_name" json:"name"`
	HR   int    `db:"hr" json:"hr"`
	GR   int    `db:"gr" json:"gr"`
}

// MonsterHuntRankEntry describes cumulative large-monster kills recorded by
// MSG_SYS_RECORD_LOG.
type MonsterHuntRankEntry struct {
	Name  string `db:"character_name" json:"name"`
	Kills int64  `db:"kills" json:"kills"`
}

// GuildRankEntry describes a guild ordered by accumulated rank points.
type GuildRankEntry struct {
	Name string `db:"guild_name" json:"name"`
	RP   int64  `db:"rp" json:"rp"`
}

// PlaytimeRankEntry describes cumulative character playtime in seconds.
type PlaytimeRankEntry struct {
	Name    string `db:"character_name" json:"name"`
	Seconds int64  `db:"playtime_seconds" json:"seconds"`
}

// MonsterTimeRankEntry is one hunter's personal-best quest time for a large
// monster. Frames are reported by the ZZ client at 30 Hz.
type MonsterTimeRankEntry struct {
	MonsterID     int    `db:"monster_id" json:"monsterId"`
	RankKind      string `db:"rank_kind" json:"rankKind"`
	VariantKind   string `db:"variant_kind" json:"variantKind"`
	RankingKey    string `json:"rankingKey"`
	FeaturedGroup string `json:"featuredGroup"`
	MonsterName   string `json:"monsterName"`
	Icon          string `json:"icon,omitempty"`
	Name          string `db:"character_name" json:"name"`
	QuestID       int    `db:"quest_id" json:"questId"`
	QuestName     string `db:"quest_name" json:"questName"`
	Frames        int64  `db:"best_time_frames" json:"frames"`
}

// DashboardRankings contains the read-only rankings shown on the dashboard.
type DashboardRankings struct {
	Hunters      []HunterRankEntry      `json:"hunters"`
	MonsterHunts []MonsterHuntRankEntry `json:"monsterHunts"`
	Guilds       []GuildRankEntry       `json:"guilds"`
	Playtime     []PlaytimeRankEntry    `json:"playtime"`
	MonsterTimes []MonsterTimeRankEntry `json:"monsterTimes"`
}

const dashboardRankingTTL = time.Minute

const dashboardMonsterTimesQuery = `
	WITH eligible_records AS (
		SELECT
			r.character_id,
			r.monster_id,
			COALESCE(r.rank_kind, 'unknown') AS rank_kind,
			COALESCE(r.variant_kind, 'unknown') AS variant_kind,
			c.name AS character_name,
			r.quest_id,
			CASE
				WHEN r.quest_name <> '' THEN r.quest_name
				WHEN r.quest_id > 0 THEN '퀘스트 #' || r.quest_id::text
				ELSE '퀘스트 번호 없음'
			END AS quest_name,
			r.best_time_frames,
			ROW_NUMBER() OVER (
				PARTITION BY
					r.character_id,
					r.monster_id,
					COALESCE(r.rank_kind, 'unknown'),
					COALESCE(r.variant_kind, 'unknown')
				ORDER BY r.best_time_frames ASC, r.quest_id ASC
			) AS personal_position
		FROM monster_hunt_records r
		INNER JOIN characters c ON c.id = r.character_id
		WHERE c.deleted = false
		  AND COALESCE(c.is_new_character, false) = false
		  AND c.name IS NOT NULL
		  AND c.name <> ''
		  AND (
			COALESCE(r.variant_kind, '') = 'zenith'
			OR COALESCE(r.variant_kind, '') = 'challenge'
			OR COALESCE(r.variant_kind, '') LIKE 'extreme\_%' ESCAPE '\'
			OR COALESCE(r.variant_kind, '') = 'upper_shiten'
			OR COALESCE(r.rank_kind, '') = 'g'
			OR r.monster_id IN (7, 50, 55, 58, 60, 119, 120)
		  )
	), personal_bests AS (
		SELECT *
		FROM eligible_records
		WHERE personal_position = 1
	), ranked AS (
		SELECT
			monster_id,
			rank_kind,
			variant_kind,
			character_name,
			quest_id,
			quest_name,
			best_time_frames,
			ROW_NUMBER() OVER (
				PARTITION BY monster_id, rank_kind, variant_kind
				ORDER BY best_time_frames ASC, character_id ASC
			) AS position
		FROM personal_bests
	)
	SELECT monster_id, rank_kind, variant_kind, character_name, quest_id, quest_name, best_time_frames
	FROM ranked
	WHERE position <= 10
	ORDER BY
		CASE
			WHEN variant_kind = 'zenith' THEN 0
			WHEN variant_kind = 'challenge' OR variant_kind LIKE 'extreme\_%' ESCAPE '\' THEN 1
			WHEN variant_kind = 'upper_shiten' THEN 2
			ELSE 3
		END,
		monster_id ASC,
		rank_kind ASC,
		variant_kind ASC,
		position ASC
`

// Dashboard serves the embedded HTML dashboard page at /dashboard.
func (s *APIServer) Dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := dashboardTmpl.Execute(w, nil); err != nil {
		s.logger.Error("Failed to render dashboard", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// DashboardMonsterIcon serves the small embedded icon set used by monster
// ranking cards. Assets are immutable and may be cached for a year.
func (s *APIServer) DashboardMonsterIcon(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	data, err := dashboardMonsterIconFS.ReadFile("dashboard_assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// DashboardStatsJSON serves GET /api/dashboard/stats with live server statistics.
func (s *APIServer) DashboardStatsJSON(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	stats := DashboardStats{
		ServerVersion:    "Erupe-CE",
		ClientMode:       s.erupeConfig.ClientMode,
		Channels:         make([]ChannelInfo, 0),
		OnlineCharacters: make([]OnlineCharacter, 0),
		Rankings:         emptyDashboardRankings(),
	}

	// Compute uptime.
	if !s.startTime.IsZero() {
		stats.Uptime = formatDuration(time.Since(s.startTime))
	} else {
		stats.Uptime = "확인 불가"
	}

	// Check database connectivity.
	if s.db != nil {
		if err := s.db.PingContext(ctx); err != nil {
			s.logger.Warn("Dashboard: database ping failed", zap.Error(err))
			stats.DatabaseOK = false
		} else {
			stats.DatabaseOK = true
		}
	}

	// Query total accounts.
	if s.db != nil {
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&stats.TotalAccounts); err != nil {
			s.logger.Warn("Dashboard: failed to count users", zap.Error(err))
		}
	}

	// Query total characters.
	if s.db != nil {
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM characters").Scan(&stats.TotalCharacters); err != nil {
			s.logger.Warn("Dashboard: failed to count characters", zap.Error(err))
		}
	}

	// Build a map from server_id to configured port, mirroring main.go's ID
	// assignment. The map deliberately excludes disabled and stale channels.
	channelByServerID := s.dashboardConfiguredChannels()

	// Query channel info from servers table.
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, "SELECT server_id, current_players, world_name, land FROM servers ORDER BY server_id")
		if err != nil {
			s.logger.Warn("Dashboard: failed to query servers", zap.Error(err))
		} else {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var serverID, players, land int
				var worldName *string
				if err := rows.Scan(&serverID, &players, &worldName, &land); err != nil {
					s.logger.Warn("Dashboard: failed to scan server row", zap.Error(err))
					continue
				}
				configured, ok := channelByServerID[serverID]
				if !ok {
					continue
				}
				name := "Channel"
				if worldName != nil {
					name = *worldName
				}
				ch := ChannelInfo{
					Name:       name,
					Land:       land,
					Port:       int(configured.port),
					Players:    players,
					MaxPlayers: int(configured.maxPlayers),
				}
				stats.Channels = append(stats.Channels, ch)
				stats.OnlinePlayers += players
			}
		}
	}

	if s.db != nil {
		online, err := s.dashboardOnlineCharacters(ctx)
		if err != nil {
			s.logger.Warn("Dashboard: failed to query online characters", zap.Error(err))
		} else {
			stats.OnlineCharacters = online
			// The bound sign session list is the authoritative source for
			// names. It also avoids a transient count/name mismatch.
			stats.OnlinePlayers = len(online)
		}
		stats.Rankings = s.getDashboardRankings(ctx)
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		s.logger.Error("Dashboard: failed to encode stats", zap.Error(err))
	}
}

func (s *APIServer) dashboardOnlineCharacters(ctx context.Context) ([]OnlineCharacter, error) {
	online := make([]OnlineCharacter, 0)
	err := s.db.SelectContext(ctx, &online, `
		SELECT character_id, character_name, hr, gr, channel_name, land
		FROM (
			SELECT DISTINCT ON (c.id)
				c.id AS character_id,
				c.name AS character_name,
				COALESCE(c.hr, 0)::integer AS hr,
				COALESCE(c.gr, 0)::integer AS gr,
				COALESCE(s.world_name, 'World') AS channel_name,
				COALESCE(s.land, 0) AS land
			FROM sign_sessions ss
			INNER JOIN characters c ON c.id = ss.char_id
			LEFT JOIN servers s ON s.server_id = ss.server_id
			WHERE ss.char_id IS NOT NULL
			  AND ss.server_id IS NOT NULL
			  AND c.deleted = false
			  AND COALESCE(c.is_new_character, false) = false
			  AND c.name IS NOT NULL
			  AND c.name <> ''
			ORDER BY c.id, ss.id DESC
		) AS connected
		ORDER BY channel_name, land, character_name
	`)
	if err != nil {
		return online, err
	}
	stageIDs := s.dashboardStageSnapshot()
	for i := range online {
		online[i].Location = dashboardLocationForStage(stageIDs[online[i].CharID])
	}
	return online, err
}

func emptyDashboardRankings() DashboardRankings {
	return DashboardRankings{
		Hunters:      make([]HunterRankEntry, 0),
		MonsterHunts: make([]MonsterHuntRankEntry, 0),
		Guilds:       make([]GuildRankEntry, 0),
		Playtime:     make([]PlaytimeRankEntry, 0),
		MonsterTimes: make([]MonsterTimeRankEntry, 0),
	}
}

func (s *APIServer) getDashboardRankings(ctx context.Context) DashboardRankings {
	s.dashboardMu.Lock()
	defer s.dashboardMu.Unlock()

	if !s.dashboardRankingsAt.IsZero() && time.Since(s.dashboardRankingsAt) < dashboardRankingTTL {
		return s.dashboardRankings
	}

	rankings := emptyDashboardRankings()
	if err := s.db.SelectContext(ctx, &rankings.Hunters, `
		SELECT
			name AS character_name,
			COALESCE(hr, 0)::integer AS hr,
			COALESCE(gr, 0)::integer AS gr
		FROM characters
		WHERE deleted = false
		  AND COALESCE(is_new_character, false) = false
		  AND name IS NOT NULL
		  AND name <> ''
		ORDER BY COALESCE(gr, 0) DESC, COALESCE(hr, 0) DESC, id ASC
		LIMIT 5
	`); err != nil {
		s.logger.Warn("Dashboard: failed to query hunter rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.MonsterHunts, `
		SELECT
			c.name AS character_name,
			SUM(kl.quantity)::bigint AS kills
		FROM kill_logs kl
		INNER JOIN characters c ON c.id = kl.character_id
		WHERE c.deleted = false
		  AND COALESCE(c.is_new_character, false) = false
		  AND c.name IS NOT NULL
		  AND c.name <> ''
		GROUP BY c.id, c.name
		ORDER BY kills DESC, c.id ASC
		LIMIT 5
	`); err != nil {
		s.logger.Warn("Dashboard: failed to query monster hunt rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.Guilds, `
		SELECT
			name AS guild_name,
			rank_rp::bigint AS rp
		FROM guilds
		WHERE name IS NOT NULL
		  AND name <> ''
		ORDER BY rank_rp DESC, id ASC
		LIMIT 5
	`); err != nil {
		s.logger.Warn("Dashboard: failed to query guild rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.Playtime, `
		SELECT
			name AS character_name,
			playtime_seconds::bigint AS playtime_seconds
		FROM characters
		WHERE deleted = false
		  AND COALESCE(is_new_character, false) = false
		  AND name IS NOT NULL
		  AND name <> ''
		  AND playtime_seconds > 0
		ORDER BY playtime_seconds DESC, id ASC
		LIMIT 5
	`); err != nil {
		s.logger.Warn("Dashboard: failed to query playtime rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.MonsterTimes, dashboardMonsterTimesQuery); err != nil {
		s.logger.Warn("Dashboard: failed to query monster time rankings", zap.Error(err))
		return s.dashboardRankings
	}
	questTitles := make(map[int]string)
	for i := range rankings.MonsterTimes {
		entry := &rankings.MonsterTimes[i]
		monsterID := entry.MonsterID
		entry.FeaturedGroup = dashboardMonsterFeaturedGroup(monsterID, entry.RankKind, entry.VariantKind)
		entry.RankingKey = fmt.Sprintf("%d:%s:%s", monsterID, entry.RankKind, entry.VariantKind)
		entry.MonsterName = dashboardMonsterDisplayName(monsterID, entry.VariantKind, entry.RankKind)
		entry.Icon = dashboardMonsterIcon(monsterID, entry.VariantKind)
		if entry.Icon == "" {
			entry.Icon = dashboardMonsterIcon(monsterID, "normal")
		}
		questID := entry.QuestID
		if questID >= 0 && questID <= int(^uint16(0)) {
			title, cached := questTitles[questID]
			if !cached {
				title, _ = mhfquest.ResolveTitle(s.erupeConfig.BinPath, uint16(questID), "ko")
				questTitles[questID] = title
			}
			if title != "" {
				entry.QuestName = title
			}
		}
	}

	s.dashboardRankings = rankings
	s.dashboardRankingsAt = time.Now()
	return rankings
}

func dashboardMonsterFeaturedGroup(monsterID int, rankKind, variantKind string) string {
	rankKind = strings.ToLower(strings.TrimSpace(rankKind))
	variantKind = strings.ToLower(strings.TrimSpace(variantKind))
	switch {
	case variantKind == "zenith":
		return "zenith"
	case variantKind == "challenge", strings.HasPrefix(variantKind, "extreme_"):
		return "challenge"
	case variantKind == "upper_shiten":
		return "upper_shiten"
	case rankKind == "g", dashboardMonsterExceptionRecord(monsterID):
		return ""
	default:
		return ""
	}
}

func dashboardMonsterExceptionRecord(monsterID int) bool {
	switch monsterID {
	case 7, 50, 55, 58, 60, 119, 120:
		return true
	default:
		return false
	}
}

func dashboardMonsterDisplayName(monsterID int, variantKind string, rankKinds ...string) string {
	// These four extreme forms have their own raw IDs, so their identity is
	// definitive even when the quest-wide flags look normal or hardcore.
	switch monsterID {
	case 163:
		return "극도로 달리는 나르가쿠르가"
	case 167:
		return "극도로 교만하는 두레무디라"
	case 172:
		return "극도로 엄습하는 보가바도름"
	case 174:
		return "극도로 빛나는 제르레우스"
	}

	base := mhfmon.KoreanName(monsterID)
	if base == "" {
		base = fmt.Sprintf("몬스터 #%d", monsterID)
	}
	variantKind = strings.ToLower(strings.TrimSpace(variantKind))
	specialName := ""
	switch variantKind {
	case "phantom_red_rajang":
		specialName = "붉은 라잔"
	case "phantom_doragyurosu":
		specialName = "환상의 드라규로스"
	case "violent_raviente":
		specialName = "라비엔테 광폭기"
	case "extreme_zinogre":
		specialName = "극도로 울부짖는 진오우거"
	case "extreme_guanzorumu":
		specialName = "극도로 통솔하는 관조룸"
	case "extreme_deviljho":
		specialName = "극도로 먹어치우는 이블조"
	case "extreme_elzelion":
		specialName = "극도로 태워 얼리는 엘제리온"
	}
	if specialName != "" {
		return specialName
	}

	rankKind := ""
	if len(rankKinds) > 0 {
		rankKind = strings.ToLower(strings.TrimSpace(rankKinds[0]))
	}
	switch variantKind {
	case "senyu":
		return "천유종 " + base
	case "zenith":
		return "천이종 " + base
	case "conquest":
		return "극정(레벨 미확정) " + base
	case "shiten":
		return "지천 " + base
	case "upper_shiten":
		return "상급 지천 " + base
	case "challenge":
		return "초난관 " + base
	}

	if rankKind == "g" {
		switch variantKind {
		case "normal", "":
			return "G급 " + base
		case "hardcore":
			return "G급 특이개체 " + base
		case "hardcore_optional":
			return "G급 일반/특이개체 미확정 · " + base
		case "ul_fixed":
			return "G급 UL · " + base
		case "unknown":
			return "G급 분류 미확정 · " + base
		}
	}

	switch variantKind {
	case "hardcore":
		return "특이개체 " + base
	case "hardcore_optional":
		return "일반/특이개체 미확정 · " + base
	case "ul_fixed":
		return "UL · " + base
	case "unknown":
		return "분류 미확정 · " + base
	default:
		return base
	}
}

// formatDuration produces a Korean human-readable duration string.
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%d일 %d시간 %d분 %d초", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%d시간 %d분 %d초", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d분 %d초", minutes, seconds)
	}
	return fmt.Sprintf("%d초", seconds)
}
