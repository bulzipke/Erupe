package channelserver

func langEnglish() i18n {
	var i i18n

	i.shutdown = "Shutting down in %d..."

	i.language = "English"
	i.cafe.reset = "Resets on %d/%d"
	i.timer = "Time: %02d:%02d:%02d.%03d (%df)"
	i.chat.ngWordRejected = "This message cannot be sent."

	i.commands.noOp = "You don't have permission to use this command"
	i.commands.disabled = "%s command is disabled"
	i.commands.reload = "Reloading players..."
	i.commands.playtime = "Playtime: %d hours %d minutes %d seconds"

	i.commands.kqf.get = "KQF: %x"
	i.commands.kqf.set.error = "Error in command. Format: %s set xxxxxxxxxxxxxxxx"
	i.commands.kqf.set.success = "KQF set, please switch Land/World"
	i.commands.kqf.version = "This command is disabled prior to MHFG10"
	i.commands.rights.error = "Error in command. Format: %s x"
	i.commands.rights.success = "Set rights integer: %d"
	i.commands.course.error = "Error in command. Format: %s <name>"
	i.commands.course.disabled = "%s Course disabled"
	i.commands.course.enabled = "%s Course enabled"
	i.commands.course.locked = "%s Course is locked"
	i.commands.teleport.error = "Error in command. Format: %s x y"
	i.commands.teleport.success = "Teleporting to %d %d"
	i.commands.psn.error = "Error in command. Format: %s <psn id>"
	i.commands.psn.success = "Connected PSN ID: %s"
	i.commands.psn.exists = "PSN ID is connected to another account!"

	i.commands.discord.success = "Your Discord token: %s"

	i.commands.ban.noUser = "Could not find user"
	i.commands.ban.success = "Successfully banned %s"
	i.commands.ban.invalid = "Invalid Character ID"
	i.commands.ban.error = "Error in command. Format: %s <id> [length]"
	i.commands.ban.length = " until %s"

	i.commands.timer.enabled = "Quest timer enabled"
	i.commands.timer.disabled = "Quest timer disabled"

	i.commands.lang.usage = "Usage: %s <en|jp|fr|es|zh|ko>"
	i.commands.lang.invalid = "Unknown language %q. Supported: en, jp, fr, es, zh, ko"
	i.commands.lang.success = "Language set to %s"
	i.commands.lang.current = "Current language: %s"

	i.commands.ravi.noCommand = "No Raviente command specified!"
	i.commands.ravi.start.success = "The Great Slaying will begin in a moment"
	i.commands.ravi.start.error = "The Great Slaying has already begun!"
	i.commands.ravi.multiplier = "Raviente multiplier is currently %.2fx"
	i.commands.ravi.res.success = "Sending resurrection support!"
	i.commands.ravi.res.error = "Resurrection support has not been requested!"
	i.commands.ravi.sed.success = "Sending sedation support if requested!"
	i.commands.ravi.request = "Requesting sedation support!"
	i.commands.ravi.error = "Raviente command not recognised!"
	i.commands.ravi.noPlayers = "No one has joined the Great Slaying!"
	i.commands.ravi.version = "This command is disabled outside of MHFZZ"

	i.raviente.berserk = "<Great Slaying: Berserk> is being held!"
	i.raviente.extreme = "<Great Slaying: Extreme> is being held!"
	i.raviente.extremeLimited = "<Great Slaying: Extreme (Limited)> is being held!"
	i.raviente.berserkSmall = "<Great Slaying: Berserk (Small)> is being held!"

	i.guild.rookieGuildName = "Rookie Clan %d"
	i.guild.returnGuildName = "Return Clan %d"

	i.guild.accepted = guildMailTemplate{"Accepted!", "Your application to join 「%s」 was accepted."}
	i.guild.rejected = guildMailTemplate{"Rejected", "Your application to join 「%s」 was rejected."}
	i.guild.kicked = guildMailTemplate{"Kicked", "You were kicked from 「%s」."}
	i.guild.withdrawal = guildMailTemplate{"Withdrawal", "You have withdrawn from 「%s」."}

	i.guild.invite.title = "Invitation!"
	i.guild.invite.body = "You have been invited to join\n「%s」\nDo you want to accept?"

	i.guild.invite.success.title = "Success!"
	i.guild.invite.success.body = "You have successfully joined\n「%s」."

	i.guild.invite.accepted.title = "Accepted"
	i.guild.invite.accepted.body = "The recipient accepted your invitation to join\n「%s」."

	i.guild.invite.rejected.title = "Rejected"
	i.guild.invite.rejected.body = "You rejected the invitation to join\n「%s」."

	i.guild.invite.declined.title = "Declined"
	i.guild.invite.declined.body = "The recipient declined your invitation to join\n「%s」."

	i.beads = divaLocalizedBeads("en")

	return i
}
