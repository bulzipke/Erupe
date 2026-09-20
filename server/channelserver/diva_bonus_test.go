package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func divaBonusTestRule() cfg.DivaBonusTarget {
	return cfg.DivaBonusTarget{Color: 1, TargetType: "monster", TargetID: 1,
		StartOffsetSeconds: 0, EndOffsetSeconds: 86400, MultiplierPercent: 200}
}

func TestDivaBonusWireLayout(t *testing.T) {
	got := divaBonusPayload([]divaBonusTarget{{Color: 4, Kind: 6, TargetID: 40217, Start: 0x11223344, End: 0x55667788, Percent: 200}})
	want := []byte{0, 1, 4, 6, 0, 0, 0x9d, 0x19, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0, 0xc8}
	if !bytes.Equal(got, want) {
		t.Fatalf("wrong native bonus wire: %x want %x", got, want)
	}
	if !bytes.Equal(divaBonusPayload(nil), []byte{0, 0}) {
		t.Fatal("empty list must be u16")
	}
}

func TestDivaBonusScheduleBoundaries(t *testing.T) {
	start := divaTestTime(20, 0, 0)
	event := DivaEvent{ID: 42, StartTime: uint32(start.Unix())}
	first := divaBonusTestRule()
	first.StartOffsetSeconds, first.EndOffsetSeconds = 10*3600, 14*3600
	second := first
	second.StartOffsetSeconds, second.EndOffsetSeconds = 14*3600, 18*3600
	second.TargetType, second.TargetID = "quest", 40217
	third := first
	third.Color, third.StartOffsetSeconds = 2, 12*3600
	rules := []cfg.DivaBonusTarget{second, third, first}
	rows, err := divaBonusTargets(event, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Kind != 5 || rows[2].Kind != 6 {
		t.Fatalf("wrong order/kinds: %+v", rows)
	}
	if rows[0].End+1 != rows[2].Start || rows[1].Start != uint32(start.Add(12*time.Hour).Unix()) {
		t.Fatal("exclusive end conversion or exact-noon start changed")
	}
	if rules[0] != second {
		t.Fatal("builder mutated shared config")
	}
	if _, err := divaBonusTargets(DivaEvent{ID: 1, StartTime: 0xfffffff0}, rules); err == nil {
		t.Fatal("timestamp overflow accepted")
	}
	if _, err := divaBonusTargets(DivaEvent{}, rules); err == nil {
		t.Fatal("missing event accepted")
	}
}

func TestDivaBonusNativeBucketLimits(t *testing.T) {
	event := DivaEvent{ID: 1, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	var rules []cfg.DivaBonusTarget
	for i := 0; i < 10; i++ {
		r := divaBonusTestRule()
		r.StartOffsetSeconds, r.EndOffsetSeconds = int64(i*60), int64((i+1)*60)
		rules = append(rules, r)
	}
	if _, err := divaBonusTargets(event, rules); err != nil {
		t.Fatal(err)
	}
	r := divaBonusTestRule()
	r.StartOffsetSeconds, r.EndOffsetSeconds = 12*3600, 12*3600+60
	if _, err := divaBonusTargets(event, append(rules, r)); err == nil {
		t.Fatal("exact noon must be counted in previous bucket, which is full")
	}
	r.StartOffsetSeconds++
	if _, err := divaBonusTargets(event, append(rules, r)); err != nil {
		t.Fatal(err)
	}
}

func TestDivaBonusHandlerAndAlreadyCalculatedPoints(t *testing.T) {
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	srv.erupeConfig.GameplayOptions.DivaBonusTargets = []cfg.DivaBonusTarget{divaBonusTestRule()}
	repo := &mockDivaRepo{events: []DivaEvent{{ID: 42, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}}}
	srv.divaRepo = repo
	s := createMockSession(56, srv)
	handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
	ack := readAck(t, s)
	if ack.ErrorCode != 0 || len(ack.Payload) != 18 || binary.BigEndian.Uint16(ack.Payload[:2]) != 1 {
		t.Fatalf("configured schedule not sent: %+v", ack)
	}
	// 60 base + 60 native target addition, plus 60 Premium. Do not multiply again.
	handleMsgMhfAddUdPoint(s, &mhfpacket.MsgMhfAddUdPoint{AckHandle: 8, QuestPoints: 120, BonusPoints: 60})
	if ack := readAck(t, s); ack.ErrorCode != 0 {
		t.Fatal("point save failed")
	}
	if got := repo.points[[2]uint32{56, 42}]; got != [2]int64{120, 60} {
		t.Fatalf("bonus multiplied twice: %v", got)
	}
	// Each session receives all colors and the same anchored schedule.
	s2 := createMockSession(56, srv)
	handleMsgMhfGetUdBonusQuestInfo(s2, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 9})
	if got := readAck(t, s2); !bytes.Equal(got.Payload, ack.Payload) {
		t.Fatal("reconnect shifted schedule")
	}
}

func TestDivaBonusHandlerInactiveAndFailure(t *testing.T) {
	for _, tt := range []struct {
		name                                          string
		phase                                         int
		age                                           time.Duration
		missing, dbFail, invalid, oldClient, wantFail bool
	}{
		{name: "disabled", phase: 0},
		{name: "interception", phase: 2},
		{name: "welcome", phase: 3},
		{name: "future", phase: -1, age: -time.Hour},
		{name: "expired", phase: -1, age: 8 * 24 * time.Hour},
		{name: "no event", phase: -1, missing: true},
		{name: "database failure", phase: -1, dbFail: true, wantFail: true},
		{name: "invalid schedule", phase: -1, age: time.Hour, invalid: true, wantFail: true},
		{name: "unverified client", phase: -1, age: time.Hour, oldClient: true, wantFail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = tt.phase
			rule := divaBonusTestRule()
			if tt.invalid {
				rule.MultiplierPercent = 2
			}
			if tt.oldClient {
				srv.erupeConfig.RealClientMode = cfg.Z2
			}
			srv.erupeConfig.GameplayOptions.DivaBonusTargets = []cfg.DivaBonusTarget{rule}
			repo := &mockDivaRepo{events: []DivaEvent{{ID: 1, StartTime: uint32(TimeAdjusted().Add(-tt.age).Unix())}}}
			if tt.missing {
				repo.events = nil
			}
			if tt.dbFail {
				repo.eventsErr = errors.New("offline")
			}
			srv.divaRepo = repo
			s := createMockSession(1, srv)
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.wantFail {
				t.Fatalf("wrong status: %+v", ack)
			}
			if !tt.wantFail && !bytes.Equal(ack.Payload, []byte{0, 0}) {
				t.Fatalf("inactive schedule leaked: %x", ack.Payload)
			}
		})
	}
}

