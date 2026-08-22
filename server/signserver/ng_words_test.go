package signserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNGWordsCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ng.csv")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbfword\n씨발\nＳＨＩＴ\nshit\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	words, err := loadNGWordsCSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[0] != "씨발" || words[1] != "SHIT" {
		t.Fatalf("loadNGWordsCSV() = %#v", words)
	}
}

func TestSelectNGWordPartsFitsProtocolLimit(t *testing.T) {
	words := make([]string, 10000)
	for i := range words {
		words[i] = "금칙어테스트"
	}
	name, message, selected := selectNGWordParts(8000, words)
	used := 8000 + 16
	for _, parts := range name {
		used += ngWordEntrySize(parts)
	}
	for _, parts := range message {
		used += ngWordEntrySize(parts)
	}
	if used > maxNGWordFilterBytes {
		t.Fatalf("selected payload size %d exceeds %d", used, maxNGWordFilterBytes)
	}
	if selected == len(words) {
		t.Fatal("oversized list was not truncated")
	}
}
