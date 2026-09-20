package channelserver

import (
	"encoding/binary"
	"errors"
	"erupe-ce/network/mhfpacket"
	"sync"
	"testing"
	"time"
)

func divaTestTime(day, hour, minute int) time.Time {
	return time.Date(2026, 9, day, hour, minute, 0, 0, divaLocation)
}

func TestDivaNoonAndPublication(t *testing.T) {
	if got := divaNoon(divaTestTime(21, 11, 59)); !got.Equal(divaTestTime(20, 12, 0)) {
		t.Fatal(got)
	}
	if got := divaNoon(divaTestTime(21, 12, 0)); !got.Equal(divaTestTime(21, 12, 0)) {
		t.Fatal(got)
	}
	start := divaTestTime(20, 16, 0)
	for _, tt := range []struct{ now, want time.Time }{
		{divaTestTime(20, 17, 59), start},
		{divaTestTime(20, 18, 0), divaTestTime(20, 18, 0)},
		{divaTestTime(21, 3, 59), divaTestTime(20, 20, 0)},
		{divaTestTime(21, 4, 0), divaTestTime(21, 4, 0)},
		{divaTestTime(21, 12, 0), divaTestTime(21, 12, 0)},
		{divaTestTime(21, 18, 0), divaTestTime(21, 12, 0)},
		{divaTestTime(21, 20, 0), divaTestTime(21, 20, 0)},
	} {
		if got := divaRankingCutoff(tt.now, start); !got.Equal(tt.want) {
			t.Fatalf("%v => %v want %v", tt.now, got, tt.want)
		}
	}
}

func TestDivaFirstPublicationWithMidnightAnchor(t *testing.T) {
	start := divaTestTime(20, 0, 0)
	for _, tt := range []struct{ now, want time.Time }{
		{divaTestTime(20, 4, 0), start},
		{divaTestTime(20, 12, 0), start},
		{divaTestTime(20, 17, 59), start},
		{divaTestTime(20, 18, 0), divaTestTime(20, 18, 0)},
		{divaTestTime(20, 20, 0), divaTestTime(20, 20, 0)},
		{divaTestTime(21, 4, 0), divaTestTime(21, 4, 0)},
		{divaTestTime(21, 12, 0), divaTestTime(21, 12, 0)},
		{divaTestTime(21, 18, 0), divaTestTime(21, 12, 0)},
	} {
		if got := divaRankingCutoff(tt.now, start); !got.Equal(tt.want) {
			t.Fatalf("%v => %v want %v", tt.now, got, tt.want)
		}
	}
}

func TestDivaPointWireDoesNotDuplicate(t *testing.T) {
	first := divaTestTime(20, 12, 0)
	p := divaMyPointPayload([]DivaDay{{Day: first, Color: 2, Quest: 60, Bonus: 5}}, first)
	if len(p) != 145 || p[0] != 0 || p[1] != 2 {
		t.Fatalf("bad status/length: %x", p)
	}
	if binary.BigEndian.Uint32(p[2:6]) != 60 || binary.BigEndian.Uint32(p[6:10]) != 5 {
		t.Fatal("wrong base/bonus")
	}
	for _, v := range p[10:] {
		if v != 0 {
			t.Fatal("duplicated into other slot/day")
		}
	}
}

func TestDivaRankingWire(t *testing.T) {
	rows := []DivaRank{{CharID: 56, Rank: 1, Name: "asdf", Points: 60}}
	for kind := uint8(0); kind < 4; kind++ {
		p := divaRankingPayload(kind, rows)
		want := 3100
		if kind%2 == 1 {
			want = 4700
		}
		if len(p) != want {
			t.Fatalf("kind %d length %d", kind, len(p))
		}
		if kind == 0 && (p[1] != 1 || string(p[2:6]) != "asdf" || binary.BigEndian.Uint32(p[27:31]) != 60) {
			t.Fatal("wrong personal fields")
		}
	}
	p := divaMyRankingPayload(rows, 56)
	if len(p) != 49 || binary.BigEndian.Uint32(p[:4]) != 1 || binary.BigEndian.Uint32(p[8:12]) != 60 {
		t.Fatalf("own ranking %x", p)
	}
	if divaDisplayPoints(1<<40) != 0x7fffffff {
		t.Fatal("signed UI overflow")
	}
}

