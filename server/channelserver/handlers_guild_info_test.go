package channelserver

import (
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// guildInfoServer creates a mock server with ClanMemberLimits set,
// which handleMsgMhfInfoGuild requires.
func guildInfoServer() *Server {
	s := createMockServer()
	s.erupeConfig.GameplayOptions.ClanMemberLimits = [][]uint8{{0, 30}}
	return s
}

// --- handleMsgMhfInfoGuild tests ---

func TestInfoGuild_ReturnType(t *testing.T) {
	for _, client := range []struct {
		name string
		mode cfg.Mode
	}{
		{"G10", cfg.G10},
		{"ZZ", cfg.ZZ},
	} {
		t.Run(client.name, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				returnType uint8
				want       uint8
			}{
				{"ordinary", 0, 0},
				{"legacy rookie uses returning guild", 1, 2},
				{"returning", 2, 2},
			} {
				t.Run(tc.name, func(t *testing.T) {
					server := guildInfoServer()
					server.erupeConfig.RealClientMode = client.mode
					now := time.Now()
					guild := &Guild{
						ID: 10, Name: "Guild", Comment: "Comment", ReturnType: tc.returnType,
						CreatedAt: now, RoomExpiry: now, EventRP: 1234,
					}
					guild.LeaderName = "Leader"
					server.guildRepo = &mockGuildRepo{
						guild:      guild,
						membership: &GuildMember{GuildID: 10, CharID: 1, JoinedAt: &now},
					}
					session := createMockSession(1, server)

					// GuildID=0 matches the authenticated character's login refresh.
					handleMsgMhfInfoGuild(session, &mhfpacket.MsgMhfInfoGuild{AckHandle: 100})
					ack := readAck(t, session)
					offset := 44 + len(guild.Name) + len(guild.Comment) + len(guild.LeaderName)
					if ack.AckHandle != 100 || ack.ErrorCode != 0 || !ack.IsBufferResponse || len(ack.Payload) < offset+8 {
						t.Fatalf("invalid guild info response: %+v", ack)
					}
					bf := byteframe.NewByteFrameFromBytes(ack.Payload[offset:])
					if got := bf.ReadUint8(); got != tc.want {
						t.Fatalf("guild type = %d, want %d; obsolete rookie type 1 must never be sent", got, tc.want)
					}
					if bf.ReadBool() || bf.ReadUint8() != 2 || bf.ReadUint8() != 2 || bf.ReadUint32() != guild.EventRP {
						t.Fatal("guild type must remain one byte without changing subsequent fields")
					}
				})
			}
		})
	}
}

func TestInfoGuild_ByGuildID(t *testing.T) {
	server := guildInfoServer()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, OrderIndex: 1, IsLeader: true},
	}
	joined := time.Now()
	guildMock.membership.JoinedAt = &joined
	guildMock.guild = &Guild{
		ID:          10,
		Name:        "Test",
		Comment:     "Hello",
		MemberCount: 5,
		CreatedAt:   time.Now(),
		RoomExpiry:  time.Now().Add(time.Hour),
	}
	guildMock.guild.LeaderCharID = 1
	guildMock.guild.LeaderName = "Leader"
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoGuild{AckHandle: 100, GuildID: 10}

	handleMsgMhfInfoGuild(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 20 {
			t.Errorf("Response too short: %d bytes", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}

	if session.prevGuildID != 10 {
		t.Errorf("prevGuildID = %d, want 10", session.prevGuildID)
	}
	if guildMock.getMembersCalls != 1 || guildMock.getMembersGuildID != 10 || !guildMock.getMembersApplicants {
		t.Errorf("applicant query = calls:%d guild:%d applicants:%t, want 1, 10, true",
			guildMock.getMembersCalls, guildMock.getMembersGuildID, guildMock.getMembersApplicants)
	}
}

func TestInfoGuild_ByCharID(t *testing.T) {
	server := guildInfoServer()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, OrderIndex: 5},
	}
	guildMock.guild = &Guild{
		ID:         10,
		Name:       "MyGuild",
		CreatedAt:  time.Now(),
		RoomExpiry: time.Now(),
	}
	guildMock.guild.LeaderCharID = 99
	guildMock.guild.LeaderName = "Boss"
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	// GuildID=0 means look up by charID
	pkt := &mhfpacket.MsgMhfInfoGuild{AckHandle: 100, GuildID: 0}

	handleMsgMhfInfoGuild(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 20 {
			t.Errorf("Response too short: %d bytes", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestInfoGuild_NotFound(t *testing.T) {
	server := createMockServer()
	guildMock := &mockGuildRepo{}
	guildMock.getErr = errNotFound
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoGuild{AckHandle: 100, GuildID: 999}

	handleMsgMhfInfoGuild(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestInfoGuild_MembershipError(t *testing.T) {
	server := createMockServer()
	guildMock := &mockGuildRepo{
		getMemberErr: errNotFound,
	}
	guildMock.guild = &Guild{
		ID:         10,
		Name:       "Test",
		CreatedAt:  time.Now(),
		RoomExpiry: time.Now(),
	}
	guildMock.guild.LeaderCharID = 1
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoGuild{AckHandle: 100, GuildID: 10}

	handleMsgMhfInfoGuild(session, pkt)

	// Should return early with count=0 response
	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestInfoGuild_WithAlliance(t *testing.T) {
	server := guildInfoServer()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, OrderIndex: 1, IsLeader: true},
		alliance: &GuildAlliance{
			ID:            5,
			Name:          "TestAlliance",
			CreatedAt:     time.Now(),
			TotalMembers:  15,
			ParentGuildID: 10,
			ParentGuild:   Guild{Name: "Test", MemberCount: 5},
		},
	}
	guildMock.guild = &Guild{
		ID:         10,
		Name:       "Test",
		CreatedAt:  time.Now(),
		RoomExpiry: time.Now(),
		AllianceID: 5,
	}
	guildMock.guild.LeaderCharID = 1
	guildMock.guild.LeaderName = "Leader"
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfInfoGuild{AckHandle: 100, GuildID: 10}

	handleMsgMhfInfoGuild(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 50 {
			t.Errorf("Alliance response too short: %d bytes", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}
}

// --- handleMsgMhfEnumerateGuild tests ---

func TestEnumerateGuild_ByName(t *testing.T) {
	server := createMockServer()
	guildMock := &mockGuildRepo{}
	guildMock.guild = nil
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	// Simulate search term in Data2
	data2 := byteframe.NewByteFrame()
	data2.WriteBytes([]byte("Test\x00"))
	_, _ = data2.Seek(0, 0)

	pkt := &mhfpacket.MsgMhfEnumerateGuild{
		AckHandle: 100,
		Type:      mhfpacket.ENUMERATE_GUILD_TYPE_GUILD_NAME,
		Data2:     data2,
	}

	handleMsgMhfEnumerateGuild(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestEnumerateGuild_NoResults(t *testing.T) {
	server := createMockServer()
	guildMock := &mockGuildRepo{}
	guildMock.getErr = errNotFound
	server.guildRepo = guildMock
	session := createMockSession(1, server)

	data2 := byteframe.NewByteFrame()
	data2.WriteBytes([]byte("NonExistent\x00"))
	_, _ = data2.Seek(0, 0)

	pkt := &mhfpacket.MsgMhfEnumerateGuild{
		AckHandle: 100,
		Type:      mhfpacket.ENUMERATE_GUILD_TYPE_GUILD_NAME,
		Data2:     data2,
	}

	handleMsgMhfEnumerateGuild(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}
