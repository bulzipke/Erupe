package gametime

import (
	"testing"
	"time"
)

func TestMezFesWindowDailyNoon(t *testing.T) {
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.November, day, hour, minute, 0, 0, jst)
	}
	noon8 := at(8, 12, 0)
	noon7 := at(7, 12, 0)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"just before noon uses previous day", at(8, 11, 59), noon7},
		{"exactly noon starts new event", noon8, noon8},
		{"evening keeps today's event", at(8, 23, 30), noon8},
		{"after midnight still today's noon event", at(9, 0, 30), noon8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass a UTC time to prove the boundary is computed in JST.
			w := MezFesWindowAt(tt.now.UTC(), 604800, true, 12)
			if w.ID != uint32(tt.want.Unix()) || w.Start != w.ID {
				t.Fatalf("ID/Start = %d/%d, want %d", w.ID, w.Start, tt.want.Unix())
			}
			if w.End != uint32(tt.want.Add(7*24*time.Hour).Unix()) {
				t.Fatalf("End = %d, want start+7d", w.End)
			}
		})
	}
}

func TestMezFesWindowDailyChangesOncePerDay(t *testing.T) {
	base := time.Date(2026, time.November, 8, 12, 0, 0, 0, jst)
	first := MezFesWindowAt(base, 0, true, 12)
	if got := MezFesWindowAt(base.Add(23*time.Hour+59*time.Minute), 0, true, 12); got.ID != first.ID {
		t.Fatal("event changed before the next reset")
	}
	if got := MezFesWindowAt(base.Add(24*time.Hour), 0, true, 12); got.ID == first.ID {
		t.Fatal("event did not change at the next reset")
	}
}

func TestMezFesWindowWeeklyLegacy(t *testing.T) {
	// Sunday 2026-11-08 20:00 JST; week started Monday 2026-11-02.
	now := time.Date(2026, time.November, 8, 20, 0, 0, 0, jst)
	monday := time.Date(2026, time.November, 2, 0, 0, 0, 0, jst)
	next := monday.AddDate(0, 0, 7)
	for _, w := range []MezFesWindow{
		MezFesWindowAt(now, 172800, false, 12),
		MezFesWindowAt(now, 172800, true, 24), // invalid hour falls back
		MezFesWindowAt(now, 172800, true, -1),
	} {
		if w.ID != uint32(monday.Unix()) || w.End != uint32(next.Unix()) ||
			w.Start != uint32(next.Add(-48*time.Hour).Unix()) {
			t.Fatalf("weekly window = %+v", w)
		}
	}
	// The live production value is unchanged by this helper: 0x6ae753f0 is
	// Monday 2026-11-02 00:00 JST, the event ID clients saved this week.
	if w := MezFesWindowAt(now, 604800, false, 0); w.ID != 0x6ae753f0 {
		t.Fatalf("weekly ID = %#x, want 0x6ae753f0", w.ID)
	}
}
