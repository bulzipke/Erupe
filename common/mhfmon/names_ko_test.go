package mhfmon

import "testing"

func TestKoreanName(t *testing.T) {
	if got := KoreanName(6); got != "얀쿡" {
		t.Errorf("monster 6 Korean name = %q, want %q", got, "얀쿡")
	}
	if got := KoreanName(0); got != "Mon0" {
		t.Errorf("untranslated monster fallback = %q, want %q", got, "Mon0")
	}
}
