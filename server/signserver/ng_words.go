package signserver

import (
	"erupe-ce/common/stringsupport"
)

const maxNGWordFilterBytes = int(^uint16(0))

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
// the largest source-order prefix that fits in the protocol's uint16 payload.
// Structural name blockers are kept first; each configured word is then added
// to both the name and message tables without ever wrapping the payload length.
func selectNGWordParts(baseFilterLen int, words []string) (nameParts, messageParts [][]uint16, selected int) {
	const twoTableHeaders = 16 // "nam\0"+len and "msg\0"+len
	remaining := maxNGWordFilterBytes - baseFilterLen - twoTableHeaders
	if remaining <= 0 {
		return nil, nil, 0
	}
	for _, word := range nameSyntaxNGWords {
		parts := stringsupport.ToNGWordCP949(word)
		if len(parts) == 0 {
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
		if len(parts) == 0 {
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
