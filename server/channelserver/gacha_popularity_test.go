package channelserver

import (
	"errors"
	"sync"
	"testing"
)

func TestGachaPopularityCountsSuccessfulDraws(t *testing.T) {
	for _, tc := range []struct {
		name, kind           string
		draws                int
		rejected, statsError bool
	}{
		{"single", "normal", 1, false, false}, {"eleven draw bundle", "normal", 11, false, false},
		{"step bundle", "step", 11, false, false}, {"guaranteed only", "step", 0, false, false},
		{"box bundle", "box", 11, false, false}, {"insufficient payment", "normal", 1, true, false},
		{"rejected step", "step", 1, true, false}, {"rejected box", "box", 1, true, false},
		{"statistics error keeps paid rewards", "normal", 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gr := &mockGachaRepo{txRolls: tc.draws, rewardPool: []GachaEntry{{ID: 10, Weight: 100}},
				entryItems:      map[uint32][]GachaItem{10: {{ItemType: 7, ItemID: 500, Quantity: 5}}},
				guaranteedItems: []GachaItem{{ItemType: 7, ItemID: 700, Quantity: 2}}}
			if tc.rejected {
				gr.txErr = errors.New("payment rejected")
			}
			if tc.statsError {
				gr.recordPlayErr = errors.New("counter unavailable")
			}
			svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
			var err error
			switch tc.kind {
			case "normal":
				_, err = svc.PlayNormalGacha(1, 1, 77, 0)
			case "step":
				_, err = svc.PlayStepupGacha(1, 1, 77, 0)
			case "box":
				_, err = svc.PlayBoxGacha(1, 1, 77, 0)
			}
			if (err != nil) != tc.rejected {
				t.Fatalf("error=%v", err)
			}
			if tc.rejected {
				if len(gr.recordedPlays) != 0 {
					t.Fatal("rejected request counted")
				}
				return
			}
			if len(gr.recordedPlays) != 1 || gr.recordedPlays[0].id != 77 || gr.recordedPlays[0].draws != max(tc.draws, 1) {
				t.Fatalf("plays=%+v", gr.recordedPlays)
			}
			if err := svc.ResetBox(77, 1); err != nil {
				t.Fatal(err)
			}
			if len(gr.recordedPlays) != 1 {
				t.Fatal("reset changed lifetime popularity")
			}
		})
	}
}

func TestGachaPopularityRejectsEmptyAndShortBox(t *testing.T) {
	gr := &mockGachaRepo{txRolls: 2}
	svc := newTestGachaService(gr, &mockUserRepoGacha{}, newMockCharacterRepo())
	if _, err := svc.PlayNormalGacha(1, 1, 77, 0); err == nil {
		t.Fatal("empty pool accepted")
	}
	gr.rewardPool = []GachaEntry{{ID: 10, Weight: 1}}
	if _, err := svc.PlayBoxGacha(1, 1, 77, 0); err == nil {
		t.Fatal("short box accepted")
	}
	if len(gr.recordedPlays) != 0 {
		t.Fatal("invalid play counted")
	}
}

func TestGachaPopularityRepositoryAtomicTotals(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)
	repo := NewGachaRepository(db, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.RecordPlay(77, 11); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := repo.DeleteBoxEntries(77, 1); err != nil {
		t.Fatal(err)
	}
	var plays, draws int64
	if err := db.QueryRow(`SELECT play_count,draw_count FROM gacha_play_totals WHERE gacha_id=77`).Scan(&plays, &draws); err != nil {
		t.Fatal(err)
	}
	if plays != 8 || draws != 88 {
		t.Fatalf("plays=%d draws=%d", plays, draws)
	}
}
