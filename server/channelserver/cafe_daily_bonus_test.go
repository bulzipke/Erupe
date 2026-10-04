package channelserver

import (
	"reflect"
	"testing"
	"time"
)

var testCafeBonuses = []CafeBonus{
	{ID: 1, TimeReq: 1800, ItemType: 17, Quantity: 125},
	{ID: 2, TimeReq: 3600, ItemType: 17, Quantity: 250},
	{ID: 3, TimeReq: 43200, ItemType: 17, Quantity: 1250},
}

func newCafeBonusSession(now time.Time, cafeTime int, claims map[uint32]CafeBonusClaim) (*Session, *mockCharacterRepo, *mockCafeRepo) {
	srv := createMockServer()
	srv.erupeConfig.GameplayOptions.MaximumNP = 100000
	charRepo := newMockCharacterRepo()
	charRepo.times["cafe_reset"] = cafeDayNext(now)
	charRepo.ints["cafe_time"] = cafeTime
	cafeRepo := &mockCafeRepo{bonuses: append([]CafeBonus(nil), testCafeBonuses...), claims: claims}
	srv.charRepo, srv.cafeRepo = charRepo, cafeRepo
	s := createMockSession(1, srv)
	s.cafeTimeStart.Store(now.Unix())
	return s, charRepo, cafeRepo
}

func TestSettleCafeBonusGrantsReachedBonuses(t *testing.T) {
	now := time.Now()
	s, _, cafeRepo := newCafeBonusSession(now, 3600, map[uint32]CafeBonusClaim{
		1: {Claimed: true, ItemType: 17, Granted: 125},
		2: {Claimed: true, ItemType: 17, Granted: 250},
	})
	want := []string{
		"30분 접속으로 PC방 포인트를 125 획득했습니다.",
		"60분 접속으로 PC방 포인트를 250 획득했습니다.",
	}
	if got := s.settleCafeBonus(now); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if !reflect.DeepEqual(cafeRepo.claimed, []uint32{1, 2}) {
		t.Fatalf("claimed %v", cafeRepo.claimed)
	}

	s, _, _ = newCafeBonusSession(now, 0, nil)
	if got := s.settleCafeBonus(now); got != nil {
		t.Fatalf("nothing reached but sent %q", got)
	}
}

func TestSettleCafeBonusCapNoticeOncePerDay(t *testing.T) {
	const capNotice = "PC방 포인트가 상한치입니다. 더 이상 획득할 수 없습니다."
	now := time.Now()
	s, _, _ := newCafeBonusSession(now, 3600, map[uint32]CafeBonusClaim{
		1: {Claimed: true, ItemType: 17, Granted: 40},
	})
	want := []string{"30분 접속으로 PC방 포인트를 40 획득했습니다.", capNotice}
	if got := s.settleCafeBonus(now); !reflect.DeepEqual(got, want) {
		t.Fatalf("partial grant %q", got)
	}

	s, _, cafeRepo := newCafeBonusSession(now, 3600, map[uint32]CafeBonusClaim{1: {Capped: true, ItemType: 17}})
	if got := s.settleCafeBonus(now); !reflect.DeepEqual(got, []string{capNotice}) {
		t.Fatalf("cap notice %q", got)
	}
	if got := s.settleCafeBonus(now); got != nil {
		t.Fatalf("repeated cap notice %q", got)
	}
	if len(cafeRepo.claimed) != 0 {
		t.Fatalf("capped bonus was claimed: %v", cafeRepo.claimed)
	}
}

// A stored weekly reset date (from before the daily cycle) or a passed reset
// starts a new day: the session's earlier time stops counting at midnight.
func TestSyncCafeDayResetsAtMidnight(t *testing.T) {
	midnight := dailyCoinMidnight(time.Now())
	now := midnight.Add(time.Minute)
	for name, reset := range map[string]time.Time{
		"passed": midnight,
		"weekly": midnight.AddDate(0, 0, 6),
	} {
		t.Run(name, func(t *testing.T) {
			s, charRepo, _ := newCafeBonusSession(now, 5000, nil)
			charRepo.times["cafe_reset"] = reset
			s.cafeTimeStart.Store(midnight.Add(-time.Hour).Unix())
			if err := s.syncCafeDay(now); err != nil {
				t.Fatal(err)
			}
			if charRepo.ints["cafe_time"] != 0 || !charRepo.times["cafe_reset"].Equal(cafeDayNext(now)) {
				t.Fatalf("cafe_time %d reset %v", charRepo.ints["cafe_time"], charRepo.times["cafe_reset"])
			}
			if got := s.cafeSessionSeconds(now); got != 60 {
				t.Fatalf("session seconds after midnight = %d", got)
			}
		})
	}
}
