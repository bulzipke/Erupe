package channelserver

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

func TestReturnGuildEntryReplies(t *testing.T) {
	for _, scenario := range []struct {
		name string
		unk  uint32
		err  error
	}{
		{"rookie", 0, nil},
		{"return one", 1, nil},
		{"return two", 2, nil},
		{"transaction failure", 2, errors.New("unavailable")},
		{"no successor", 2, ErrNoEligibleLeader},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.Language = "ko"
			repo := &mockGuildRepo{returnGuildID: 17, joinReturnErr: scenario.err}
			server.guildRepo = repo
			s := createMockSession(42, server)
			s.prevGuildID = 10
			wantError := uint8(0)
			if scenario.err != nil {
				wantError = 1
			}
			handleMsgMhfEntryRookieGuild(s, &mhfpacket.MsgMhfEntryRookieGuild{AckHandle: 123, Unk: scenario.unk})
			ack := readAck(t, s)
			if ack.AckHandle != 123 || ack.IsBufferResponse || ack.ErrorCode != wantError {
				t.Fatal(ack)
			}
			if wantError == 0 {
				if got := byteframe.NewByteFrameFromBytes(ack.Payload).ReadUint32(); got != 17 {
					t.Fatal(got)
				}
				if s.prevGuildID != 17 {
					t.Fatal("success left stale guild UI context", s.prevGuildID)
				}
			} else if s.prevGuildID != 10 {
				t.Fatal("failure changed old guild UI context", s.prevGuildID)
			}
			if repo.joinReturnCalls != 1 || repo.joinReturnCharID != 42 || repo.findReturnCalls != 0 || repo.addMemberCalls != 0 {
				t.Fatal("entry must use one atomic operation", repo)
			}
			wantType, wantTemplate := uint8(2), s.I18n().guild.returnGuildName
			if scenario.unk == 0 {
				wantType, wantTemplate = 1, s.I18n().guild.rookieGuildName
			}
			if repo.returnGuildType != wantType || repo.returnGuildTemplate != wantTemplate {
				t.Fatal(repo.returnGuildType, repo.returnGuildTemplate)
			}
			if s.closed.Load() {
				t.Fatal("guild rejection disconnected the player")
			}
			if len(s.sendPackets) != 0 {
				t.Fatal("unexpected extra response")
			}
		})
	}
}

