package api

import (
	"strings"
	"testing"
)

func TestNewAuthData_LocalizesBuiltInLoginNotice(t *testing.T) {
	config := NewTestConfig()
	config.Language = "ko"
	config.HideLoginNotice = false
	config.LoginNotices = []string{
		"<BODY><CENTER><SIZE_3><C_4>Welcome to Erupe!",
		"운영자 공지",
	}
	server := &APIServer{
		logger: NewTestLogger(t),
		erupeConfig: config,
		userRepo: newTestUserRepo(),
	}
	data := server.newAuthData(1, 0, 1, "test-token", nil)
	if len(data.Notices) != 1 || strings.Contains(data.Notices[0], "Welcome") || !strings.HasSuffix(data.Notices[0], "<PAGE>운영자 공지") {
		t.Fatalf("launcher notices were not localized correctly: %q", data.Notices)
	}
	config.Language = "en"
	data = server.newAuthData(1, 0, 1, "test-token", nil)
	if len(data.Notices) != 1 || !strings.Contains(data.Notices[0], "Welcome to Erupe!") {
		t.Fatalf("English launcher notice changed: %q", data.Notices)
	}
}
