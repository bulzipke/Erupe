package channelserver

import (
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	"erupe-ce/network/mhfpacket"
)

func TestMailStruct(t *testing.T) {
	mail := Mail{
		ID:                   123,
		SenderID:             1000,
		RecipientID:          2000,
		Subject:              "Test Subject",
		Body:                 "Test Body Content",
		Read:                 false,
		Deleted:              false,
		Locked:               true,
		AttachedItemReceived: false,
		AttachedItemID:       500,
		AttachedItemAmount:   10,
		CreatedAt:            time.Now(),
		IsGuildInvite:        false,
		IsSystemMessage:      true,
		SenderName:           "TestSender",
	}

	if mail.ID != 123 {
		t.Errorf("ID = %d, want 123", mail.ID)
	}
	if mail.SenderID != 1000 {
		t.Errorf("SenderID = %d, want 1000", mail.SenderID)
	}
	if mail.RecipientID != 2000 {
		t.Errorf("RecipientID = %d, want 2000", mail.RecipientID)
	}
	if mail.Subject != "Test Subject" {
		t.Errorf("Subject = %s, want 'Test Subject'", mail.Subject)
	}
	if mail.Body != "Test Body Content" {
		t.Errorf("Body = %s, want 'Test Body Content'", mail.Body)
	}
	if mail.Read {
		t.Error("Read should be false")
	}
	if mail.Deleted {
		t.Error("Deleted should be false")
	}
	if !mail.Locked {
		t.Error("Locked should be true")
	}
	if mail.AttachedItemReceived {
		t.Error("AttachedItemReceived should be false")
	}
	if mail.AttachedItemID != 500 {
		t.Errorf("AttachedItemID = %d, want 500", mail.AttachedItemID)
	}
	if mail.AttachedItemAmount != 10 {
		t.Errorf("AttachedItemAmount = %d, want 10", mail.AttachedItemAmount)
	}
	if mail.IsGuildInvite {
		t.Error("IsGuildInvite should be false")
	}
	if !mail.IsSystemMessage {
		t.Error("IsSystemMessage should be true")
	}
	if mail.SenderName != "TestSender" {
		t.Errorf("SenderName = %s, want 'TestSender'", mail.SenderName)
	}
}

func TestMailStruct_DefaultValues(t *testing.T) {
	mail := Mail{}

	if mail.ID != 0 {
		t.Errorf("Default ID should be 0, got %d", mail.ID)
	}
	if mail.Subject != "" {
		t.Errorf("Default Subject should be empty, got %s", mail.Subject)
	}
	if mail.Read {
		t.Error("Default Read should be false")
	}
}

// --- Mock-based handler tests ---

