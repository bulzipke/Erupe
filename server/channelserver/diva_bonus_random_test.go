package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func TestDivaBonusRandomGridClippingAndCapacity(t *testing.T) {
	// All 24 start hours, including off-grid minute/second offsets. This is a
	// pure schedule/wire test: it does not connect to or mutate any database.
	if divaPhaseDuration != 601200 {
		t.Fatal("song duration changed")
	}
	if cfg.DivaBonusPhaseSeconds != divaPhaseDuration {
		t.Fatal("bonus configuration and song phase disagree")
	}
	catalog := make(map[int64]bool)
	for _, monster := range divaSongMonsterPoints {
		catalog[int64(monster.MID)] = true
	}
	maxRows := 0
	for hour := 0; hour < 24; hour++ {
		for _, offset := range []time.Duration{0, time.Minute + 17*time.Second, 59*time.Minute + 59*time.Second} {
			t.Run(fmt.Sprintf("hour=%d/offset=%s", hour, offset), func(t *testing.T) {
				start := divaTestTime(20, hour, 0).Add(offset)
				event := DivaEvent{ID: 42, StartTime: uint32(start.Unix())}
				rules, err := divaRandomBonusRules(event)
				if err != nil {
					t.Fatal(err)
				}
				maxRows = max(maxRows, len(rules))
				if len(rules)%4 != 0 || len(rules) < 224 || len(rules) > 228 {
					t.Fatalf("unexpected window count: %d", len(rules))
				}
				if rules[0].StartOffsetSeconds != 0 || rules[len(rules)-1].EndOffsetSeconds != 601200 {
					t.Fatal("schedule does not cover exactly the original song phase")
				}
				var total [4]int64
				var buckets [8][4]int
				firstNoon := divaNoon(start).Unix()
				for i := 0; i < len(rules); i += 4 {
					first := rules[i]
					if first.EndOffsetSeconds-first.StartOffsetSeconds > 3*3600 {
						t.Fatal("a window exceeds three hours")
					}
					startAt := time.Unix(start.Unix()+first.StartOffsetSeconds, 0).In(start.Location())
					endAt := time.Unix(start.Unix()+first.EndOffsetSeconds, 0).In(start.Location())
					onGrid := func(at time.Time) bool { return at.Hour()%3 == 0 && at.Minute() == 0 && at.Second() == 0 }
					if i > 0 && !onGrid(startAt) {
						t.Fatalf("start not on UTC+9 00/03/.../21 grid: %s", startAt)
					}
					if i+4 < len(rules) && !onGrid(endAt) {
						t.Fatalf("end not on UTC+9 grid: %s", endAt)
					}
					ids := make(map[int64]bool)
					for color := 0; color < 4; color++ {
						rule := rules[i+color]
						if rule.Color != color+1 || rule.TargetType != "monster" || rule.MultiplierPercent != 200 ||
							rule.StartOffsetSeconds != first.StartOffsetSeconds || rule.EndOffsetSeconds != first.EndOffsetSeconds {
							t.Fatalf("wrong four-color window: %+v", rules[i:i+4])
						}
						if !catalog[rule.TargetID] || ids[rule.TargetID] {
							t.Fatalf("unknown or duplicated monster in four-color window: %+v", rules[i:i+4])
						}
						ids[rule.TargetID] = true
						if i > 0 && rules[i+color-4].EndOffsetSeconds != rule.StartOffsetSeconds {
							t.Fatal("gap or overlap between adjacent same-color windows")
						}
						total[color] += rule.EndOffsetSeconds - rule.StartOffsetSeconds
						bucket := -1
						for day := range buckets {
							lo := firstNoon + int64(day)*86400
							if startAt.Unix() >= lo && startAt.Unix() <= lo+86400 {
								bucket = day
								break
							}
						}
						if bucket < 0 {
							t.Fatal("random schedule exceeds native eight-day storage")
						}
						buckets[bucket][color]++
						if buckets[bucket][color] > 9 {
							t.Fatalf("three-hour grid unexpectedly uses over nine entries in a native bucket: %v", buckets)
						}
					}
				}
				if total != [4]int64{601200, 601200, 601200, 601200} {
					t.Fatalf("not every color covers the full original phase: %v", total)
				}
				rows, err := divaBonusTargets(event, rules)
				if err != nil {
					t.Fatal(err)
				}
				payload := divaBonusPayload(rows)
				assertDivaBonusTestRows(t, payload, event, rules)
				if len(rows) > cfg.MaxDivaBonusTargets || len(payload) > 5122 {
					t.Fatal("random schedule exceeds client capacity")
				}
				for i := 4; i < len(rows); i++ {
					if rows[i-4].End+1 != rows[i].Start {
						t.Fatal("inclusive wire endpoints overlap or leave a gap")
					}
				}
			})
		}
	}
	if maxRows != 228 {
		t.Fatalf("maximum clipped schedule = %d entries, want 228", maxRows)
	}
}

