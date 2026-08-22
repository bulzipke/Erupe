package stringsupport

import "testing"

func TestNGWordFilterContainsNormalizedSubstring(t *testing.T) {
	filter := NewNGWordFilter([]string{"욕설", "badword"})
	tests := []struct {
		text string
		want bool
	}{
		{"앞욕설뒤", true},
		{"BADWORD", true},
		{"ＢＡＤＷＯＲＤ", true},
		{"정상적인 문장", false},
		{"욕 설", false},
	}
	for _, tt := range tests {
		if got := filter.Contains(tt.text); got != tt.want {
			t.Fatalf("Contains(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestNilNGWordFilterAllowsText(t *testing.T) {
	var filter *NGWordFilter
	if filter.Contains("anything") {
		t.Fatal("nil filter rejected text")
	}
}