func TestDivaBonusRejectsUnavailableBeadColor(t *testing.T) {
	for _, tt := range []struct {
		name     string
		beads    []int
		err      error
		wantFail bool
	}{
		{"default four", nil, nil, false},
		{"configured three", []int{1, 3, 4}, nil, true},
		{"configured four", []int{1, 3, 4, 8}, nil, false},
		{"database failure", nil, errors.New("offline"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = -1
			r := divaBonusTestRule()
			r.Color = 4
			srv.erupeConfig.GameplayOptions.DivaBonusTargets = []cfg.DivaBonusTarget{r}
			srv.divaRepo = &mockDivaRepo{beads: tt.beads, beadsErr: tt.err,
				events: []DivaEvent{{ID: 1, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}}}
			s := createMockSession(1, srv)
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
			if ack := readAck(t, s); (ack.ErrorCode != 0) != tt.wantFail {
				t.Fatalf("unavailable color handling: %+v", ack)
			}
		})
	}
}

// These fixtures assume an event starting at UTC+9 midnight. Each of the eight
// native noon buckets gets ten non-overlapping entries per available color.
// The returned order is chronological, then color, independently of the builder.
func divaBonusTestCapacityRules(colors int) []cfg.DivaBonusTarget {
	rules := make([]cfg.DivaBonusTarget, 0, 8*colors*10)
	for day := 0; day < 8; day++ {
		base := int64(0)
		if day > 0 {
			base = int64(day*86400 - 43200 + 1)
		}
		for slot := 0; slot < 10; slot++ {
			for color := 1; color <= colors; color++ {
				start := base + int64(slot*60)
				rules = append(rules, cfg.DivaBonusTarget{
					Color: color, TargetType: "monster", TargetID: 1,
					StartOffsetSeconds: start, EndOffsetSeconds: start + 30,
					MultiplierPercent: 100 + len(rules),
				})
			}
		}
	}
	return rules
}

