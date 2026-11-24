package channelserver

import (
	"testing"
	"time"
)

func TestRepoDivaMelodyContinuousDeadlineUsesRoundTime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start time.Time
	}{
		{"spring", time.Date(2026, 3, 2, 12, 0, 0, 0, divaLocation)},
		{"fall", time.Date(2026, 10, 26, 12, 0, 0, 0, divaLocation)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db, charID, guildID, event, start, quest := setupDivaMapRepoTest(t)
			depart := start.Add(time.Minute)
			divaMapTestReport(t, r, charID, guildID, event, quest, divaRunKeyOne, 59702, depart, depart.Add(time.Minute))

			event.Continuous = true
			event.StartTime = uint32(tc.start.Unix())
			if _, err := db.Exec(`UPDATE events SET start_time=$2,diva_continuous=true WHERE id=$1`, event.ID, tc.start); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO diva_interception_periods(event_id,starts_at,ends_at)
				VALUES($1,$2,$3) ON CONFLICT(event_id) DO UPDATE SET starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at`,
				event.ID, event.phaseTime(2), event.phaseTime(3)); err != nil {
				t.Fatal(err)
			}

			// Hold the DB session timezone across the calls that cross DST.
			db.SetMaxOpenConns(1)
			t.Cleanup(func() {
				if _, err := db.Exec(`RESET TIME ZONE`); err != nil {
					t.Error(err)
				}
				db.SetMaxOpenConns(0)
			})
			if _, err := db.Exec(`SET TIME ZONE 'America/New_York'`); err != nil {
				t.Fatal(err)
			}

			end := event.phaseTime(5)
			for _, check := range []struct {
				now  time.Time
				want int
			}{{end.Add(-2 * time.Hour), 8}, {end.Add(-time.Minute), 8}, {end, 0}} {
				offers, err := r.offerDivaRewardsAt(charID, event.ID, 6, nil, check.now, 3)
				if err != nil || len(offers) != check.want {
					t.Fatalf("at %v relative to end: offers=%d want=%d err=%v", check.now.Sub(end), len(offers), check.want, err)
				}
			}
		})
	}
}
