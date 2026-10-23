package channelserver

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// Use independently specified CP949 bytes: an encode/decode round trip through
// the same helper would also pass with the wrong client code page.
const (
	guildNameCP949    = "\xbc\xf6\xb7\xc6\xb4\xdc" // 수렵단
	leaderNameCP949   = "\xc7\xe5\xc5\xcd"         // 헌터
	allianceCP949     = "\xb5\xbf\xb8\xcd"         // 동맹
	pugiNameCP949     = "\xc7\xaa\xb1\xe2"         // 푸기
	applicantCP949    = "\xbd\xc5\xc3\xbb\xc0\xda" // 신청자
	extendedNameCP949 = "\xc7\xd1\xb1\xdb\xc6\x52" // 한글힣 (includes a CP949 extension)
)

type koreanGuildListRepo struct {
	mockGuildRepo
	guilds    []*Guild
	alliances []*GuildAlliance
}

func (r *koreanGuildListRepo) ListAll() ([]*Guild, error) {
	return r.guilds, nil
}

func (r *koreanGuildListRepo) ListAlliances() ([]*GuildAlliance, error) {
	return r.alliances, nil
}

func assertGuildWireString(t *testing.T, bf *byteframe.ByteFrame, width int, want string) {
	t.Helper()
	var size uint
	if width == 1 {
		size = uint(bf.ReadUint8())
	} else {
		size = uint(bf.ReadUint16())
	}
	if size != uint(len(want)+1) {
		t.Fatalf("name length = %d, want %d CP949 bytes including NUL", size, len(want)+1)
	}
	if got := bf.ReadBytes(size); !bytes.Equal(got, append([]byte(want), 0)) {
		t.Fatalf("name bytes = %x, want %x followed by NUL", got, want)
	}
}

func assertGuildWireEnd(t *testing.T, bf *byteframe.ByteFrame) {
	t.Helper()
	if bf.Err() != nil || len(bf.DataFromCurrent()) != 0 {
		t.Fatalf("misaligned guild response: err=%v, remaining=%x", bf.Err(), bf.DataFromCurrent())
	}
}

func TestEnumerateGuildKoreanNamesAndSearch(t *testing.T) {
	guild := &Guild{
		ID: 10, Name: "수렵단", MemberCount: 2, Recruiting: true,
		GuildLeader: GuildLeader{LeaderCharID: 100, LeaderName: "헌터"},
	}
	alliance := &GuildAlliance{
		ID: 20, Name: "동맹", ParentGuildID: guild.ID, ParentGuild: *guild,
		TotalMembers: 2, Recruiting: true,
	}
	for _, tc := range []struct {
		name       string
		kind       mhfpacket.EnumerateGuildType
		search     string
		alliance   bool
		wantName   string
		wantID     uint32
	}{
		{"guild list", mhfpacket.ENUMERATE_GUILD_TYPE_ORDER_RANK, "", false, guildNameCP949, guild.ID},
		{"guild name search", mhfpacket.ENUMERATE_GUILD_TYPE_GUILD_NAME, guildNameCP949, false, guildNameCP949, guild.ID},
		{"guild leader search", mhfpacket.ENUMERATE_GUILD_TYPE_LEADER_NAME, leaderNameCP949, false, guildNameCP949, guild.ID},
		{"alliance list", mhfpacket.ENUMERATE_ALLIANCE_TYPE_ORDER_MEMBERS, "", true, allianceCP949, alliance.ID},
		{"alliance name search", mhfpacket.ENUMERATE_ALLIANCE_TYPE_ALLIANCE_NAME, allianceCP949, true, allianceCP949, alliance.ID},
		{"alliance leader search", mhfpacket.ENUMERATE_ALLIANCE_TYPE_LEADER_NAME, leaderNameCP949, true, allianceCP949, alliance.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.guildRepo = &koreanGuildListRepo{
				guilds: []*Guild{guild}, alliances: []*GuildAlliance{alliance},
			}
			session := createMockSession(100, server)
			handleMsgMhfEnumerateGuild(session, &mhfpacket.MsgMhfEnumerateGuild{
				AckHandle: 77, Type: tc.kind,
				Data2: byteframe.NewByteFrameFromBytes(append([]byte(tc.search), 0)),
			})
			ack := readAck(t, session)
			if ack.AckHandle != 77 || ack.ErrorCode != 0 || !ack.IsBufferResponse {
				t.Fatalf("invalid enumeration ACK: %+v", ack)
			}
			bf := byteframe.NewByteFrameFromBytes(ack.Payload)
			if count, more := bf.ReadUint16(), bf.ReadBool(); count != 1 || more {
				t.Fatalf("result count=%d, more=%t; Korean search should find one entry", count, more)
			}
			if id, leader := bf.ReadUint32(), bf.ReadUint32(); id != tc.wantID || leader != guild.LeaderCharID {
				t.Fatalf("guild/alliance ID=%d, leader=%d", id, leader)
			}
			if count := bf.ReadUint16(); count != 2 {
				t.Fatalf("member count = %d, want 2", count)
			}
			bf.ReadUint16() // unknown
			if tc.alliance {
				if count := bf.ReadUint16(); count != 1 {
					t.Fatalf("alliance guild count = %d, want 1", count)
				}
			} else {
				bf.ReadUint16() // guild rank
			}
			bf.ReadUint32() // creation time
			assertGuildWireString(t, bf, 1, tc.wantName)
			assertGuildWireString(t, bf, 1, leaderNameCP949)
			if bf.ReadUint8() != 1 || bf.ReadBool() {
				t.Fatal("fields after Korean names were corrupted")
			}
			assertGuildWireEnd(t, bf)
		})
	}
}

