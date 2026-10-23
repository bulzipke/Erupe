package channelserver

import (
	"fmt"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// Korean quest/scenario JSON targets the CP949-patched client. Keep the
// original Shift-JIS format for other languages and binary editing tools
// which omit the language. Encoding cannot be guessed from the bytes: valid
// CP949 Hangul often also decodes as unrelated Shift-JIS characters.
func contentTextEncoding(lang ...string) encoding.Encoding {
	if len(lang) > 0 && lang[0] == "ko" {
		return korean.EUCKR
	}
	return japanese.ShiftJIS
}

func encodeContentText(s string, lang ...string) ([]byte, error) {
	enc := contentTextEncoding(lang...)
	out, _, err := transform.Bytes(enc.NewEncoder(), []byte(s))
	if err != nil {
		return nil, fmt.Errorf("%s encode %q: %w", enc, s, err)
	}
	return append(out, 0), nil
}

func decodeContentText(b []byte, lang ...string) (string, error) {
	enc := contentTextEncoding(lang...)
	out, _, err := transform.Bytes(enc.NewDecoder(), b)
	if err != nil {
		return "", fmt.Errorf("%s decode: %w", enc, err)
	}
	return string(out), nil
}
