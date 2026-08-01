package mhfquest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"erupe-ce/common/decryption"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

var questVariantSuffixes = []string{"d0", "n0", "d1", "n1", "d2", "n2"}

// ResolveTitle returns the best available title for a quest. Manual Korean
// overrides win, followed by localized JSON and the CP949 title embedded in
// any Korean client binary variant.
func ResolveTitle(binPath string, questID uint16, lang string) (string, bool) {
	if questID == 0 {
		return "", false
	}
	if strings.EqualFold(lang, "ko") {
		if title, ok := KoreanName(questID); ok {
			return title, true
		}
	}

	questsDir := filepath.Join(binPath, "quests")
	for _, suffix := range questVariantSuffixes {
		path := filepath.Join(questsDir, fmt.Sprintf("%05d%s.json", questID, suffix))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if title := titleFromJSON(data, lang); title != "" {
			return title, true
		}
	}

	for _, suffix := range questVariantSuffixes {
		path := filepath.Join(questsDir, fmt.Sprintf("%05d%s.bin", questID, suffix))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if title := titleFromBinary(decryption.UnpackSimple(data)); title != "" {
			return title, true
		}
	}

	return "", false
}

func titleFromJSON(data []byte, lang string) string {
	var quest struct {
		Title json.RawMessage `json:"title"`
	}
	if json.Unmarshal(data, &quest) != nil || len(quest.Title) == 0 {
		return ""
	}

	var plain string
	if json.Unmarshal(quest.Title, &plain) == nil {
		return normalizeTitle(plain)
	}

	var localized map[string]string
	if json.Unmarshal(quest.Title, &localized) != nil {
		return ""
	}
	for _, key := range []string{strings.ToLower(lang), "ko", "jp", "en"} {
		if title := normalizeTitle(localized[key]); title != "" {
			return title
		}
	}
	for _, title := range localized {
		if title = normalizeTitle(title); title != "" {
			return title
		}
	}
	return ""
}

// titleFromBinary reads only the stable pointers needed for the title. Korean
// client quest binaries use CP949 for their embedded strings.
func titleFromBinary(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	main := int(binary.LittleEndian.Uint32(data[:4]))
	if main < 0 || main+0x2c > len(data) {
		return ""
	}
	stringTable := int(binary.LittleEndian.Uint32(data[main+0x28 : main+0x2c]))
	if stringTable < 0 || stringTable+4 > len(data) {
		return ""
	}
	titleOffset := int(binary.LittleEndian.Uint32(data[stringTable : stringTable+4]))
	if titleOffset < 0 || titleOffset >= len(data) {
		return ""
	}
	end := titleOffset
	for end < len(data) && data[end] != 0 {
		end++
	}
	if end == titleOffset {
		return ""
	}
	decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), data[titleOffset:end])
	if err != nil {
		return ""
	}
	return normalizeTitle(string(decoded))
}

func normalizeTitle(title string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
}