func TestDivaChoiceLockedAcrossSessions(t *testing.T) {
	srv := createMockServer()
	repo := &mockDivaRepo{events: []DivaEvent{{ID: 1, StartTime: uint32(TimeAdjusted().Add(-time.Hour).Unix())}}}
	srv.divaRepo = repo
	for i, color := range []uint8{1, 2, 1, 0, 5, 2} {
		s := createMockSession(56, srv)
		handleMsgMhfSetKiju(s, &mhfpacket.MsgMhfSetKiju{AckHandle: 1, Unk1: color})
		ack := readAck(t, s)
		if i >= 2 && i <= 4 {
			if ack.ErrorCode != 0 || len(ack.Payload) != 1 || ack.Payload[0] != 0xF9 {
				t.Fatalf("accepted forbidden color %d", color)
			}
		} else if ack.Payload[0] != 0 {
			t.Fatal("valid/repeated choice failed")
		}
	}
	if repo.bead != 2 || repo.beadExpiry.Hour() != 12 {
		t.Fatal("choice/deadline changed")
	}
}

func TestDivaSaveFailureIsNotSuccess(t *testing.T) {
	srv := createMockServer()
	srv.divaRepo = &mockDivaRepo{events: []DivaEvent{{ID: 1}}, addErr: errors.New("offline")}
	s := createMockSession(56, srv)
	handleMsgMhfAddUdPoint(s, &mhfpacket.MsgMhfAddUdPoint{AckHandle: 7, QuestPoints: 60})
	if ack := readAck(t, s); ack.ErrorCode == 0 {
		t.Fatal("failed save acknowledged as success")
	}
}

func TestRepoDivaSongTransactionAndCutoff(t *testing.T) {
	repo, db := setupDivaRepo(t)
	uid := CreateTestUser(t, db, "diva_song_test")
	cid := CreateTestCharacter(t, db, uid, "SongTester")
	now := divaTestTime(21, 11, 59)
	ev, err := repo.EnsureDivaSongEvent(now)
	if err != nil {
		t.Fatal(err)
	}
	ev2, err := repo.EnsureDivaSongEvent(now.Add(24 * time.Hour))
	if err != nil || ev2 != ev {
		t.Fatalf("midnight changed event: %+v %+v %v", ev, ev2, err)
	}
	expiry := divaNoon(now).Add(24 * time.Hour)
	if err = repo.AssignBead(cid, ev.ID, 4, now); err != nil {
		t.Fatal(err)
	}
	// A real DB lock must arbitrate concurrent choices across instances.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for color := 1; color <= 2; color++ {
		wg.Add(1)
		go func(c int) { defer wg.Done(); results <- repo.AssignBead(cid, ev.ID, c, now) }(color)
	}
	wg.Wait()
	close(results)
	success, locked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errDivaBeadLocked) {
			locked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || locked != 1 {
		t.Fatalf("success=%d locked=%d", success, locked)
	}
	if err = repo.RecordDivaPoints(cid, ev.ID, 60, 5, now); err != nil {
		t.Fatal(err)
	}
	q, b, err := repo.GetPoints(cid, ev.ID)
	if err != nil || q != 60 || b != 5 {
		t.Fatalf("saved %d/%d %v", q, b, err)
	}
	ranks, err := repo.GetDivaRanking(ev.ID, cid, now)
	if err != nil || len(ranks) != 0 {
		t.Fatalf("unpublished records visible: %+v %v", ranks, err)
	}
	ranks, err = repo.GetDivaRanking(ev.ID, cid, expiry)
	if err != nil || len(ranks) != 1 || ranks[0].Points != 65 {
		t.Fatalf("rank %+v %v", ranks, err)
	}
	days, err := repo.GetDivaDays(cid, ev.ID, divaNoon(now))
	if err != nil || len(days) != 1 || days[0].SecondQuest != 60 || days[0].Color != 4 || days[0].SecondColor == 0 {
		t.Fatalf("days %+v %v", days, err)
	}
	if err = repo.AssignBead(cid, ev.ID, 3, expiry); err != nil {
		t.Fatal("noon did not unlock", err)
	}
	// Force an integer overflow in the legacy color log. No partial total may commit.
	if err = repo.RecordDivaPoints(cid, ev.ID, 0xffffffff, 1, now); err == nil {
		t.Fatal("expected transaction failure")
	}
	q, b, err = repo.GetPoints(cid, ev.ID)
	if err != nil || q != 60 || b != 5 {
		t.Fatalf("partial save after rollback %d/%d %v", q, b, err)
	}
}

