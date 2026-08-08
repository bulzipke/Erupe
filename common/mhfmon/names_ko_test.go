package mhfmon

import "testing"

func TestKoreanName(t *testing.T) {
	tests := []struct {
		id   int
		want string
	}{
		{6, "얀쿡크"},
		{27, "도스람포스"},
		{60, "녹슨 크샬다오라"},
		{100, "안노운"},
	}
	for _, test := range tests {
		if got := KoreanName(test.id); got != test.want {
			t.Errorf("monster %d Korean name = %q, want %q", test.id, got, test.want)
		}
	}
	if got := KoreanName(0); got != "Mon0" {
		t.Errorf("untranslated monster fallback = %q, want %q", got, "Mon0")
	}
}
