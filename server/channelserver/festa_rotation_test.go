package channelserver

import (
	"encoding/binary"
	"errors"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
	"sync"
	"testing"
	"time"
)

func TestFestaRotationScheduleUsesCurrentWireID(t *testing.T) {
	r, _, cid, _ := setupFestaRepo(t)
	srv := createMockServer()
	srv.festaRepo = r
	srv.erupeConfig.DebugOptions.FestaOverride = -1
	srv.erupeConfig.RealClientMode = cfg.ZZ
	srv.erupeConfig.GameplayOptions.MaximumFP = 120000
	s := createMockSession(cid, srv)
	handleMsgMhfInfoFesta(s, &mhfpacket.MsgMhfInfoFesta{AckHandle: 42})
	_, code, payload := parseAckBufData(t, (<-s.sendPackets).data)
	if code != 0 || len(payload) < 24 {
		t.Fatalf("schedule ACK: %d %x", code, payload)
	}
	e, err := r.EnsureFestaEvent(TimeAdjusted())
	if err != nil || binary.BigEndian.Uint32(payload) != e.ID {
		t.Fatalf("wrong first-round ID: %x %+v %v", payload[:4], e, err)
	}
	for i, timestamp := range e.timestamps() {
		if got := binary.BigEndian.Uint32(payload[4+i*4:]); got != timestamp {
			t.Fatalf("wire timestamp %d = %d want %d", i, got, timestamp)
		}
	}
	s.festaPersonalOfferEvent.Store(e.ID + 1)
	handleMsgMhfAcquireFestaPersonalPrize(s, &mhfpacket.MsgMhfAcquireFestaPersonalPrize{PrizeID: 37})
	if code, _ := parseAckSimpleValue(t, (<-s.sendPackets).data); code == 0 {
		t.Fatal("stale personal offer accepted")
	}
}

func TestFestaContinuousScheduleBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, divaLocation)
	e := FestaEvent{ID: 12, StartTime: uint32(start.Unix()), Continuous: true}
	ts := e.timestamps()
	if ts[4]-ts[0] != festaCycleSeconds || ts[4]-ts[3] != secsPerWeek-festaVotingDuration {
		t.Fatalf("rest days or incorrect reward week: %v", ts)
	}
	for _, tc := range []struct {
		at    time.Time
		phase uint
	}{
		{start.Add(-time.Second), 0}, {start, 1}, {start.AddDate(0, 0, 7).Add(-time.Second), 1},
		{start.AddDate(0, 0, 7), 2}, {start.AddDate(0, 0, 14).Add(-time.Second), 2},
		{start.AddDate(0, 0, 14), 3}, {time.Unix(int64(ts[3])-1, 0), 3},
		{time.Unix(int64(ts[3]), 0), 4}, {start.AddDate(0, 0, 21).Add(-time.Second), 4},
		{start.AddDate(0, 0, 21), 0},
	} {
		if got := e.phase(tc.at); got != tc.phase {
			t.Fatalf("at %v: phase %d, want %d", tc.at, got, tc.phase)
		}
	}
}

