package channelserver

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Every test owns a dedicated schema. Never call SetupTestDB or reset public.
func setupDailyPointRepository(t *testing.T) (*CharacterRepository, *sqlx.DB) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("daily point fixture requires isolated localhost:5433/erupe_test")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=3", c.Host, c.Port, c.User, c.Password, c.DBName)
	admin, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot initialize isolated daily point fixture")
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		if os.Getenv("ERUPE_DAILY_ATOMIC_DB_REQUIRED") == "1" {
			t.Fatal("required isolated daily point fixture is unavailable")
		}
		t.Skip("isolated localhost:5433/erupe_test is unavailable")
	}
	schema := fmt.Sprintf("daily_points_atomic_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`); err != nil {
			t.Errorf("daily point fixture cleanup: %v", err)
		}
		_ = admin.Close()
	})
	db, err := sqlx.Open("postgres", dsn+" search_path="+schema+" application_name="+schema)
	if err != nil {
		t.Fatal("cannot open isolated daily point schema")
	}
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE characters (
		id integer PRIMARY KEY,daily_time timestamptz,
		netcafe_points integer DEFAULT 0 CHECK(netcafe_points>=0),
		bonus_quests integer NOT NULL DEFAULT 0,daily_quests integer NOT NULL DEFAULT 0,
		promo_points integer NOT NULL DEFAULT 0);
		INSERT INTO characters(id) VALUES(11),(12)`)
	return NewCharacterRepository(db), db
}

func dailyPointFixtureState(t *testing.T, db *sqlx.DB, id uint32) (int64, int64, int64, int64, sql.NullTime) {
	t.Helper()
	var np, bonus, daily, promo int64
	var until sql.NullTime
	if err := db.QueryRow(`SELECT COALESCE(netcafe_points,0),bonus_quests,daily_quests,promo_points,daily_time
		FROM characters WHERE id=$1`, id).Scan(&np, &bonus, &daily, &promo, &until); err != nil {
		t.Fatal(err)
	}
	return np, bonus, daily, promo, until
}

func TestDailyPointRepositoryClaimPolicy(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	target := time.Date(2026, 10, 25, 12, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	for _, tc := range []struct {
		name       string
		stored     any
		points     any
		maximum    int
		wantPoints int64
		claim      bool
	}{
		{"eligible", target.Add(-time.Hour), 10, 100, 15, true},
		{"same_boundary", target, 10, 100, 10, false},
		{"future_boundary", target.Add(time.Hour), 10, 100, 10, false},
		{"null_time_and_points", nil, nil, 100, 5, true},
		{"at_cap_still_claims", nil, 100, 100, 100, true},
		{"partial_cap", nil, 98, 100, 100, true},
		{"above_cap_keeps_old_min", nil, 120, 100, 100, true},
		{"zero_cap_still_claims", nil, 10, 0, 0, true},
		{"int4_cap_uses_wide_sum", nil, math.MaxInt32, math.MaxInt32, math.MaxInt32, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET daily_time=$1,netcafe_points=$2,bonus_quests=2,daily_quests=3,promo_points=13 WHERE id=11`, tc.stored, tc.points)
			claimed, err := repo.ClaimDailyCafe(11, target, 10, 5, tc.maximum)
			if err != nil || claimed != tc.claim {
				t.Fatalf("claim=%v error=%v", claimed, err)
			}
			np, bonus, daily, promo, until := dailyPointFixtureState(t, db, 11)
			if np != tc.wantPoints || promo != 13 {
				t.Fatalf("NP/promo=%d/%d", np, promo)
			}
			if tc.claim {
				if bonus != 10 || daily != 5 || !until.Valid || !until.Time.Equal(target) {
					t.Fatal("claim fields were not committed together")
				}
			} else if bonus != 2 || daily != 3 {
				t.Fatal("ineligible claim changed allowances")
			}
		})
	}
}

