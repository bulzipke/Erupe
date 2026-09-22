package channelserver

import (
	"testing"

	"erupe-ce/network/mhfpacket"
)

func TestHandleMsgMhfGetUdTacticsPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfAddUdTacticsPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfAddUdTacticsPoint{
		AckHandle: 12345,
	}

	handleMsgMhfAddUdTacticsPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRewardList(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRewardList{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRewardList(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsFollower(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsFollower{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsFollower(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsBonusQuest(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsBonusQuest{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsBonusQuest(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsFirstQuestBonus(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsFirstQuestBonus(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRemainingPoint(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRemainingPoint(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfGetUdTacticsRanking(t *testing.T) {
	server := createMockServer()
	session := createMockSession(1, server)

	pkt := &mhfpacket.MsgMhfGetUdTacticsRanking{
		AckHandle: 12345,
	}

	handleMsgMhfGetUdTacticsRanking(session, pkt)

	select {
	case p := <-session.sendPackets:
		if len(p.data) == 0 {
			t.Error("Response packet should have data")
		}
	default:
		t.Error("No response packet queued")
	}
}

func TestHandleMsgMhfSetUdTacticsFollower(t *testing.T) {
	session, repo := newDivaTacticsFollowerSession()
	pkt := &mhfpacket.MsgMhfSetUdTacticsFollower{
		AckHandle: 12345, NameIndex: 39, Voice: 3, Weapon: 1, Strength: 2,
	}
	handleMsgMhfSetUdTacticsFollower(session, pkt)
	ack := readAck(t, session)
	if ack.AckHandle != pkt.AckHandle || ack.IsBufferResponse || ack.ErrorCode != 0 ||
		repo.setCalls != 1 || repo.choice != (DivaTacticsFollowerChoice{39, 3, 1, 2}) {
		t.Fatalf("wrong follower SET success ACK: %+v; calls=%d choice=%+v", ack, repo.setCalls, repo.choice)
	}

	// The wrapper must return a failed simple ACK, not silently swallow a
	// rejected duplicate or send the GET protocol's buffered response.
	repo.err = errDivaTacticsFollowerLocked
	handleMsgMhfSetUdTacticsFollower(session, pkt)
	ack = readAck(t, session)
	if ack.AckHandle != pkt.AckHandle || ack.IsBufferResponse || ack.ErrorCode != 1 || repo.setCalls != 2 {
		t.Fatalf("wrong follower SET failure ACK: %+v; calls=%d", ack, repo.setCalls)
	}
}

// Tests consolidated from handlers_coverage3_test.go

func TestSimpleAckHandlers_TacticsGo(t *testing.T) {
	server := createMockServer()

	tests := []struct {
		name string
		fn   func(s *Session)
	}{
		{"handleMsgMhfAddUdTacticsPoint", func(s *Session) {
			handleMsgMhfAddUdTacticsPoint(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 1})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := createMockSession(1, server)
			tt.fn(session)
			select {
			case p := <-session.sendPackets:
				if len(p.data) == 0 {
					t.Errorf("%s: response should have data", tt.name)
				}
			default:
				t.Errorf("%s: no response queued", tt.name)
			}
		})
	}
}

func TestNonTrivialHandlers_TacticsGo(t *testing.T) {
	server := createMockServer()

	tests := []struct {
		name string
		fn   func(s *Session)
	}{
		{"handleMsgMhfGetUdTacticsPoint", func(s *Session) {
			handleMsgMhfGetUdTacticsPoint(s, &mhfpacket.MsgMhfGetUdTacticsPoint{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRewardList", func(s *Session) {
			handleMsgMhfGetUdTacticsRewardList(s, &mhfpacket.MsgMhfGetUdTacticsRewardList{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsFollower", func(s *Session) {
			handleMsgMhfGetUdTacticsFollower(s, &mhfpacket.MsgMhfGetUdTacticsFollower{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsBonusQuest", func(s *Session) {
			handleMsgMhfGetUdTacticsBonusQuest(s, &mhfpacket.MsgMhfGetUdTacticsBonusQuest{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsFirstQuestBonus", func(s *Session) {
			handleMsgMhfGetUdTacticsFirstQuestBonus(s, &mhfpacket.MsgMhfGetUdTacticsFirstQuestBonus{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRemainingPoint", func(s *Session) {
			handleMsgMhfGetUdTacticsRemainingPoint(s, &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsRanking", func(s *Session) {
			handleMsgMhfGetUdTacticsRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 1})
		}},
		{"handleMsgMhfGetUdTacticsLog", func(s *Session) {
			handleMsgMhfGetUdTacticsLog(s, &mhfpacket.MsgMhfGetUdTacticsLog{AckHandle: 1})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := createMockSession(1, server)
			tt.fn(session)
			select {
			case p := <-session.sendPackets:
				if len(p.data) == 0 {
					t.Errorf("%s: response should have data", tt.name)
				}
			default:
				t.Errorf("%s: no response queued", tt.name)
			}
		})
	}
}
