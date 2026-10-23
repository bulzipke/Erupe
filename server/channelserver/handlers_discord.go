package channelserver

import (
	"erupe-ce/common/stringsupport"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"golang.org/x/crypto/bcrypt"
	"strings"
)

// onInteraction handles slash commands
func (s *Server) onInteraction(ds *discordgo.Session, i *discordgo.InteractionCreate) {
	messages := discordInteractionMessages(s.erupeConfig.Language)
	switch i.Interaction.ApplicationCommandData().Name {
	case "link":
		_, err := s.userRepo.LinkDiscord(i.Member.User.ID, i.ApplicationCommandData().Options[0].StringValue())
		if err == nil {
			_ = ds.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: messages.linked,
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		} else {
			_ = ds.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: messages.linkFailed,
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		}
	case "password":
		password, err := bcrypt.GenerateFromPassword([]byte(i.ApplicationCommandData().Options[0].StringValue()), 10)
		if err != nil {
			_ = ds.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: messages.passwordHashFailed,
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
			return
		}
		err = s.userRepo.SetPasswordByDiscordID(i.Member.User.ID, password)
		if err == nil {
			_ = ds.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: messages.passwordUpdated,
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		} else {
			_ = ds.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: messages.passwordUpdateFailed,
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		}
	}
}

func discordRelayName(username string) string {
	encoded := stringsupport.UTF8ToSJIS(strings.TrimSpace(username))
	name := stringsupport.SJISToUTF8Lossy(encoded)
	if len(encoded) < 8 {
		name += strings.Repeat(" ", 8-len(encoded))
	}
	return name
}

// discordRelayLines measures the configured limit and each chat line in the
// client's CP949 bytes. Slicing the original UTF-8 string at byte 61 could cut
// a Hangul syllable in half before the chat packet encoder saw it.
func discordRelayLines(message string, maxBytes int) []string {
	encoded := stringsupport.UTF8ToSJIS(message)
	if len(encoded) > maxBytes {
		return nil
	}
	var lines []string
	for len(encoded) > 0 {
		line := stringsupport.TruncateCP949(encoded, 61)
		lines = append(lines, stringsupport.SJISToUTF8Lossy(line))
		encoded = encoded[len(line):]
	}
	return lines
}

// onDiscordMessage handles receiving messages from discord and forwarding them ingame.
func (s *Server) onDiscordMessage(ds *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from bots, or messages that are not in the correct channel.
	if m.Author.Bot || m.ChannelID != s.erupeConfig.Discord.RelayChannel.RelayChannelID {
		return
	}

	paddedName := discordRelayName(m.Author.Username)
	message := s.discordBot.NormalizeDiscordMessage(fmt.Sprintf("[D] %s > %s", paddedName, m.Content))
	for _, line := range discordRelayLines(message, s.erupeConfig.Discord.RelayChannel.MaxMessageLength) {
		s.BroadcastChatMessage(line)
	}
}
