package stringsupport

import "testing"

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

func TestToNGWordCP949PreservesHangul(t *testing.T) {
	got := ToNGWordCP949("샤넬")
	if len(got) != 2 {
		t.Fatalf("ToNGWordCP949(샤넬) length = %d, want 2", len(got))
	}
	if got[0] == 0 || got[1] == 0 {
		t.Fatalf("ToNGWordCP949(샤넬) contains zero: %v", got)
	}
}