func TestDailyPointRepositoryConcurrentClaims(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	target := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	db.MustExec(`UPDATE characters SET netcafe_points=10`)
	var wg sync.WaitGroup
	results := make(chan bool, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimDailyCafe(11, target, 10, 5, 100)
			results <- claimed
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
	claims := 0
	for result := range results {
		if result {
			claims++
		}
	}
	np, bonus, daily, _, _ := dailyPointFixtureState(t, db, 11)
	if claims != 1 || np != 15 || bonus != 10 || daily != 5 {
		t.Fatalf("claims=%d NP/bonus/daily=%d/%d/%d", claims, np, bonus, daily)
	}
	if claimed, err := repo.ClaimDailyCafe(12, target, 10, 5, 100); err != nil || !claimed {
		t.Fatal("other character's claim was consumed", claimed, err)
	}
	if claimed, err := repo.ClaimDailyCafe(11, target.Add(-time.Hour), 99, 99, 100); err != nil || claimed {
		t.Fatal("late older boundary changed the new claim", claimed, err)
	}
}

func TestDailyPointRepositoryReadStrictExpiry(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	stored := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		stored  any
		now     time.Time
		expired bool
	}{
		{"before", stored, stored.Add(-time.Nanosecond), false},
		{"equal", stored, stored, false},
		{"one_nanosecond_after", stored, stored.Add(time.Nanosecond), true},
		{"null_uses_old_fallback", nil, stored, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET daily_time=$1,netcafe_points=19,bonus_quests=10,daily_quests=5,promo_points=13 WHERE id=11`, tc.stored)
			bonus, daily, promo, err := repo.ReadDailyEtcPoints(11, tc.now)
			if err != nil || promo != 13 {
				t.Fatal(bonus, daily, promo, err)
			}
			wantBonus, wantDaily := uint32(10), uint32(5)
			if tc.expired {
				wantBonus, wantDaily = 0, 0
			}
			np, savedBonus, savedDaily, savedPromo, _ := dailyPointFixtureState(t, db, 11)
			if bonus != wantBonus || daily != wantDaily || savedBonus != int64(bonus) || savedDaily != int64(daily) || savedPromo != int64(promo) || np != 19 {
				t.Fatal("returned point values differ from the committed expiry state")
			}
		})
	}
}

func TestDailyPointRepositoryStatementFailureRollsBackClaim(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	target := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		points  int64
		bonus   uint32
		daily   uint32
		maximum int
	}{
		{"bonus_int4_overflow", 10, uint32(math.MaxInt32) + 1, 5, 100},
		{"daily_int4_overflow", 10, 10, uint32(math.MaxInt32) + 1, 100},
		{"NP_int4_overflow", math.MaxInt32, 10, 5, int(math.MaxInt32) + 5},
		{"negative_cap_constraint", 10, 10, 5, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET daily_time=NULL,netcafe_points=$1,bonus_quests=2,daily_quests=3,promo_points=13 WHERE id=11`, tc.points)
			if claimed, err := repo.ClaimDailyCafe(11, target, tc.bonus, tc.daily, tc.maximum); err == nil || claimed {
				t.Fatal("invalid claim was accepted", claimed, err)
			}
			np, bonus, daily, promo, until := dailyPointFixtureState(t, db, 11)
			if np != tc.points || bonus != 2 || daily != 3 || promo != 13 || until.Valid {
				t.Fatal("a failed claim partially committed")
			}
		})
	}
}

func TestDailyPointRepositoryReadFailureRollsBackReset(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	db.MustExec(`UPDATE characters SET daily_time=NULL,bonus_quests=10,daily_quests=5,promo_points=-1 WHERE id=11`)
	if bonus, daily, promo, err := repo.ReadDailyEtcPoints(11, time.Now()); err == nil || bonus != 0 || daily != 0 || promo != 0 {
		t.Fatal("invalid stored point should fail instead of committing expiry", bonus, daily, promo, err)
	}
	_, bonus, daily, promo, _ := dailyPointFixtureState(t, db, 11)
	if bonus != 10 || daily != 5 || promo != -1 {
		t.Fatal("scan error committed the preceding reset")
	}
}

