package config

import "testing"

func TestLocalizedLoginNotices_PreservesCustomNotices(t *testing.T) {
	config := Config{Language: "ko", LoginNotices: []string{defaultLoginNotice, "Custom operator notice"}}
	got := config.LocalizedLoginNotices()
	if got[0] != "<BODY><CENTER><SIZE_3><C_4>Erupe에 오신 것을 환영합니다!" || got[1] != config.LoginNotices[1] {
		t.Fatalf("unexpected notices: %q", got)
	}
	if config.LoginNotices[0] != defaultLoginNotice {
		t.Fatal("localization mutated the configured notices")
	}
	config.Language = "en"
	if got := config.LocalizedLoginNotices(); got[0] != defaultLoginNotice {
		t.Fatalf("English default changed: %q", got)
	}
}
