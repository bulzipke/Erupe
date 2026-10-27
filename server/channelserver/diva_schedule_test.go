package channelserver

import (
	"errors"
	"reflect"
	"testing"
	"time"

	cfg "erupe-ce/config"
)

func TestDivaContinuousScheduleExactWeeks(t *testing.T) {
	event := DivaEvent{ID: 1, StartTime: uint32(divaTestTime(21, 12, 0).Unix()), Continuous: true}
	ts := divaEventTimestamps(event)
	want := []uint32{event.StartTime, event.StartTime + secsPerWeek, event.StartTime + secsPerWeek,
		event.StartTime + 2*secsPerWeek, event.StartTime + 2*secsPerWeek, event.StartTime + 3*secsPerWeek}
	if !reflect.DeepEqual(ts, want) {
		t.Fatalf("non-contiguous schedule: %v want %v", ts, want)
	}
	for _, at := range []time.Time{event.phaseTime(2), event.phaseTime(3).Add(-time.Nanosecond)} {
		if !divaBattleSongPhase(event, at) {
			t.Fatal("battle phase unavailable", at)
		}
	}
	if divaBattleSongPhase(event, event.phaseTime(2).Add(-time.Nanosecond)) || divaBattleSongPhase(event, event.phaseTime(3)) {
		t.Fatal("battle phase leaks outside its exact week")
	}
	if !divaSpecialHallPeriod(event, event.phaseTime(4)) || divaSpecialHallPeriod(event, event.phaseTime(5)) {
		t.Fatal("welcome week boundary")
	}
	options := &cfg.Config{}
	options.DebugOptions.DivaOverride = -1
	notices := divaEventNotices(event, options)
	if len(notices) != 3 || notices[2].End != want[5]-1 {
		t.Fatalf("continuous schedule advertises an idle phase: %+v", notices)
	}
	for i := 1; i < len(notices); i++ {
		if notices[i-1].End+1 != notices[i].Start {
			t.Fatal("notice gap/overlap")
		}
	}
	if !event.songRankingCutoff(event.phaseTime(1)).Equal(event.phaseTime(1)) {
		t.Fatal("ranking waits for removed interlude")
	}
	rules, err := divaRandomBonusRules(event)
	if err != nil || len(rules) == 0 || rules[len(rules)-1].EndOffsetSeconds != secsPerWeek {
		t.Fatalf("prayer bonuses miss the last hour: %v", err)
	}
	if _, err := divaBonusTargets(event, rules); err != nil {
		t.Fatal(err)
	}
}

func TestDivaContinuousSchedulePreservesLegacyDates(t *testing.T) {
	event := DivaEvent{ID: 1, StartTime: uint32(divaTestTime(21, 12, 0).Unix())}
	if !reflect.DeepEqual(divaEventTimestamps(event), generateDivaTimestamps(nil, event.StartTime, false)) {
		t.Fatal("legacy schedule changed")
	}
	if event.lifespan() != divaTotalLifespan || event.prayerDuration() != divaPhaseDuration {
		t.Fatal("legacy deadline changed")
	}
	if !divaMelodyUnexpired(event, event.phaseTime(5).Add(-time.Second)) || divaMelodyUnexpired(event, event.phaseTime(5)) {
		t.Fatal("legacy melody deadline changed")
	}
}

func TestRepoDivaContinuousRolloverAnchoredAfterDowntime(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	first, err := r.EnsureDivaEvent(now, -1)
	if err != nil || !first.Continuous || first.StartTime != uint32(now.Unix()) {
		t.Fatalf("bootstrap: %+v %v", first, err)
	}
	before, err := r.EnsureDivaEvent(first.phaseTime(5).Add(-time.Second), -1)
	if err != nil || before != first {
		t.Fatalf("early rollover: %+v %v", before, err)
	}
	next, err := r.EnsureDivaEvent(first.phaseTime(5).Add(8*time.Hour), -1)
	if err != nil || next.ID == first.ID || next.StartTime != uint32(first.phaseTime(5).Unix()) {
		t.Fatalf("delayed rollover drift: %+v %v", next, err)
	}
	later := next.phaseTime(5).Add(2*21*24*time.Hour + time.Hour)
	current, err := r.EnsureDivaEvent(later, -1)
	if err != nil || current.StartTime != uint32(next.phaseTime(5).Add(2*21*24*time.Hour).Unix()) {
		t.Fatalf("downtime drift: %+v %v", current, err)
	}
	again, err := NewDivaRepository(db).EnsureDivaEvent(later, -1)
	if err != nil || again != current {
		t.Fatalf("restart anchor changed: %+v %v", again, err)
	}
	var count int
	if err := db.Get(&count, "SELECT count(*) FROM events WHERE event_type='diva'"); err != nil || count != 3 {
		t.Fatalf("invented missed rounds: %d %v", count, err)
	}
	events, err := r.GetEvents()
	if err != nil || events[len(events)-1] != current {
		t.Fatalf("schedule policy not reloaded: %+v %v", events, err)
	}
}