func TestDailyPointRepositoryCommitFailuresRollBack(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	db.MustExec(`CREATE FUNCTION reject_daily_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected daily commit failure'; END $$;
		CREATE CONSTRAINT TRIGGER daily_commit_failure AFTER UPDATE ON characters
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_daily_commit()`)
	if claimed, err := repo.ClaimDailyCafe(11, time.Now(), 10, 5, 100); err == nil || claimed {
		t.Fatal("commit failure acknowledged a claim", claimed, err)
	}
	_, bonus, daily, _, until := dailyPointFixtureState(t, db, 11)
	if bonus != 0 || daily != 0 || until.Valid {
		t.Fatal("deferred failure committed claim state")
	}
	if _, _, _, err := repo.ReadDailyEtcPoints(11, time.Now()); err == nil {
		t.Fatal("expiry commit failure was hidden")
	}
}

func TestDailyPointRepositoryDeltaAndCapBoundaries(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	for pointType, column := range []string{"bonus_quests", "daily_quests", "promo_points"} {
		t.Run(column, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET bonus_quests=0,daily_quests=0,promo_points=0 WHERE id=11`)
			if err := repo.AdjustEtcPoint(11, uint8(pointType), math.MaxInt16); err != nil {
				t.Fatal(err)
			}
			if err := repo.AdjustEtcPoint(11, uint8(pointType), math.MinInt16); err != nil {
				t.Fatal(err)
			}
			var value int
			if err := db.QueryRow(`SELECT ` + column + ` FROM characters WHERE id=11`).Scan(&value); err != nil || value != 0 {
				t.Fatal("zero floor changed", value, err)
			}
			db.MustExec(`UPDATE characters SET ` + column + `=2147483647 WHERE id=11`)
			if err := repo.AdjustEtcPoint(11, uint8(pointType), 1); err == nil {
				t.Fatal("int4 overflow was accepted")
			}
			if err := db.QueryRow(`SELECT ` + column + ` FROM characters WHERE id=11`).Scan(&value); err != nil || value != math.MaxInt32 {
				t.Fatal("overflow changed stored points", value, err)
			}
		})
	}
	if err := repo.AdjustEtcPoint(999, 99, 1); err != nil {
		t.Fatal("unsupported-type no-op changed", err)
	}
	for _, tc := range []struct {
		name       string
		initial    any
		delta, cap int
		want       int64
		fail       bool
	}{
		{"null", nil, 5, 100, 5, false},
		{"partial", 98, 5, 100, 100, false},
		{"above", 120, 5, 100, 100, false},
		{"wide_sum_at_cap", math.MaxInt32, 5, math.MaxInt32, math.MaxInt32, false},
		{"int4_overflow", math.MaxInt32, 1, int(math.MaxInt32) + 1, math.MaxInt32, true},
		{"negative_constraint", 0, -1, 100, 0, true},
	} {
		t.Run("NP_"+tc.name, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET netcafe_points=$1 WHERE id=11`, tc.initial)
			err := repo.AddCappedNetcafePoints(11, tc.delta, tc.cap)
			if (err != nil) != tc.fail {
				t.Fatal("unexpected capped NP result", err)
			}
			np, _, _, _, _ := dailyPointFixtureState(t, db, 11)
			if np != tc.want {
				t.Fatal("capped NP arithmetic changed", np, tc.want)
			}
		})
	}
}

func TestDailyPointRepositoryMissingCharacter(t *testing.T) {
	repo, _ := setupDailyPointRepository(t)
	_, claimErr := repo.ClaimDailyCafe(999, time.Now(), 3, 1, 100)
	_, _, _, readErr := repo.ReadDailyEtcPoints(999, time.Now())
	adjustErr := repo.AdjustEtcPoint(999, 1, -1)
	addErr := repo.AddCappedNetcafePoints(999, 5, 100)
	for _, err := range []error{claimErr, readErr, adjustErr, addErr} {
		if !errors.Is(err, ErrCharacterNotFound) {
			t.Fatal("missing character was not distinguished", err)
		}
	}
}