// Decode the wire independently rather than comparing two payloads generated by
// the same serializer. The caller supplies rules in chronological/color order.
func assertDivaBonusTestRows(t *testing.T, payload []byte, event DivaEvent, rules []cfg.DivaBonusTarget) {
	t.Helper()
	if len(payload) != 2+16*len(rules) {
		t.Fatalf("payload length = %d, want %d", len(payload), 2+16*len(rules))
	}
	if got := binary.BigEndian.Uint16(payload[:2]); int(got) != len(rules) {
		t.Fatalf("u16 row count = %d, want %d", got, len(rules))
	}
	for i, rule := range rules {
		row := payload[2+i*16 : 2+(i+1)*16]
		kind := byte(5)
		if rule.TargetType == "quest" {
			kind = 6
		}
		wantStart := uint32(int64(event.StartTime) + rule.StartOffsetSeconds)
		wantEnd := uint32(int64(event.StartTime) + rule.EndOffsetSeconds - 1)
		if row[0] != byte(rule.Color) || row[1] != kind ||
			binary.BigEndian.Uint32(row[2:6]) != uint32(rule.TargetID) ||
			binary.BigEndian.Uint32(row[6:10]) != wantStart ||
			binary.BigEndian.Uint32(row[10:14]) != wantEnd ||
			binary.BigEndian.Uint16(row[14:16]) != uint16(rule.MultiplierPercent) {
			t.Fatalf("row %d = %x, want color=%d kind=%d target=%d start=%d end=%d percent=%d",
				i, row, rule.Color, kind, rule.TargetID, wantStart, wantEnd, rule.MultiplierPercent)
		}
	}
}

func TestDivaBonusFullCapacityWire(t *testing.T) {
	// Mock-only: no database connection, event creation or persistence is tested.
	event := DivaEvent{ID: 42, StartTime: uint32(TimeMidnight().Add(-24 * time.Hour).Unix())}
	all := divaBonusTestCapacityRules(4)
	for _, count := range []int{255, 256, 320} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rules := all[:count]
			if err := cfg.ValidateDivaBonusTargets(rules); err != nil {
				t.Fatalf("invalid capacity fixture: %v", err)
			}
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = -1
			srv.erupeConfig.GameplayOptions.DivaBonusTargets = rules
			srv.divaRepo = &mockDivaRepo{events: []DivaEvent{event}}
			s := createMockSession(56, srv)
			// This request has only an ACK handle, not a request limit or page.
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 7})
			ack := readAck(t, s)
			if ack.ErrorCode != 0 || ack.AckHandle != 7 || !ack.IsBufferResponse {
				t.Fatalf("capacity request failed: %+v", ack)
			}
			assertDivaBonusTestRows(t, ack.Payload, event, rules)
			if ack.PayloadSize != uint(2+16*count) {
				t.Fatalf("ACK size = %d, want %d", ack.PayloadSize, 2+16*count)
			}
			if count == 320 && len(ack.Payload) != 5122 {
				t.Fatalf("full native schedule must be 5122 bytes, got %d", len(ack.Payload))
			}
			if len(s.sendPackets) != 0 {
				t.Fatal("full schedule was split across multiple responses")
			}
		})
	}

	t.Run("321 rejected without partial payload", func(t *testing.T) {
		extra := divaBonusTestRule()
		extra.StartOffsetSeconds, extra.EndOffsetSeconds = 3600, 3630
		rules := append(append([]cfg.DivaBonusTarget(nil), all...), extra)
		if err := cfg.ValidateDivaBonusTargets(rules); err == nil {
			t.Fatal("321-entry configuration accepted")
		}
		if _, err := divaBonusTargets(event, rules); err == nil {
			t.Fatal("321-entry schedule accepted by builder")
		}
		srv := createMockServer()
		srv.erupeConfig.RealClientMode = cfg.ZZ
		srv.erupeConfig.DebugOptions.DivaOverride = -1
		srv.erupeConfig.GameplayOptions.DivaBonusTargets = rules
		srv.divaRepo = &mockDivaRepo{events: []DivaEvent{event}}
		s := createMockSession(56, srv)
		handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 8})
		ack := readAck(t, s)
		if ack.ErrorCode == 0 || ack.AckHandle != 8 || len(ack.Payload) != 0 {
			t.Fatalf("oversized schedule was accepted or partially transmitted: %+v", ack)
		}
	})
}

