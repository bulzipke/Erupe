package channelserver

func langJapanese() i18n {
	var i i18n

	i.shutdown = "%d秒後にサーバーを終了します。"

	i.language = "日本語"
	i.cafe.reset = "%d/%dにリセット"
	i.timer = "タイマー：%02d'%02d\"%02d.%03d (%df)"
	i.chat.ngWordRejected = "このメッセージは送信できません。"

	i.commands.noOp = "このコマンドを使用する権限がありません"
	i.commands.disabled = "%sのコマンドは無効です"
	i.commands.reload = "リロードします"
	i.commands.kqf.get = "現在のキークエストフラグ：%x"
	i.commands.kqf.set.error = "キークエコマンドエラー　例：%s set xxxxxxxxxxxxxxxx"
	i.commands.kqf.set.success = "キークエストのフラグが更新されました。ワールド／ランドを移動してください"
	i.commands.kqf.version = "このコマンドはMHFG10以前では無効です"
	i.commands.rights.error = "コース更新コマンドエラー　例：%s x"
	i.commands.rights.success = "コース情報を更新しました：%d"
	i.commands.course.error = "コース確認コマンドエラー　例：%s <name>"
	i.commands.course.disabled = "%sコースは無効です"
	i.commands.course.enabled = "%sコースは有効です"
	i.commands.course.locked = "%sコースはロックされています"
	i.commands.teleport.error = "テレポートコマンドエラー　構文：%s x y"
	i.commands.teleport.success = "%d %dにテレポート"
	i.commands.psn.error = "PSN連携コマンドエラー　例：%s <psn id>"
	i.commands.psn.success = "PSN「%s」が連携されています"
	i.commands.psn.exists = "PSNは既存のユーザに接続されています"

	i.commands.discord.success = "あなたのDiscordトークン：%s"

	i.commands.ban.noUser = "ユーザーが見つかりません"
	i.commands.ban.success = "%sをBANしました"
	i.commands.ban.invalid = "無効なキャラクターIDです"
	i.commands.ban.error = "コマンドエラー　例：%s <id> [期間]"
	i.commands.ban.length = " ～%sまで"

	i.commands.playtime = "プレイ時間：%d時間%d分%d秒"

	i.commands.timer.enabled = "クエストタイマーが有効になりました"
	i.commands.timer.disabled = "クエストタイマーが無効になりました"

	i.commands.lang.usage = "使い方: %s <en|jp|fr|es|zh|ko>"
	i.commands.lang.invalid = "未対応の言語 %q。対応言語: en, jp, fr, es, zh, ko"
	i.commands.lang.success = "言語を %s に設定しました"
	i.commands.lang.current = "現在の言語: %s"

	i.commands.ravi.noCommand = "ラヴィコマンドが指定されていません"
	i.commands.ravi.start.success = "大討伐を開始します"
	i.commands.ravi.start.error = "大討伐は既に開催されています"
	i.commands.ravi.multiplier = "ラヴィダメージ倍率：ｘ%.2f"
	i.commands.ravi.res.success = "復活支援を実行します"
	i.commands.ravi.res.error = "復活支援は実行されませんでした"
	i.commands.ravi.sed.success = "鎮静支援を実行します"
	i.commands.ravi.request = "鎮静支援を要請します"
	i.commands.ravi.error = "ラヴィコマンドが認識されません"
	i.commands.ravi.noPlayers = "誰も大討伐に参加していません"
	i.commands.ravi.version = "このコマンドはMHFZZ以外では無効です"

	i.raviente.berserk = "<大討伐：猛狂期>が開催されました！"
	i.raviente.extreme = "<大討伐：猛狂期【極】>が開催されました！"
	i.raviente.extremeLimited = "<大討伐：猛狂期【極】(制限付)>が開催されました！"
	i.raviente.berserkSmall = "<大討伐：猛狂期(小数)>が開催されました！"

	i.guild.rookieGuildName = "新米猟団%d"
	i.guild.returnGuildName = "復帰猟団%d"

	i.guild.accepted = guildMailTemplate{"加入承認", "猟団「%s」への加入申請が承認されました。"}
	i.guild.rejected = guildMailTemplate{"加入拒否", "猟団「%s」への加入申請が拒否されました。"}
	i.guild.kicked = guildMailTemplate{"除名", "猟団「%s」から除名されました。"}
	i.guild.withdrawal = guildMailTemplate{"脱退", "猟団「%s」から脱退しました。"}

	i.guild.invite.title = "猟団勧誘のご案内"
	i.guild.invite.body = "猟団「%s」からの勧誘通知です。\n「勧誘に返答」より、返答を行ってください。"

	i.guild.invite.success.title = "成功"
	i.guild.invite.success.body = "あなたは「%s」に参加できました。"

	i.guild.invite.accepted.title = "承諾されました"
	i.guild.invite.accepted.body = "招待した狩人が「%s」への招待を承諾しました。"

	i.guild.invite.rejected.title = "却下しました"
	i.guild.invite.rejected.body = "あなたは「%s」への参加を却下しました。"

	i.guild.invite.declined.title = "辞退しました"
	i.guild.invite.declined.body = "招待した狩人が「%s」への招待を辞退しました。"

	i.beads = divaLocalizedBeads("jp")

	return i
}