func TestDivaBonusRandomReproducibleAndEventSpecific(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	rules, err := divaRandomBonusRules(event)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := divaRandomBonusRules(event)
		if err != nil || !reflect.DeepEqual(rules, again) {
			t.Fatalf("repeat changed the event schedule: %v", err)
		}
	}
	// Fixed vector protects ongoing events from accidental RNG/seed changes.
	pool := []uint8{1, 2, 6, 7, 8, 11, 14, 15}
	selected, err := divaBalancedBonusAssignments(event, 8, pool)
	if err != nil {
		t.Fatal(err)
	}
	want := [][4]uint8{
		{6, 2, 15, 7}, {1, 11, 14, 8}, {2, 15, 7, 6}, {11, 14, 8, 1},
		{15, 7, 6, 2}, {14, 8, 1, 11}, {7, 6, 2, 15}, {8, 1, 11, 14},
	}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("stable balanced SHA-256 selection vector changed: %v", selected)
	}
	reversed := []uint8{15, 14, 11, 8, 7, 6, 2, 1, 15, 1}
	if got, err := divaBalancedBonusAssignments(event, 8, reversed); err != nil || !reflect.DeepEqual(got, selected) {
		t.Fatal("catalog ordering or duplicate entries changed the selection")
	}
	for _, changed := range []DivaEvent{
		{ID: event.ID + 1, StartTime: event.StartTime},
		{ID: event.ID, StartTime: event.StartTime + 86400},
	} {
		other, err := divaRandomBonusRules(changed)
		if err != nil {
			t.Fatal(err)
		}
		different := false
		for i, rule := range rules {
			if rule.TargetID != other[i].TargetID {
				different = true
				break
			}
		}
		if !different {
			t.Fatal("changing persisted event identity did not change the targets")
		}
	}
	for _, invalid := range []DivaEvent{{}, {ID: 1, StartTime: 0xfffffff0}} {
		if _, err := divaRandomBonusRules(invalid); err == nil {
			t.Fatalf("invalid event accepted: %+v", invalid)
		}
	}
}

// Verify the promised distribution independently of the implementation's
// cursor arithmetic. Include zero-count monsters when comparing balance.
func assertDivaBalancedAssignments(t *testing.T, rows [][4]uint8, pool []uint8) {
	t.Helper()
	counts := make(map[uint8]int, len(pool))
	for _, id := range pool {
		counts[id] = 0
	}
	n := len(counts)
	var byColor [4]map[uint8]int
	for color := range byColor {
		byColor[color] = make(map[uint8]int, n)
	}
	lastSlot := make(map[uint8]int, n)
	previous := make(map[uint8]bool)
	for slot, row := range rows {
		current := make(map[uint8]bool, 4)
		overlap := 0
		for color, id := range row {
			if _, exists := counts[id]; !exists || current[id] {
				t.Fatalf("slot %d has an unknown or same-slot duplicate monster: %v", slot, row)
			}
			current[id] = true
			if previous[id] {
				overlap++
			}
			if last := lastSlot[id]; last != 0 && slot+1-last < n/4 {
				t.Fatalf("monster %d repeated after %d slots, expected at least %d", id, slot+1-last, n/4)
			}
			lastSlot[id] = slot + 1
			counts[id]++
			byColor[color][id]++
		}
		if slot > 0 && overlap != max(8-n, 0) {
			t.Fatalf("slot %d repeats %d previous targets, minimum is %d", slot, overlap, max(8-n, 0))
		}
		previous = current
	}
	globalMin, globalMax := 4*len(rows)/n, (4*len(rows)+n-1)/n
	colorMin, colorMax := len(rows)/n, (len(rows)+n-1)/n
	for id, count := range counts {
		if count < globalMin || count > globalMax {
			t.Fatalf("monster %d appears %d times, balanced range is %d..%d", id, count, globalMin, globalMax)
		}
		for color := range byColor {
			count = byColor[color][id]
			if count < colorMin || count > colorMax {
				t.Fatalf("color %d monster %d appears %d times, balanced range is %d..%d", color+1, id, count, colorMin, colorMax)
			}
		}
	}
	for color, counts := range byColor {
		if len(counts) != min(len(rows), n) {
			t.Fatalf("color %d reused a monster before exhausting the pool", color+1)
		}
	}
}

