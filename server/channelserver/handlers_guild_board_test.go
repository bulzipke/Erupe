package channelserver

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// --- handleMsgMhfUpdateGuildMessageBoard tests ---

func TestUpdateGuildMessageBoard_CreatePost(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, OrderIndex: 1},
	}
	guildMock.guild = &Guild{ID: 10}
	guildMock.guild.LeaderCharID = 1
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 100,
		MessageOp: 0, // Create
		PostType:  0,
		StampID:   5,
		Title:     "Test Title",
		Body:      "Test Body",
	}

	handleMsgMhfUpdateGuildMessageBoard(session, pkt)

	if guildMock.createdPost == nil {
		t.Fatal("CreatePost should be called")
	}
	if guildMock.createdPost[0].(uint32) != 10 {
		t.Errorf("CreatePost guildID = %d, want 10", guildMock.createdPost[0])
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestUpdateGuildMessageBoard_DeletePost(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, OrderIndex: 1},
	}
	guildMock.guild = &Guild{ID: 10}
	guildMock.guild.LeaderCharID = 1
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 100,
		MessageOp: 1, // Delete
		PostID:    42,
	}

	handleMsgMhfUpdateGuildMessageBoard(session, pkt)

	if guildMock.deletedPostID != 42 {
		t.Errorf("DeletePost postID = %d, want 42", guildMock.deletedPostID)
	}
	if guildMock.deletedPostGuildID != 10 {
		t.Errorf("DeletePost guildID = %d, want 10", guildMock.deletedPostGuildID)
	}
}