func TestDivaBonusThreeColorSlotsMatchKijuInfo(t *testing.T) {
	// Effect IDs are deliberately different from the 1-based color slot IDs.
	beads := []int{1, 3, 4}
	event := DivaEvent{ID: 42, StartTime: uint32(TimeMidnight().Add(-24 * time.Hour).Unix())}
	rules := divaBonusTestCapacityRules(3)
	srv := createMockServer()
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.DebugOptions.DivaOverride = -1
	srv.erupeConfig.GameplayOptions.DivaBonusTargets = rules
	srv.divaRepo = &mockDivaRepo{events: []DivaEvent{event}, beads: beads}
	s := createMockSession(56, srv)
	handleMsgMhfGetKijuInfo(s, &mhfpacket.MsgMhfGetKijuInfo{AckHandle: 10})
	kiju := readAck(t, s)
	if kiju.ErrorCode != 0 || len(kiju.Payload) != 1+3*546 || kiju.Payload[0] != 3 {
		t.Fatalf("wrong three-bead response: %+v", kiju)
	}
	for i, effect := range beads {
		row := kiju.Payload[1+i*546 : 1+(i+1)*546]
		if row[544] != byte(i+1) || row[545] != byte(effect) {
			t.Fatalf("bead %d: color=%d effect=%d, want color=%d effect=%d",
				i, row[544], row[545], i+1, effect)
		}
	}
	handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 11})
	bonus := readAck(t, s)
	if bonus.ErrorCode != 0 || bonus.AckHandle != 11 || len(bonus.Payload) != 3842 {
		t.Fatalf("three-color schedule not sent in full: %+v", bonus)
	}
	assertDivaBonusTestRows(t, bonus.Payload, event, rules)
	var counts [3]int
	for i := range rules {
		color := bonus.Payload[2+i*16]
		if color < 1 || color > 3 {
			t.Fatalf("effect ID leaked into bonus color: %d", color)
		}
		counts[color-1]++
	}
	if counts != [3]int{80, 80, 80} {
		t.Fatalf("not every color received its complete schedule: %v", counts)
	}
}

func TestDivaBonusAnchorMatchesScheduleResponse(t *testing.T) {
	// This tests agreement between two handlers using a pre-seeded mock event.
	// It does not claim to test real DB persistence or renewal across restarts.
	event := DivaEvent{ID: 42, StartTime: uint32(TimeMidnight().Add(-24 * time.Hour).Unix())}
	rules := []cfg.DivaBonusTarget{
		{Color: 1, TargetType: "monster", TargetID: 1, StartOffsetSeconds: 0, EndOffsetSeconds: 25 * 3600, MultiplierPercent: 200},
		{Color: 2, TargetType: "monster", TargetID: 14, StartOffsetSeconds: 12 * 3600, EndOffsetSeconds: 14 * 3600, MultiplierPercent: 300},
		{Color: 3, TargetType: "monster", TargetID: 111, StartOffsetSeconds: 6*86400 + 12*3600 + 1, EndOffsetSeconds: 6*86400 + 13*3600, MultiplierPercent: 150},
	}
	for _, override := range []int{-1, 1} {
		t.Run(fmt.Sprintf("override=%d", override), func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = override
			srv.erupeConfig.GameplayOptions.DivaBonusTargets = rules
			srv.divaRepo = &mockDivaRepo{events: []DivaEvent{event}}
			s := createMockSession(56, srv)
			handleMsgMhfGetUdSchedule(s, &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 20})
			schedule := readAck(t, s)
			if schedule.ErrorCode != 0 || schedule.AckHandle != 20 || len(schedule.Payload) != 36 {
				t.Fatalf("invalid schedule response: %+v", schedule)
			}
			wireEvent := DivaEvent{ID: binary.BigEndian.Uint32(schedule.Payload[:4]),
				StartTime: binary.BigEndian.Uint32(schedule.Payload[4:8])}
			if wireEvent != event {
				t.Fatalf("schedule changed persisted anchor: %+v want %+v", wireEvent, event)
			}
			phaseEnd := binary.BigEndian.Uint32(schedule.Payload[8:12])
			if phaseEnd != event.StartTime+601200 {
				t.Fatalf("wrong song-phase end: %d", phaseEnd)
			}
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 21})
			bonus := readAck(t, s)
			if bonus.ErrorCode != 0 || bonus.AckHandle != 21 {
				t.Fatalf("bonus request failed: %+v", bonus)
			}
			assertDivaBonusTestRows(t, bonus.Payload, wireEvent, rules)
			// The fixture starts at midnight, so the client calendar begins at
			// the preceding noon. Exact next noon is in the first inclusive bucket.
			firstNoon := int64(wireEvent.StartTime) - 12*3600
			wantBuckets := []int{0, 0, 7}
			for i, wantBucket := range wantBuckets {
				row := bonus.Payload[2+i*16 : 2+(i+1)*16]
				start, end := binary.BigEndian.Uint32(row[6:10]), binary.BigEndian.Uint32(row[10:14])
				if start < wireEvent.StartTime || end >= phaseEnd {
					t.Fatalf("bonus row %d lies outside advertised phase", i)
				}
				bucket := -1
				for day := 0; day < 8; day++ {
					lo := firstNoon + int64(day)*86400
					if int64(start) >= lo && int64(start) <= lo+86400 {
						bucket = day
						break
					}
				}
				if bucket != wantBucket {
					t.Fatalf("row %d maps to client bucket %d, want %d", i, bucket, wantBucket)
				}
			}
		})
	}
}