func TestEnumerateGuildMemberKeepsKoreanNames(t *testing.T) {
	for _, client := range []struct {
		name string
		mode cfg.Mode
	}{
		{"G10", cfg.G10},
		{"ZZ", cfg.ZZ},
	} {
		t.Run(client.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = client.mode
			members := []*GuildMember{
				{CharID: 100, Name: "한글힣", HR: 100, GR: 200, WeaponID: 321, OrderIndex: 0, LastLogin: 12345, RPToday: 12, RPYesterday: 34},
				{CharID: 200, Name: "Thunder", HR: 101, GR: 201, WeaponID: 322, OrderIndex: 1, LastLogin: 12346, RPToday: 13, RPYesterday: 35},
			}
			server.guildRepo = &mockGuildRepo{
				guild: &Guild{ID: 10, RPResetAt: time.Now().Add(24 * time.Hour)}, members: members,
			}
			session := createMockSession(100, server)
			handleMsgMhfEnumerateGuildMember(session, &mhfpacket.MsgMhfEnumerateGuildMember{AckHandle: 78, GuildID: 10})
			ack := readAck(t, session)
			if ack.ErrorCode != 0 || !ack.IsBufferResponse {
				t.Fatalf("invalid member ACK: %+v", ack)
			}
			bf := byteframe.NewByteFrameFromBytes(ack.Payload)
			if count := bf.ReadUint16(); count != 2 {
				t.Fatalf("member count = %d, want 2", count)
			}
			for i, member := range members {
				if id, hr, gr := bf.ReadUint32(), bf.ReadUint16(), bf.ReadUint16(); id != member.CharID || hr != member.HR || gr != member.GR {
					t.Fatalf("member %d: id=%d, HR=%d, GR=%d", i, id, hr, gr)
				}
				weapon := bf.ReadUint16()
				wantWeapon := uint16(0)
				if client.mode >= cfg.ZZ {
					wantWeapon = member.WeaponID
				}
				if weapon != wantWeapon || bf.ReadUint8() != 6 || bf.ReadUint16() != member.OrderIndex || bf.ReadBool() {
					t.Fatalf("member %d metadata was corrupted", i)
				}
				wantName := extendedNameCP949
				if i == 1 {
					wantName = "Thunder"
				}
				assertGuildWireString(t, bf, 1, wantName)
			}
			for _, member := range members {
				if got := bf.ReadUint32(); got != member.LastLogin {
					t.Fatalf("last login = %d, want %d", got, member.LastLogin)
				}
			}
			if bf.ReadUint16() != 0 {
				t.Fatal("unexpected alliance members")
			}
			for _, member := range members {
				if today, yesterday := bf.ReadUint16(), bf.ReadUint16(); today != member.RPToday || yesterday != member.RPYesterday {
					t.Fatalf("RP after Korean member names: today=%d, yesterday=%d", today, yesterday)
				}
			}
			assertGuildWireEnd(t, bf)
		})
	}
}

