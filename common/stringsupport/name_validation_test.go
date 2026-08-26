package stringsupport

import (
	"reflect"
	"testing"
)

func TestIsValidPlayerName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"코코샤넬", true},
		{"Hunter01", true},
		{"한글이름", true},
		{"", false},
		{"   ", false},
		{"코코 샤넬", false},
		{"코코\t샤넬", false},
		{"ㄱㄴ", false},
		{"ㅏㅓ", false},
		{"가", false},
		{"코코ㄱ", false},
		{"!!!", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidPlayerName(tt.name); got != tt.want {
				t.Fatalf("IsValidPlayerName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestToNGWordCP949UsesPatchedClientCP949Tokenization(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []uint16
	}{
		{
			name: "Hangul syllables remain CP949 pairs",
			text: "시발", // CP949: BD C3 B9 DF
			want: []uint16{0xC3BD, 0xDFB9},
		},
		{
			name: "CP949 trail FE stays in its syllable",
			text: "샤넬", // CP949: BB FE B3 DA
			want: []uint16{0xFEBB, 0xDAB3},
		},
		{
			name: "ASCII remains one token per byte",
			text: "fuck",
			want: []uint16{0x66, 0x75, 0x63, 0x6B},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToNGWordCP949(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ToNGWordCP949(%q) = %#v, want %#v", tt.text, got, tt.want)
			}
		})
	}
}
