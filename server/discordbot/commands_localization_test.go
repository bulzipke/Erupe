package discordbot

import (
	"strings"
	"testing"
)

func TestRegisterCommands_KoreanDescriptionsAreIsolated(t *testing.T) {
	session := &mockSession{}
	bot := newTestBot(session)
	bot.config.Language = "ko"
	if err := bot.RegisterCommands(); err != nil {
		t.Fatal(err)
	}
	for index, command := range session.bulkOverwriteCommands {
		if !strings.Contains(command.Description, "계정") || command.Name != Commands[index].Name {
			t.Fatalf("unexpected Korean command: %+v", command)
		}
		for optionIndex, option := range command.Options {
			if option.Description == Commands[index].Options[optionIndex].Description || option.Name != Commands[index].Options[optionIndex].Name {
				t.Fatalf("option was not translated or its protocol name changed: %+v", option)
			}
		}
	}
	bot.config.Language = "en"
	if err := bot.RegisterCommands(); err != nil {
		t.Fatal(err)
	}
	if session.bulkOverwriteCommands[0].Description != "Link your Erupe account to Discord" {
		t.Fatal("Korean registration changed the shared English command definitions")
	}
}

func TestNormalizeDiscordMessage_KoreanUnknownMention(t *testing.T) {
	bot := newTestBot(&mockSession{})
	bot.config.Language = "ko"
	if got := bot.NormalizeDiscordMessage("안녕 <@12345678901234567>"); got != "안녕 @알수없음" {
		t.Fatalf("unknown mention = %q", got)
	}
}
