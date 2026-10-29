package channelserver

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/mhfpallone"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

func automaticPalloneOptions() cfg.PalloneFestivalOptions {
	return cfg.PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 21}
}

func TestPalloneAutomaticMondayNoonAndRewardBoundaries(t *testing.T) {
	zone := time.FixedZone("KST", 9*60*60)
	o := automaticPalloneOptions()
	start := time.Date(2026, 10, 26, 12, 0, 0, 0, zone)
	for _, tc := range []struct{ now, want time.Time }{
		{start.Add(-time.Second), start.AddDate(0, 0, -7)},
		{start, start}, {start.Add(time.Second), start},
	} {
		got, err := palloneInitialStart(tc.now, o)
		if err != nil || !got.Equal(tc.want) {
			t.Fatalf("Monday noon %s: %s, %v", tc.now, got, err)
		}
	}
	o.StartAt = start.Format(time.RFC3339)
	for _, days := range []int{0, 20, 21, 62, 63} {
		now := start.AddDate(0, 0, days)
		got, err := palloneInitialStart(now, o)
		if err != nil || !got.Equal(start.AddDate(0, 0, days/21*21)) {
			t.Fatalf("anchor/downtime at day %d: %s %v", days, got, err)
		}
	}
	first, err := palloneRotationEvent(1, start, o)
	if err != nil || first.End.Sub(first.Start) != 21*24*time.Hour || first.ClaimUntil.Sub(first.End) != 21*24*time.Hour {
		t.Fatal("unexpected continuous round deadlines", first, err)
	}
	second, err := palloneRotationEvent(2, first.End, o)
	if err != nil || !first.ClaimUntil.Equal(second.End) || !first.Claimable(second.Start) || !second.Active(first.End) ||
		first.Active(first.End) || first.Claimable(first.End.Add(-time.Nanosecond)) || first.Claimable(first.ClaimUntil) {
		t.Fatal("old claims/current play do not overlap correctly")
	}
	if _, err := palloneRotationEvent(int64(math.MaxInt32)+1, start, o); err == nil {
		t.Fatal("round ID wrapped")
	}
	if _, err := palloneRotationEvent(1, time.Unix(math.MaxUint32-1, 0), o); err == nil {
		t.Fatal("protocol time wrapped")
	}
}

type mockPalloneRotationRepo struct {
	*mockPalloneRepo
	schedule PalloneSchedule
	calls    int
	err      error
	digest   string
	routes   []PalloneRoute
}

func (r *mockPalloneRotationRepo) EnsurePalloneSchedule(_ time.Time, o cfg.PalloneFestivalOptions, digest string, routes []PalloneRoute) (PalloneSchedule, error) {
	r.calls++
	r.digest, r.routes = digest, routes
	if o.EventID != 0 {
		return PalloneSchedule{}, ErrPalloneInvalid
	}
	return r.schedule, r.err
}

func palloneAutomaticSession(t *testing.T) (*Session, *mockTowerRotationRepo, *mockPalloneRotationRepo) {
	t.Helper()
	s, tower, legacy := palloneCoexistenceSession()
	installPalloneTestProfile(t, s, legacy)
	s.server.erupeConfig.PalloneFestival = automaticPalloneOptions()
	start := rotationMondayNoon(TimeAdjusted())
	o := s.server.erupeConfig.PalloneFestival
	current, err := palloneRotationEvent(100, start, o)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := palloneRotationEvent(101, current.End, o)
	previous, _ := palloneRotationEvent(99, start.AddDate(0, 0, -21), o)
	r := &mockPalloneRotationRepo{mockPalloneRepo: legacy, schedule: PalloneSchedule{Current: current, Next: next, Previous: &previous}}
	s.server.palloneRepo = r
	s.server.palloneService = NewPalloneService(r)
	return s, tower, r
}

func TestPalloneAutomaticCurrentScoresAndPreviousClaimsStaySeparate(t *testing.T) {
	s, _, r := palloneAutomaticSession(t)
	r.scores = []mhfpallone.Score{{RouteKey: 1, Total: 123, Best: 123}}
	handleMsgMhfCaravanMyScore(s, &mhfpacket.MsgMhfCaravanMyScore{AckHandle: 1, Unk0: 1})
	a := readAck(t, s)
	if a.ErrorCode != 0 || binary.BigEndian.Uint32(a.Payload) != 100 || r.eventID != 100 {
		t.Fatal("current score request was redirected to previous round", a)
	}
	claim, err := s.server.palloneClaimEvent(TimeAdjusted())
	if err != nil || claim == nil || claim.ID != 99 || !claim.Claimable(TimeAdjusted()) {
		t.Fatal("previous claim ledger was lost", claim, err)
	}
	if len(r.digest) != 64 || len(r.routes) != 1 || r.routes[0].Key != 1 || r.settled != 0 || s.palloneReceipt != nil {
		t.Fatal("rotation did not use the verified route or issued points")
	}
	handleMsgMhfGetWeeklySeibatuRankingReward(s, &mhfpacket.MsgMhfGetWeeklySeibatuRankingReward{AckHandle: 2, Unk1: 3})
	a = readAck(t, s)
	if a.ErrorCode != 0 || binary.BigEndian.Uint32(a.Payload) != 100 || r.eventID != 100 {
		t.Fatal("new departures inherited the previous festival")
	}
}