func TestDailyPointRepositoryConcurrentDeltaAndNP(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	for _, kind := range []string{"etc", "NP"} {
		t.Run(kind, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET daily_quests=10,netcafe_points=10 WHERE id=11`)
			var wg sync.WaitGroup
			errs := make(chan error, 24)
			for i := 0; i < 24; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if kind == "etc" {
						errs <- repo.AdjustEtcPoint(11, 1, 1)
					} else {
						errs <- repo.AddCappedNetcafePoints(11, 1, 1000)
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			np, _, daily, _, _ := dailyPointFixtureState(t, db, 11)
			if (kind == "etc" && daily != 34) || (kind == "NP" && np != 34) {
				t.Fatal("concurrent additions were lost", np, daily)
			}
		})
	}
}

func TestDailyPointRepositoryExpiryClaimRace(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	now := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	target := now.Add(23 * time.Hour)
	for i := 0; i < 12; i++ {
		db.MustExec(`UPDATE characters SET daily_time=NULL,netcafe_points=10,bonus_quests=2,daily_quests=3,promo_points=13 WHERE id=11`)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, _, _, err := repo.ReadDailyEtcPoints(11, now)
			errs <- err
		}()
		go func() {
			<-start
			claimed, err := repo.ClaimDailyCafe(11, target, 10, 5, 100)
			if err == nil && !claimed {
				err = errors.New("fresh grant was not claimed")
			}
			errs <- err
		}()
		close(start)
		for j := 0; j < 2; j++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		np, bonus, daily, promo, until := dailyPointFixtureState(t, db, 11)
		if np != 15 || bonus != 10 || daily != 5 || promo != 13 || !until.Time.Equal(target) {
			t.Fatal("expiry erased a concurrently granted allowance", np, bonus, daily, promo, until)
		}
	}
}

func TestDailyPointRepositoryWaitingWritersUseFreshRow(t *testing.T) {
	repo, db := setupDailyPointRepository(t)
	now := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	target := now.Add(23 * time.Hour)
	for _, kind := range []string{"expiry", "delta", "NP"} {
		t.Run(kind, func(t *testing.T) {
			db.MustExec(`UPDATE characters SET daily_time=NULL,netcafe_points=10,bonus_quests=2,daily_quests=3,promo_points=13 WHERE id=11`)
			holder, err := db.Beginx()
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback() //nolint:errcheck
			var id uint32
			if err := holder.QueryRow(`SELECT id FROM characters WHERE id=11 FOR UPDATE`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			type result struct {
				bonus, daily, promo uint32
				err                 error
			}
			done := make(chan result, 1)
			go func() {
				var value result
				switch kind {
				case "expiry":
					value.bonus, value.daily, value.promo, value.err = repo.ReadDailyEtcPoints(11, now)
				case "delta":
					value.err = repo.AdjustEtcPoint(11, 1, -1)
				case "NP":
					value.err = repo.AddCappedNetcafePoints(11, 5, 100)
				}
				done <- value
			}()
			// Observe an actual lock wait under this fixture's unique application
			// name before committing the replacement grant. This is not a sleep-
			// based assumption about which worker reached the database first.
			deadline := time.Now().Add(3 * time.Second)
			for {
				var waiting bool
				err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE application_name=current_setting('application_name') AND wait_event_type='Lock')`).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach the held character row")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := holder.Exec(`UPDATE characters SET daily_time=$1,netcafe_points=15,bonus_quests=10,daily_quests=5 WHERE id=11`, target); err != nil {
				t.Fatal(err)
			}
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			value := <-done
			if value.err != nil {
				t.Fatal(value.err)
			}
			if kind == "expiry" && (value.bonus != 10 || value.daily != 5 || value.promo != 13) {
				t.Fatal("expiry did not return the fresh grant", value)
			}
			np, bonus, daily, promo, until := dailyPointFixtureState(t, db, 11)
			wantNP, wantDaily := int64(15), int64(5)
			if kind == "delta" {
				wantDaily = 4
			}
			if kind == "NP" {
				wantNP = 20
			}
			if np != wantNP || bonus != 10 || daily != wantDaily || promo != 13 || !until.Time.Equal(target) {
				t.Fatal("waiting writer overwrote the fresh row", np, bonus, daily, promo, until)
			}
		})
	}
}
