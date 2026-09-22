package channelserver

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaTacticsFollowerRepository = (*DivaRepository)(nil)

func setupDivaTacticsFollowerTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32, DivaEvent, time.Time) {
	t.Helper()
	r, db, charID, guildID, event, now := setupDivaInterceptionRepoTest(t)
	if _, err := db.Exec(`UPDATE characters SET gr=1,gcp=10000 WHERE id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	return r, db, charID, guildID, event, now
}

func divaTacticsFollowerTestGP(t *testing.T, db *sqlx.DB, charID uint32, want int) {
	t.Helper()
	var got int
	if err := db.Get(&got, `SELECT gcp FROM characters WHERE id=$1`, charID); err != nil || got != want {
		t.Fatalf("GP=%d want=%d error=%v", got, want, err)
	}
}

func TestRepoDivaTacticsFollowerContractAndChangeCooldown(t *testing.T) {
	r, db, charID, _, event, now := setupDivaTacticsFollowerTest(t)
	choice := DivaTacticsFollowerChoice{NameIndex: 39, Voice: 3, Weapon: 1, Strength: 2}
	state, err := r.setDivaTacticsFollowerAt(charID, event.ID, choice, 2, now)
	if err != nil || state.DivaTacticsFollowerChoice != choice || state.AvailableAt != uint32(now.Unix()+86400) {
		t.Fatal(state, err)
	}
	divaTacticsFollowerTestGP(t, db, charID, 7700)
	if _, err = r.setDivaTacticsFollowerAt(charID, event.ID, choice, 2, now); !errors.Is(err, errDivaTacticsFollowerLocked) {
		t.Fatal("duplicate was not rejected", err)
	}
	divaTacticsFollowerTestGP(t, db, charID, 7700)
	// The native client saves this reduced absolute value after ACK. It is not a
	// second delta, so the existing GP save does not charge the fee twice.
	if err = NewCharacterRepository(db).UpdateGCPAndPact(charID, 7700, 0); err != nil {
		t.Fatal(err)
	}
	divaTacticsFollowerTestGP(t, db, charID, 7700)
	got, err := NewDivaRepository(db).getDivaTacticsFollowerAt(charID, event.ID, 2, now.Add(time.Hour))
	if err != nil || got != state {
		t.Fatal("reconnect lost contract", got, err)
	}
	got, err = r.getDivaTacticsFollowerAt(charID, event.ID, 2, now.Add(24*time.Hour))
	if err != nil || got != state {
		t.Fatal("24-hour change cooldown incorrectly expired the follower", got, err)
	}
	choice.Strength = 0
	if _, err = r.setDivaTacticsFollowerAt(charID, event.ID, choice, 2, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	divaTacticsFollowerTestGP(t, db, charID, 6900)
}

func TestRepoDivaTacticsFollowerConcurrentHire(t *testing.T) {
	r, db, charID, _, event, now := setupDivaTacticsFollowerTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.setDivaTacticsFollowerAt(charID, event.ID, DivaTacticsFollowerChoice{Strength: 1}, 2, now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, locked := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, errDivaTacticsFollowerLocked) {
			locked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || locked != 1 {
		t.Fatal(success, locked)
	}
	divaTacticsFollowerTestGP(t, db, charID, 8700)
}

func TestRepoDivaTacticsFollowerEligibilityAndAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*sqlx.DB, uint32) error
		want   error
	}{
		{"HR", func(db *sqlx.DB, id uint32) error {
			_, err := db.Exec(`UPDATE characters SET gr=0 WHERE id=$1`, id)
			return err
		}, errDivaTacticsFollowerUnavailable},
		{"no guild", func(db *sqlx.DB, id uint32) error {
			_, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, id)
			return err
		}, errDivaTacticsFollowerUnavailable},
		{"insufficient GP", func(db *sqlx.DB, id uint32) error {
			_, err := db.Exec(`UPDATE characters SET gcp=799 WHERE id=$1`, id)
			return err
		}, errDivaTacticsFollowerFunds},
		{"contract storage failure", func(db *sqlx.DB, id uint32) error {
			_, err := db.Exec(`ALTER TABLE diva_tactics_followers ADD CONSTRAINT test_reject_follower CHECK (FALSE) NOT VALID`)
			return err
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db, charID, _, event, now := setupDivaTacticsFollowerTest(t)
			if err := tc.change(db, charID); err != nil {
				t.Fatal(err)
			}
			var before int
			if err := db.Get(&before, `SELECT gcp FROM characters WHERE id=$1`, charID); err != nil {
				t.Fatal(err)
			}
			_, err := r.setDivaTacticsFollowerAt(charID, event.ID, DivaTacticsFollowerChoice{}, 2, now)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal("invalid hire accepted", err)
			}
			divaTacticsFollowerTestGP(t, db, charID, before)
			var count int
			if err := db.Get(&count, `SELECT COUNT(*) FROM diva_tactics_followers`); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if tc.name == "contract storage failure" {
				if _, err := db.Exec(`ALTER TABLE diva_tactics_followers DROP CONSTRAINT test_reject_follower`); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRepoDivaTacticsFollowerGuildAndPhaseChanges(t *testing.T) {
	r, db, charID, _, event, now := setupDivaTacticsFollowerTest(t)
	if _, err := r.setDivaTacticsFollowerAt(charID, event.ID, DivaTacticsFollowerChoice{}, 2, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	if got, err := r.getDivaTacticsFollowerAt(charID, event.ID, 2, now); err != nil || got != (DivaTacticsFollowerState{}) {
		t.Fatal("guildless contract active", got, err)
	}
	CreateTestGuild(t, db, charID, "OtherFollowerGuild")
	if got, err := r.getDivaTacticsFollowerAt(charID, event.ID, 2, now); err != nil || got != (DivaTacticsFollowerState{}) {
		t.Fatal("contract followed guild transfer", got, err)
	}
	if _, err := r.setDivaTacticsFollowerAt(charID, event.ID, DivaTacticsFollowerChoice{}, 2, now); !errors.Is(err, errDivaTacticsFollowerLocked) {
		t.Fatal("guild transfer bypassed 24-hour lock", err)
	}
	for _, when := range []time.Time{time.Unix(int64(event.StartTime), 0), divaLifecycleExpiry(event, 2)} {
		if _, err := r.setDivaTacticsFollowerAt(charID, event.ID, DivaTacticsFollowerChoice{}, 2, when); !errors.Is(err, errDivaTacticsFollowerUnavailable) {
			t.Fatal("wrong phase accepted", when, err)
		}
	}
	if _, err := r.setDivaTacticsFollowerAt(charID, event.ID+1, DivaTacticsFollowerChoice{}, 2, now); !errors.Is(err, errDivaTacticsFollowerUnavailable) {
		t.Fatal("wrong event accepted", err)
	}
	divaTacticsFollowerTestGP(t, db, charID, 9200)
}
