package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func tacticsBonusEventAt(start time.Time) DivaEvent {
	return DivaEvent{ID: 42, StartTime: uint32(start.Unix() - divaPhaseDuration - divaInterlude)}
}

func TestDivaTacticsBonusWireAndCacheClearing(t *testing.T) {
	row := divaTacticsBonusQuest{QuestID: 58062, Start: 0x11223344, End: 0x55667788, Percent: 633}
	got := divaTacticsBonusPayload([]divaTacticsBonusQuest{row})
	wantPrefix := []byte{32, 0xe2, 0xce, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x02, 0x79}
	if len(got) != 385 || !bytes.Equal(got[:13], wantPrefix) || !bytes.Equal(got[13:], make([]byte, 31*12)) {
		t.Fatalf("wrong native bonus response: %x", got)
	}
	empty := divaTacticsBonusPayload(nil)
	if len(empty) != 385 || empty[0] != 32 || !bytes.Equal(empty[1:], make([]byte, 384)) {
		t.Fatal("empty schedule must overwrite all 32 native slots")
	}
	// Model FUN_11537320: it only overwrites the count rows and does not clear.
	var native [32]divaTacticsBonusQuest
	parse := func(payload []byte) {
		for i := 0; i < int(payload[0]) && i < len(native); i++ {
			p := payload[1+i*12:]
			native[i] = divaTacticsBonusQuest{
				QuestID: binary.BigEndian.Uint16(p), Start: binary.BigEndian.Uint32(p[2:]),
				End: binary.BigEndian.Uint32(p[6:]), Percent: binary.BigEndian.Uint16(p[10:]),
			}
		}
	}
	full := make([]divaTacticsBonusQuest, 32)
	for i := range full {
		full[i] = row
	}
	parse(divaTacticsBonusPayload(full))
	parse(got)
	for i := 1; i < len(native); i++ {
		if native[i] != (divaTacticsBonusQuest{}) {
			t.Fatal("shorter schedule retained a ghost bonus")
		}
	}
	parse(empty)
	if native != ([32]divaTacticsBonusQuest{}) {
		t.Fatal("inactive event retained a ghost bonus")
	}
	if got := divaTacticsBonusPayload(append(full, row)); len(got) != 385 || got[0] != 32 {
		t.Fatal("payload exceeded native table capacity")
	}
}

func TestDivaTacticsBonusRebasesExistingOperatingTable(t *testing.T) {
	start := time.Date(2026, 9, 23, 16, 5, 0, 0, divaLocation)
	event := tacticsBonusEventAt(start)
	rows, err := divaTacticsBonusSchedule(event)
	if err != nil || len(rows) != 20 {
		t.Fatalf("expected existing 20-entry catalog: %v, %v", rows, err)
	}
	first := time.Date(2026, 9, 23, 22, 0, 0, 0, divaLocation)
	for i, row := range rows {
		wantStart := first.Add(time.Duration(i) * 8 * time.Hour)
		if row.Start != uint32(wantStart.Unix()) || row.End != uint32(wantStart.Add(2*time.Hour).Unix()-1) {
			t.Fatalf("row %d did not retain two-hour UTC+9 schedule: %+v", i, row)
		}
		if row.QuestID != divaTacticsLegacyBonusQuests[i].QuestID || row.Percent != divaTacticsLegacyBonusQuests[i].Percent {
			t.Fatalf("row %d changed the existing quest/multiplier", i)
		}
		if row.QuestID < udTacticsQuestMin || row.QuestID > udTacticsQuestMax || row.Percent < 100 {
			t.Fatal("invalid operating catalog entry")
		}
	}
	// Golden existing values, independently derived from the former hex payload.
	wantQuests := []uint16{58101, 58053, 58062, 58119, 58097, 58052, 58101, 58050, 58062, 58119,
		58062, 58099, 58051, 58096, 58062, 58101, 58098, 58058, 58119, 58101}
	wantPercent := []uint16{1000, 600, 633, 1050, 600, 600, 1000, 600, 633, 1050,
		633, 650, 600, 600, 633, 1000, 750, 600, 1050, 1000}
	for i, row := range rows {
		if row.QuestID != wantQuests[i] || row.Percent != wantPercent[i] {
			t.Fatalf("original operating row %d changed: %+v", i, row)
		}
	}
	next := event
	next.ID++
	next.StartTime += 35 * 86400
	nextRows, err := divaTacticsBonusSchedule(next)
	if err != nil || len(nextRows) != len(rows) {
		t.Fatal("next round could not reuse operating schedule")
	}
	for i, row := range rows {
		if nextRows[i].Start-row.Start != 35*86400 || nextRows[i].End-row.End != 35*86400 {
			t.Fatal("next round retained old absolute dates")
		}
	}
	check, err := divaTacticsBonusSchedule(event)
	if err != nil || !reflect.DeepEqual(check, rows) {
		t.Fatal("same event generated an unstable schedule")
	}
}

