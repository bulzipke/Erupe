package channelserver

import (
	"strings"
	"testing"
	"unicode/utf8"

	"erupe-ce/common/stringsupport"
)

func TestDiscordRelayNamePreservesKorean(t *testing.T) {
	for _, tt := range []struct {
		username string
		want string
	}{
		{" 한글 ", "한글    "},
		{"Hunter", "Hunter  "},
		{"한글이름", "한글이름"},
		{"한😀글", "한글    "},
	} {
		if got := discordRelayName(tt.username); got != tt.want {
			t.Errorf("username %q: got %q, want %q", tt.username, got, tt.want)
		}
	}
}

func TestDiscordRelayLinesUseCP949ByteLimits(t *testing.T) {
	// This occupies 122 CP949 bytes but 182 UTF-8 bytes. It fits the
	// configured wire limit and the first 61-byte line ends before a syllable.
	message := "A" + strings.Repeat("한", 60) + "B"
	lines := discordRelayLines(message, 122)
	if len(lines) != 2 || strings.Join(lines, "") != message {
		t.Fatalf("split lines did not preserve the message: %#v", lines)
	}
	for _, line := range lines {
		if !utf8.ValidString(line) || len(stringsupport.UTF8ToSJIS(line)) > 61 {
			t.Errorf("invalid relay line %q", line)
		}
	}
	if lines := discordRelayLines(message, 121); len(lines) != 0 {
		t.Fatalf("over-limit message was accepted: %#v", lines)
	}
}

func TestDiscordRelayLinesDoNotSplitExtensionHangul(t *testing.T) {
	message := strings.Repeat("똠", 61)
	lines := discordRelayLines(message, 200)
	if len(lines) != 3 || strings.Join(lines, "") != message {
		t.Fatalf("extension Hangul did not survive the relay: %#v", lines)
	}
	for _, line := range lines {
		if strings.ContainsRune(line, utf8.RuneError) {
			t.Errorf("replacement character in line %q", line)
		}
	}
}