func TestUpdateGuildMessageBoard_NoGuild(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{getMemberErr: errNotFound}
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 100,
		MessageOp: 0,
	}

	handleMsgMhfUpdateGuildMessageBoard(session, pkt)

	// Returns early with empty success
	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestUpdateGuildMessageBoard_Applicant(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, IsApplicant: true},
	}
	guildMock.guild = &Guild{ID: 10}
	guildMock.guild.LeaderCharID = 999
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 100,
		MessageOp: 0,
	}

	handleMsgMhfUpdateGuildMessageBoard(session, pkt)

	if guildMock.createdPost != nil {
		t.Error("Applicant should not be able to create posts")
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestUpdateGuildMessageBoard_MembershipError(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{getMemberErr: errNotFound}
	guildMock.guild = &Guild{ID: 10}
	guildMock.guild.LeaderCharID = 1
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 100,
		MessageOp: 0,
		Title:     "Test",
		Body:      "Body",
	}

	handleMsgMhfUpdateGuildMessageBoard(session, pkt)
	if guildMock.createdPost != nil {
		t.Error("CreatePost should not be called when membership lookup fails")
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

// --- handleMsgMhfEnumerateGuildMessageBoard tests ---

func TestEnumerateGuildMessageBoard_NoPosts(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		posts: []*MessageBoardPost{},
	}
	guildMock.guild = &Guild{ID: 10}
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateGuildMessageBoard{
		AckHandle: 100,
		BoardType: 0,
		MaxPosts:  100,
	}

	handleMsgMhfEnumerateGuildMessageBoard(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestEnumerateGuildMessageBoard_WithPosts(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		posts: []*MessageBoardPost{
			{ID: 1, AuthorID: 100, StampID: 5, Title: "Hello", Body: "World", Timestamp: time.Now()},
			{ID: 2, AuthorID: 200, StampID: 0, Title: "Test", Body: "Post", Timestamp: time.Now()},
		},
	}
	guildMock.guild = &Guild{ID: 10}
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateGuildMessageBoard{
		AckHandle: 100,
		BoardType: 0,
		MaxPosts:  100,
	}

	handleMsgMhfEnumerateGuildMessageBoard(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 8 {
			t.Errorf("Response too short for 2 posts: %d bytes", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestEnumerateGuildMessageBoard_CP949(t *testing.T) {
	cases := []struct {
		title, body, wantTitle, wantBody string
	}{
		{"코코샤넬 공지 1", "한글 본문입니다.\r\n둘째 줄 ABC 123", "코코샤넬 공지 1", "한글 본문입니다.\r\n둘째 줄 ABC 123"},
		{"", "", "", ""},
		{"ASCII title", "Test\r\nNext line 123", "ASCII title", "Test\r\nNext line 123"},
		{"안내😀", "앞😀뒤", "안내", "앞뒤"}, // Match the existing CP949 text helper's unsupported-rune policy.
	}
	for _, boardType := range []uint32{0, 1} {
		t.Run(fmt.Sprintf("board_type_%d", boardType), func(t *testing.T) {
			server := createMockServer()
			posts := make([]*MessageBoardPost, len(cases))
			for i, test := range cases {
				posts[i] = &MessageBoardPost{
					ID: uint32(10 + i), AuthorID: uint32(100 + i), StampID: uint32(5 + i),
					Title: test.title, Body: test.body, Timestamp: time.Unix(1700000000+int64(i), 0), LikedBy: "1,2",
				}
			}
			server.guildRepo = &mockGuildRepo{guild: &Guild{ID: 10}, posts: posts}
			server.charRepo = newMockCharacterRepo()
			session := createMockSession(1, server)
			handleMsgMhfEnumerateGuildMessageBoard(session, &mhfpacket.MsgMhfEnumerateGuildMessageBoard{
				AckHandle: 77, BoardType: boardType, MaxPosts: 100,
			})
			ack := readAck(t, session)
			if ack.AckHandle != 77 || !ack.IsBufferResponse || ack.ErrorCode != 0 {
				t.Fatalf("unexpected ACK: %+v", ack)
			}
			bf := byteframe.NewByteFrameFromBytes(ack.Payload)
			if got := bf.ReadUint32(); got != uint32(len(posts)) {
				t.Fatalf("post count = %d, want %d", got, len(posts))
			}
			readText := func(field, want string) {
				t.Helper()
				expected, _, err := transform.String(korean.EUCKR.NewEncoder(), want)
				if err != nil {
					t.Fatal(err)
				}
				length := bf.ReadUint32()
				if length != uint32(len(expected)+1) {
					t.Fatalf("%s byte length = %d, want %d including NUL", field, length, len(expected)+1)
				}
				wire := bf.ReadBytes(uint(length))
				if !bytes.Equal(wire, append([]byte(expected), 0)) {
					t.Fatalf("%s CP949 bytes = %x, want %x00", field, wire, expected)
				}
			}
			for i, post := range posts {
				if id, author, reserved, timestamp := bf.ReadUint32(), bf.ReadUint32(), bf.ReadUint32(), bf.ReadUint32(); id != post.ID || author != post.AuthorID || reserved != 0 || timestamp != uint32(post.Timestamp.Unix()) {
					t.Fatalf("post %d metadata changed: %d %d %d %d", i, id, author, reserved, timestamp)
				}
				if count, liked, stamp := bf.ReadUint32(), bf.ReadBool(), bf.ReadUint32(); count != 2 || !liked || stamp != post.StampID {
					t.Fatalf("post %d likes/stamp changed: %d %t %d", i, count, liked, stamp)
				}
				readText(fmt.Sprintf("post %d title", i), cases[i].wantTitle)
				readText(fmt.Sprintf("post %d body", i), cases[i].wantBody)
			}
			if bf.Err() != nil || bf.Index() != uint(len(ack.Payload)) {
				t.Fatalf("misaligned response: read=%d bytes=%d err=%v", bf.Index(), len(ack.Payload), bf.Err())
			}
		})
	}
}

func TestEnumerateGuildMessageBoard_DBError(t *testing.T) {
	server := createMockServer()
	charMock := newMockCharacterRepo()
	guildMock := &mockGuildRepo{
		listPostsErr: errNotFound,
	}
	guildMock.guild = &Guild{ID: 10}
	server.guildRepo = guildMock
	server.charRepo = charMock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfEnumerateGuildMessageBoard{
		AckHandle: 100,
		BoardType: 0,
		MaxPosts:  100,
	}

	handleMsgMhfEnumerateGuildMessageBoard(session, pkt)

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfUpdateGuildMessageBoard_CreatePost(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 0,
		PostType:  0,
		StampID:   1,
		Title:     "Test",
		Body:      "Hello",
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfUpdateGuildMessageBoard_CreatePostType1(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 0,
		PostType:  1,
		Title:     "Notice",
		Body:      "Board",
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfUpdateGuildMessageBoard_DeletePost(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	guildRepo := &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.guildRepo = guildRepo
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 1,
		PostID:    42,
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
	if guildRepo.deletedPostGuildID != 1 || guildRepo.deletedPostID != 42 {
		t.Fatalf("DeletePost args = (%d, %d), want (1, 42)", guildRepo.deletedPostGuildID, guildRepo.deletedPostID)
	}
}

func TestHandleMsgMhfUpdateGuildMessageBoard_UpdatePost(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	guildRepo := &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.guildRepo = guildRepo
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 2,
		PostID:    1,
		Title:     "Updated",
		Body:      "New body",
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
	if guildRepo.updatedPostGuildID != 1 || guildRepo.updatedPostID != 1 {
		t.Fatalf("UpdatePost args = (%d, %d), want (1, 1)", guildRepo.updatedPostGuildID, guildRepo.updatedPostID)
	}
}

func TestHandleMsgMhfUpdateGuildMessageBoard_UpdateStamp(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	guildRepo := &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.guildRepo = guildRepo
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 3,
		PostID:    1,
		StampID:   5,
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
	if guildRepo.updatedStampGuildID != 1 || guildRepo.updatedStampPostID != 1 || guildRepo.updatedStampID != 5 {
		t.Fatalf("UpdatePostStamp args = (%d, %d, %d), want (1, 1, 5)", guildRepo.updatedStampGuildID, guildRepo.updatedStampPostID, guildRepo.updatedStampID)
	}
}

func TestHandleMsgMhfUpdateGuildMessageBoard_LikePost(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	guildRepo := &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.guildRepo = guildRepo
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 4,
		PostID:    1,
		LikeState: true,
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
	if guildRepo.likedPostGuildID != 1 || guildRepo.likedPostID != 1 ||
		guildRepo.setLikedPostGuildID != 1 || guildRepo.setLikedPostID != 1 {
		t.Fatalf("like calls used wrong guild/post: get=(%d,%d) set=(%d,%d)",
			guildRepo.likedPostGuildID, guildRepo.likedPostID,
			guildRepo.setLikedPostGuildID, guildRepo.setLikedPostID)
	}
}

func TestHandleMsgMhfUpdateGuildMessageBoard_CheckNewPosts(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100}}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{
		AckHandle: 1,
		MessageOp: 5,
	}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfUpdateGuildMessageBoard_NoGuild(t *testing.T) {
	srv := createMockServer()
	srv.guildRepo = &mockGuildRepo{getMemberErr: errNotFound}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{AckHandle: 1, MessageOp: 0}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfUpdateGuildMessageBoard_Applicant(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild, membership: &GuildMember{GuildID: 1, CharID: 100, IsApplicant: true}}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfUpdateGuildMessageBoard{AckHandle: 1, MessageOp: 0}
	handleMsgMhfUpdateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfEnumerateGuildMessageBoard(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfEnumerateGuildMessageBoard{AckHandle: 1, BoardType: 0}
	handleMsgMhfEnumerateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}

func TestHandleMsgMhfEnumerateGuildMessageBoard_Type1(t *testing.T) {
	srv := createMockServer()
	guild := &Guild{ID: 1}
	srv.guildRepo = &mockGuildRepo{guild: guild}
	srv.charRepo = newMockCharacterRepo()
	s := createMockSession(100, srv)

	pkt := &mhfpacket.MsgMhfEnumerateGuildMessageBoard{AckHandle: 1, BoardType: 1}
	handleMsgMhfEnumerateGuildMessageBoard(s, pkt)
	<-s.sendPackets
}
