package stringsupport

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// NGWordFilter is an immutable, normalized substring filter for player input.
// It intentionally keeps matching contiguous: structural name rules are
// handled separately by IsValidPlayerName.
type NGWordFilter struct {
	words []string
}

// NewNGWordFilter builds a case-insensitive NFKC-normalized word filter.
func NewNGWordFilter(words []string) *NGWordFilter {
	seen := make(map[string]struct{}, len(words))
	normalized := make([]string, 0, len(words))
	for _, word := range words {
		word = normalizeNGText(word)
		if word == "" {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		normalized = append(normalized, word)
	}
	return &NGWordFilter{words: normalized}
}

// Contains reports whether text contains a configured NG word after Unicode
// compatibility normalization and case folding.
func (f *NGWordFilter) Contains(text string) bool {
	if f == nil || len(f.words) == 0 {
		return false
	}
	text = normalizeNGText(text)
	for _, word := range f.words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// WordCount returns the number of distinct active words.
func (f *NGWordFilter) WordCount() int {
	if f == nil {
		return 0
	}
	return len(f.words)
}

func normalizeNGText(text string) string {
	return strings.ToLower(norm.NFKC.String(strings.TrimSpace(text)))
}

// LoadNGWordsCSV reads the first column of a UTF-8 CSV. A leading BOM and a
// word/slang header are accepted for compatibility with existing lists.
func LoadNGWordsCSV(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	seen := make(map[string]struct{})
	var words []string
	for row := 1; ; row++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row %d: %w", row, err)
		}
		if len(record) == 0 {
			continue
		}
		word := strings.TrimPrefix(record[0], "\ufeff")
		word = norm.NFKC.String(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		if row == 1 && (strings.EqualFold(word, "word") || strings.EqualFold(word, "slang")) {
			continue
		}
		key := strings.ToLower(word)
		if _, ok := seen[key]; ok {
			continue
		}
		if len(ToNGWordCP949(word)) == 0 {
			continue
		}
		seen[key] = struct{}{}
		words = append(words, word)
	}
	return words, nil
}
