package channelserver

import (
	"bytes"
	"errors"
	"testing"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaTacticsFollowerCostAndWire(t *testing.T) {
	for weapon := uint16(0); weapon < 2; weapon++ {
		for strength, want := range []uint32{800, 1300, 2300} {
			choice := DivaTacticsFollowerChoice{NameIndex: 39, Voice: 3, Weapon: weapon, Strength: uint16(strength)}
			if cost, ok := divaTacticsFollowerCost(choice); !ok || cost != want {
				t.Fatalf("%+v cost=%d valid=%v", choice, cost, ok)
			}
		}
	}
	for _, choice := range []DivaTacticsFollowerChoice{{NameIndex: 40}, {Voice: 4}, {Weapon: 2}, {Strength: 3}, {NameIndex: 65535}} {
		if _, ok := divaTacticsFollowerCost(choice); ok {
			t.Fatalf("invalid native index accepted: %+v", choice)
		}
	}
	state := DivaTacticsFollowerState{DivaTacticsFollowerChoice: DivaTacticsFollowerChoice{39, 3, 1, 2}, AvailableAt: 0x12345678}
	if got := divaTacticsFollowerPayload(state); !bytes.Equal(got, []byte{0, 39, 0, 3, 0, 1, 0, 2, 0x12, 0x34, 0x56, 0x78}) {
		t.Fatalf("native response %x", got)
	}
	if got := divaTacticsFollowerPayload(DivaTacticsFollowerState{}); !bytes.Equal(got, make([]byte, 12)) {
		t.Fatalf("inactive response %x", got)
	}
}

type divaTacticsFollowerTestRepo struct {
	mockDivaRepo
	state              DivaTacticsFollowerState
	err                error
	getCalls, setCalls int
	choice             DivaTacticsFollowerChoice
	mode               int
}

func (r *divaTacticsFollowerTestRepo) GetDivaTacticsFollower(charID, eventID uint32, override int) (DivaTacticsFollowerState, error) {
	r.getCalls++
	r.mode = override
	return r.state, r.err
}
func (r *divaTacticsFollowerTestRepo) SetDivaTacticsFollower(charID, eventID uint32, choice DivaTacticsFollowerChoice, override int) (DivaTacticsFollowerState, error) {
	r.setCalls++
	r.choice = choice
	r.mode = override
	return r.state, r.err
}

func newDivaTacticsFollowerSession() (*Session, *divaTacticsFollowerTestRepo) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude - 60)}
	repo := &divaTacticsFollowerTestRepo{mockDivaRepo: mockDivaRepo{events: []DivaEvent{event}}, state: DivaTacticsFollowerState{DivaTacticsFollowerChoice: DivaTacticsFollowerChoice{39, 3, 1, 2}, AvailableAt: uint32(TimeAdjusted().Unix() + 86400)}}
	srv.divaRepo = repo
	return createMockSession(56, srv), repo
}

func TestDivaTacticsFollowerAcknowledgements(t *testing.T) {
	s, r := newDivaTacticsFollowerSession()
	getDivaTacticsFollower(s, 17)
	ack := readAck(t, s)
	if ack.AckHandle != 17 || ack.ErrorCode != 0 || !ack.IsBufferResponse || !bytes.Equal(ack.Payload, divaTacticsFollowerPayload(r.state)) || r.getCalls != 1 {
		t.Fatal(ack)
	}
	p := &mhfpacket.MsgMhfSetUdTacticsFollower{AckHandle: 18, NameIndex: 39, Voice: 3, Weapon: 1, Strength: 2}
	setDivaTacticsFollower(s, p)
	ack = readAck(t, s)
	if ack.AckHandle != 18 || ack.IsBufferResponse || ack.ErrorCode != 0 || r.setCalls != 1 || r.choice != r.state.DivaTacticsFollowerChoice || r.mode != -1 {
		t.Fatal(ack, r.choice)
	}
	for _, err := range []error{errDivaTacticsFollowerLocked, errDivaTacticsFollowerFunds, errDivaTacticsFollowerUnavailable, errors.New("db unavailable")} {
		r.err = err
		setDivaTacticsFollower(s, p)
		ack = readAck(t, s)
		if ack.IsBufferResponse || ack.ErrorCode != 1 {
			t.Fatalf("error %v produced %+v", err, ack)
		}
	}
	r.err = errors.New("db unavailable")
	getDivaTacticsFollower(s, 19)
	if ack = readAck(t, s); !ack.IsBufferResponse || ack.ErrorCode != 1 {
		t.Fatal(ack)
	}
}

func TestDivaTacticsFollowerHandlerGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Session, *mhfpacket.MsgMhfSetUdTacticsFollower)
	}{
		{"name", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) { p.NameIndex = 40 }},
		{"voice", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) { p.Voice = 4 }},
		{"weapon", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) { p.Weapon = 2 }},
		{"strength", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) { p.Strength = 3 }},
		{"character", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) { s.charID = 0 }},
		{"disabled", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 0
		}},
		{"prayer", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 1
		}},
		{"welcome", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
			s.server.erupeConfig.DebugOptions.DivaOverride = 3
		}},
		{"client", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
			s.server.erupeConfig.RealClientMode = cfg.Z1
		}},
		{"clock", func(s *Session, p *mhfpacket.MsgMhfSetUdTacticsFollower) {
			hour := 1
			s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r := newDivaTacticsFollowerSession()
			p := &mhfpacket.MsgMhfSetUdTacticsFollower{AckHandle: 1}
			tc.modify(s, p)
			setDivaTacticsFollower(s, p)
			if ack := readAck(t, s); ack.ErrorCode != 1 || ack.IsBufferResponse || r.setCalls != 0 {
				t.Fatal(ack, r.setCalls)
			}
		})
	}
	s, r := newDivaTacticsFollowerSession()
	s.server.erupeConfig.DebugOptions.DivaOverride = 1
	getDivaTacticsFollower(s, 1)
	if ack := readAck(t, s); ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, make([]byte, 12)) || r.getCalls != 0 {
		t.Fatal(ack)
	}
}
