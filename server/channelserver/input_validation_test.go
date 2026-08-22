package channelserver

import (
	"testing"

	"erupe-ce/common/stringsupport"
)

func TestNameInputPolicy(t *testing.T) {
	server := createMockServer()
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"욕설"})
	session := createMockSession(1, server)

	tests := []struct {
		name string
		want bool
	}{
		{"정상이름1", true},
		{"공백 이름", false},
		{"제어\n문자", false},
		{"ㄱ이름", false},
		{"!!!", false},
		{"앞욕설뒤", false},
	}
	for _, tt := range tests {
		if got := session.validateNameInput("test", tt.name); got != tt.want {
			t.Fatalf("validateNameInput(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestMessageInputPolicyOnlyChecksDictionary(t *testing.T) {
	server := createMockServer()
	server.ngWordFilter = stringsupport.NewNGWordFilter([]string{"욕설"})
	session := createMockSession(1, server)

	for _, text := range []string{"공백 포함", "ㄱㄴ", "!!!", "여러\n줄"} {
		if !session.validateMessageInput("test", text) {
			t.Fatalf("message policy unexpectedly rejected %q", text)
		}
	}
	if session.validateMessageInput("test", "앞욕설뒤") {
		t.Fatal("message policy accepted configured NG word")
	}
}