func TestRepoDivaContinuousLegacyCutoverPreservesRewards(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-25*24*time.Hour), -1)
	u := CreateTestUser(t, db, "diva_cutover")
	charID := CreateTestCharacter(t, db, u, "Cutover")
	if err := r.AddPoints(charID, old.ID, 123, 45); err != nil {
		t.Fatal(err)
	}
	entries := []DivaRewardCatalogEntry{divaRewardRepoTestEntry("cutover-claimed", 6), divaRewardRepoTestEntry("cutover-pending", 6)}
	offers, err := r.offerDivaRewardsAt(charID, old.ID, 6, entries, now)
	if err != nil || len(offers) != 2 {
		t.Fatalf("offers: %+v %v", offers, err)
	}
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err := commitDivaRewardReceipts(tx, charID, []uint32{offers[0].ID}, 7); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := db.Get(&value, `SELECT jsonb_build_object(
			'event',(SELECT to_jsonb(e) FROM events e WHERE id=$1),
			'points',(SELECT jsonb_agg(to_jsonb(p)) FROM diva_points p WHERE event_id=$1),
			'period',(SELECT to_jsonb(p) FROM diva_interception_periods p WHERE event_id=$1),
			'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM diva_reward_receipts r WHERE event_id=$1))::text`, old.ID); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	next, err := r.EnsureDivaEvent(now, -1)
	if err != nil || next.ID == old.ID || !next.Continuous || next.StartTime != uint32(now.Unix()) {
		t.Fatalf("legacy idle cutover: %+v %v", next, err)
	}
	if after := snapshot(); after != before {
		t.Fatal("old event, points, period or receipts were rewritten")
	}
	prepared, err := r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[1].ID}, now, -1)
	if err != nil || len(prepared) != 1 || prepared[0] != offers[1] {
		t.Fatalf("pending old reward lost during new prayer: %+v %v", prepared, err)
	}
	prepared, err = r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[0].ID}, now, -1)
	if err != nil || len(prepared) != 0 {
		t.Fatalf("claimed reward reopened: %+v %v", prepared, err)
	}
	if _, err := r.EnsureDivaEvent(next.phaseTime(2), -1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prepareDivaRewardClaimsAt(charID, 6, []uint32{offers[1].ID}, next.phaseTime(2), -1); !errors.Is(err, ErrDivaInterceptionRewardExpired) {
		t.Fatalf("original next-interception expiry rule changed: %v", err)
	}
	if snapshot() != before {
		t.Fatal("rollover deleted old reward history")
	}
}

func TestRepoDivaContinuousRetainsActiveLegacyPhase(t *testing.T) {
	r, db := setupDivaRepo(t)
	now := divaTestTime(21, 12, 0)
	old := insertDivaRewardWindowTestEvent(t, db, now.Add(-time.Hour), -1)
	actual, err := r.EnsureDivaEvent(now, -1)
	if err != nil || actual != old {
		t.Fatalf("active legacy schedule replaced: %+v %v", actual, err)
	}
	next, err := r.EnsureDivaEvent(old.phaseTime(5), -1)
	if err != nil || next.ID == old.ID || next.StartTime != uint32(old.phaseTime(5).Unix()) || !next.Continuous {
		t.Fatalf("gap after legacy welcome: %+v %v", next, err)
	}
}

func TestRepoDivaContinuousPrayerLastHourRanking(t *testing.T) {
	r, db := setupDivaRepo(t)
	u := CreateTestUser(t, db, "diva_last_hour")
	charID := CreateTestCharacter(t, db, u, "LastHour")
	start := divaTestTime(1, 0, 0)
	for _, continuous := range []bool{false, true} {
		event := DivaEvent{StartTime: uint32(start.Unix()), Continuous: continuous}
		if err := db.QueryRow(`INSERT INTO events(event_type,start_time,diva_continuous) VALUES('diva',$1,$2) RETURNING id`, start, continuous).Scan(&event.ID); err != nil {
			t.Fatal(err)
		}
		at := start.Add(7*24*time.Hour - 30*time.Minute)
		if _, err := db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
			VALUES($1,$2,$3,1,100,0,$4)`, charID, event.ID, divaNoon(at), at); err != nil {
			t.Fatal(err)
		}
		ranks, err := r.GetDivaRanking(event.ID, charID, event.phaseTime(1))
		want := 0
		if continuous {
			want = 1
		}
		if err != nil || len(ranks) != want {
			t.Fatalf("continuous=%v ranks=%+v err=%v", continuous, ranks, err)
		}
		progress, err := r.GetDivaRewardProgress(charID, event.ID, start, event.phaseTime(1))
		if err != nil || progress.Points != int64(want*100) {
			t.Fatalf("continuous=%v progress=%+v err=%v", continuous, progress, err)
		}
	}
}
