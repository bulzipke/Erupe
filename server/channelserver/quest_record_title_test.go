package channelserver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestQuestTitleForRecordPrefersKoreanJSON(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.BinPath = t.TempDir()
	server.questCache = NewQuestCache(0)
	questsDir := filepath.Join(server.erupeConfig.BinPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatalf("create quest directory: %v", err)
	}
	questID := uint16(12345)
	questJSON := []byte(`{"quest_id":12345,"title":{"jp":"日本語","ko":"한글 퀘스트"}}`)
	path := filepath.Join(questsDir, fmt.Sprintf("%05dd0.json", questID))
	if err := os.WriteFile(path, questJSON, 0o600); err != nil {
		t.Fatalf("write quest JSON: %v", err)
	}

	session := createMockSession(1, server)
	if got := questTitleForRecord(session, questID); got != "한글 퀘스트" {
		t.Errorf("quest title = %q, want %q", got, "한글 퀘스트")
	}
}

func TestQuestTitleForRecordPrefersManualKoreanOverride(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.BinPath = t.TempDir()
	session := createMockSession(1, server)

	if got := questTitleForRecord(session, 53187); got != "≪이벤트 퀘스트≫ 사냥 지원! 출발 전 사전 준비" {
		t.Errorf("quest title = %q", got)
	}
}
