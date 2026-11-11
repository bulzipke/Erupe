package channelserver

import (
	"testing"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestGuildAllianceApplications(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	server := guildInfoServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	repo := NewGuildRepository(db)
	server.guildRepo = repo
	newGuild := func(name string) (uint32, *Session) {
		t.Helper()
		userID := CreateTestUser(t, db, name)
		charID := CreateTestCharacter(t, db, userID, name)
		guildID := CreateTestGuild(t, db, charID, name+"Guild")
		if _, err := db.Exec(`UPDATE guilds SET rank_rp = 24 WHERE id = $1`, guildID); err != nil {
			t.Fatal(err)
		}
		return guildID, createMockSession(charID, server)
	}
	parentID, parent := newGuild("Parent")
	otherID, other := newGuild("Other")
	firstID, first := newGuild("First")
	secondID, second := newGuild("Second")
	thirdID, third := newGuild("Third")
	memberID := CreateTestCharacter(t, db, CreateTestUser(t, db, "ParentMember"), "ParentMember")
	if _, err := db.Exec(`INSERT INTO guild_characters (guild_id, character_id) VALUES ($1, $2)`, parentID, memberID); err != nil {
		t.Fatal(err)
	}
	parentMember := createMockSession(memberID, server)

	createAlliance := func(guildID, charID uint32, name string) uint32 {
		t.Helper()
		if err := repo.CreateAllianceForMember(name, guildID, charID); err != nil {
			t.Fatal(err)
		}
		guild, err := repo.GetByID(guildID)
		if err != nil || guild == nil || guild.AllianceID == 0 {
			t.Fatalf("created alliance missing: %+v, %v", guild, err)
		}
		return guild.AllianceID
	}
	allianceID := createAlliance(parentID, parent.charID, "ParentAlliance")
	otherAllianceID := createAlliance(otherID, other.charID, "OtherAlliance")

	operate := func(s *Session, targetAlliance, actorGuild, applicantGuild uint32, action uint8, succeed bool) {
		t.Helper()
		bf := byteframe.NewByteFrame()
		bf.WriteUint32(42)
		bf.WriteUint32(targetAlliance)
		bf.WriteUint32(actorGuild)
		bf.WriteUint8(action)
		bf.WriteUint8(0)
		bf.WriteUint32(applicantGuild)
		pkt := &mhfpacket.MsgMhfOperateJoint{}
		if err := pkt.Parse(byteframe.NewByteFrameFromBytes(bf.Data()), nil); err != nil {
			t.Fatal(err)
		}
		handleMsgMhfOperateJoint(s, pkt)
		ack := readAck(t, s)
		if ack.AckHandle != 42 || ack.IsBufferResponse || (ack.ErrorCode == 0) != succeed {
			t.Fatalf("alliance action %#x with actor %d: ACK %+v, want success=%t", action, s.charID, ack, succeed)
		}
	}

	operate(first, allianceID, firstID, 0, 0x02, false) // Closed recruiting.
	operate(parent, allianceID, parentID, 0, 0x05, true)
	operate(other, otherAllianceID, otherID, 0, 0x05, true)
	operate(second, allianceID, firstID, 0, 0x02, false) // Forged guild ID.
	operate(first, allianceID, firstID, 0, 0x02, true)
	operate(first, allianceID, firstID, 0, 0x02, false) // Duplicate.
	operate(first, otherAllianceID, firstID, 0, 0x02, false)
	if err := repo.CreateAllianceForMember("PendingAlliance", firstID, first.charID); err == nil {
		t.Fatal("guild with an outstanding application created an alliance")
	}
	operate(other, allianceID, otherID, firstID, 0x07, false)
	operate(parentMember, allianceID, parentID, firstID, 0x07, false)
	operate(other, otherAllianceID, otherID, firstID, 0x08, false)
	operate(second, allianceID, firstID, 0, 0x03, false)

	checkGuildInfo := func(s *Session, guildID, wantAlliance uint32, wantMember bool, wantApplicant uint32) {
		t.Helper()
		handleMsgMhfInfoGuild(s, &mhfpacket.MsgMhfInfoGuild{AckHandle: 43, GuildID: guildID})
		ack := readAck(t, s)
		if ack.AckHandle != 43 || ack.ErrorCode != 0 || !ack.IsBufferResponse {
			t.Fatalf("invalid guild info response: %+v", ack)
		}
		bf := byteframe.NewByteFrameFromBytes(ack.Payload)
		reportedGuild := bf.ReadUint32()
		bf.ReadBytes(27) // Guild summary through the timestamps.
		stringsLength := uint(bf.ReadUint8()) + uint(bf.ReadUint8()) + uint(bf.ReadUint8()) + uint(bf.ReadUint8())
		bf.ReadBytes(stringsLength + 4) // Strings and the four-byte tail.
		bf.ReadBytes(8)                 // Guild type, flags, version and event RP.
		for i := 0; i < 3; i++ {
			bf.ReadBytes(uint(bf.ReadUint8()))
		}
		bf.ReadBytes(23) // Outfits, member limit and room fields.
		if got := bf.ReadUint32(); got != wantAlliance {
			t.Fatalf("reported alliance = %d, want %d", got, wantAlliance)
		}
		isMember := false
		if wantAlliance != 0 {
			bf.ReadBytes(8) // Alliance creation date, member total and flags.
			bf.ReadBytes(uint(bf.ReadUint16()))
			guildCount := bf.ReadUint8()
			for i := uint8(0); i < guildCount; i++ {
				memberGuild := bf.ReadUint32()
				bf.ReadUint32()
				memberFlag := bf.ReadUint16()
				isMember = isMember || (memberGuild == reportedGuild && memberFlag != 0)
				bf.ReadBytes(4)
				bf.ReadBytes(uint(bf.ReadUint16()))
				bf.ReadBytes(uint(bf.ReadUint16()))
			}
		}
		if isMember != wantMember {
			t.Fatalf("alliance member = %t, want %t", isMember, wantMember)
		}
		if bf.ReadUint16() != 0 || bf.ReadUint8() != 0 {
			t.Fatal("unexpected guild applicant or activity entries")
		}
		count := bf.ReadUint8()
		if wantApplicant == 0 {
			if count != 0 || bf.Err() != nil {
				t.Fatalf("unexpected alliance applications: count=%d, err=%v", count, bf.Err())
			}
			return
		}
		if count != 1 || bf.ReadUint32() != wantApplicant || bf.ReadUint32() != first.charID ||
			bf.ReadUint16() != 0 || bf.ReadUint16() != 1 || bf.ReadUint16() != 1 ||
			string(bf.ReadBytes(uint(bf.ReadUint16()))) != "FirstGuild\x00" ||
			string(bf.ReadBytes(uint(bf.ReadUint16()))) != "First\x00" || bf.Err() != nil {
			t.Fatal("invalid alliance applicant entry")
		}
	}
	checkGuildInfo(first, 0, allianceID, false, 0)
	checkGuildInfo(parent, 0, allianceID, true, firstID)
	checkGuildInfo(parentMember, 0, allianceID, true, 0)
	checkGuildInfo(other, parentID, allianceID, true, 0)
	checkGuildInfo(other, firstID, 0, false, 0)

	operate(first, allianceID, firstID, 0, 0x03, true) // Cancel.
	checkGuildInfo(first, 0, 0, false, 0)
	operate(first, allianceID, firstID, 0, 0x02, true)
	operate(parent, allianceID, parentID, firstID, 0x08, true) // Reject.
	checkGuildInfo(first, 0, 0, false, 0)

	operate(first, allianceID, firstID, 0, 0x02, true)
	operate(second, allianceID, secondID, 0, 0x02, true)
	operate(third, allianceID, thirdID, 0, 0x02, true)
	operate(parent, allianceID, parentID, firstID, 0x07, true)
	operate(parent, allianceID, parentID, secondID, 0x07, true)
	operate(parent, allianceID, parentID, thirdID, 0x07, false)
	checkGuildInfo(third, 0, allianceID, false, 0) // A failed approval preserves the request.
	checkGuildInfo(first, 0, allianceID, true, 0)
	operate(first, otherAllianceID, firstID, 0, 0x02, false) // Already joined.
	operate(first, allianceID, firstID, 0, 0x03, true)       // Leave, opening a slot.
	operate(parent, allianceID, parentID, thirdID, 0x07, true)
	checkGuildInfo(third, 0, allianceID, true, 0)
	operate(parent, allianceID, parentID, 0, 0x04, true)
	operate(second, allianceID, secondID, 0, 0x03, true)
	operate(first, allianceID, firstID, 0, 0x02, false) // Closed again.
	operate(first, otherAllianceID, firstID, 0, 0x02, true)
	operate(other, otherAllianceID, otherID, 0, 0x01, true) // Disband cascades.
	checkGuildInfo(first, 0, 0, false, 0)
}