func TestFestaRotationBoundWritesRespectChapterAndReceiptIdentity(t *testing.T) {
	r, db, cid, gid := setupFestaRepo(t)
	now := TimeAdjusted()
	e, err := r.EnsureFestaEvent(now)
	if err != nil {
		t.Fatal(err)
	}
	scoped := r.ForFestaEvent(e.ID)
	if err = scoped.RegisterGuild(gid, "blue"); err != nil {
		t.Fatal(err)
	}
	if err = scoped.SubmitSouls(cid, gid, []uint16{10}); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("souls accepted during registration: %v", err)
	}
	if err = scoped.ClaimPrize(0, cid); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("main reward accepted before rewards: %v", err)
	}
	start := rotationMondayNoon(now).AddDate(0, 0, -14)
	if _, err = db.Exec(`UPDATE events SET start_time=$1 WHERE id=$2`, start, e.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- scoped.ClaimPrize(0, cid) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var claims int
	if err = db.Get(&claims, `SELECT count(*) FROM festa_prizes_accepted WHERE event_id=$1 AND character_id=$2 AND prize_id=0`, e.ID, cid); err != nil || claims != 1 {
		t.Fatalf("duplicate receipt: %d %v", claims, err)
	}
	if err = scoped.SubmitSouls(cid, gid, []uint16{10}); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("souls changed during rewards: %v", err)
	}
	if _, err = db.Exec(`UPDATE events SET start_time=$1 WHERE id=$2`, now.Add(-21*24*time.Hour-time.Minute), e.ID); err != nil {
		t.Fatal(err)
	}
	if err = scoped.ClaimPrize(0, cid); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("expired receipt accepted before worker caught up: %v", err)
	}
}

func TestFestaRotationPreservesRoundHistoryAndConcurrentRollover(t *testing.T) {
	r, db, cid, gid := setupFestaRepo(t)
	start := rotationMondayNoon(TimeAdjusted()).AddDate(0, 0, -21)
	first, err := r.EnsureFestaEvent(start)
	if err != nil || first.StartTime != uint32(start.Unix()) {
		t.Fatalf("bootstrap: %+v %v", first, err)
	}
	if err = r.RegisterGuild(gid, "blue"); err != nil {
		t.Fatal(err)
	}
	if err = r.SubmitSouls(cid, gid, []uint16{123}); err != nil {
		t.Fatal(err)
	}
	if err = r.ClaimPrize(37, cid); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	results := make(chan FestaEvent, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := r.EnsureFestaEvent(start.AddDate(0, 0, 21))
			results <- e
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var current FestaEvent
	for e := range results {
		if current.ID != 0 && current != e {
			t.Fatal("concurrent duplicate rounds")
		}
		current = e
	}
	if current.ID == first.ID || current.StartTime != first.StartTime+festaCycleSeconds {
		t.Fatalf("gap or reused ID: %+v", current)
	}
	oldRepo := r.ForFestaEvent(first.ID)
	oldSouls, err := oldRepo.GetCharSouls(cid)
	if err != nil || oldSouls != 123 {
		t.Fatalf("history lost: %d %v", oldSouls, err)
	}
	var receipts int
	if err = db.Get(&receipts, `SELECT count(*) FROM festa_prizes_accepted WHERE event_id=$1 AND character_id=$2`, first.ID, cid); err != nil || receipts != 1 {
		t.Fatalf("old receipts lost: %d %v", receipts, err)
	}
	newSouls, err := r.ForFestaEvent(current.ID).GetCharSouls(cid)
	if err != nil || newSouls != 0 {
		t.Fatalf("old souls leaked into new round: %d %v", newSouls, err)
	}
	if err := oldRepo.ClaimPrize(37, cid); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("old claim accepted: %v", err)
	}
	if err := oldRepo.SubmitSouls(cid, gid, []uint16{20}); !errors.Is(err, errFestaRoundChanged) {
		t.Fatalf("old submission accepted: %v", err)
	}
	guild, err := NewGuildRepository(db).GetByID(gid)
	if err != nil || guild.Souls != 0 || guild.FestivalColor != FestivalColorNone {
		t.Fatalf("old guild state leaked: %+v %v", guild, err)
	}
	var count int
	if err = db.Get(&count, `SELECT count(*) FROM events WHERE event_type='festa'`); err != nil || count != 2 {
		t.Fatalf("history erased or duplicate rounds: %d %v", count, err)
	}
	later, err := r.EnsureFestaEvent(start.AddDate(0, 0, 21*4).Add(time.Hour))
	if err != nil || later.StartTime != first.StartTime+4*festaCycleSeconds {
		t.Fatalf("downtime drift: %+v %v", later, err)
	}
}
