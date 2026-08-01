package mhfquest

import "testing"

func TestKoreanName(t *testing.T) {
	got, ok := KoreanName(53187)
	if !ok || got != "≪이벤트 퀘스트≫ 사냥 지원! 출발 전 사전 준비" {
		t.Errorf("quest 53187 Korean title = %q, %v", got, ok)
	}
	if _, ok := KoreanName(1); ok {
		t.Error("unknown quest should not have a Korean override")
	}
}
