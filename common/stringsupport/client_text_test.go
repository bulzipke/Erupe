package stringsupport

import (
	"bytes"
	"testing"
)

func TestPaddedStringCP949CharacterBoundary(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		size uint
		want string
	}{
		{"zero width", "한글", 0, ""},
		{"only terminator", "한글", 1, ""},
		{"no room for syllable", "한글", 2, ""},
		{"one syllable fits", "한글", 3, "한"},
		{"odd limit", "한글", 4, "한"},
		{"exact fit", "한글", 5, "한글"},
		{"mixed ASCII", "A한글", 5, "A한"},
		{"unsupported rune does not blank field", "앞😀뒤", 8, "앞뒤"},
		{"extension syllable", "똠똠", 4, "똠"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := PaddedString(tt.text, tt.size, true)
			if len(got) != int(tt.size) {
				t.Fatalf("size = %d, want %d", len(got), tt.size)
			}
			want := make([]byte, tt.size)
			copy(want, UTF8ToSJIS(tt.want))
			if !bytes.Equal(got, want) {
				t.Fatalf("padded bytes = %x, want %x", got, want)
			}
			if decoded := SJISToUTF8Lossy(bytes.TrimRight(got, "\x00")); decoded != tt.want {
				t.Fatalf("round trip = %q, want %q", decoded, tt.want)
			}
		})
	}
}

func TestTruncateCP949PreservesCompletePairs(t *testing.T) {
	// Extension Hangul can have ASCII-valued trail bytes, so checking only
	// the final byte cannot reliably identify a character boundary.
	encoded := UTF8ToSJIS("A똠한B")
	for _, tt := range []struct {
		limit int
		want  string
	}{
		{-1, ""}, {0, ""}, {1, "A"}, {2, "A"}, {3, "A똠"},
		{4, "A똠"}, {5, "A똠한"}, {6, "A똠한B"}, {7, "A똠한B"},
	} {
		got := TruncateCP949(encoded, tt.limit)
		if want := UTF8ToSJIS(tt.want); !bytes.Equal(got, want) {
			t.Errorf("limit %d: got %x, want %x", tt.limit, got, want)
		}
	}
}
