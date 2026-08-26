package signserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erupe-ce/common/stringsupport"
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

func TestSelectNGWordPartsSkipsWordBeyondLauncherPartLimit(t *testing.T) {
	_, message, selected := selectNGWordParts(0, []string{strings.Repeat("a", 256), "ok"})
	if selected != 1 {
		t.Fatalf("selected words = %d, want 1", selected)
	}
	if len(message) != 1 {
		t.Fatalf("message entries = %d, want 1", len(message))
	}
	want := stringsupport.ToNGWordCP949("ok")
	if len(message[0]) != len(want) {
		t.Fatalf("selected fallback entry has %d parts, want %d", len(message[0]), len(want))
	}
	for i := range want {
		if message[0][i] != want[i] {
			t.Fatalf("fallback entry part %d = %04X, want %04X", i, message[0][i], want[i])
		}
	}
}

func TestSelectNGWordPartsFitsProtocolLimit(t *testing.T) {
	words := make([]string, 10000)
	for i := range words {
		words[i] = "금칙어테스트"
	}
	name, message, selected := selectNGWordParts(8000, words)
	used := 8000 + 16 + 8
	for _, parts := range name {
		used += ngWordEntrySize(parts)
	}
	for _, parts := range message {
		used += ngWordEntrySize(parts)
	}
	if used > maxNGWordFilterPayloadBytes {
		t.Fatalf("selected payload size %d exceeds %d", used, maxNGWordFilterPayloadBytes)
	}
	if selected == len(words) {
		t.Fatal("oversized list was not truncated")
	}
}

func TestSelectNGWordPartsKeepsNameSyntaxBlockers(t *testing.T) {
	name, _, _ := selectNGWordParts(3000, []string{"시발"})
	if len(name) < len(nameSyntaxNGWords) {
		t.Fatalf("name table contains %d entries, want at least %d syntax blockers", len(name), len(nameSyntaxNGWords))
	}
	for i, word := range nameSyntaxNGWords {
		want := stringsupport.ToNGWordCP949(word)
		if len(want) == 0 || len(name[i]) != len(want) {
			t.Fatalf("syntax blocker %q was not preserved at index %d", word, i)
		}
		for j := range want {
			if name[i][j] != want[j] {
				t.Fatalf("syntax blocker %q differs at part %d: got %04x want %04x", word, j, name[i][j], want[j])
			}
		}
	}
}
