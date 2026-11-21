package channelserver

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setupStampRepo(t *testing.T) (*StampRepository, *sqlx.DB, uint32) {
	t.Helper()
	db := SetupTestDB(t)
	userID := CreateTestUser(t, db, "stamp_test_user")
	charID := CreateTestCharacter(t, db, userID, "StampChar")
	repo := NewStampRepository(db)
	t.Cleanup(func() { TeardownTestDB(t, db) })
	return repo, db, charID
}

func initStamp(t *testing.T, repo *StampRepository, charID uint32) {
	t.Helper()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	if _, err := repo.db.Exec("INSERT INTO stamps (character_id, hl_checked, ex_checked) VALUES ($1,$2,$2)", charID, now); err != nil {
		t.Fatalf("Stamp setup failed: %v", err)
	}
}

func TestRepoStampClaimDailyExisting(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	noon := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO stamps (character_id, hl_total, hl_redeemed, hl_checked)
		VALUES ($1, 12, 8, $2)`, charID, noon.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		kind            string
		period          time.Time
		total, redeemed uint16
		added           bool
	}{
		{"same period as legacy claim", "hl", noon.AddDate(0, 0, -1), 12, 8, false},
		{"next period as of noon", "hl", noon, 13, 8, true},
		{"duplicate request", "hl", noon, 13, 8, false},
		{"late request from prior period", "hl", noon.AddDate(0, 0, -1), 13, 8, false},
		{"independent EX claim", "ex", noon, 1, 0, true},
		{"one stamp after a gap", "hl", noon.AddDate(0, 0, 4), 14, 8, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total, redeemed, added, err := repo.ClaimDaily(charID, tc.kind, tc.period)
			if err != nil || total != tc.total || redeemed != tc.redeemed || added != tc.added {
				t.Fatalf("ClaimDaily = %d, %d, %t, %v; want %d, %d, %t, nil", total, redeemed, added, err, tc.total, tc.redeemed, tc.added)
			}
		})
	}
}

func TestRepoStampClaimDailyConcurrentNewCharacter(t *testing.T) {
	repo, _, charID := setupStampRepo(t)
	noon := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	const requests = 8
	type result struct {
		kind            string
		total, redeemed uint16
		added           bool
		err             error
	}
	start := make(chan struct{})
	results := make(chan result, requests)
	kinds := [...]string{"hl", "ex"}
	for i := range requests {
		kind := kinds[i%len(kinds)]
		go func() {
			<-start
			total, redeemed, added, err := repo.ClaimDaily(charID, kind, noon)
			results <- result{kind, total, redeemed, added, err}
		}()
	}
	close(start)
	granted := make(map[string]int)
	for range requests {
		got := <-results
		if got.err != nil || got.total != 1 || got.redeemed != 0 {
			t.Errorf("unexpected stamp result: %+v", got)
		}
		if got.added {
			granted[got.kind]++
		}
	}
	if granted["hl"] != 1 || granted["ex"] != 1 {
		t.Fatalf("number of grants = %v; want one of each", granted)
	}
}

func TestRepoStampGetTotals(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	if _, err := db.Exec("UPDATE stamps SET hl_total=10, hl_redeemed=3 WHERE character_id=$1", charID); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	total, redeemed, err := repo.GetTotals(charID, "hl")
	if err != nil {
		t.Fatalf("GetTotals failed: %v", err)
	}
	if total != 10 || redeemed != 3 {
		t.Errorf("Expected total=10 redeemed=3, got total=%d redeemed=%d", total, redeemed)
	}
}

func TestRepoStampExchange(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	if _, err := db.Exec("UPDATE stamps SET hl_total=20, hl_redeemed=0 WHERE character_id=$1", charID); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	total, redeemed, err := repo.Exchange(charID, "hl")
	if err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}
	if total != 20 {
		t.Errorf("Expected total=20, got: %d", total)
	}
	if redeemed != 8 {
		t.Errorf("Expected redeemed=8, got: %d", redeemed)
	}
}

func TestRepoStampExchangeYearly(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	if _, err := db.Exec("UPDATE stamps SET hl_total=100, hl_redeemed=50 WHERE character_id=$1", charID); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	total, redeemed, err := repo.ExchangeYearly(charID)
	if err != nil {
		t.Fatalf("ExchangeYearly failed: %v", err)
	}
	if total != 52 {
		t.Errorf("Expected total=52 (100-48), got: %d", total)
	}
	if redeemed != 2 {
		t.Errorf("Expected redeemed=2 (50-48), got: %d", redeemed)
	}
}

func TestRepoStampGetMonthlyClaimed(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	claimedTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec("UPDATE stamps SET monthly_claimed=$1 WHERE character_id=$2", claimedTime, charID); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	got, err := repo.GetMonthlyClaimed(charID, "monthly")
	if err != nil {
		t.Fatalf("GetMonthlyClaimed failed: %v", err)
	}
	if !got.Equal(claimedTime) {
		t.Errorf("Expected %v, got: %v", claimedTime, got)
	}
}

func TestRepoStampSetMonthlyClaimed(t *testing.T) {
	repo, _, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	claimedTime := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.SetMonthlyClaimed(charID, "monthly", claimedTime); err != nil {
		t.Fatalf("SetMonthlyClaimed failed: %v", err)
	}

	got, err := repo.GetMonthlyClaimed(charID, "monthly")
	if err != nil {
		t.Fatalf("GetMonthlyClaimed failed: %v", err)
	}
	if !got.Equal(claimedTime) {
		t.Errorf("Expected %v, got: %v", claimedTime, got)
	}
}

func TestRepoStampExTypes(t *testing.T) {
	repo, db, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	if _, err := db.Exec("UPDATE stamps SET ex_total=16, ex_redeemed=0 WHERE character_id=$1", charID); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	total, redeemed, err := repo.Exchange(charID, "ex")
	if err != nil {
		t.Fatalf("Exchange(ex) failed: %v", err)
	}
	if total != 16 {
		t.Errorf("Expected ex_total=16, got: %d", total)
	}
	if redeemed != 8 {
		t.Errorf("Expected ex_redeemed=8, got: %d", redeemed)
	}
}

func TestRepoStampMonthlyHlClaimed(t *testing.T) {
	repo, _, charID := setupStampRepo(t)
	initStamp(t, repo, charID)

	claimedTime := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)
	if err := repo.SetMonthlyClaimed(charID, "monthly_hl", claimedTime); err != nil {
		t.Fatalf("SetMonthlyClaimed(monthly_hl) failed: %v", err)
	}

	got, err := repo.GetMonthlyClaimed(charID, "monthly_hl")
	if err != nil {
		t.Fatalf("GetMonthlyClaimed(monthly_hl) failed: %v", err)
	}
	if !got.Equal(claimedTime) {
		t.Errorf("Expected %v, got: %v", claimedTime, got)
	}
}
