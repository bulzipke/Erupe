package channelserver

type discordInteractionText struct {
	linked, linkFailed, passwordHashFailed, passwordUpdated, passwordUpdateFailed string
}

func discordInteractionMessages(language string) discordInteractionText {
	if language == "ko" {
		return discordInteractionText{
			linked: "Erupe 계정을 Discord에 연동했습니다.",
			linkFailed: "Erupe 계정을 연동하지 못했습니다.",
			passwordHashFailed: "비밀번호를 처리하지 못했습니다.",
			passwordUpdated: "Erupe 계정의 비밀번호를 변경했습니다.",
			passwordUpdateFailed: "Erupe 계정의 비밀번호를 변경하지 못했습니다.",
		}
	}
	return discordInteractionText{
		linked: "Your Erupe account was linked successfully.",
		linkFailed: "Failed to link Erupe account.",
		passwordHashFailed: "Failed to hash password.",
		passwordUpdated: "Your Erupe account password has been updated.",
		passwordUpdateFailed: "Failed to update Erupe account password.",
	}
}
