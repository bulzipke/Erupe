package channelserver

import (
	"reflect"
	"testing"
	"time"
)

func TestFestaPersonalFPBonus(t *testing.T) {
	for _, tt := range []struct{ souls, cap, want uint32 }{
		{99, 9999999, 0},
		{100, 9999999, 300},
		{499, 9999999, 120000},
		{500, 9999999, 120750},
		{528, 9999999, 141750},
		{1000, 9999999, 495750},
		{1001, 9999999, 496750},
		{1501, 9999999, 996750},
		{10505, 9999999, 9999999},
		{^uint32(0), 9999999, 9999999},
	} {
		if got := festaPersonalFPBonus(tt.souls, tt.cap); got != tt.want {
			t.Errorf("%d souls with cap %d = %d FP, want %d", tt.souls, tt.cap, got, tt.want)
		}
	}
}

func TestFestaSecretWindows(t *testing.T) {
	for _, tt := range []struct {
		day, hour, minute int
		slot, cutoffHour  int
		both              bool
	}{
		{13, 13, 59, 0, -1, false}, // Friday
		{13, 14, 0, 13, 12, false},
		{13, 16, 0, 0, -1, false},
		{14, 6, 0, 15, 4, false},
		{14, 14, 0, 16, 12, true},
		{15, 6, 0, 18, 4, true},
		{16, 6, 0, 0, 4, true},
		{16, 14, 0, 1, 12, false},
	} {
		now := time.Date(2026, time.November, tt.day, tt.hour, tt.minute, 0, 0, festaJST)
		cutoff, slot, both := festaSecretWindow(now.UTC())
		if tt.cutoffHour < 0 {
			if !cutoff.IsZero() {
				t.Errorf("%s: unexpected secret window", now)
			}
			continue
		}
		want := time.Date(2026, time.November, tt.day, tt.cutoffHour, 0, 0, 0, festaJST)
		if !cutoff.Equal(want) || slot != tt.slot || both != tt.both {
			t.Errorf("%s: cutoff %s, slot %d, both %t", now, cutoff, slot, both)
		}
	}

	sunday := festaSecretTrials(146, 18, true, false)
	if len(sunday) != 2 || sunday[0].GoalID != 26675 || sunday[0].Reward != 94 || sunday[0].Unk != 2 ||
		sunday[1].GoalID != 26675 || sunday[1].Reward != 20 || sunday[1].Unk != 0 {
		t.Fatalf("Sunday HR secret and trailing-team bonus: %+v", sunday)
	}
	tied := festaSecretTrials(146, 18, true, true)
	if len(tied) != 1 || tied[0].Unk != 2 {
		t.Fatalf("Tied teams should both receive only the base secret bonus: %+v", tied)
	}
}

func TestFestaMonopolyPriority(t *testing.T) {
	votes := []festaMonopolyVote{
		{1, FestivalColorBlue}, {1, FestivalColorRed},
		{2, FestivalColorBlue}, {3, FestivalColorRed},
		{4, FestivalColorBlue}, {5, FestivalColorRed},
	}
	want := map[uint32]FestivalColor{1: FestivalColorRed, 2: FestivalColorBlue, 3: FestivalColorRed, 4: FestivalColorBlue}
	if got := festaMonopolies(votes, FestivalColorRed); !reflect.DeepEqual(got, want) {
		t.Fatalf("picks = %v, want %v", got, want)
	}
}

func TestFestaGoalCutsAndRewardGroups(t *testing.T) {
	for original, want := range map[uint32]uint32{0: 0, 1: 1, 100: 34, 1000: 334, 1500: 500, 30000: 10000, 500000: 166667} {
		if got := festaSoulGoal(original); got != want {
			t.Errorf("goal %d = %d, want %d", original, got, want)
		}
	}
	rewards := festaMainRewards()
	for _, tt := range []struct {
		team                          FestivalColor
		rank, red                     uint32
		result, tickets, heat, points uint16
	}{
		{FestivalColorBlue, 300, 80, 1, 70, 200, 1000},
		{FestivalColorBlue, 301, 80, 2, 50, 140, 500},
		{FestivalColorRed, 300, 80, 3, 40, 100, 300},
		{FestivalColorRed, 301, 80, 4, 10, 40, 100},
		{FestivalColorRed, 1, 100, 5, 50, 140, 500},
	} {
		result := festaRewardResult(tt.team, tt.rank, 100, tt.red)
		if result != uint32(tt.result) {
			t.Fatalf("reward result = %d, want %d", result, tt.result)
		}
		group := rewards[(result-1)*4 : result*4]
		if group[0].Quantity != tt.tickets || group[1].Quantity != tt.heat || group[2].Quantity != tt.points {
			t.Errorf("result %d: unexpected items or FP: %+v", result, group)
		}
	}
}
