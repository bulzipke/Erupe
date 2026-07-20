package channelserver

func langKorean() i18n {
	var i i18n

	i.language = "한국어"
	i.cafe.reset = "%d/%d에 리셋"
	i.timer = "타이머: %02d'%02d\"%02d.%03d (%df)"

	i.commands.noOp = "이 명령어를 사용할 권한이 없습니다"
	i.commands.disabled = "%s 명령어는 비활성화되어 있습니다"
	i.commands.reload = "리로드합니다"
	i.commands.kqf.get = "현재 키퀘스트 플래그: %x"
	i.commands.kqf.set.error = "키퀘스트 명령어 오류. 예: %s set xxxxxxxxxxxxxxxx"
	i.commands.kqf.set.success = "키퀘스트 플래그가 갱신되었습니다. 월드/랜드를 이동해 주십시오"
	i.commands.kqf.version = "이 명령어는 MHFG10 이전 버전에서는 사용할 수 없습니다"
	i.commands.rights.error = "코스 갱신 명령어 오류. 예: %s x"
	i.commands.rights.success = "코스 정보를 갱신했습니다: %d"
	i.commands.course.error = "코스 확인 명령어 오류. 예: %s <name>"
	i.commands.course.disabled = "%s 코스는 비활성화되어 있습니다"
	i.commands.course.enabled = "%s 코스는 활성화되어 있습니다"
	i.commands.course.locked = "%s 코스는 잠겨 있습니다"
	i.commands.teleport.error = "텔레포트 명령어 오류. 구문: %s x y"
	i.commands.teleport.success = "%d %d(으)로 텔레포트"
	i.commands.psn.error = "PSN 연동 명령어 오류. 예: %s <psn id>"
	i.commands.psn.success = "PSN 「%s」이(가) 연동되었습니다"
	i.commands.psn.exists = "이 PSN은 이미 다른 유저에 연결되어 있습니다"

	i.commands.discord.success = "당신의 Discord 토큰: %s"

	i.commands.ban.noUser = "유저를 찾을 수 없습니다"
	i.commands.ban.success = "%s을(를) 정지 처리했습니다"
	i.commands.ban.invalid = "잘못된 캐릭터 ID입니다"
	i.commands.ban.error = "명령어 오류. 예: %s <id> [기간]"
	i.commands.ban.length = " ~%s까지"

	i.commands.playtime = "플레이 시간: %d시간 %d분 %d초"

	i.commands.timer.enabled = "퀘스트 타이머가 활성화되었습니다"
	i.commands.timer.disabled = "퀘스트 타이머가 비활성화되었습니다"

	i.commands.lang.usage = "사용법: %s <en|jp|fr|es|zh|ko>"
	i.commands.lang.invalid = "지원하지 않는 언어 %q. 지원 언어: en, jp, fr, es, zh, ko"
	i.commands.lang.success = "언어를 %s(으)로 설정했습니다"
	i.commands.lang.current = "현재 언어: %s"

	i.commands.ravi.noCommand = "라비 명령어가 지정되지 않았습니다"
	i.commands.ravi.start.success = "대토벌을 시작합니다"
	i.commands.ravi.start.error = "대토벌이 이미 개최 중입니다"
	i.commands.ravi.multiplier = "라비 대미지 배율: x%.2f"
	i.commands.ravi.res.success = "부활 지원을 실행합니다"
	i.commands.ravi.res.error = "부활 지원이 실행되지 않았습니다"
	i.commands.ravi.sed.success = "진정 지원을 실행합니다"
	i.commands.ravi.request = "진정 지원을 요청합니다"
	i.commands.ravi.error = "라비 명령어를 인식할 수 없습니다"
	i.commands.ravi.noPlayers = "아무도 대토벌에 참가하지 않았습니다"
	i.commands.ravi.version = "이 명령어는 MHFZZ 이외에서는 사용할 수 없습니다"

	i.raviente.berserk = "<대토벌: 맹광기>가 개최되었습니다!"
	i.raviente.extreme = "<대토벌: 맹광기[극]>이 개최되었습니다!"
	i.raviente.extremeLimited = "<대토벌: 맹광기[극](제한부)>가 개최되었습니다!"
	i.raviente.berserkSmall = "<대토벌: 맹광기(소인원)>가 개최되었습니다!"

	i.guild.rookieGuildName = "신입 수렵단%d"
	i.guild.returnGuildName = "복귀 수렵단%d"

	i.guild.invite.title = "수렵단 권유 안내"
	i.guild.invite.body = "수렵단 「%s」의 권유 알림입니다.\n「권유에 응답」에서 응답해 주십시오."

	i.guild.invite.success.title = "성공"
	i.guild.invite.success.body = "「%s」에 참가했습니다."

	i.guild.invite.accepted.title = "승낙되었습니다"
	i.guild.invite.accepted.body = "초대한 헌터가 「%s」의 초대를 승낙했습니다."

	i.guild.invite.rejected.title = "거절했습니다"
	i.guild.invite.rejected.body = "「%s」의 참가를 거절했습니다."

	i.guild.invite.declined.title = "사퇴했습니다"
	i.guild.invite.declined.body = "초대한 헌터가 「%s」의 초대를 사퇴했습니다."

	i.beads = []Bead{
		{1, "폭풍의 기원주", "폭풍의 힘이 깃든 기원주.\n폭풍을 부르는 힘으로 동료를 고무한다."},
		{3, "단력의 기원주", "단력의 힘이 깃든 기원주.\n참격의 힘을 동료에게 내린다."},
		{4, "활력의 기원주", "활력의 힘이 깃든 기원주.\n체력을 높이는 힘으로 동료를 고무한다."},
		{8, "치유의 기원주", "치유의 힘이 깃든 기원주.\n회복의 힘으로 동료를 지킨다."},
		{9, "격앙의 기원주", "격앙의 힘이 깃든 기원주.\n분노의 힘을 동료에게 준다."},
		{10, "장기의 기원주", "장기의 힘이 깃든 기원주.\n독안개의 힘을 동료에게 준다."},
		{11, "강력의 기원주", "강력의 힘이 깃든 기원주.\n강대한 힘을 동료에게 내린다."},
		{14, "뇌광의 기원주", "뇌광의 힘이 깃든 기원주.\n번개의 힘을 동료에게 준다."},
		{15, "빙결의 기원주", "빙결의 힘이 깃든 기원주.\n냉기의 힘을 동료에게 준다."},
		{17, "염열의 기원주", "염열의 힘이 깃든 기원주.\n불꽃의 힘을 동료에게 준다."},
		{18, "수류의 기원주", "수류의 힘이 깃든 기원주.\n물의 힘을 동료에게 준다."},
		{19, "용기의 기원주", "용기(龍氣)의 힘이 깃든 기원주.\n용속성의 힘을 동료에게 준다."},
		{20, "대지의 기원주", "대지의 힘이 깃든 기원주.\n대지의 힘을 동료에게 준다."},
		{21, "질풍의 기원주", "질풍의 힘이 깃든 기원주.\n민첩함을 높이는 힘을 동료에게 준다."},
		{22, "광휘의 기원주", "광휘의 힘이 깃든 기원주.\n빛의 힘으로 동료를 고무한다."},
		{23, "암영의 기원주", "암영의 힘이 깃든 기원주.\n어둠의 힘을 동료에게 준다."},
		{24, "강철의 기원주", "강철의 힘이 깃든 기원주.\n방어력을 높이는 힘을 동료에게 준다."},
		{25, "봉속의 기원주", "봉속의 힘이 깃든 기원주.\n속성을 봉인하는 힘을 동료에게 준다."},
	}

	return i
}
