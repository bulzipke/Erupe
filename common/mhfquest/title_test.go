package mhfquest

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTitleUsesNonDefaultJSONVariant(t *testing.T) {
	binPath := t.TempDir()
	questsDir := filepath.Join(binPath, "quests")
	if err := os.MkdirAll(questsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(questsDir, "12345d1.json"), []byte(`{"title":{"jp":"日本語","ko":"한국어 제목"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	title, ok := ResolveTitle(binPath, 12345, "ko")
	if !ok || title != "한국어 제목" {
		t.Fatalf("ResolveTitle() = %q, %v", title, ok)
	}
}

func TestTitleFromBinaryPointers(t *testing.T) {
	data := make([]byte, 0x180)
	binary.LittleEndian.PutUint32(data[0:4], 0x40)
	binary.LittleEndian.PutUint32(data[0x68:0x6c], 0x100)
	binary.LittleEndian.PutUint32(data[0x100:0x104], 0x120)
	copy(data[0x120:], []byte("Quest\nTest\x00"))

	if got := titleFromBinary(data); got != "Quest Test" {
		t.Fatalf("titleFromBinary() = %q", got)
	}
}

func TestTitleFromKoreanLocalizedBinaryPointers(t *testing.T) {
	data := make([]byte, 0x180)
	binary.LittleEndian.PutUint32(data[0:4], 0x40)
	binary.LittleEndian.PutUint32(data[0x68:0x6c], 0x100)
	binary.LittleEndian.PutUint32(data[0x100:0x104], 0x120)
	// CP949 for "≪늪지 탐색≫ 일격필살?".
	copy(data[0x120:], []byte{0xa1, 0xec, 0xb4, 0xcb, 0xc1, 0xf6, 0x20, 0xc5, 0xbd, 0xbb, 0xf6, 0xa1, 0xed, 0x20, 0xc0, 0xcf, 0xb0, 0xdd, 0xc7, 0xca, 0xbb, 0xec, 0x3f, 0x00})

	if got := titleFromBinary(data); got != "≪늪지 탐색≫ 일격필살?" {
		t.Fatalf("titleFromBinary() = %q", got)
	}
}

func TestResolveTitleForBundledQuest23618(t *testing.T) {
	binPath := filepath.Join("..", "..", "bin")
	if _, err := os.Stat(filepath.Join(binPath, "quests", "23618d0.bin")); err != nil {
		t.Skip("bundled quest assets are unavailable")
	}
	title, ok := ResolveTitle(binPath, 23618, "ko")
	if !ok || title == "" || strings.HasPrefix(title, "퀘스트 #") {
		t.Fatalf("ResolveTitle(23618) = %q, %v", title, ok)
	}
	t.Logf("quest 23618 title: %s", title)
}