func TestDivaChoiceAndTwoSlots(t *testing.T) {
	var c divaChoice
	for _, color := range []int{1, 1, 4, 4} {
		if err := c.selectColor(color); err != nil {
			t.Fatal(err)
		}
	}
	if c.First != 1 || c.Second != 4 || c.active() != 4 {
		t.Fatal(c)
	}
	if !errors.Is(c.selectColor(2), errDivaBeadLocked) {
		t.Fatal("second change accepted")
	}
	first := divaTestTime(20, 12, 0)
	p := divaMyPointPayload([]DivaDay{{Day: first, Color: 1, Quest: 11, Bonus: 2, SecondColor: 4, SecondQuest: 60, SecondBonus: 3}}, first)
	if len(p) != 145 || p[1] != 1 || p[10] != 4 || binary.BigEndian.Uint32(p[2:6]) != 11 || binary.BigEndian.Uint32(p[11:15]) != 60 || binary.BigEndian.Uint32(p[15:19]) != 3 {
		t.Fatalf("two color wire: %x", p)
	}
}

func TestRepoDivaChoiceCarryAndAttribution(t *testing.T) {
	repo, db := setupDivaRepo(t)
	uid := CreateTestUser(t, db, "diva_carry_test")
	cid := CreateTestCharacter(t, db, uid, "CarryTester")
	now := divaTestTime(21, 11, 59)
	ev, err := repo.EnsureDivaSongEvent(now)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, ev.ID, 1, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordDivaPoints(cid, ev.ID, 11, 0, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, ev.ID, 4, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordDivaPoints(cid, ev.ID, 60, 5, now); err != nil {
		t.Fatal(err)
	}
	next := now.Add(2 * time.Minute)
	// No NPC visit at noon: contribution must still belong to yellow.
	if err = repo.RecordDivaPoints(cid, ev.ID, 9, 0, next); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, ev.ID, 4, next); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, ev.ID, 2, next); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, ev.ID, 3, next); !errors.Is(err, errDivaBeadLocked) {
		t.Fatal(err)
	}
	if err = repo.RecordDivaPoints(cid, ev.ID, 20, 0, next); err != nil {
		t.Fatal(err)
	}
	days, err := repo.GetDivaDays(cid, ev.ID, divaNoon(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Color != 1 || days[0].Quest != 11 || days[0].SecondColor != 4 || days[0].SecondQuest != 60 || days[1].Color != 4 || days[1].Quest != 9 || days[1].SecondColor != 2 || days[1].SecondQuest != 20 {
		t.Fatalf("attribution: %+v", days)
	}
	q, b, err := repo.GetPoints(cid, ev.ID)
	if err != nil || q != 100 || b != 5 {
		t.Fatalf("total %d/%d %v", q, b, err)
	}
	// A different event must not inherit this event's color or change lock.
	var eid uint32
	if err = db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", next).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	if err = repo.AssignBead(cid, eid, 3, next); err != nil {
		t.Fatal(err)
	}
	colors, err := repo.GetDivaWinningColors(ev.ID, divaNoon(now), now)
	if err != nil || len(colors) != 8 || colors[0] != 0 {
		t.Fatalf("unsettled: %v %v", colors, err)
	}
	colors, err = repo.GetDivaWinningColors(ev.ID, divaNoon(now), next)
	if err != nil || colors[0] != 4 || colors[1] != 0 {
		t.Fatalf("settled chronological: %v %v", colors, err)
	}
}

func TestDivaSelectionOutsidePhase(t *testing.T) {
	for _, start := range []time.Time{TimeAdjusted().Add(time.Hour), TimeAdjusted().Add(-8 * 24 * time.Hour)} {
		srv := createMockServer()
		repo := &mockDivaRepo{events: []DivaEvent{{ID: 1, StartTime: uint32(start.Unix())}}}
		srv.divaRepo = repo
		s := createMockSession(56, srv)
		handleMsgMhfSetKiju(s, &mhfpacket.MsgMhfSetKiju{AckHandle: 1, Unk1: 1})
		ack := readAck(t, s)
		if len(ack.Payload) != 1 || ack.Payload[0] != 0xF9 || repo.bead != 0 {
			t.Fatalf("out of phase selection: %+v", ack)
		}
	}
}
