package discordbot

import "github.com/bwmarrin/discordgo"

// commandsForLanguage copies registered commands so Korean descriptions do not
// mutate the English definitions used by another bot instance.
func commandsForLanguage(language string) []*discordgo.ApplicationCommand {
	if language != "ko" {
		return Commands
	}
	commands := make([]*discordgo.ApplicationCommand, len(Commands))
	for index, original := range Commands {
		command := *original
		switch command.Name {
		case "link":
			command.Description = "Erupe 계정을 Discord에 연동합니다"
		case "password":
			command.Description = "Erupe 계정의 비밀번호를 변경합니다"
		}
		command.Options = make([]*discordgo.ApplicationCommandOption, len(original.Options))
		for optionIndex, originalOption := range original.Options {
			option := *originalOption
			switch command.Name {
			case "link":
				option.Description = "게임 내 Discord 명령어로 발급받은 토큰"
			case "password":
				option.Description = "새 비밀번호"
			}
			command.Options[optionIndex] = &option
		}
		commands[index] = &command
	}
	return commands
}
