package channelserver

import (
	"sync"
	"testing"
	"time"
)

var _ DivaEventLifecycleRepository = (*DivaRepository)(nil)

func TestDivaLifecyclePhaseAnchors(t *testing.T) {
	now := time.Date(2026, 9, 21, 14, 35, 0, 0, divaLocation)
	midnight := time.Date(2026, 9, 21, 0, 0, 0, 0, divaLocation)
	for _, mode := range []int{-1, 2, 3} {
		start := divaLifecycleStart(now, mode)
		event := DivaEvent{ID: 1, StartTime: uint32(start.Unix())}
		ts := generateDivaTimestamps(nil, event.StartTime, false)
		if mode == -1 {
			if !start.Equal(midnight.Add(24 * time.Hour)) {
				t.Fatalf("normal start=%v", start)
			}
			if !divaLifecycleExpiry(event, mode).Equal(start.Add(divaTotalLifespan * time.Second)) {
				t.Fatal("normal expiry")
			}
			continue
		}
		index := 2 * (mode - 1)
		if int64(ts[index]) != midnight.Unix() {
			t.Fatalf("mode=%d anchor=%v want midnight", mode, ts)
		}
		if divaLifecycleExpiry(event, mode).Unix() != int64(ts[index+1]) {
			t.Fatalf("mode=%d expiry differs from schedule", mode)
		}
		if !now.Before(divaLifecycleExpiry(event, mode)) {
			t.Fatalf("mode=%d initially expired", mode)
		}
	}
}

func TestRepoDivaLifecycleStableAndPreservesHistory(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	for _, mode := range []int{-1, 2, 3} {
		first, err := r.EnsureDivaEvent(now, mode)
		if err != nil {
			t.Fatal(err)
		}
		var storedEpoch int64
		if err = db.QueryRow("SELECT EXTRACT(epoch FROM start_time)::bigint FROM events WHERE id=$1", first.ID).Scan(&storedEpoch); err != nil || storedEpoch != int64(first.StartTime) {
			t.Fatalf("mode=%d epoch roundtrip=%d want=%d err=%v", mode, storedEpoch, first.StartTime, err)
		}
		second, err := NewDivaRepository(db).EnsureDivaEvent(now.Add(24*time.Hour), mode)
		if err != nil || first != second {
			t.Fatalf("mode=%d lost midnight/restart anchor: %+v %+v %v", mode, first, second, err)
		}
		expired := divaLifecycleExpiry(first, mode)
		next, err := r.EnsureDivaEvent(expired, mode)
		if err != nil || first.ID == next.ID {
			t.Fatalf("mode=%d did not renew: %+v %v", mode, next, err)
		}
		var remains bool
		if err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM events WHERE id=$1)", first.ID).Scan(&remains); err != nil || !remains {
			t.Fatalf("history erased: %v", err)
		}
	}
}

func TestRepoDivaLifecycleForcedCutover(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "all legacy expired", true: "legacy ongoing"}[active], func(t *testing.T) {
			r, db := setupDivaRepo(t)
			now := divaTestTime(21, 12, 0)
			start := now.Add(-48 * time.Hour)
			if !active {
				start = now.Add(-2 * divaTotalLifespan * time.Second)
			}
			var old uint32
			if err := db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", start).Scan(&old); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO diva_interception_legacy_events(event_id) VALUES($1)", old); err != nil {
				t.Fatal(err)
			}
			first, err := r.EnsureDivaEvent(now, 2)
			if err != nil {
				t.Fatal(err)
			}
			var legacy bool
			if err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM diva_interception_legacy_events WHERE event_id=$1)", first.ID).Scan(&legacy); err != nil || legacy != active {
				t.Fatalf("bootstrap legacy=%v want=%v err=%v", legacy, active, err)
			}
			next, err := r.EnsureDivaEvent(divaLifecycleExpiry(first, 2), 2)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM diva_interception_legacy_events WHERE event_id=$1)", next.ID).Scan(&legacy); err != nil || legacy {
				t.Fatalf("new round incorrectly legacy=%v %v", legacy, err)
			}
		})
	}
}

func TestRepoDivaLifecycleAdoptsExistingFutureRound(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	var existing uint32
	start := now.Add(24 * time.Hour)
	if err := db.QueryRow("INSERT INTO events(event_type,start_time) VALUES('diva',$1) RETURNING id", start).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	event, err := r.EnsureDivaEvent(now, -1)
	if err != nil || event.ID != existing || int64(event.StartTime) != start.Unix() {
		t.Fatalf("future round lost: %+v %v", event, err)
	}
	if event, err = r.EnsureDivaEvent(now, 0); err != nil || event.ID != 0 {
		t.Fatalf("disabled event: %+v %v", event, err)
	}
	if _, err = r.EnsureDivaEvent(now, 4); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestRepoDivaLifecycleConcurrentCreation(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	var wg sync.WaitGroup
	events := make(chan DivaEvent, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); event, err := r.EnsureDivaEvent(now, 2); events <- event; errs <- err }()
	}
	wg.Wait()
	close(events)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uint32
	for event := range events {
		if id != 0 && id != event.ID {
			t.Fatal("multiple concurrent event anchors")
		}
		id = event.ID
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type='diva'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d %v", count, err)
	}
}

func TestRepoDivaLifecycleTimezoneRoundTrip(t *testing.T) {
	r, db := setupDivaRepo(t)
	oldMax := db.Stats().MaxOpenConnections
	// Session timezone is connection-local. One connection makes each setting
	// apply to the subsequent repository transaction as well as the verification.
	db.SetMaxOpenConns(1)
	var oldZone string
	if err := db.QueryRow("SHOW TimeZone").Scan(&oldZone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("SELECT set_config('TimeZone',$1,false)", oldZone); err != nil {
			t.Errorf("restore timezone: %v", err)
		}
		db.SetMaxOpenConns(oldMax)
	})
	now := divaTestTime(21, 12, 0)
	for i, zone := range []string{"UTC", "Asia/Seoul", "America/New_York"} {
		if _, err := db.Exec("SELECT set_config('TimeZone',$1,false)", zone); err != nil {
			t.Fatal(err)
		}
		at := now.Add(time.Duration(i) * 45 * 24 * time.Hour)
		for _, mode := range []int{-1, 1, 2, 3} {
			event, err := r.EnsureDivaEvent(at, mode)
			if err != nil {
				t.Fatal(err)
			}
			var epoch int64
			if err = db.QueryRow("SELECT EXTRACT(epoch FROM start_time)::bigint FROM events WHERE id=$1", event.ID).Scan(&epoch); err != nil || epoch != int64(event.StartTime) {
				t.Fatalf("timezone=%s mode=%d epoch=%d want=%d err=%v", zone, mode, epoch, event.StartTime, err)
			}
			second, err := r.EnsureDivaEvent(at, mode)
			if err != nil || second != event {
				t.Fatalf("timezone=%s mode=%d reloaded=%+v want=%+v err=%v", zone, mode, second, event, err)
			}
		}
	}
}