func TestDivaTacticsBonusPhaseClippingAndOverflow(t *testing.T) {
	for _, hour := range []int{0, 12, 16, 23} {
		phaseStart := time.Date(2026, 9, 23, hour, 0, 0, 0, divaLocation)
		event := tacticsBonusEventAt(phaseStart)
		_, phaseEnd := divaInterceptionWindow(event)
		rows, err := divaTacticsBonusSchedule(event)
		if err != nil || len(rows) == 0 {
			t.Fatalf("phase hour %d produced no schedule: %v", hour, err)
		}
		for _, row := range rows {
			if int64(row.Start) < phaseStart.Unix() || int64(row.End) >= phaseEnd.Unix() || row.End < row.Start {
				t.Fatalf("bonus escaped phase: %+v", row)
			}
		}
		if hour == 0 && (len(rows) != 19 || rows[len(rows)-1].End != uint32(phaseEnd.Unix()-1)) {
			t.Fatal("forced midnight phase did not trim late/outside slots")
		}
		if hour == 23 && rows[0].Start != uint32(phaseStart.Unix()) {
			t.Fatal("partial first slot started before interception")
		}
	}
	for _, event := range []DivaEvent{{}, {ID: 1, StartTime: 0xffffffff}} {
		if _, err := divaTacticsBonusSchedule(event); err == nil {
			t.Fatalf("invalid event accepted: %+v", event)
		}
	}
}

func TestDivaTacticsBonusHandlerRoundAndReconnect(t *testing.T) {
	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.DebugOptions.DivaOverride = -1
	event := tacticsBonusEventAt(TimeAdjusted().Add(-time.Hour))
	server.divaRepo = &mockDivaRepo{events: []DivaEvent{event}}
	s := createMockSession(1, server)
	handleMsgMhfGetUdTacticsBonusQuest(s, &mhfpacket.MsgMhfGetUdTacticsBonusQuest{AckHandle: 7})
	ack := readAck(t, s)
	rows, err := divaTacticsBonusSchedule(event)
	if err != nil || ack.ErrorCode != 0 || !bytes.Equal(ack.Payload, divaTacticsBonusPayload(rows)) {
		t.Fatalf("current round schedule not sent: %+v, %v", ack, err)
	}
	s2 := createMockSession(1, server)
	handleMsgMhfGetUdTacticsBonusQuest(s2, &mhfpacket.MsgMhfGetUdTacticsBonusQuest{AckHandle: 8})
	if got := readAck(t, s2); got.ErrorCode != 0 || !bytes.Equal(got.Payload, ack.Payload) {
		t.Fatal("reconnect changed the bonus schedule")
	}
}

func TestDivaTacticsBonusHandlerInactiveAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name            string
		phase           int
		change          func(*Server, *mockDivaRepo)
		wantFail, older bool
	}{
		{name: "disabled", phase: 0},
		{name: "prayer", phase: 1},
		{name: "welcome", phase: 3},
		{name: "no event", phase: -1, change: func(_ *Server, r *mockDivaRepo) { r.events = nil }},
		{name: "before phase", phase: -1, change: func(_ *Server, r *mockDivaRepo) {
			r.events = []DivaEvent{tacticsBonusEventAt(TimeAdjusted().Add(time.Hour))}
		}},
		{name: "after phase", phase: -1, change: func(_ *Server, r *mockDivaRepo) {
			r.events = []DivaEvent{tacticsBonusEventAt(TimeAdjusted().Add(-8 * 24 * time.Hour))}
		}},
		{name: "no repository", phase: -1, wantFail: true, change: func(s *Server, _ *mockDivaRepo) { s.divaRepo = nil }},
		{name: "repository failure", phase: -1, wantFail: true, change: func(_ *Server, r *mockDivaRepo) { r.eventsErr = errors.New("offline") }},
		{name: "shifted clock", phase: -1, wantFail: true, change: func(s *Server, _ *mockDivaRepo) {
			hour := 12
			s.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
		}},
		{name: "older client", phase: -1, older: true, change: func(s *Server, _ *mockDivaRepo) { s.erupeConfig.RealClientMode = cfg.Z2 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := createMockServer()
			server.erupeConfig.RealClientMode = cfg.ZZ
			server.erupeConfig.DebugOptions.DivaOverride = tt.phase
			repo := &mockDivaRepo{events: []DivaEvent{tacticsBonusEventAt(TimeAdjusted().Add(-time.Hour))}}
			server.divaRepo = repo
			if tt.change != nil {
				tt.change(server, repo)
			}
			s := createMockSession(1, server)
			handleMsgMhfGetUdTacticsBonusQuest(s, &mhfpacket.MsgMhfGetUdTacticsBonusQuest{AckHandle: 9})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.wantFail {
				t.Fatalf("incorrect failure status: %+v", ack)
			}
			if tt.wantFail {
				return
			}
			want := divaTacticsBonusPayload(nil)
			if tt.older {
				want = []byte{0}
			}
			if !bytes.Equal(ack.Payload, want) {
				t.Fatalf("inactive schedule leaked bonus entries: %x", ack.Payload)
			}
		})
	}
}

func TestDivaTacticsBonusPointsAreNotMultipliedTwice(t *testing.T) {
	s, repo, event := newDivaInterceptionHandlerTestSession()
	s.server.erupeConfig.DebugOptions.DivaOverride = 2
	s.divaTacticsRun = divaInterceptionRun{EventID: event.ID, GuildID: 99,
		QuestID: 58101, Key: "00000000-0000-4000-8000-000000000001", StartedAt: TimeAdjusted().Add(-time.Minute)}
	// The native calculation has already turned 100 base into 1,000 total
	// with a 1000% row. The wire and ledger must retain 1,000, not 10,000.
	handleDivaTacticsAdd(s, &mhfpacket.MsgMhfAddUdTacticsPoint{AckHandle: 10, QuestID: 58101, TacticsPoints: 1000})
	if ack := readAck(t, s); ack.ErrorCode != 0 || len(repo.newAdds) != 1 || repo.newAdds[0].points != 1000 {
		t.Fatalf("client's bonus total was not saved exactly once: %+v, %+v", ack, repo.newAdds)
	}
}
