package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"erupe-ce/common/mhfmon"
	"erupe-ce/common/mhfquest"

	"github.com/lib/pq"
	"go.uber.org/zap"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed dashboard_assets/namu_* dashboard_assets/weapon_*
var dashboardMonsterIconFS embed.FS

var dashboardTmpl = template.Must(template.New("dashboard").Parse(dashboardHTML))

// dashboardOperatorCookie holds the unlock token issued after the scroll
// sequence is matched. It is HttpOnly so page scripts cannot read it back out.
const dashboardOperatorCookie = "erupe_dashboard_operator"

// dashboardOperatorRememberAge is how long "remember this device" keeps the
// cookie. Without it the cookie is a session cookie and dies with the browser.
const dashboardOperatorRememberAge = 90 * 24 * time.Hour

// dashboardMaxUnlockGestures bounds how many gestures a client may submit before
// the attempt is rejected, so the endpoint cannot be walked indefinitely.
const dashboardMaxUnlockGestures = 32

// dashboardOperatorSequence returns the configured unlock gestures. An empty
// result means the operator panels are disabled entirely.
func (s *APIServer) dashboardOperatorSequence() []string {
	if s.erupeConfig == nil {
		return nil
	}
	raw := strings.Split(s.erupeConfig.API.DashboardOperatorSequence, ",")
	sequence := make([]string, 0, len(raw))
	for _, part := range raw {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "up":
			sequence = append(sequence, "up")
		case "down":
			sequence = append(sequence, "down")
		}
	}
	return sequence
}

// operatorToken returns this process's unlock token, generating it on first use.
// It is random per start, so restarting the server signs every device out.
func (s *APIServer) operatorToken() string {
	s.operatorTokenOnce.Do(func() {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			// Without a token nothing can authenticate, which is the safe result.
			if s.logger != nil {
				s.logger.Error("Failed to generate dashboard operator token", zap.Error(err))
			}
			return
		}
		s.operatorTokenValue = hex.EncodeToString(buf)
	})
	return s.operatorTokenValue
}

// dashboardOperatorAuthorized reports whether a request may see who is online
// and broadcast to every world. An unset sequence closes those features to
// everyone rather than opening them.
func (s *APIServer) dashboardOperatorAuthorized(r *http.Request) bool {
	if len(s.dashboardOperatorSequence()) == 0 {
		return false
	}
	token := s.operatorToken()
	if token == "" {
		return false
	}
	cookie, err := r.Cookie(dashboardOperatorCookie)
	if err != nil {
		return false
	}
	// Constant time so a wrong token cannot be recovered by timing the response.
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1
}

type dashboardUnlockRequest struct {
	Gestures []string `json:"gestures"`
	Remember bool     `json:"remember"`
}

type dashboardUnlockResponse struct {
	// Status is "pending" while the gestures so far are a prefix of the
	// sequence, "matched" once they equal it, and "rejected" otherwise.
	Status string `json:"status"`
}

// DashboardUnlock validates the scroll sequence and issues the operator cookie.
// Keeping the sequence server side means it never appears in the page source,
// unlike a check written in the page's own script.
func (s *APIServer) DashboardUnlock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	sequence := s.dashboardOperatorSequence()
	if len(sequence) == 0 {
		writeJSON(w, http.StatusForbidden, dashboardUnlockResponse{Status: "rejected"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request dashboardUnlockRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, dashboardUnlockResponse{Status: "rejected"})
		return
	}

	// A client that is already unlocked only wants to change how long the
	// cookie lives, so accept the remember toggle without re-running gestures.
	if len(request.Gestures) == 0 && s.dashboardOperatorAuthorized(r) {
		s.setOperatorCookie(w, r, request.Remember)
		writeJSON(w, http.StatusOK, dashboardUnlockResponse{Status: "matched"})
		return
	}

	if len(request.Gestures) == 0 || len(request.Gestures) > dashboardMaxUnlockGestures ||
		len(request.Gestures) > len(sequence) {
		writeJSON(w, http.StatusOK, dashboardUnlockResponse{Status: "rejected"})
		return
	}
	for i, gesture := range request.Gestures {
		if gesture != sequence[i] {
			writeJSON(w, http.StatusOK, dashboardUnlockResponse{Status: "rejected"})
			return
		}
	}
	if len(request.Gestures) < len(sequence) {
		writeJSON(w, http.StatusOK, dashboardUnlockResponse{Status: "pending"})
		return
	}

	s.setOperatorCookie(w, r, request.Remember)
	writeJSON(w, http.StatusOK, dashboardUnlockResponse{Status: "matched"})
}

