package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

type divaMapHandlerRepo struct {
	*divaInterceptionHandlerFakeRepo
	view                     DivaMapView
	ranking                  DivaMapRanking
	mapErr, rankingErr       error
	mapCalls, rankingCalls   int
	charID, guildID, eventID uint32
}

func (r *divaMapHandlerRepo) SettleDivaMaps(time.Time, int) error { return nil }
func (r *divaMapHandlerRepo) GetDivaMap(charID, guildID, eventID uint32, _ time.Time) (DivaMapView, error) {
	r.mapCalls++
	r.charID, r.guildID, r.eventID = charID, guildID, eventID
	return r.view, r.mapErr
}
func (r *divaMapHandlerRepo) BindDivaMapDeparture(uint32, uint32, uint32, uint16, string, time.Time, time.Time) (DivaMapDeparture, error) {
	return DivaMapDeparture{}, nil
}
func (r *divaMapHandlerRepo) GetDivaMapRanking(charID, eventID uint32, _ time.Time) (DivaMapRanking, error) {
	r.rankingCalls++
	r.charID, r.eventID = charID, eventID
	return r.ranking, r.rankingErr
}

func newDivaMapHandlerSession(t *testing.T) (*Session, *divaMapHandlerRepo, DivaEvent) {
	t.Helper()
	s, base, event := newDivaInterceptionHandlerTestSession()
	r := &divaMapHandlerRepo{divaInterceptionHandlerFakeRepo: base, view: DivaMapView{Enabled: true, Map: mustCustomDivaMap(t)}}
	s.server.divaRepo = r
	return s, r, event
}

type divaMapWindowHandlerRepo struct {
	*divaMapHandlerRepo
	mapWindow      DivaEvent
	mapWindowErr   error
	mapWindowCalls int
}

func (r *divaMapWindowHandlerRepo) GetDivaMapRewardEvent(time.Time) (DivaEvent, error) {
	r.mapWindowCalls++
	return r.mapWindow, r.mapWindowErr
}

func TestDivaMapHandlersPreferMapOnlyRewardWindow(t *testing.T) {
	s, base, event := newDivaMapHandlerSession(t)
	// Personal rewards remain unavailable. The map-only window must not reuse
	// that cutover or fall back to it when the new API reports no active map.
	base.rewardWindowErr = errors.New("personal legacy window must not be queried")
	base.interception[event.ID] = DivaInterceptionProgress{}
	mapEvent := DivaEvent{ID: event.ID + 1, StartTime: event.StartTime}
	r := &divaMapWindowHandlerRepo{divaMapHandlerRepo: base, mapWindow: mapEvent}
	s.server.divaRepo = r
	handleDivaMapQuery(s, 19, false)
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || !ack.IsBufferResponse || len(ack.Payload) <= 1 || ack.Payload[0] != 0 ||
		r.mapWindowCalls != 1 || r.mapCalls != 1 || r.eventID != mapEvent.ID {
		t.Fatalf("map-only window not used: %+v; window calls=%d map calls=%d event=%d", ack, r.mapWindowCalls, r.mapCalls, r.eventID)
	}
	for _, err := range []error{nil, errors.New("map window unavailable")} {
		r.mapWindow = DivaEvent{}
		r.mapWindowErr = err
		handleDivaMapQuery(s, 20, false)
		assertDivaMapTerminalFailure(t, readAck(t, s), false)
		if r.mapCalls != 1 {
			t.Fatal("empty/failed map window fell back to another event")
		}
	}
}

func TestDivaMapHandlersServeNativeStateAndGenerate(t *testing.T) {
	s, r, event := newDivaMapHandlerSession(t)
	handleMsgMhfGetUdGuildMapInfo(s, &mhfpacket.MsgMhfGetUdGuildMapInfo{AckHandle: 7})
	ack := readAck(t, s)
	want, err := divaInterceptionMapPayload(r.view.Map)
	if err != nil || ack.ErrorCode != 0 || !ack.IsBufferResponse || !bytes.Equal(ack.Payload, want) {
		t.Fatalf("wrong map reply: %+v, %v", ack, err)
	}
	if r.mapCalls != 1 || r.charID != s.charID || r.guildID != 99 || r.eventID != event.ID {
		t.Fatal("unowned map scope")
	}
	handleMsgMhfGenerateUdGuildMap(s, &mhfpacket.MsgMhfGenerateUdGuildMap{AckHandle: 8})
	ack = readAck(t, s)
	if ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, []byte{0}) {
		t.Fatalf("existing valid map not acknowledged: %+v", ack)
	}
	complete, err := advanceDivaCustomMap(r.view.Map, map[uint16]uint64{0: 80000})
	if err != nil {
		t.Fatal(err)
	}
	r.view.Map = complete.Map
	handleMsgMhfGenerateUdGuildMap(s, &mhfpacket.MsgMhfGenerateUdGuildMap{AckHandle: 9})
	ack = readAck(t, s)
	assertDivaMapTerminalFailure(t, ack, true)
}

func TestDivaMapHandlersFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Session, *divaMapHandlerRepo)
	}{
		{"disabled", func(s *Session, _ *divaMapHandlerRepo) { s.server.erupeConfig.DebugOptions.DivaOverride = 0 }},
		{"unsupported", func(s *Session, _ *divaMapHandlerRepo) { s.server.erupeConfig.RealClientMode = cfg.G10 }},
		{"clock-override", func(s *Session, _ *divaMapHandlerRepo) {
			hour := 12
			s.server.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
		}},
		{"no-character", func(s *Session, _ *divaMapHandlerRepo) { s.charID = 0 }},
		{"no-member", func(s *Session, _ *divaMapHandlerRepo) { s.server.guildRepo = &mockGuildRepo{} }},
		{"applicant", func(s *Session, _ *divaMapHandlerRepo) {
			s.server.guildRepo = &mockGuildRepo{membership: &GuildMember{GuildID: 99, IsApplicant: true}}
		}},
		{"membership-error", func(s *Session, _ *divaMapHandlerRepo) { s.server.guildRepo = nil }},
		{"old-round", func(_ *Session, r *divaMapHandlerRepo) { r.view.Enabled = false }},
		{"repository-error", func(_ *Session, r *divaMapHandlerRepo) { r.mapErr = errors.New("db") }},
		{"malformed", func(_ *Session, r *divaMapHandlerRepo) { r.view.Map = DivaInterceptionMap{} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, generate := range []bool{false, true} {
				s, r, _ := newDivaMapHandlerSession(t)
				tt.change(s, r)
				handleDivaMapQuery(s, 7, generate)
				ack := readAck(t, s)
				assertDivaMapTerminalFailure(t, ack, generate)
			}
		})
	}
}

func TestDivaMapHandlersCompletedMapStopsAfterGenerateRefusal(t *testing.T) {
	s, r, _ := newDivaMapHandlerSession(t)
	complete, err := advanceDivaCustomMap(r.view.Map, map[uint16]uint64{0: 80000})
	if err != nil {
		t.Fatal(err)
	}
	r.view.Map = complete.Map
	handleDivaMapQuery(s, 17, false)
	ack := readAck(t, s)
	if action := nativeDivaMapNextAction(ack, false, true, true); action != "generate" {
		t.Fatalf("completed-map control did not exercise generation: %s", action)
	}
	handleDivaMapQuery(s, 18, true)
	ack = readAck(t, s)
	assertDivaMapTerminalFailure(t, ack, true)
	if r.mapCalls != 2 || ack.AckHandle != 18 {
		t.Fatal("completed map did not finish after query and generate", r.mapCalls, ack)
	}
}

func TestDivaMapHandlersRemainingAndRanking(t *testing.T) {
	s, r, event := newDivaMapHandlerSession(t)
	for _, areas := range []uint32{0, 1, 22} {
		r.view.Map.AcquiredAreas = areas
		handleDivaRemainingAreas(s, &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{AckHandle: 7, Unk0: 99})
		ack := readAck(t, s)
		want := uint32(0)
		if areas == 0 {
			want = 1
		}
		if len(ack.Payload) != 4 || binary.BigEndian.Uint32(ack.Payload) != want {
			t.Fatalf("wrong remaining areas: %+v", ack)
		}
	}
	r.view.Enabled = false
	handleDivaRemainingAreas(s, &mhfpacket.MsgMhfGetUdTacticsRemainingPoint{AckHandle: 8, Unk0: 99})
	if ack := readAck(t, s); binary.BigEndian.Uint32(ack.Payload) != 1 {
		t.Fatal("missing map unlocked hall")
	}
	r.ranking = DivaMapRanking{Enabled: true, Rows: []divaAreaRank{{GuildID: 99, Name: "수렵단", Areas: 22, Rank: 1}}, Own: &divaAreaRank{GuildID: 99, Name: "수렵단", Areas: 22, Rank: 1}}
	handleDivaAreaRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 9, GuildID: 99})
	ack := readAck(t, s)
	want, err := divaAreaRankingPayload(r.ranking.Rows, r.ranking.Own)
	if err != nil || !bytes.Equal(ack.Payload, want) || r.rankingCalls != 1 || r.charID != s.charID || r.eventID != event.ID {
		t.Fatal("ranking not connected", err)
	}
	handleDivaAreaRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 10, GuildID: 999})
	if ack := readAck(t, s); ack.ErrorCode == 0 || r.rankingCalls != 1 {
		t.Fatal("other guild accepted")
	}
	r.rankingErr = errors.New("db")
	handleDivaAreaRanking(s, &mhfpacket.MsgMhfGetUdTacticsRanking{AckHandle: 11})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("database failure hidden")
	}
}
