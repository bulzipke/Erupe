package channelserver

import (
	"bytes"
	"fmt"
	"testing"

	"erupe-ce/common/byteframe"
	"erupe-ce/common/stringsupport"
	"erupe-ce/network/mhfpacket"
)

func TestFriendListLogin(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	newCharacter := func(account, name string) uint32 {
		t.Helper()
		return CreateTestCharacter(t, db, CreateTestUser(t, db, account), name)
	}
	owner := newCharacter("friend_owner", "Owner")
	first := newCharacter("friend_first", "친구")
	second := newCharacter("friend_second", "Second")
	blocked := newCharacter("friend_blocked", "Blocked")
	deleted := newCharacter("friend_deleted", "Deleted")
	if _, err := db.Exec(`UPDATE characters SET deleted=true WHERE id=$1`, deleted); err != nil {
		t.Fatal(err)
	}
	csv := fmt.Sprintf("bad, %d ,%d,%d,%d,%d,9999999999", first, second, first, deleted, owner)
	if _, err := db.Exec(`UPDATE characters SET friends=$2, blocked=$3 WHERE id=$1`, owner, csv, fmt.Sprint(blocked)); err != nil {
		t.Fatal(err)
	}

	server := createMockServer()
	server.charRepo = NewCharacterRepository(db)
	session := createMockSession(owner, server)
	load := func(s *Session, marker uint8) []byte {
		t.Helper()
		pkt := &mhfpacket.MsgMhfListMember{}
		request := byteframe.NewByteFrameFromBytes([]byte{0, 0, 0, 42, 1, marker})
		if err := pkt.Parse(request, nil); err != nil {
			t.Fatal(err)
		}
		handleMsgMhfListMember(s, pkt)
		ack := readAck(t, s)
		if ack.AckHandle != 42 || !ack.IsBufferResponse || ack.ErrorCode != 0 {
			t.Fatalf("invalid list ACK: %+v", ack)
		}
		return ack.Payload
	}

	legacy := byteframe.NewByteFrame()
	legacy.WriteUint32(1)
	legacy.WriteUint32(blocked)
	legacy.WriteUint32(16)
	legacy.WriteBytes(stringsupport.PaddedString("Blocked", 16, true))
	if got := load(session, 0); !bytes.Equal(got, legacy.Data()) {
		t.Fatalf("stock blacklist response = %x, want %x", got, legacy.Data())
	}
	check := func(s *Session, prefix []byte, want ...FriendListEntry) {
		t.Helper()
		expected := byteframe.NewByteFrame()
		expected.WriteBytes(prefix)
		expected.WriteBytes([]byte("FRL1"))
		expected.WriteUint32(uint32(len(want)))
		for _, row := range want {
			expected.WriteUint32(row.ID)
			expected.WriteBytes(stringsupport.PaddedString(row.Name, 16, true))
		}
		if got := load(s, 0x46); !bytes.Equal(got, expected.Data()) {
			t.Fatalf("friend reply = %x, want %x", got, expected.Data())
		}
	}
	check(session, legacy.Data(), FriendListEntry{ID: first, Name: "친구"}, FriendListEntry{ID: second, Name: "Second"})
	check(createMockSession(blocked, server), make([]byte, 4))

	// Start with the usual canonical form saved by the native friend actions.
	if err := server.charRepo.SaveString(owner, "friends", fmt.Sprint(first)); err != nil {
		t.Fatal(err)
	}
	handleMsgMhfOprMember(session, &mhfpacket.MsgMhfOprMember{AckHandle: 43, CharIDs: []uint32{second}})
	readAck(t, session)
	handleMsgMhfOprMember(session, &mhfpacket.MsgMhfOprMember{AckHandle: 44, Operation: true, CharIDs: []uint32{first}})
	readAck(t, session)
	check(createMockSession(owner, server), legacy.Data(), FriendListEntry{ID: second, Name: "Second"})
}
