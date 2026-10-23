package channelserver

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"erupe-ce/common/stringsupport"
)

type localizationUserRepo struct {
	mockUserRepoCommands
	languageErr error
}

func (r *localizationUserRepo) GetLanguage(_ uint32) (string, error) {
	return r.langStored, r.languageErr
}

func TestCharacterI18n_RecipientPreference(t *testing.T) {
	for _, tc := range []struct {
		name, stored, online, want string
		lookupErr                 error
	}{
		{name: "offline default", want: "한국어"},
		{name: "offline preference", stored: "en", want: "English"},
		{name: "invalid preference", stored: "invalid", want: "한국어"},
		{name: "lookup failure", lookupErr: errors.New("offline lookup failed"), want: "한국어"},
		{name: "online preference wins", stored: "ko", online: "en", want: "English"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.Language = "ko"
			server.charRepo = &mockCharacterRepo{}
			server.userRepo = &localizationUserRepo{
				mockUserRepoCommands: mockUserRepoCommands{langStored: tc.stored},
				languageErr: tc.lookupErr,
			}
			if tc.online != "" {
				recipient := createMockSession(42, server)
				recipient.SetLang(tc.online)
				server.sessions = map[net.Conn]*Session{nil: recipient}
			}
			if got := server.characterI18n(42); got.language != tc.want {
				t.Fatalf("recipient language = %q, want %q", got.language, tc.want)
			}
		})
	}
}

func TestGuildOperationMails_UseRecipientLanguage(t *testing.T) {
	for _, tc := range []struct {
		action GuildMemberAction
		title  string
		body   string
	}{
		{GuildMemberActionAccept, "가입 승인", "가입 신청이 승인되었습니다."},
		{GuildMemberActionReject, "가입 거절", "가입 신청이 거절되었습니다."},
		{GuildMemberActionKick, "수렵단 제명", "제명되었습니다."},
	} {
		t.Run(tc.title, func(t *testing.T) {
			guild := &mockGuildRepo{
				guild: &Guild{ID: 10, Name: "보노보노해적단", GuildLeader: GuildLeader{LeaderCharID: 1}},
				membership: &GuildMember{GuildID: 10, CharID: 1, IsLeader: true},
			}
			mail := &mockMailRepo{}
			svc := newTestGuildService(guild, mail)
			svc.localize = func(charID uint32) i18n {
				if charID != 42 {
					t.Fatalf("localized actor %d instead of recipient 42", charID)
				}
				return langKorean()
			}
			result, err := svc.OperateMember(1, 42, tc.action)
			if err != nil || result == nil || len(mail.sentMails) != 1 {
				t.Fatalf("operation failed: result=%v err=%v sent=%d", result, err, len(mail.sentMails))
			}
			sent := mail.sentMails[0]
			if sent.subject != tc.title || !strings.Contains(sent.body, tc.body) || !strings.Contains(sent.body, guild.guild.Name) {
				t.Fatalf("unexpected localized mail: %+v", sent)
			}
			if sent.body != result.Mail.Body || sent.subject != result.Mail.Subject || !sent.isSystemMessage {
				t.Fatal("stored mail and immediate notification must have the same localized text")
			}
		})
	}
}

func TestGuildLeaveMail_Korean(t *testing.T) {
	mail := &mockMailRepo{}
	svc := newTestGuildService(&mockGuildRepo{}, mail)
	svc.localize = func(uint32) i18n { return langKorean() }
	result, err := svc.Leave(42, 10, false, "보노보노해적단")
	if err != nil || !result.Success || len(mail.sentMails) != 1 {
		t.Fatalf("leave: result=%v err=%v sent=%d", result, err, len(mail.sentMails))
	}
	if sent := mail.sentMails[0]; sent.subject != "수렵단 탈퇴" || sent.body != "수렵단 「보노보노해적단」에서\n탈퇴했습니다." {
		t.Fatalf("unexpected withdrawal mail: %+v", sent)
	}
}

type localizedScoutGuildRepo struct {
	*mockGuildRepo
	title, body string
}

func (r *localizedScoutGuildRepo) CreateInviteWithMail(_, _, _, _, _ uint32, title, body string) error {
	r.title, r.body = title, body
	return nil
}

func TestGuildScoutMails_LocalizeEachRecipient(t *testing.T) {
	guild := &localizedScoutGuildRepo{mockGuildRepo: &mockGuildRepo{
		guild: &Guild{ID: 10, Name: "보노보노해적단", GuildLeader: GuildLeader{LeaderCharID: 1}},
		membership: &GuildMember{GuildID: 10, CharID: 1, IsLeader: true},
	}}
	mail := &mockMailRepo{}
	svc := newTestGuildService(guild, mail)
	svc.localize = func(charID uint32) i18n {
		if charID == 42 {
			return langKorean()
		}
		return langEnglish()
	}
	if err := svc.PostScout(1, 42, ScoutInviteStrings{Title: "Invitation!", Body: "Join %s"}); err != nil {
		t.Fatal(err)
	}
	if guild.title != "수렵단 권유안내" || !strings.Contains(guild.body, "보노보노해적단") {
		t.Fatalf("invitation did not use recipient language: %q %q", guild.title, guild.body)
	}
	for _, accept := range []bool{true, false} {
		guild.hasInviteResult = true
		mail.sentMails = nil
		result, err := svc.AnswerScout(42, 1, accept, AnswerScoutStrings{})
		if err != nil || !result.Success || len(mail.sentMails) != 2 {
			t.Fatalf("answer: result=%v err=%v sent=%d", result, err, len(mail.sentMails))
		}
		wantRecipient, wantLeader := "성공", "Accepted"
		if !accept {
			wantRecipient, wantLeader = "거절했습니다", "Declined"
		}
		if mail.sentMails[0].subject != wantRecipient || mail.sentMails[1].subject != wantLeader {
			t.Fatalf("mixed-language recipients got incorrect subjects: %+v", mail.sentMails)
		}
	}
}

func TestKoreanCommandHelp_DefaultsAndCustomText(t *testing.T) {
	i := langKorean()
	if got := i.commandDescription("Show enabled chat commands"); got != "사용 가능한 채팅 명령어 표시" {
		t.Fatalf("default help description = %q", got)
	}
	custom := "운영자가 정한 설명"
	if got := i.commandDescription(custom); got != custom {
		t.Fatalf("operator description was changed: %q", got)
	}
	en := langEnglish()
	if got := en.commandDescription("Show enabled chat commands"); got != "Show enabled chat commands" {
		t.Fatalf("English help changed: %q", got)
	}
}

func TestShutdownCountdown_LocalizesEachSession(t *testing.T) {
	for _, code := range []string{"ko", "en"} {
		t.Run(code, func(t *testing.T) {
			server := createMockServer()
			session := createMockSession(42, server)
			session.SetLang(code)
			server.sessions = map[net.Conn]*Session{nil: session}
			server.BroadcastShutdownCountdown(7)
			select {
			case pkt := <-session.sendPackets:
				text := fmt.Sprintf(getLangStringsFor(code).shutdown, 7)
				if !bytes.Contains(pkt.data, stringsupport.UTF8ToSJIS(text)) {
					t.Fatalf("countdown packet did not contain %q", text)
				}
			default:
				t.Fatal("no countdown packet was queued")
			}
		})
	}
}