func TestReturnGuildLeaderlessLookupAndMembership(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	userID := CreateTestUser(t, db, "return_member")
	charID := CreateTestCharacter(t, db, userID, "ReturnMember")
	for _, returnType := range []uint8{1, 2} {
		t.Run(fmt.Sprint(returnType), func(t *testing.T) {
			id, err := repo.FindOrCreateReturnGuild(returnType, "Temporary %d")
			if err != nil {
				t.Fatal(err)
			}
			g, err := repo.GetByID(id)
			if err != nil || g == nil || g.ReturnType != returnType || g.LeaderCharID != 0 || g.LeaderName != "" || g.MemberCount != 0 {
				t.Fatal(g, err)
			}
			if err = repo.AddMember(id, charID); err != nil {
				t.Fatal(err)
			}
			var rowID, order int
			var joined string
			if err = db.QueryRow(`SELECT id,order_index,joined_at::text FROM guild_characters WHERE character_id=$1`, charID).Scan(&rowID, &order, &joined); err != nil {
				t.Fatal(err)
			}
			if err = repo.AddMember(id, charID); err != nil {
				t.Fatal("same guild retry", err)
			}
			var same bool
			if err = db.Get(&same, `SELECT id=$2 AND order_index=$3 AND joined_at::text=$4 FROM guild_characters WHERE character_id=$1`, charID, rowID, order, joined); err != nil || !same {
				t.Fatal("retry rewrote membership", same, err)
			}
			g, err = repo.GetByCharID(charID)
			if err != nil || g == nil || g.ID != id || g.MemberCount != 1 {
				t.Fatal(g, err)
			}
			member, err := repo.GetCharacterMembership(charID)
			if err != nil || member == nil || member.GuildID != id || member.IsLeader || member.IsApplicant {
				t.Fatal(member, err)
			}
			all, err := repo.ListAll()
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, guild := range all {
				if guild.ID == id {
					count++
				}
			}
			if count != 1 {
				t.Fatal("temporary guild not listed once", count)
			}
			if err = repo.RemoveCharacter(id, charID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReturnGuildAddMemberPreservesExistingGuild(t *testing.T) {
	repo, db, originalID, charID := setupGuildRepo(t)
	id, err := repo.FindOrCreateReturnGuild(2, "Temporary %d")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.AddMember(id, charID); !errors.Is(err, ErrAlreadyGuildMember) {
		t.Fatal(err)
	}
	g, err := repo.GetByCharID(charID)
	if err != nil || g == nil || g.ID != originalID {
		t.Fatal(g, err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM guild_characters WHERE guild_id=$1`, id); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestReturnGuildAddMemberConcurrentDuplicate(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	userID := CreateTestUser(t, db, "duplicate_return")
	charID := CreateTestCharacter(t, db, userID, "ReturnMember")
	id, err := repo.FindOrCreateReturnGuild(2, "Temporary %d")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.AddMember(id, charID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM guild_characters WHERE character_id=$1`, charID); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestReturnGuildAddMemberConcurrentCapacity(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	userID := CreateTestUser(t, db, "return_capacity")
	id, err := repo.FindOrCreateReturnGuild(2, "Temporary %d")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 59; i++ {
		charID := CreateTestCharacter(t, db, userID, fmt.Sprintf("Member%d", i))
		if err = repo.AddMember(id, charID); err != nil {
			t.Fatal(err)
		}
	}
	char1 := CreateTestCharacter(t, db, userID, "LastMember1")
	char2 := CreateTestCharacter(t, db, userID, "LastMember2")
	results := make(chan error, 2)
	for _, charID := range []uint32{char1, char2} {
		go func(charID uint32) { results <- repo.AddMember(id, charID) }(charID)
	}
	joined, full := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			joined++
		} else if errors.Is(err, ErrGuildFull) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if joined != 1 || full != 1 {
		t.Fatal(joined, full)
	}
	next, err := repo.FindOrCreateReturnGuild(2, "Temporary %d")
	if err != nil || next == id {
		t.Fatal(next, err)
	}
	if err = repo.AddMember(next, char1); errors.Is(err, ErrAlreadyGuildMember) {
		err = repo.AddMember(next, char2)
	}
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM guild_characters WHERE guild_id=$1`, id); err != nil || count != 60 {
		t.Fatal(count, err)
	}
}

func TestReturnGuildLookupDoesNotExposeLeaderlessOrdinaryGuild(t *testing.T) {
	db := SetupTestDB(t)
	repo := NewGuildRepository(db)
	var id uint32
	if err := db.QueryRow(`INSERT INTO guilds(name,leader_id) VALUES('Invalid',0) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	g, err := repo.GetByID(id)
	if err != nil || g != nil {
		t.Fatal(g, err)
	}
	all, err := repo.ListAll()
	if err != nil || len(all) != 0 {
		t.Fatal(all, err)
	}
}

func TestReturnGuildLookupPrefersFullMembership(t *testing.T) {
	repo, db, appliedGuildID, _ := setupGuildRepo(t)
	userID := CreateTestUser(t, db, "return_applicant")
	charID := CreateTestCharacter(t, db, userID, "ReturnMember")
	if err := repo.CreateApplication(appliedGuildID, charID, charID, GuildApplicationTypeApplied); err != nil {
		t.Fatal(err)
	}
	id, err := repo.FindOrCreateReturnGuild(2, "Temporary %d")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.AddMember(id, charID); err != nil {
		t.Fatal(err)
	}
	g, err := repo.GetByCharID(charID)
	if err != nil || g == nil || g.ID != id {
		t.Fatal(g, err)
	}
}

func TestReturnGuildEntryPostgresInfoResponse(t *testing.T) {
	_, db, oldID, charID := setupGuildRepo(t)
	server := guildInfoServer()
	server.erupeConfig.Language = "ko"
	server.guildRepo = NewGuildRepository(db)
	s := createMockSession(charID, server)
	s.prevGuildID = oldID
	handleMsgMhfEntryRookieGuild(s, &mhfpacket.MsgMhfEntryRookieGuild{AckHandle: 123, Unk: 2})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 {
		t.Fatal(ack)
	}
	id := byteframe.NewByteFrameFromBytes(ack.Payload).ReadUint32()
	var exists bool
	if err := db.Get(&exists, `SELECT EXISTS(SELECT 1 FROM guilds WHERE id=$1)`, oldID); err != nil || exists {
		t.Fatal("solo leader's old guild survived successful move", exists, err)
	}
	handleMsgMhfGetGuildManageRight(s, &mhfpacket.MsgMhfGetGuildManageRight{AckHandle: 125})
	rights := readAck(t, s)
	if rights.ErrorCode != 0 || !rights.IsBufferResponse || byteframe.NewByteFrameFromBytes(rights.Payload).ReadUint32() != 1 {
		t.Fatal("follow-up UI reused former guild", rights)
	}
	handleMsgMhfInfoGuild(s, &mhfpacket.MsgMhfInfoGuild{AckHandle: 124})
	info := readAck(t, s)
	if info.ErrorCode != 0 || !info.IsBufferResponse || len(info.Payload) < 20 || byteframe.NewByteFrameFromBytes(info.Payload).ReadUint32() != id {
		t.Fatal(info)
	}
	if s.closed.Load() || len(s.sendPackets) != 0 {
		t.Fatal("unexpected disconnect or extra message")
	}
}
