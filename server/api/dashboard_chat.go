package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"
)

const (
	dashboardChatHistoryLimit       = 100
	dashboardChatHistoryFileLimit   = 256 << 10
	dashboardChatNameMaxRunes       = 8
	dashboardChatStoredNameMaxRunes = 32
	dashboardChatTextMaxRunes       = 120
	dashboardChatBodyLimit          = 4 << 10
)

// DashboardChatMessage is one recent public game-chat line shown on the dashboard.
type DashboardChatMessage struct {
	Sender  string    `json:"sender"`
	Message string    `json:"message"`
	Source  string    `json:"source"`
	Scope   string    `json:"scope"`
	Time    time.Time `json:"time"`
}

type dashboardChatRequest struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
}

type dashboardChatResponse struct {
	Messages []DashboardChatMessage `json:"messages"`
}

// SetWorldChatBroadcaster connects the HTTP dashboard to the in-process
// channel registry. It is set after all channel servers have been constructed.
func (s *APIServer) SetWorldChatBroadcaster(broadcaster func(sender, message string) error) {
	s.chatMu.Lock()
	s.worldChatBroadcaster = broadcaster
	s.chatMu.Unlock()
}

// RecordWorldChat records a player-originated world chat message. It remains
// as a convenience wrapper for callers that only produce world chat.
func (s *APIServer) RecordWorldChat(sender, message string) {
	s.RecordGameChat("world", sender, message)
}

// RecordGameChat records player-originated world, land, and party chat.
// Guild, alliance, whisper, and unknown scopes are intentionally rejected.
func (s *APIServer) RecordGameChat(scope, sender, message string) {
	switch scope {
	case "world", "land", "party":
		s.recordDashboardChat("game", scope, sender, message)
	}
}

func (s *APIServer) recordDashboardChat(source, scope, sender, message string) {
	entry := DashboardChatMessage{
		Sender:  truncateRunes(cleanChatLine(sender), dashboardChatStoredNameMaxRunes),
		Message: truncateRunes(cleanChatLine(message), dashboardChatTextMaxRunes),
		Source:  source,
		Scope:   scope,
		Time:    time.Now(),
	}
	if entry.Sender == "" || entry.Message == "" {
		return
	}

	s.chatMu.Lock()
	path := s.dashboardChatPath()
	messages, err := readDashboardChat(path)
	if err == nil {
		messages = append(messages, entry)
		if len(messages) > dashboardChatHistoryLimit {
			messages = messages[len(messages)-dashboardChatHistoryLimit:]
		}
		err = writeDashboardChat(path, messages)
	}
	s.chatMu.Unlock()
	if err != nil && s.logger != nil {
		s.logger.Warn("Dashboard: failed to save chat history", zap.Error(err))
	}
}

func (s *APIServer) dashboardChatPath() string {
	return filepath.Join(s.erupeConfig.BinPath, "dashboard-chat.json")
}

func (s *APIServer) dashboardChatSnapshot() ([]DashboardChatMessage, error) {
	s.chatMu.RLock()
	defer s.chatMu.RUnlock()
	return readDashboardChat(s.dashboardChatPath())
}

func readDashboardChat(path string) ([]DashboardChatMessage, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []DashboardChatMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, dashboardChatHistoryFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > dashboardChatHistoryFileLimit {
		return nil, errors.New("dashboard chat history file exceeds size limit")
	}
	messages := []DashboardChatMessage{}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	if len(messages) > dashboardChatHistoryLimit {
		messages = messages[len(messages)-dashboardChatHistoryLimit:]
	}
	return messages, nil
}

func writeDashboardChat(path string, messages []DashboardChatMessage) error {
	// CreateTemp uses mode 0600. The same-directory rename preserves the old
	// file if writing is interrupted before the new snapshot is complete.
	f, err := os.CreateTemp(filepath.Dir(path), ".dashboard-chat-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(messages); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// DashboardChat serves the recent chat log and accepts web-originated world
// chat messages.
func (s *APIServer) DashboardChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	// Both directions are operator-only: the log carries player chat, and POST
	// broadcasts to every world. Without this the page's key sequence would be
	// decoration, since anyone can call these endpoints directly.
	if !s.dashboardOperatorAuthorized(r) {
		writeDashboardChatError(w, http.StatusForbidden, "권한이 없습니다.")
		return
	}

	if r.Method == http.MethodGet {
		s.writeDashboardChatResponse(w, http.StatusOK)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, dashboardChatBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request dashboardChatRequest
	if err := decoder.Decode(&request); err != nil {
		writeDashboardChatError(w, http.StatusBadRequest, "요청 형식이 올바르지 않습니다.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeDashboardChatError(w, http.StatusBadRequest, "요청에는 하나의 JSON 객체만 사용할 수 있습니다.")
		return
	}

	request.Sender = cleanChatLine(request.Sender)
	request.Message = cleanChatLine(request.Message)
	nameLength := utf8.RuneCountInString(request.Sender)
	if nameLength < 1 || nameLength > dashboardChatNameMaxRunes {
		writeDashboardChatError(w, http.StatusBadRequest, "사용자명은 1~8글자로 입력해 주세요.")
		return
	}
	messageLength := utf8.RuneCountInString(request.Message)
	if messageLength < 1 || messageLength > dashboardChatTextMaxRunes {
		writeDashboardChatError(w, http.StatusBadRequest, "메시지는 1~120글자로 입력해 주세요.")
		return
	}

	s.chatMu.RLock()
	broadcaster := s.worldChatBroadcaster
	s.chatMu.RUnlock()
	if broadcaster == nil {
		writeDashboardChatError(w, http.StatusServiceUnavailable, "월드 채팅이 아직 준비되지 않았습니다.")
		return
	}
	if err := broadcaster(request.Sender, request.Message); err != nil {
		if s.logger != nil {
			s.logger.Warn("Dashboard: failed to broadcast world chat", zap.Error(err))
		}
		writeDashboardChatError(w, http.StatusBadGateway, "게임 월드로 메시지를 전송하지 못했습니다.")
		return
	}

	s.recordDashboardChat("web", "world", request.Sender, request.Message)
	s.writeDashboardChatResponse(w, http.StatusCreated)
}

func (s *APIServer) writeDashboardChatResponse(w http.ResponseWriter, status int) {
	messages, err := s.dashboardChatSnapshot()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("Dashboard: failed to load chat history", zap.Error(err))
		}
		if status != http.StatusCreated {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// POST already broadcast successfully. A failed history read must
		// not prompt the operator to send that same message again.
	}
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(dashboardChatResponse{Messages: messages}); err != nil && s.logger != nil {
		s.logger.Error("Dashboard: failed to encode chat response", zap.Error(err))
	}
}

func writeDashboardChatError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func cleanChatLine(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		default:
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}
	}, value)
	return strings.TrimSpace(value)
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximum])
}
