package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardChatKeepsLatestTenMessages(t *testing.T) {
	server := &APIServer{}
	for i := 0; i < 12; i++ {
		server.RecordWorldChat(fmt.Sprintf("Hunter%02d", i), fmt.Sprintf("message-%02d", i))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/chat", nil)
	rec := httptest.NewRecorder()
	server.DashboardChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response dashboardChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Messages) != dashboardChatHistoryLimit {
		t.Fatalf("message count = %d, want %d", len(response.Messages), dashboardChatHistoryLimit)
	}
	if response.Messages[0].Sender != "Hunter02" || response.Messages[9].Sender != "Hunter11" {
		t.Fatalf("unexpected retained range: first=%q last=%q", response.Messages[0].Sender, response.Messages[9].Sender)
	}
	if response.Messages[0].Source != "game" || response.Messages[0].Scope != "world" || response.Messages[0].Time.IsZero() {
		t.Fatalf("unexpected message metadata: %+v", response.Messages[0])
	}
}

func TestDashboardChatRecordsPublicScopesOnly(t *testing.T) {
	server := &APIServer{}
	for _, scope := range []string{"world", "land", "party", "guild", "alliance", "whisper", "unknown"} {
		server.RecordGameChat(scope, "Hunter", scope+" message")
	}

	messages := server.dashboardChatSnapshot()
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3: %+v", len(messages), messages)
	}
	for i, want := range []string{"world", "land", "party"} {
		if messages[i].Scope != want {
			t.Fatalf("message %d scope = %q, want %q", i, messages[i].Scope, want)
		}
	}
}

func TestDashboardChatPostBroadcastsAndRecords(t *testing.T) {
	server := &APIServer{}
	var gotSender, gotMessage string
	server.SetWorldChatBroadcaster(func(sender, message string) error {
		gotSender = sender
		gotMessage = message
		return nil
	})

	body := bytes.NewBufferString(`{"sender":"웹헌터","message":"안녕하세요 월드!"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/chat", body)
	rec := httptest.NewRecorder()
	server.DashboardChat(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if gotSender != "웹헌터" || gotMessage != "안녕하세요 월드!" {
		t.Fatalf("broadcast = %q/%q", gotSender, gotMessage)
	}

	var response dashboardChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Messages) != 1 || response.Messages[0].Source != "web" || response.Messages[0].Scope != "world" {
		t.Fatalf("unexpected chat history: %+v", response.Messages)
	}
}

func TestDashboardChatPostValidatesNameAndMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "name too long", body: `{"sender":"123456789","message":"hello"}`},
		{name: "empty name", body: `{"sender":" ","message":"hello"}`},
		{name: "message too long", body: fmt.Sprintf(`{"sender":"web","message":"%s"}`, strings.Repeat("가", 121))},
		{name: "empty message", body: `{"sender":"web","message":" "}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &APIServer{}
			called := false
			server.SetWorldChatBroadcaster(func(_, _ string) error {
				called = true
				return nil
			})

			req := httptest.NewRequest(http.MethodPost, "/api/dashboard/chat", bytes.NewBufferString(test.body))
			rec := httptest.NewRecorder()
			server.DashboardChat(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if called {
				t.Fatal("invalid message reached broadcaster")
			}
		})
	}
}

func TestDashboardChatPostRequiresChannelBridge(t *testing.T) {
	server := &APIServer{}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/dashboard/chat",
		bytes.NewBufferString(`{"sender":"web","message":"hello"}`),
	)
	rec := httptest.NewRecorder()
	server.DashboardChat(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