func (s *APIServer) setOperatorCookie(w http.ResponseWriter, r *http.Request, remember bool) {
	cookie := &http.Cookie{
		Name:     dashboardOperatorCookie,
		Value:    s.operatorToken(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Behind a TLS-terminating proxy r.TLS is nil, so trust the forwarded
		// scheme as well or the cookie would never carry the Secure flag.
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	}
	if remember {
		cookie.MaxAge = int(dashboardOperatorRememberAge / time.Second)
	}
	http.SetCookie(w, cookie)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

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
	// Operator is false for ordinary visitors. The page uses it to decide whether
	// to render the channel, online hunter, and world chat panels at all.
	Operator bool `json:"operator"`
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
	CharID     uint32 `db:"character_id" json:"-"`
	Name       string `db:"character_name" json:"name"`
	HR         int    `db:"hr" json:"hr"`
	GR         int    `db:"gr" json:"gr"`
	Channel    string `db:"channel_name" json:"channel"`
	Land       int    `db:"land" json:"land"`
	Location   string `json:"location"`
	WeaponType *int   `db:"weapon_type" json:"weaponType,omitempty"`
	WeaponName string `json:"weaponName,omitempty"`
	// Quest fields are empty unless the character is inside a quest.
	QuestName      string `json:"questName"`
	QuestElapsedMs int64  `json:"questElapsedMs"`
}

// DashboardSessionInfo is the live per-character state the channel servers
// publish to the dashboard. Quest fields are zero outside a quest.
type DashboardSessionInfo struct {
	StageID        string
	QuestName      string
	QuestStartedAt time.Time
}

// SetDashboardStageProvider connects the API dashboard to the channel
// registry without making the API package depend on channelserver internals.
func (s *APIServer) SetDashboardStageProvider(provider func() map[uint32]DashboardSessionInfo) {
	s.dashboardStageMu.Lock()
	s.dashboardStageIDs = provider
	s.dashboardStageMu.Unlock()
}

func (s *APIServer) dashboardStageSnapshot() map[uint32]DashboardSessionInfo {
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
	ConquestLevel int    `db:"conquest_level" json:"conquestLevel,omitempty"`
	RankingKey    string `json:"rankingKey"`
	FeaturedGroup string `json:"featuredGroup"`
	MonsterName   string `json:"monsterName"`
	Icon          string `json:"icon,omitempty"`
	Name          string `db:"character_name" json:"name"`
	WeaponType    *int   `db:"weapon_type" json:"weaponType,omitempty"`
	WeaponName    string `json:"weaponName,omitempty"`
	QuestID       int    `db:"quest_id" json:"questId"`
	QuestName     string `db:"quest_name" json:"questName"`
	Frames        int64  `db:"best_time_frames" json:"frames"`
}

// RavienteRunRankEntry is one completed Raviente great-hunt run. Unlike the
// per-quest monster records above, one row represents the full event from its
// start through its final completion and can include many hunters and quests.
type RavienteRunRankEntry struct {
	ID               int64          `db:"id" json:"id"`
	EventKind        string         `db:"event_kind" json:"eventKind"`
	DurationMS       int64          `db:"duration_ms" json:"durationMs"`
	EndedAt          time.Time      `db:"ended_at" json:"endedAt"`
	ParticipantCount int            `db:"participant_count" json:"participantCount"`
	Participants     pq.StringArray `db:"participant_names" json:"participants"`
}

// QuestResultRankEntry describes how often a quest was cleared or left
// uncleared. Quests whose title was never resolved fall back to their ID.
type QuestResultRankEntry struct {
	QuestID   int    `db:"quest_id" json:"questId"`
	QuestName string `db:"quest_name" json:"questName"`
	Count     int64  `db:"result_count" json:"count"`
}

// WeaponUsageRankEntry describes how many authenticated hunters departed on a
// quest with one weapon class. WeaponType follows the client's 0..13 enum.
type WeaponUsageRankEntry struct {
	WeaponType int    `db:"weapon_type" json:"weaponType"`
	WeaponName string `json:"weaponName"`
	Uses       int64  `db:"usage_count" json:"uses"`
}

var dashboardWeaponNames = [...]string{
	"대검",
	"헤비보우건",
	"해머",
	"랜스",
	"한손검",
	"라이트보우건",
	"쌍검",
	"태도",
	"수렵피리",
	"건랜스",
	"활",
	"천룡곤",
	"슬래시액스F",
	"마그넷스파이크",
}

func dashboardWeaponDisplayName(weaponType int) string {
	if weaponType < 0 || weaponType >= len(dashboardWeaponNames) {
		return ""
	}
	return dashboardWeaponNames[weaponType]
}

// DashboardRankings contains the read-only rankings shown on the dashboard.
type DashboardRankings struct {
	MonsterHunts     []MonsterHuntRankEntry `json:"monsterHunts"`
	Guilds           []GuildRankEntry       `json:"guilds"`
	Playtime         []PlaytimeRankEntry    `json:"playtime"`
	MostUsedWeapons  []WeaponUsageRankEntry `json:"mostUsedWeapons"`
	LeastUsedWeapons []WeaponUsageRankEntry `json:"leastUsedWeapons"`
	MonsterTimes     []MonsterTimeRankEntry `json:"monsterTimes"`
	RavienteRuns     []RavienteRunRankEntry `json:"ravienteRuns"`
	QuestClears      []QuestResultRankEntry `json:"questClears"`
	QuestFails       []QuestResultRankEntry `json:"questFails"`
}

const dashboardRankingTTL = time.Minute

// dashboardQuestResultQuery ranks quests by one of the two counter columns. The
// column is chosen from a fixed pair rather than interpolated from a request,
// so this cannot become an injection point.
func dashboardQuestResultQuery(column string) string {
	if column != "cleared" && column != "failed" {
		column = "cleared"
	}
	return `
		SELECT
			quest_id,
			quest_name,
			` + column + `::bigint AS result_count
		FROM quest_result_stats
		WHERE ` + column + ` > 0
		ORDER BY ` + column + ` DESC, quest_id ASC
		LIMIT 14
	`
}

const dashboardMostUsedWeaponsQuery = `
	SELECT weapon_type, usage_count
	FROM weapon_usage_stats
	ORDER BY usage_count DESC, weapon_type ASC
`

const dashboardLeastUsedWeaponsQuery = `
	SELECT weapon_type, usage_count
	FROM weapon_usage_stats
	ORDER BY usage_count ASC, weapon_type ASC
`

const dashboardMonsterTimesQuery = `
	WITH eligible_records AS (
		SELECT
			r.character_id,
			r.monster_id,
			COALESCE(r.rank_kind, 'unknown') AS rank_kind,
			COALESCE(r.variant_kind, 'unknown') AS variant_kind,
			r.conquest_level,
			c.name AS character_name,
			r.weapon_type,
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
					COALESCE(r.variant_kind, 'unknown'),
					r.conquest_level
				ORDER BY r.best_time_frames ASC, r.quest_id ASC
			) AS personal_position
		FROM monster_hunt_records r
		INNER JOIN characters c ON c.id = r.character_id
		WHERE c.deleted = false
		  AND COALESCE(c.is_new_character, false) = false
		  AND c.name IS NOT NULL
		  AND c.name <> ''
		  AND r.monster_id NOT IN (93, 149)
		  AND (
			COALESCE(r.variant_kind, '') <> 'conquest'
			OR r.conquest_level = 9999
		  )
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
			conquest_level,
			character_name,
			weapon_type,
			quest_id,
			quest_name,
			best_time_frames,
			ROW_NUMBER() OVER (
				PARTITION BY monster_id, rank_kind, variant_kind, conquest_level
				ORDER BY best_time_frames ASC, character_id ASC
			) AS position
		FROM personal_bests
	)
	SELECT monster_id, rank_kind, variant_kind, conquest_level, character_name, weapon_type, quest_id, quest_name, best_time_frames
	FROM ranked
	WHERE position <= 10
	ORDER BY
		CASE
			WHEN variant_kind = 'challenge' OR variant_kind LIKE 'extreme\_%' ESCAPE '\' THEN 0
			WHEN variant_kind = 'upper_shiten' THEN 1
			WHEN variant_kind = 'zenith' THEN 2
			ELSE 3
		END,
		monster_id ASC,
		rank_kind ASC,
		variant_kind ASC,
		conquest_level ASC,
		position ASC
`

const dashboardRavienteRunsQuery = `
	WITH ranked_runs AS (
		SELECT
			id,
			event_kind,
			ended_at,
			duration_ms,
			ROW_NUMBER() OVER (
				PARTITION BY event_kind
				ORDER BY duration_ms ASC, ended_at ASC, id ASC
			) AS position
		FROM raviente_runs
		WHERE status = 'completed'
		  AND event_kind IN ('berserk', 'extreme', 'small')
		  AND ended_at IS NOT NULL
		  AND duration_ms > 0
	), unique_participants AS (
		SELECT DISTINCT ON (run_id, character_id_snapshot)
			run_id,
			character_id_snapshot,
			character_name_snapshot
		FROM raviente_run_participants
		ORDER BY run_id, character_id_snapshot, character_name_snapshot
	)
	SELECT
		r.id,
		r.event_kind,
		r.duration_ms,
		r.ended_at,
		COUNT(p.character_id_snapshot)::integer AS participant_count,
		COALESCE(
			ARRAY_AGG(p.character_name_snapshot ORDER BY LOWER(p.character_name_snapshot), p.character_name_snapshot, p.character_id_snapshot)
				FILTER (WHERE p.character_name_snapshot IS NOT NULL AND BTRIM(p.character_name_snapshot) <> ''),
			ARRAY[]::text[]
		) AS participant_names
	FROM ranked_runs r
	LEFT JOIN unique_participants p ON p.run_id = r.id
	WHERE r.position <= 10
	GROUP BY r.id, r.event_kind, r.duration_ms, r.ended_at, r.position
	ORDER BY
		CASE r.event_kind
			WHEN 'berserk' THEN 0
			WHEN 'extreme' THEN 1
			WHEN 'small' THEN 2
			ELSE 3
		END,
		r.position ASC
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
		Operator:         s.dashboardOperatorAuthorized(r),
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

	// Rankings are public; everything that names who is currently playing, or
	// reveals the channel layout, is stripped for ordinary visitors. Filtering
	// here rather than skipping the queries keeps one code path, and the queries
	// are already served from the shared ranking cache.
	if !stats.Operator {
		stats.Channels = make([]ChannelInfo, 0)
		stats.OnlineCharacters = make([]OnlineCharacter, 0)
		stats.OnlinePlayers = 0
		stats.TotalAccounts = 0
		stats.TotalCharacters = 0
		stats.Uptime = ""
		stats.ServerVersion = ""
		stats.ClientMode = ""
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		s.logger.Error("Dashboard: failed to encode stats", zap.Error(err))
	}
}

const dashboardOnlineCharactersQuery = `
		SELECT character_id, character_name, hr, gr, channel_name, land, weapon_type
		FROM (
			SELECT DISTINCT ON (c.id)
				c.id AS character_id,
				c.name AS character_name,
				COALESCE(c.hr, 0)::integer AS hr,
				COALESCE(c.gr, 0)::integer AS gr,
				COALESCE(s.world_name, 'World') AS channel_name,
				COALESCE(s.land, 0) AS land,
				CASE
					WHEN c.weapon_type BETWEEN 0 AND 13 THEN c.weapon_type::integer
					ELSE NULL
				END AS weapon_type
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
`

func (s *APIServer) dashboardOnlineCharacters(ctx context.Context) ([]OnlineCharacter, error) {
	online := make([]OnlineCharacter, 0)
	err := s.db.SelectContext(ctx, &online, dashboardOnlineCharactersQuery)
	if err != nil {
		return online, err
	}
	enrichDashboardOnlineCharacters(online, s.dashboardStageSnapshot(), time.Now())
	return online, nil
}

func enrichDashboardOnlineCharacters(online []OnlineCharacter, sessions map[uint32]DashboardSessionInfo, now time.Time) {
	for i := range online {
		session := sessions[online[i].CharID]
		online[i].Location = dashboardLocationForStage(session.StageID)
		if online[i].WeaponType != nil {
			online[i].WeaponName = dashboardWeaponDisplayName(*online[i].WeaponType)
			if online[i].WeaponName == "" {
				online[i].WeaponType = nil
			}
		}
		// Quest columns stay empty unless the character is actually in one, so a
		// hunter standing in town shows blanks rather than a stale last quest.
		if !session.QuestStartedAt.IsZero() {
			online[i].QuestName = session.QuestName
			if elapsed := now.Sub(session.QuestStartedAt); elapsed > 0 {
				online[i].QuestElapsedMs = elapsed.Milliseconds()
			}
		}
	}
}

func emptyDashboardRankings() DashboardRankings {
	return DashboardRankings{
		MonsterHunts:     make([]MonsterHuntRankEntry, 0),
		Guilds:           make([]GuildRankEntry, 0),
		Playtime:         make([]PlaytimeRankEntry, 0),
		MostUsedWeapons:  make([]WeaponUsageRankEntry, 0),
		LeastUsedWeapons: make([]WeaponUsageRankEntry, 0),
		MonsterTimes:     make([]MonsterTimeRankEntry, 0),
		RavienteRuns:     make([]RavienteRunRankEntry, 0),
		QuestClears:      make([]QuestResultRankEntry, 0),
		QuestFails:       make([]QuestResultRankEntry, 0),
	}
}

func (s *APIServer) getDashboardRankings(ctx context.Context) DashboardRankings {
	s.dashboardMu.Lock()
	defer s.dashboardMu.Unlock()

	if !s.dashboardRankingsAt.IsZero() && time.Since(s.dashboardRankingsAt) < dashboardRankingTTL {
		return s.dashboardRankings
	}

	rankings := emptyDashboardRankings()
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

	if err := s.db.SelectContext(ctx, &rankings.MostUsedWeapons, dashboardMostUsedWeaponsQuery); err != nil {
		s.logger.Warn("Dashboard: failed to query most-used weapon rankings", zap.Error(err))
		return s.dashboardRankings
	}
	if err := s.db.SelectContext(ctx, &rankings.LeastUsedWeapons, dashboardLeastUsedWeaponsQuery); err != nil {
		s.logger.Warn("Dashboard: failed to query least-used weapon rankings", zap.Error(err))
		return s.dashboardRankings
	}
	for i := range rankings.MostUsedWeapons {
		rankings.MostUsedWeapons[i].WeaponName = dashboardWeaponDisplayName(rankings.MostUsedWeapons[i].WeaponType)
	}
	for i := range rankings.LeastUsedWeapons {
		rankings.LeastUsedWeapons[i].WeaponName = dashboardWeaponDisplayName(rankings.LeastUsedWeapons[i].WeaponType)
	}

	if err := s.db.SelectContext(ctx, &rankings.QuestClears, dashboardQuestResultQuery("cleared")); err != nil {
		s.logger.Warn("Dashboard: failed to query quest clear rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.QuestFails, dashboardQuestResultQuery("failed")); err != nil {
		s.logger.Warn("Dashboard: failed to query quest failure rankings", zap.Error(err))
		return s.dashboardRankings
	}

	if err := s.db.SelectContext(ctx, &rankings.MonsterTimes, dashboardMonsterTimesQuery); err != nil {
		s.logger.Warn("Dashboard: failed to query monster time rankings", zap.Error(err))
		return s.dashboardRankings
	}
	if err := s.db.SelectContext(ctx, &rankings.RavienteRuns, dashboardRavienteRunsQuery); err != nil {
		s.logger.Warn("Dashboard: failed to query Raviente run rankings", zap.Error(err))
		return s.dashboardRankings
	}
	for i := range rankings.RavienteRuns {
		entry := &rankings.RavienteRuns[i]
		sort.SliceStable(entry.Participants, func(i, j int) bool {
			left := strings.ToLower(entry.Participants[i])
			right := strings.ToLower(entry.Participants[j])
			if left == right {
				return entry.Participants[i] < entry.Participants[j]
			}
			return left < right
		})
	}
	questTitles := make(map[int]string)
	for i := range rankings.MonsterTimes {
		entry := &rankings.MonsterTimes[i]
		monsterID := entry.MonsterID
		entry.FeaturedGroup = dashboardMonsterFeaturedGroup(monsterID, entry.RankKind, entry.VariantKind)
		entry.RankingKey = dashboardMonsterRankingKey(monsterID, entry.RankKind, entry.VariantKind, entry.ConquestLevel)
		entry.MonsterName = dashboardMonsterDisplayNameWithConquestLevel(monsterID, entry.VariantKind, entry.ConquestLevel, entry.RankKind)
		entry.Icon = dashboardMonsterIcon(monsterID, entry.VariantKind)
		if entry.WeaponType != nil {
			entry.WeaponName = dashboardWeaponDisplayName(*entry.WeaponType)
		}
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
	if monsterID == mhfmon.BerserkRaviente {
		return "other"
	}
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

func dashboardMonsterRankingKey(monsterID int, rankKind, variantKind string, conquestLevel int) string {
	key := fmt.Sprintf("%d:%s:%s", monsterID, rankKind, variantKind)
	if strings.EqualFold(strings.TrimSpace(variantKind), "conquest") {
		return fmt.Sprintf("%s:%d", key, conquestLevel)
	}
	return key
}

func dashboardMonsterDisplayName(monsterID int, variantKind string, rankKinds ...string) string {
	return dashboardMonsterDisplayNameWithConquestLevel(monsterID, variantKind, 0, rankKinds...)
}

func dashboardMonsterDisplayNameWithConquestLevel(monsterID int, variantKind string, conquestLevel int, rankKinds ...string) string {
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
		if conquestLevel > 0 {
			return fmt.Sprintf("극정 Lv.%d %s", conquestLevel, base)
		}
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