func TestDivaBonusRandomBalancedCandidateCounts(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	// All potential catalog sizes, including candidates divisible by four and
	// small catalogs where adjacent-slot overlap is mathematically unavoidable.
	for n := 4; n <= 176; n++ {
		pool := make([]uint8, n)
		for i := range pool {
			pool[i] = uint8(i + 1)
		}
		original := append([]uint8(nil), pool...)
		for _, slots := range []int{0, 1, n - 1, n, n + 1, 56, 57, 2*n + 3} {
			t.Run(fmt.Sprintf("candidates=%d/slots=%d", n, slots), func(t *testing.T) {
				rows, err := divaBalancedBonusAssignments(event, slots, pool)
				if err != nil || len(rows) != slots {
					t.Fatalf("assignment failed: %v", err)
				}
				assertDivaBalancedAssignments(t, rows, pool)
				if !reflect.DeepEqual(pool, original) {
					t.Fatal("shared catalog order was mutated")
				}
			})
		}
	}
	for _, pool := range [][]uint8{nil, {1}, {1, 2, 3}, {1, 2, 3, 1, 2, 3}} {
		if _, err := divaBalancedBonusAssignments(event, 57, pool); err == nil {
			t.Fatalf("fewer than four unique candidates accepted: %v", pool)
		}
	}
	if _, err := divaBalancedBonusAssignments(event, -1, []uint8{1, 2, 3, 4}); err == nil {
		t.Fatal("negative slot count accepted")
	}
}

func TestDivaBonusRandomWholePhaseBalance(t *testing.T) {
	var pool []uint8
	for _, monster := range divaSongMonsterPoints {
		pool = append(pool, monster.MID)
	}
	for _, start := range []time.Time{divaTestTime(20, 0, 0), divaTestTime(20, 2, 1)} {
		event := DivaEvent{ID: 42, StartTime: uint32(start.Unix())}
		rules, err := divaRandomBonusRules(event)
		if err != nil {
			t.Fatal(err)
		}
		rows := make([][4]uint8, len(rules)/4)
		for i, rule := range rules {
			rows[i/4][i%4] = uint8(rule.TargetID)
		}
		assertDivaBalancedAssignments(t, rows, pool)
		if len(rows) > len(pool) {
			t.Fatal("current catalog no longer supports zero per-color repeats; update the documented guarantee")
		}
	}
}

func TestDivaBonusRandomBalancedRequestOrderIndependent(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(divaTestTime(20, 0, 0).Unix())}
	var pool []uint8
	for _, monster := range divaSongMonsterPoints {
		pool = append(pool, monster.MID)
	}
	want, err := divaBalancedBonusAssignments(event, 57, pool)
	if err != nil {
		t.Fatal(err)
	}
	// Neither other events nor shorter/later queries may advance shared RNG or
	// rotation state. Restart reconstruction always begins at persisted day zero.
	for _, slots := range []int{28, 1, 57, 8, 56, 57} {
		if _, err := divaBalancedBonusAssignments(DivaEvent{ID: 99, StartTime: event.StartTime}, 57, pool); err != nil {
			t.Fatal(err)
		}
		got, err := divaBalancedBonusAssignments(event, slots, pool)
		if err != nil || !reflect.DeepEqual(got, want[:slots]) {
			t.Fatalf("request order/length changed the %d-slot prefix", slots)
		}
	}
}

func TestDivaBonusRandomHandlerFullScheduleAndRestart(t *testing.T) {
	// Two independently initialized mock servers model channels/restarts with
	// the same persisted event. Actual database persistence is not tested here.
	event := DivaEvent{ID: 42, StartTime: uint32(TimeMidnight().Add(-24 * time.Hour).Unix())}
	rules, err := divaRandomBonusRules(event)
	if err != nil {
		t.Fatal(err)
	}
	var baseline []byte
	for _, phase := range []int{-1, 1} {
		for server := 0; server < 2; server++ {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = phase
			srv.erupeConfig.GameplayOptions.DivaBonusRandom = true
			srv.divaRepo = &mockDivaRepo{events: []DivaEvent{event}}
			for request := 0; request < 2; request++ {
				s := createMockSession(uint32(56+request), srv)
				handleMsgMhfGetUdSchedule(s, &mhfpacket.MsgMhfGetUdSchedule{AckHandle: 40})
				schedule := readAck(t, s)
				if schedule.ErrorCode != 0 || len(schedule.Payload) != 36 ||
					binary.BigEndian.Uint32(schedule.Payload[4:8]) != event.StartTime ||
					binary.BigEndian.Uint32(schedule.Payload[8:12]) != event.StartTime+601200 {
					t.Fatalf("random mode changed original event dates: %+v", schedule)
				}
				handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 41})
				ack := readAck(t, s)
				if ack.ErrorCode != 0 || ack.AckHandle != 41 || ack.PayloadSize != uint(2+16*len(rules)) {
					t.Fatalf("random schedule not sent in full: %+v", ack)
				}
				assertDivaBonusTestRows(t, ack.Payload, event, rules)
				if baseline == nil {
					baseline = ack.Payload
				} else if !bytes.Equal(baseline, ack.Payload) {
					t.Fatal("channel/restart/reconnect changed event targets")
				}
				if len(s.sendPackets) != 0 {
					t.Fatal("random schedule split into multiple responses")
				}
			}
		}
	}
}