func TestPalloneAutomaticCalendarCachesConcurrentAndNextRounds(t *testing.T) {
	s, tower, r := palloneAutomaticSession(t)
	s.server.erupeConfig.PalloneFestival.Enabled = false
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 1})
	before := readAck(t, s)
	s.server.erupeConfig.PalloneFestival.Enabled = true
	handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 2})
	a := readAck(t, s)
	if a.ErrorCode != 0 || len(a.Payload) != 6*32 || !bytes.Equal(before.Payload, a.Payload[:32]) {
		t.Fatal("automatic calendar changed Tower", a)
	}
	now := TimeAdjusted()
	if nativeCalendarEvent(a.Payload, 21, now) != uint32(tower.event.ID) || nativeCalendarEvent(a.Payload, 11, now) != 100 || nativeCalendarEvent(a.Payload, 12, now) != 99 {
		t.Fatal("native cache cannot select current play and previous claim independently")
	}
	boundary := r.schedule.Current.End.Add(time.Second)
	if nativeCalendarEvent(a.Payload, 11, boundary) != 101 || nativeCalendarEvent(a.Payload, 12, boundary) != 100 {
		t.Fatal("cached next-round handover requires another login")
	}
	old := r.schedule.Current
	r.schedule.Current = r.schedule.Next
	r.schedule.Next, _ = palloneRotationEvent(102, r.schedule.Current.End, automaticPalloneOptions())
	r.schedule.Previous = &old
	e, err := s.server.palloneEvent(boundary)
	claim, claimErr := s.server.palloneClaimEvent(boundary)
	if err != nil || claimErr != nil || e == nil || e.ID != 101 || claim == nil || claim.ID != 100 {
		t.Fatal("server/cache disagree after automatic handover")
	}
}

func TestPalloneAutomaticPublicationAndRepositoryFailuresKeepTower(t *testing.T) {
	for _, kind := range []string{"missing publication", "changed stage", "repository failure", "no lifecycle"} {
		t.Run(kind, func(t *testing.T) {
			s, tower, r := palloneAutomaticSession(t)
			switch kind {
			case "missing publication":
				if err := os.Remove(filepath.Join(s.server.erupeConfig.BinPath, "pallone-client-profile.json")); err != nil {
					t.Fatal(err)
				}
			case "changed stage":
				if err := os.WriteFile(filepath.Join(s.server.erupeConfig.BinPath, "quests", "27438d0.bin"), []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
			case "repository failure":
				r.err = errors.New("unavailable")
			case "no lifecycle":
				s.server.palloneRepo = r.mockPalloneRepo
			}
			handleMsgMhfGetEarthStatus(s, &mhfpacket.MsgMhfGetEarthStatus{AckHandle: 1})
			a := readAck(t, s)
			if a.ErrorCode != 0 || len(a.Payload) != 32 || nativeCalendarEvent(a.Payload, 21, TimeAdjusted()) != uint32(tower.event.ID) {
				t.Fatal("festival error closed Tower", a)
			}
			if (kind == "missing publication" || kind == "changed stage") && r.calls != 0 {
				t.Fatal("unverified publication created a round")
			}
			if !s.server.erupeConfig.TowerRotation.Enabled || s.server.erupeConfig.EarthID != 7 || r.settled != 0 {
				t.Fatal("festival changed Tower or credited points")
			}
		})
	}
}

func TestPalloneAutomaticFutureAndDisabledSelection(t *testing.T) {
	s, _, r := palloneAutomaticSession(t)
	current, _ := palloneRotationEvent(100, rotationMondayNoon(TimeAdjusted()).AddDate(0, 0, 7), automaticPalloneOptions())
	r.schedule = PalloneSchedule{Current: current}
	r.schedule.Next, _ = palloneRotationEvent(101, current.End, automaticPalloneOptions())
	e, err := s.server.palloneEvent(TimeAdjusted())
	if err != nil || e != nil {
		t.Fatal("future schedule admitted current play")
	}
	if claim, err := s.server.palloneClaimEvent(TimeAdjusted()); err != nil || claim != nil {
		t.Fatal("first automatic round invented old rewards")
	}
	s.server.erupeConfig.PalloneFestival.Enabled = false
	calls := r.calls
	e, err = s.server.palloneEvent(TimeAdjusted())
	if err != nil || e != nil || r.calls != calls {
		t.Fatal("disabled festival created a round")
	}
	bad := automaticPalloneOptions()
	bad.StartAt = strings.Repeat("x", 10)
	if _, err := palloneInitialStart(TimeAdjusted(), bad); err == nil {
		t.Fatal("invalid anchor accepted")
	}
}