func TestInfoGuildKoreanNestedNames(t *testing.T) {
	server := guildInfoServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	guild := &Guild{
		ID: 10, Name: "수렵단", Comment: "동맹", PugiName1: "푸기", AllianceID: 20,
		GuildLeader: GuildLeader{LeaderCharID: 100, LeaderName: "헌터"},
	}
	server.guildRepo = &mockGuildRepo{
		guild: guild, membership: &GuildMember{GuildID: 10, CharID: 100, IsLeader: true},
		members: []*GuildMember{{CharID: 200, Name: "신청자", HR: 50, GR: 100, IsApplicant: true}},
		alliance: &GuildAlliance{ID: 20, Name: "동맹", ParentGuildID: guild.ID, ParentGuild: *guild},
	}
	session := createMockSession(100, server)
	handleMsgMhfInfoGuild(session, &mhfpacket.MsgMhfInfoGuild{AckHandle: 79, GuildID: 10})
	ack := readAck(t, session)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse {
		t.Fatalf("invalid guild info ACK: %+v", ack)
	}
	bf := byteframe.NewByteFrameFromBytes(ack.Payload)
	bf.ReadBytes(31) // IDs, rank, members, mottos, flags, membership, timestamps
	nameSize, commentSize := bf.ReadUint8(), bf.ReadUint8()
	bf.ReadUint8() // length of festival/rank data
	leaderSize := bf.ReadUint8()
	if int(nameSize) != len(guildNameCP949) || int(commentSize) != len(allianceCP949) || int(leaderSize) != len(leaderNameCP949) {
		t.Fatalf("guild info lengths = %d/%d/%d, want CP949 byte lengths", nameSize, commentSize, leaderSize)
	}
	if got := bf.ReadBytes(uint(nameSize)); string(got) != guildNameCP949 {
		t.Fatalf("guild name = %x", got)
	}
	if got := bf.ReadBytes(uint(commentSize)); string(got) != allianceCP949 {
		t.Fatalf("guild comment = %x", got)
	}
	bf.ReadBytes(5) // festival color and rank RP
	if got := bf.ReadBytes(uint(leaderSize)); string(got) != leaderNameCP949 {
		t.Fatalf("guild leader = %x", got)
	}
	bf.ReadBytes(12) // unknown, guild type, hall, constants, event RP
	assertGuildWireString(t, bf, 1, pugiNameCP949)
	assertGuildWireString(t, bf, 1, "")
	assertGuildWireString(t, bf, 1, "")
	bf.ReadBytes(23) // outfits, unlocked outfits, member limit, room data (ZZ)
	if bf.ReadUint32() != 20 {
		t.Fatal("missing alliance after Korean pugi name")
	}
	bf.ReadBytes(8) // alliance creation time, member count, ignored fields
	assertGuildWireString(t, bf, 2, allianceCP949)
	if bf.ReadUint8() != 1 || bf.ReadUint32() != guild.ID {
		t.Fatal("alliance parent guild metadata was corrupted")
	}
	bf.ReadUint32() // unknown
	if bf.ReadUint16() != 1 {
		t.Fatal("alliance parent guild is not marked as the viewed guild")
	}
	bf.ReadBytes(4) // parent guild rank and member count
	assertGuildWireString(t, bf, 2, guildNameCP949)
	assertGuildWireString(t, bf, 2, leaderNameCP949)
	if bf.ReadUint16() != 1 {
		t.Fatal("missing applicant after Korean alliance names")
	}
	if bf.ReadUint32() != 200 || bf.ReadUint32() != 0 || bf.ReadUint16() != 50 || bf.ReadUint16() != 100 {
		t.Fatal("applicant metadata was corrupted")
	}
	assertGuildWireString(t, bf, 1, applicantCP949)
	if got := bf.ReadBytes(4); !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("trailing activity/invites/icon fields = %x", got)
	}
	assertGuildWireEnd(t, bf)
}

func TestInfoGuildKoreanCommentByteLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		comment string
		want    string
	}{
		{"exactly 255 bytes", strings.Repeat("가", 127) + "A", strings.Repeat("\xb0\xa1", 127) + "A"},
		{"two-byte character at boundary", strings.Repeat("가", 128), strings.Repeat("\xb0\xa1", 127)},
		{"ASCII followed by Hangul", strings.Repeat("A", 254) + "가", strings.Repeat("A", 254)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := guildInfoServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.guildRepo = &mockGuildRepo{guild: &Guild{
				ID: 10, Name: "수렵단", Comment: tc.comment, PugiName1: "푸기", EventRP: 1234,
				GuildLeader: GuildLeader{LeaderCharID: 100, LeaderName: "헌터"},
			}}
			session := createMockSession(100, server)
			handleMsgMhfInfoGuild(session, &mhfpacket.MsgMhfInfoGuild{AckHandle: 80, GuildID: 10})
			ack := readAck(t, session)
			if ack.ErrorCode != 0 || !ack.IsBufferResponse {
				t.Fatalf("invalid guild info ACK: %+v", ack)
			}
			bf := byteframe.NewByteFrameFromBytes(ack.Payload)
			bf.ReadBytes(31)
			nameSize, commentSize := bf.ReadUint8(), bf.ReadUint8()
			if bf.ReadUint8() != 5 || int(bf.ReadUint8()) != len(leaderNameCP949) {
				t.Fatal("invalid festival/rank or leader name lengths")
			}
			if int(nameSize) != len(guildNameCP949) || int(commentSize) != len(tc.want) {
				t.Fatalf("guild name/comment lengths = %d/%d, want %d/%d", nameSize, commentSize, len(guildNameCP949), len(tc.want))
			}
			if got := string(bf.ReadBytes(uint(nameSize))); got != guildNameCP949 {
				t.Fatalf("guild name = %x", got)
			}
			if got := string(bf.ReadBytes(uint(commentSize))); got != tc.want {
				t.Fatalf("comment bytes = %x, want %x", got, tc.want)
			}
			bf.ReadBytes(5) // festival color and rank RP
			if got := string(bf.ReadBytes(uint(len(leaderNameCP949)))); got != leaderNameCP949 {
				t.Fatalf("leader name after long Korean comment = %x", got)
			}
			bf.ReadBytes(8) // unknown, guild type, hall, constants
			if got := bf.ReadUint32(); got != 1234 {
				t.Fatalf("event RP after long Korean comment = %d, want 1234", got)
			}
			assertGuildWireString(t, bf, 1, pugiNameCP949)
			if err := bf.Err(); err != nil {
				t.Fatalf("guild info read failed: %v", err)
			}
		})
	}
}