func TestDivaBonusRandomHandlerSafeguards(t *testing.T) {
	event := DivaEvent{ID: 42, StartTime: uint32(TimeMidnight().Add(-24 * time.Hour).Unix())}
	for _, tt := range []struct {
		name                string
		change              func(*Server, *mockDivaRepo)
		wantFail, wantEmpty bool
	}{
		{name: "four colors", change: func(_ *Server, r *mockDivaRepo) { r.beads = []int{1, 3, 4, 8} }},
		{name: "three colors fail", change: func(_ *Server, r *mockDivaRepo) { r.beads = []int{1, 3, 4} }, wantFail: true},
		{name: "beads read error", change: func(_ *Server, r *mockDivaRepo) { r.beadsErr = errors.New("offline") }, wantFail: true},
		{name: "event read error", change: func(_ *Server, r *mockDivaRepo) { r.eventsErr = errors.New("offline") }, wantFail: true},
		{name: "missing repository", change: func(s *Server, _ *mockDivaRepo) { s.divaRepo = nil }, wantFail: true},
		{name: "manual conflict", change: func(s *Server, _ *mockDivaRepo) {
			s.erupeConfig.GameplayOptions.DivaBonusTargets = []cfg.DivaBonusTarget{divaBonusTestRule()}
		}, wantFail: true},
		{name: "older client", change: func(s *Server, _ *mockDivaRepo) { s.erupeConfig.RealClientMode = cfg.Z2 }, wantFail: true},
		{name: "shifted client clock", change: func(s *Server, _ *mockDivaRepo) {
			hour := 12
			s.erupeConfig.DebugOptions.InGameTimeOverrideHour = &hour
		}, wantFail: true},
		{name: "disabled phase", change: func(s *Server, _ *mockDivaRepo) { s.erupeConfig.DebugOptions.DivaOverride = 0 }, wantEmpty: true},
		{name: "interception phase", change: func(s *Server, _ *mockDivaRepo) { s.erupeConfig.DebugOptions.DivaOverride = 2 }, wantEmpty: true},
		{name: "welcome phase", change: func(s *Server, _ *mockDivaRepo) { s.erupeConfig.DebugOptions.DivaOverride = 3 }, wantEmpty: true},
		{name: "no event", change: func(_ *Server, r *mockDivaRepo) { r.events = nil }, wantEmpty: true},
		{name: "future event", change: func(_ *Server, r *mockDivaRepo) { r.events[0].StartTime = uint32(TimeAdjusted().Add(time.Hour).Unix()) }, wantEmpty: true},
		{name: "phase expired without eight-day extension", change: func(_ *Server, r *mockDivaRepo) { r.events[0].StartTime = uint32(TimeAdjusted().Unix() - 601201) }, wantEmpty: true},
		{name: "default off remains empty", change: func(s *Server, _ *mockDivaRepo) {
			s.erupeConfig.GameplayOptions.DivaBonusRandom = false
			s.divaRepo = nil
		}, wantEmpty: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := createMockServer()
			srv.erupeConfig.RealClientMode = cfg.ZZ
			srv.erupeConfig.DebugOptions.DivaOverride = -1
			srv.erupeConfig.GameplayOptions.DivaBonusRandom = true
			repo := &mockDivaRepo{events: []DivaEvent{event}}
			srv.divaRepo = repo
			tt.change(srv, repo)
			s := createMockSession(56, srv)
			handleMsgMhfGetUdBonusQuestInfo(s, &mhfpacket.MsgMhfGetUdBonusQuestInfo{AckHandle: 42})
			ack := readAck(t, s)
			if (ack.ErrorCode != 0) != tt.wantFail {
				t.Fatalf("unexpected ACK: %+v", ack)
			}
			if tt.wantFail && len(ack.Payload) != 0 {
				t.Fatal("failed request leaked a partial schedule")
			}
			if tt.wantEmpty && !bytes.Equal(ack.Payload, []byte{0, 0}) {
				t.Fatalf("expected empty default/inactive schedule: %x", ack.Payload)
			}
			if !tt.wantFail && !tt.wantEmpty && len(ack.Payload) <= 2 {
				t.Fatal("active four-color random schedule missing")
			}
		})
	}
}
