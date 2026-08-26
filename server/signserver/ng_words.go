package signserver

import (
	"erupe-ce/common/stringsupport"
)

const (
	// The PC clients reserve exactly 0x3000 bytes for the complete smc/nam/msg
	// filter block. The parser reads one additional record header after the
	// final msg record, so keep eight zero-filled bytes at the end of the slot.
	maxNGWordFilterBytes        = 0x3000
	maxNGWordFilterPayloadBytes = maxNGWordFilterBytes - 8
	maxClientNGWordParts        = 0xFF
)

// nameSyntaxNGWords are name-only blockers. They are intentionally not sent
// in the chat table, where spaces and standalone Hangul jamo are legitimate.
var nameSyntaxNGWords = buildNameSyntaxNGWords()

func buildNameSyntaxNGWords() []string {
	words := []string{" ", "\t", "\r", "\n", "\u3000"}
	for r := rune(0x3131); r <= 0x318E; r++ {
		words = append(words, string(r))
	}
	return words
}

func loadNGWordsCSV(path string) ([]string, error) {
	return stringsupport.LoadNGWordsCSV(path)
}

func ngWordEntrySize(parts []uint16) int {
	return 8 + 4*len(parts)
}

// selectNGWordParts reserves the exact fixed/filter-header size and chooses
// representable source-order entries that fit in the client's fixed block.
// Structural name blockers are kept first; each configured word is then added
// to both the name and message tables without ever wrapping the payload length.
func selectNGWordParts(baseFilterLen int, words []string) (nameParts, messageParts [][]uint16, selected int) {
	const (
		twoTableHeaders     = 16 // "nam\0"+len and "msg\0"+len
		twoTableTerminators = 8  // independent uint32(0) terminators
	)
	remaining := maxNGWordFilterPayloadBytes - baseFilterLen - twoTableHeaders - twoTableTerminators
	if remaining <= 0 {
		return nil, nil, 0
	}
	for _, word := range nameSyntaxNGWords {
		parts := stringsupport.ToNGWordCP949(word)
		if len(parts) == 0 || len(parts) > maxClientNGWordParts {
			continue
		}
		entrySize := ngWordEntrySize(parts)
		if entrySize > remaining {
			break
		}
		nameParts = append(nameParts, parts)
		remaining -= entrySize
	}
	for _, word := range words {
		parts := stringsupport.ToNGWordCP949(word)
		if len(parts) == 0 || len(parts) > maxClientNGWordParts {
			continue
		}
		entrySize := ngWordEntrySize(parts)
		if entrySize*2 > remaining {
			break
		}
		nameParts = append(nameParts, parts)
		messageParts = append(messageParts, parts)
		remaining -= entrySize * 2
		selected++
	}
	return nameParts, messageParts, selected
}