func TestHandleMsgMhfListMail_Empty(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{mails: []Mail{}}
	server.mailRepo = mock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfListMail{AckHandle: 100}

	handleMsgMhfListMail(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 4 {
			t.Fatal("Response too short")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfListMail_WithMails(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mails: []Mail{
			{ID: 10, SenderID: 100, Subject: "Hello", SenderName: "Sender1", CreatedAt: time.Now()},
			{ID: 20, SenderID: 200, Subject: "World", SenderName: "Sender2", CreatedAt: time.Now(), Locked: true},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfListMail{AckHandle: 100}

	handleMsgMhfListMail(session, pkt)

	// Verify mailList was populated
	if session.mailList == nil {
		t.Fatal("mailList should be initialized")
	}
	if session.mailList[0] != 10 {
		t.Errorf("mailList[0] = %d, want 10", session.mailList[0])
	}
	if session.mailList[1] != 20 {
		t.Errorf("mailList[1] = %d, want 20", session.mailList[1])
	}
	if session.mailAccIndex != 2 {
		t.Errorf("mailAccIndex = %d, want 2", session.mailAccIndex)
	}

	select {
	case p := <-session.sendPackets:
		if len(p.data) < 10 {
			t.Errorf("Response too short: %d bytes", len(p.data))
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfListMail_DBError(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{listErr: errNotFound}
	server.mailRepo = mock
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfListMail{AckHandle: 100}

	handleMsgMhfListMail(session, pkt)

	select {
	case p := <-session.sendPackets:
		// Should return a fallback response with single zero byte
		if len(p.data) == 0 {
			t.Error("Should have fallback response data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfListMail_OperatorAttachmentWireSender(t *testing.T) {
	for _, tc := range []struct {
		name       string
		senderID   uint32
		system     bool
		received   bool
		wireID     uint32
		wireSystem bool
	}{
		{"operator gift", 0, true, false, 1, false},
		{"already received operator gift", 0, true, true, 1, false},
		{"player mail", 56, false, false, 56, false},
		{"player-originated system notice", 56, true, false, 56, true},
		{"unmarked zero sender", 0, false, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := createMockServer()
			server.mailRepo = &mockMailRepo{mails: []Mail{{ID: 11, SenderID: tc.senderID,
				Subject: "Gift", SenderName: "Operator", CreatedAt: time.Now(), IsSystemMessage: tc.system,
				AttachedItemID: 16, AttachedItemAmount: 5, AttachedItemReceived: tc.received}}}
			session := createMockSession(56, server)
			handleMsgMhfListMail(session, &mhfpacket.MsgMhfListMail{AckHandle: 100})
			ack := readAck(t, session)
			if !ack.IsBufferResponse || ack.ErrorCode != 0 {
				t.Fatalf("mail list response: %+v", ack)
			}
			frame := byteframe.NewByteFrameFromBytes(ack.Payload)
			if count := frame.ReadUint32(); count != 1 {
				t.Fatalf("mail count = %d", count)
			}
			if sender := frame.ReadUint32(); sender != tc.wireID {
				t.Fatalf("wire sender = %d, want %d", sender, tc.wireID)
			}
			frame.ReadUint32() // Created at.
			frame.ReadUint8()  // AccIndex.
			frame.ReadUint8()  // Index.
			flags := frame.ReadUint8()
			if (flags&4 != 0) != tc.wireSystem || (flags&8 != 0) != tc.received {
				t.Fatalf("incorrect system/received wire flags: 0x%x", flags)
			}
			if !frame.ReadBool() {
				t.Fatal("attachment missing from wire record")
			}
			subjectSize, senderSize := frame.ReadUint8(), frame.ReadUint8()
			frame.ReadBytes(uint(subjectSize) + uint(senderSize))
			if amount, item := frame.ReadUint16(), frame.ReadUint16(); amount != 5 || item != 16 {
				t.Fatalf("attachment = %d x %d, want 16 x 5", item, amount)
			}
		})
	}
}

func TestHandleMsgMhfListMail_OperatorAttachmentPickupFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mail  Mail
		flags byte
	}{
		{"unread gift", Mail{AttachedItemID: 2213, AttachedItemAmount: 1}, 0},
		{"read gift", Mail{AttachedItemID: 16, AttachedItemAmount: 5, Read: true}, 1},
		{"locked gift", Mail{AttachedItemID: 16, AttachedItemAmount: 5, Read: true, Locked: true}, 3},
		{"received gift", Mail{AttachedItemID: 16, AttachedItemAmount: 5, Read: true, AttachedItemReceived: true}, 9},
		{"system notice without attachment", Mail{Read: true}, 5},
		{"zero quantity is not a gift", Mail{AttachedItemID: 16}, 4},
		{"guild invitation is not a gift", Mail{AttachedItemID: 16, AttachedItemAmount: 5, IsGuildInvite: true}, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mail := tc.mail
			mail.ID, mail.SenderID, mail.IsSystemMessage = 12, 0, true
			server := createMockServer()
			server.mailRepo = &mockMailRepo{mails: []Mail{mail}}
			session := createMockSession(56, server)
			handleMsgMhfListMail(session, &mhfpacket.MsgMhfListMail{AckHandle: 100})
			ack := readAck(t, session)
			if !ack.IsBufferResponse || ack.ErrorCode != 0 {
				t.Fatalf("mail list response: %+v", ack)
			}
			frame := byteframe.NewByteFrameFromBytes(ack.Payload)
			frame.ReadUint32() // Count.
			if sender := frame.ReadUint32(); sender != systemMailWireSenderID {
				t.Fatalf("wire sender = %d", sender)
			}
			frame.ReadUint32() // Created at.
			frame.ReadUint8()  // AccIndex.
			frame.ReadUint8()  // Index.
			if flags := frame.ReadUint8(); flags != tc.flags {
				t.Fatalf("wire flags = 0x%x, want 0x%x", flags, tc.flags)
			}
			if hasAttachment := frame.ReadBool(); hasAttachment != (mail.AttachedItemID != 0) {
				t.Fatalf("attachment presence changed: %v", hasAttachment)
			}
			// This must be a wire-only translation; notices and DB identity stay intact.
			if !server.mailRepo.(*mockMailRepo).mails[0].IsSystemMessage {
				t.Fatal("operator mail lost its stored system identity")
			}
		})
	}
}

func TestHandleMsgMhfReadMail_Success(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mailByID: map[int]*Mail{
			42: {ID: 42, Body: "Test body content"},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	session.mailList[0] = 42

	pkt := &mhfpacket.MsgMhfReadMail{
		AckHandle: 100,
		AccIndex:  0,
	}

	handleMsgMhfReadMail(session, pkt)

	if mock.markReadCalled != 42 {
		t.Errorf("MarkRead called with %d, want 42", mock.markReadCalled)
	}

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response should have body data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfReadMail_OutOfBounds(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{}
	server.mailRepo = mock
	session := createMockSession(1, server)
	// mailList is nil, so any AccIndex is out of bounds

	pkt := &mhfpacket.MsgMhfReadMail{
		AckHandle: 100,
		AccIndex:  5,
	}

	handleMsgMhfReadMail(session, pkt)

	select {
	case p := <-session.sendPackets:
		// Should get fallback single-byte response
		if len(p.data) == 0 {
			t.Error("Should have fallback response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfReadMail_ZeroMailID(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	// mailList[0] is 0 (default)

	pkt := &mhfpacket.MsgMhfReadMail{
		AckHandle: 100,
		AccIndex:  0,
	}

	handleMsgMhfReadMail(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Should have fallback response")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfOprtMail_Delete(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mailByID: map[int]*Mail{
			42: {ID: 42},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	session.mailList[0] = 42

	pkt := &mhfpacket.MsgMhfOprtMail{
		AckHandle: 100,
		AccIndex:  0,
		Operation: mhfpacket.OperateMailDelete,
	}

	handleMsgMhfOprtMail(session, pkt)

	if mock.markDeletedID != 42 {
		t.Errorf("MarkDeleted called with %d, want 42", mock.markDeletedID)
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfOprtMail_Lock(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mailByID: map[int]*Mail{
			42: {ID: 42},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	session.mailList[0] = 42

	pkt := &mhfpacket.MsgMhfOprtMail{
		AckHandle: 100,
		AccIndex:  0,
		Operation: mhfpacket.OperateMailLock,
	}

	handleMsgMhfOprtMail(session, pkt)

	if mock.lockID != 42 || !mock.lockValue {
		t.Errorf("SetLocked called with ID=%d locked=%v, want ID=42 locked=true", mock.lockID, mock.lockValue)
	}
}

func TestHandleMsgMhfOprtMail_Unlock(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mailByID: map[int]*Mail{
			42: {ID: 42},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	session.mailList[0] = 42

	pkt := &mhfpacket.MsgMhfOprtMail{
		AckHandle: 100,
		AccIndex:  0,
		Operation: mhfpacket.OperateMailUnlock,
	}

	handleMsgMhfOprtMail(session, pkt)

	if mock.lockID != 42 || mock.lockValue {
		t.Errorf("SetLocked called with ID=%d locked=%v, want ID=42 locked=false", mock.lockID, mock.lockValue)
	}
}

func TestHandleMsgMhfOprtMail_AcquireItem(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{
		mailByID: map[int]*Mail{
			42: {ID: 42, AttachedItemID: 100, AttachedItemAmount: 5},
		},
	}
	server.mailRepo = mock
	session := createMockSession(1, server)
	session.mailList = make([]int, 256)
	session.mailList[0] = 42

	pkt := &mhfpacket.MsgMhfOprtMail{
		AckHandle: 100,
		AccIndex:  0,
		Operation: mhfpacket.OperateMailAcquireItem,
	}

	handleMsgMhfOprtMail(session, pkt)

	if mock.itemReceivedID != 42 {
		t.Errorf("MarkItemReceived called with %d, want 42", mock.itemReceivedID)
	}
}

func TestHandleMsgMhfOprtMail_OutOfBounds(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{}
	server.mailRepo = mock
	session := createMockSession(1, server)
	// No mailList set

	pkt := &mhfpacket.MsgMhfOprtMail{
		AckHandle: 100,
		AccIndex:  5,
		Operation: mhfpacket.OperateMailDelete,
	}

	handleMsgMhfOprtMail(session, pkt)

	// Should not have called any repo methods
	if mock.markDeletedID != 0 {
		t.Error("Should not have called MarkDeleted for out-of-bounds access")
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfSendMail_Direct(t *testing.T) {
	server := createMockServer()
	mock := &mockMailRepo{}
	server.mailRepo = mock
	ensureMailService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfSendMail{
		AckHandle:   100,
		RecipientID: 42,
		Subject:     "Hello",
		Body:        "World",
		ItemID:      500,
		Quantity:    3,
	}

	handleMsgMhfSendMail(session, pkt)

	if len(mock.sentMails) != 1 {
		t.Fatalf("Expected 1 sent mail, got %d", len(mock.sentMails))
	}
	sent := mock.sentMails[0]
	if sent.senderID != 1 {
		t.Errorf("SenderID = %d, want 1", sent.senderID)
	}
	if sent.recipientID != 42 {
		t.Errorf("RecipientID = %d, want 42", sent.recipientID)
	}
	if sent.subject != "Hello" {
		t.Errorf("Subject = %s, want Hello", sent.subject)
	}
	if sent.itemID != 500 {
		t.Errorf("ItemID = %d, want 500", sent.itemID)
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfSendMail_Guild(t *testing.T) {
	server := createMockServer()
	mailMock := &mockMailRepo{}
	guildMock := &mockGuildRepo{
		guild:      &Guild{ID: 10},
		membership: &GuildMember{GuildID: 10, CharID: 1},
		members: []*GuildMember{
			{CharID: 100},
			{CharID: 200},
			{CharID: 300},
		},
	}
	server.mailRepo = mailMock
	server.guildRepo = guildMock
	ensureMailService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfSendMail{
		AckHandle:   100,
		RecipientID: 0, // 0 = guild mail
		Subject:     "Guild News",
		Body:        "Important update",
	}

	handleMsgMhfSendMail(session, pkt)

	if len(mailMock.sentMails) != 3 {
		t.Fatalf("Expected 3 sent mails (one per guild member), got %d", len(mailMock.sentMails))
	}
	for i, sent := range mailMock.sentMails {
		if sent.senderID != 1 {
			t.Errorf("Mail %d: SenderID = %d, want 1", i, sent.senderID)
		}
	}
	recipients := map[uint32]bool{}
	for _, sent := range mailMock.sentMails {
		recipients[sent.recipientID] = true
	}
	if !recipients[100] || !recipients[200] || !recipients[300] {
		t.Errorf("Expected recipients 100, 200, 300, got %v", recipients)
	}
}

func TestHandleMsgMhfSendMail_GuildNotFound(t *testing.T) {
	server := createMockServer()
	mailMock := &mockMailRepo{}
	guildMock := &mockGuildRepo{getErr: errNotFound}
	server.mailRepo = mailMock
	server.guildRepo = guildMock
	ensureMailService(server)
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfSendMail{
		AckHandle:   100,
		RecipientID: 0, // Guild mail
		Subject:     "Guild News",
		Body:        "Update",
	}

	handleMsgMhfSendMail(session, pkt)

	if len(mailMock.sentMails) != 0 {
		t.Errorf("No mails should be sent when guild not found, got %d", len(mailMock.sentMails))
	}

	select {
	case <-session.sendPackets:
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfSendMail_GuildApplicantRejected(t *testing.T) {
	server := createMockServer()
	mailMock := &mockMailRepo{}
	server.mailRepo = mailMock
	server.guildRepo = &mockGuildRepo{
		membership: &GuildMember{GuildID: 10, CharID: 1, IsApplicant: true},
		members:    []*GuildMember{{CharID: 100}},
	}
	ensureMailService(server)
	session := createMockSession(1, server)

	handleMsgMhfSendMail(session, &mhfpacket.MsgMhfSendMail{
		AckHandle: 100, RecipientID: 0, Subject: "Guild News", Body: "Update",
	})

	if len(mailMock.sentMails) != 0 {
		t.Fatalf("applicant broadcast %d guild mails, want 0", len(mailMock.sentMails))
	}
}
