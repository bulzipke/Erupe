package channelserver

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaBattleSongWireAndClock(t *testing.T) {
	start := uint32(1700000000)
	state := DivaBattleSongState{Used: 3, ActivationID: 1234, StartedAt: start,
		Effects: []DivaBattleSongEffect{{ID: 8, Used: 2}, {ID: 3, Used: 0}}}
	p := divaBattleSongPayload(state, defaultBeadTypes)
	if len(p) != 22 || p[0] != 0 || p[1] != 3 || binary.BigEndian.Uint32(p[2:6]) != 1234 || binary.BigEndian.Uint32(p[6:10]) != start {
		t.Fatalf("wrong native header: %x", p)
	}
	if !bytes.Equal(p[10:], []byte{0, 8, 2, 0, 3, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("wrong effect counters: %x", p[10:])
	}
	for _, tt := range []struct {
		offset int64
		active bool
	}{{-1, false}, {0, true}, {3599, true}, {3600, false}} {
		if divaBattleSongActive(state, time.Unix(int64(start)+tt.offset, 0)) != tt.active {
			t.Fatal(tt)
		}
	}
	if got := binary.BigEndian.Uint32(divaBattleSongPayload(DivaBattleSongState{}, nil)[6:10]); got != 0xFFFFFFFF {
		t.Fatalf("inactive start %x", got)
	}
}

func TestDivaBattleSongEligibilityBoundaries(t *testing.T) {
	for _, tt := range []struct {
		total int64
		uses  uint8
	}{{-1, 0}, {0, 0}, {499999, 0}, {500000, 1}, {999999, 1}, {1000000, 2}, {100000000, 13}} {
		if got := divaBattleSongEarned(tt.total); got != tt.uses {
			t.Fatalf("%d=>%d want%d", tt.total, got, tt.uses)
		}
	}
	event := DivaEvent{ID: 1, StartTime: 1700000000}
	for _, tt := range []struct {
		delta int64
		valid bool
	}{{divaPhaseDuration, false}, {divaPhaseDuration + divaInterlude - 1, false}, {divaPhaseDuration + divaInterlude, true}, {divaPhaseDuration + divaWeekDuration - 1, true}, {divaPhaseDuration + divaWeekDuration, false}} {
		if divaBattleSongPhase(event, time.Unix(int64(event.StartTime)+tt.delta, 0)) != tt.valid {
			t.Fatal(tt)
		}
	}
	for _, ids := range [][]uint16{nil, {0}, {26}, {1, 1}, {1, 2, 3, 4, 5}} {
		if _, ok := divaBattleSongEffectIDs(ids); ok {
			t.Fatalf("accepted %v", ids)
		}
	}
	if ids, ok := divaBattleSongEffectIDs([]uint16{8, 3, 1}); !ok || ids[0] != 1 || ids[2] != 8 {
		t.Fatal(ids, ok)
	}
}

type divaBattleSongTestRepo struct {
	mockDivaRepo
	state                   DivaBattleSongState
	err                     error
	usedCalls, consumeCalls int
}

func (r *divaBattleSongTestRepo) GetDivaBattleSong(uint32, uint32) (DivaBattleSongState, error) {
	return r.state, r.err
}
func (r *divaBattleSongTestRepo) UseDivaBattleSong(uint32, uint32) (DivaBattleSongState, error) {
	r.usedCalls++
	return r.state, r.err
}
func (r *divaBattleSongTestRepo) ConsumeDivaBattleSongEffects(uint32, divaBattleSongRun, []uint16, time.Time) error {
	r.consumeCalls++
	return r.err
}

func newDivaBattleSongSession() (*Session, *divaBattleSongTestRepo) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	event := DivaEvent{ID: 42, StartTime: uint32(TimeAdjusted().Unix() - divaPhaseDuration - divaInterlude - 60)}
	repo := &divaBattleSongTestRepo{mockDivaRepo: mockDivaRepo{events: []DivaEvent{event}}, state: DivaBattleSongState{Used: 1, ActivationID: 55, StartedAt: uint32(TimeAdjusted().Unix())}}
	srv.divaRepo = repo
	return createMockSession(56, srv), repo
}

