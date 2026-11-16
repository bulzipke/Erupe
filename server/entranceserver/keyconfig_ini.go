package entranceserver

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"erupe-ce/common/keyconfigsync"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/unicode"
)

type iniTextKey struct {
	section string
	name    string
}

const (
	keyconfigIniSlot = 2
	iniSpace         = " \t\r"
)

var (
	iniUTF8BOM     = []byte("\xef\xbb\xbf")
	errIniEncoding = errors.New("INI encoding rejected")
)

func validKeyconfigUpload(slot uint32, data []byte) bool {
	if slot == keyconfigIniSlot {
		if len(data) > keyconfigsync.MaxIniBytes {
			return false
		}
		data = bytes.TrimPrefix(data, iniUTF8BOM)
	}
	return keyconfigsync.ValidProfile(slot, data)
}

// Work with the native bytes and the first value of each key. All callers have
// a size-limited INI. Other settings are opaque to the encoding check.
func iniTextValues(data []byte) map[iniTextKey]string {
	values := make(map[iniTextKey]string)
	section, prefix := "", ""
	data = bytes.TrimPrefix(data, iniUTF8BOM)
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.Trim(raw, iniSpace)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToUpper(line[1 : len(line)-1])
			switch {
			case section == "FIXED_SENTENCE":
				prefix = "CWORDS_"
			case strings.HasPrefix(section, "CHATPHI_"):
				prefix = "PHINF_NAME_"
			default:
				prefix = ""
			}
			continue
		}
		if prefix == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.ToUpper(strings.Trim(name, iniSpace))
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		key := iniTextKey{section, name}
		if _, exists := values[key]; !exists {
			values[key] = strings.Trim(value, iniSpace)
		}
	}
	return values
}

func possibleIniReencoding(data []byte) map[iniTextKey]string {
	values := iniTextValues(data)
	for key, value := range values {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) == len(value) {
			delete(values, key)
		}
	}
	return values
}

func iniTextMatches(previous, candidates map[iniTextKey]string, matches func(string, string) bool) bool {
	var firstSource string
	for key, source := range previous {
		result, ok := candidates[key]
		if !ok || utf8.ValidString(source) || source == firstSource {
			continue
		}
		if !matches(source, result) {
			continue
		}
		// Two distinct matching values distinguish a file-wide rewrite from a
		// single phrase edit or a legacy byte sequence that happens to be UTF-8.
		if firstSource != "" {
			return true
		}
		firstSource = source
	}
	return false
}

func matchesDamagedUTF8(source, result string) bool {
	// A legacy file opened as UTF-8 can have invalid sequences replaced by
	// U+FFFD or spaces. Match the exact result so normal edits still sync.
	damaged, err := unicode.UTF8.NewDecoder().String(source)
	return err == nil && (damaged == result || strings.Trim(strings.ReplaceAll(damaged, "\ufffd", " "), iniSpace) == result)
}

func mayBeUTF8Ini(data []byte) bool {
	return bytes.HasPrefix(data, iniUTF8BOM) ||
		(utf8.Valid(data) && bytes.IndexFunc(data, func(r rune) bool { return r >= utf8.RuneSelf }) >= 0)
}

func validLegacyIniText(codePage encoding.Encoding, data []byte) bool {
	decoded, err := codePage.NewDecoder().Bytes(data)
	return err == nil && !bytes.ContainsRune(decoded, utf8.RuneError)
}

func normalizeIniUpload(data, previous []byte, candidates map[iniTextKey]string) ([]byte, error) {
	body, hasBOM := bytes.CutPrefix(data, iniUTF8BOM)
	codePages := [2]encoding.Encoding{korean.EUCKR, japanese.ShiftJIS}
	var matchedPage encoding.Encoding
	if len(previous) > 0 && len(candidates) >= 2 {
		oldText := iniTextValues(previous)
		if iniTextMatches(oldText, candidates, matchesDamagedUTF8) {
			return nil, fmt.Errorf("%w: matches damaged text from a UTF-8 rewrite", errIniEncoding)
		}
		for _, codePage := range codePages {
			if iniTextMatches(oldText, candidates, func(source, result string) bool {
				decoded, err := codePage.NewDecoder().String(source)
				return err == nil && decoded == result && !strings.ContainsRune(decoded, utf8.RuneError)
			}) {
				matchedPage = codePage
				break
			}
		}
	}
	if !utf8.Valid(body) {
		if hasBOM || matchedPage != nil {
			return nil, fmt.Errorf("%w: incomplete or mixed UTF-8", errIniEncoding)
		}
		return data, nil
	}
	// A BOM or matching text from a known revision establishes the encoding.
	// With neither, keep BOM-less bytes that can already be read as native text.
	if !hasBOM && matchedPage == nil &&
		(validLegacyIniText(korean.EUCKR, body) || validLegacyIniText(japanese.ShiftJIS, body)) {
		return data, nil
	}
	// Follow the account's existing code page when we can identify it. Use the
	// Korean client's CP949 first for a new account or an ambiguous baseline.
	if matchedPage == japanese.ShiftJIS || (matchedPage == nil && len(previous) > 0 &&
		!validLegacyIniText(korean.EUCKR, previous) && validLegacyIniText(japanese.ShiftJIS, previous)) {
		codePages[0], codePages[1] = codePages[1], codePages[0]
	}
	for _, codePage := range codePages {
		encoded, err := codePage.NewEncoder().Bytes(body)
		if err != nil {
			continue
		}
		decoded, err := codePage.NewDecoder().Bytes(encoded)
		if err == nil && bytes.Equal(decoded, body) && keyconfigsync.ValidProfile(keyconfigIniSlot, encoded) {
			return encoded, nil
		}
	}
	return nil, fmt.Errorf("%w: text cannot round-trip through CP949 or CP932", errIniEncoding)
}