func TestDivaBattleSongHandlersUseBufferedAck(t *testing.T) {
	s, r := newDivaBattleSongSession()
	handleMsgMhfUseRewardSong(s, &mhfpacket.MsgMhfUseRewardSong{AckHandle: 9})
	ack := readAck(t, s)
	if !ack.IsBufferResponse || !bytes.Equal(ack.Payload, []byte{0}) || r.usedCalls != 1 {
		t.Fatalf("bad use ack %+v", ack)
	}
	handleMsgMhfGetRewardSong(s, &mhfpacket.MsgMhfGetRewardSong{AckHandle: 10})
	ack = readAck(t, s)
	if !ack.IsBufferResponse || len(ack.Payload) != 22 || ack.Payload[1] != 1 || binary.BigEndian.Uint32(ack.Payload[2:6]) != 55 {
		t.Fatalf("bad get %+v", ack)
	}
	r.err = errDivaBattleSongUnavailable
	handleMsgMhfUseRewardSong(s, &mhfpacket.MsgMhfUseRewardSong{AckHandle: 11})
	if ack = readAck(t, s); !ack.IsBufferResponse || !bytes.Equal(ack.Payload, []byte{1}) {
		t.Fatal(ack)
	}
}

func TestDivaBattleSongRejectsUnsafeRequests(t *testing.T) {
	for _, mode := range []int{0, 1, 3} {
		s, r := newDivaBattleSongSession()
		s.server.erupeConfig.DebugOptions.DivaOverride = mode
		handleMsgMhfUseRewardSong(s, &mhfpacket.MsgMhfUseRewardSong{AckHandle: 1})
		if ack := readAck(t, s); !bytes.Equal(ack.Payload, []byte{1}) || r.usedCalls != 0 {
			t.Fatal(mode, ack)
		}
	}
	s, r := newDivaBattleSongSession()
	hour := 1
	s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
	handleMsgMhfUseRewardSong(s, &mhfpacket.MsgMhfUseRewardSong{AckHandle: 1})
	if ack := readAck(t, s); !bytes.Equal(ack.Payload, []byte{1}) || r.usedCalls != 0 {
		t.Fatal(ack)
	}
	s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = nil
	p := &mhfpacket.MsgMhfAddRewardSongCount{AckHandle: 2, PrayerID: 55, Count: 1, ArraySizeBytes: 2, Entries: []uint16{8}}
	handleMsgMhfAddRewardSongCount(s, p)
	if ack := readAck(t, s); !bytes.Equal(ack.Payload, []byte{1}) || r.consumeCalls != 0 {
		t.Fatal(ack)
	}
	s.divaBattleRun = divaBattleSongRun{Key: divaRunKeyOne, ActivationID: 55}
	handleMsgMhfAddRewardSongCount(s, p)
	if ack := readAck(t, s); !ack.IsBufferResponse || !bytes.Equal(ack.Payload, []byte{0}) || r.consumeCalls != 1 {
		t.Fatal(ack)
	}
}

func TestDivaBattleSongDepartureRetainsStableRun(t *testing.T) {
	s, r := newDivaBattleSongSession()
	s.questWeaponGeneration = 1
	s.divaBattleRun = divaBattleSongRun{Generation: 1, StartedAt: TimeAdjusted()}
	s.captureDivaBattleSongDeparture(123, 1)
	run := s.divaBattleRun
	if !validDivaInterceptionRunKey(run.Key) || run.ActivationID != r.state.ActivationID || run.EventID != 42 || run.QuestID != 123 {
		t.Fatalf("bad run %+v", run)
	}
	s.captureDivaBattleSongDeparture(123, 1)
	if s.divaBattleRun != run {
		t.Fatal("duplicate departure replaced receipt key")
	}
	s.questWeaponGeneration = 2
	s.divaBattleRun = divaBattleSongRun{Generation: 2, StartedAt: TimeAdjusted()}
	s.captureDivaBattleSongDeparture(123, 1)
	if s.divaBattleRun.Key != "" {
		t.Fatal("stale generation captured")
	}
}
