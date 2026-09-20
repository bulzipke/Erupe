package channelserver

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaInterceptionRepository = (*DivaRepository)(nil)

const divaRunKeyOne = "12345678-1234-4567-89ab-123456789abc"
const divaRunKeyTwo = "12345678-1234-4567-89ab-123456789abd"

func TestDivaInterceptionRunKeyValidation(t *testing.T) {
	for _, key := range []string{"", "1", "00000000-0000-0000-0000-000000000000", "12345678_1234-4567-89ab-123456789abc", "gg345678-1234-4567-89ab-123456789abc"} {
		if validDivaInterceptionRunKey(key) {
			t.Fatalf("invalid key accepted: %q", key)
		}
	}
	if !validDivaInterceptionRunKey(divaRunKeyOne) {
		t.Fatal("valid UUID rejected")
	}
}

func setupDivaInterceptionRepoTest(t *testing.T) (*DivaRepository, *sqlx.DB, uint32, uint32, DivaEvent, time.Time) {
	t.Helper()
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "interception_test")
	charID := CreateTestCharacter(t, db, user, "Interception")
	guildID := CreateTestGuild(t, db, charID, "InterceptionGuild")
	now := divaTestTime(21, 12, 0)
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	return r, db, charID, guildID, event, now
}

func TestRepoDivaInterceptionProgressAndIdempotence(t *testing.T) {
	r, db, charID, guildID, event, now := setupDivaInterceptionRepoTest(t)
	if _, err := db.Exec(`UPDATE characters SET gr=7 WHERE id=$1;`, charID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET interception_points='{"58043":999999}' WHERE character_id=$1`, charID); err != nil {
		t.Fatal(err)
	}
	started := now.Add(-time.Minute).Add(123456789 * time.Nanosecond)
	for i := 0; i < 2; i++ {
		if err := r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1500, guildID, divaRunKeyOne, started, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddDivaInterceptionPoints(charID, event.ID, 58044, 300, guildID, divaRunKeyTwo, started, now); err != nil {
		t.Fatal(err)
	}
	progress, err := r.GetDivaInterceptionProgress(charID, event.ID)
	if err != nil || !progress.Enabled || progress.GR != 7 || progress.Points != 1800 || len(progress.QuestPoints) != 2 || progress.QuestPoints[58043] != 1500 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	legacy, err := r.GetCharacterInterceptionPoints(charID)
	if err != nil || legacy["58043"] != 999999 {
		t.Fatalf("legacy was modified: %+v %v", legacy, err)
	}
	if err = r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1501, guildID, divaRunKeyOne, started, now); !errors.Is(err, ErrDivaInterceptionInvalid) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	if _, err = db.Exec("DELETE FROM guild_characters WHERE character_id=$1", charID); err != nil {
		t.Fatal(err)
	}
	if err = r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1500, guildID, divaRunKeyOne, started, now); err != nil {
		t.Fatalf("accepted retry after leaving guild: %v", err)
	}
	_, end := divaInterceptionWindow(event)
	if err = r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1500, guildID, divaRunKeyOne, started, end.Add(time.Second)); err != nil {
		t.Fatalf("accepted retry after phase end: %v", err)
	}
	if err = r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1501, guildID, divaRunKeyOne, started, end.Add(time.Second)); !errors.Is(err, ErrDivaInterceptionInvalid) {
		t.Fatalf("changed retry after phase end: %v", err)
	}
	progress, err = r.GetDivaInterceptionProgress(charID, event.ID)
	if err != nil || progress.Points != 1800 {
		t.Fatalf("leaving guild erased personal progress: %+v %v", progress, err)
	}
}

func TestRepoDivaInterceptionRejectsWrongPhaseAndMembership(t *testing.T) {
	r, db, charID, guildID, event, now := setupDivaInterceptionRepoTest(t)
	start, end := divaInterceptionWindow(event)
	for _, tt := range []struct {
		name            string
		started, report time.Time
	}{
		{"before phase", start.Add(-time.Second), now},
		{"report at end", now, end},
		{"report before departure", now, now.Add(-time.Second)},
		{"departure at end", end, end},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1, guildID, divaRunKeyOne, tt.started, tt.report); !errors.Is(err, ErrDivaInterceptionInvalid) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
	if err := r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1, guildID+1, divaRunKeyOne, start, now); !errors.Is(err, ErrDivaInterceptionMembership) {
		t.Fatalf("wrong guild accepted: %v", err)
	}
	if _, err := db.Exec("DELETE FROM guild_characters WHERE character_id=$1", charID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO guild_applications(guild_id,character_id,actor_id,application_type) VALUES($1,$2,$2,'applied')", guildID, charID); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1, guildID, divaRunKeyOne, start, now); !errors.Is(err, ErrDivaInterceptionMembership) {
		t.Fatalf("applicant accepted: %v", err)
	}
	progress, err := r.GetDivaInterceptionProgress(charID, event.ID)
	if err != nil || progress.Points != 0 {
		t.Fatalf("rejected points persisted: %+v %v", progress, err)
	}
}

func TestRepoDivaInterceptionLegacyAndRoundIsolation(t *testing.T) {
	r, db, charID, guildID, event, now := setupDivaInterceptionRepoTest(t)
	if _, err := db.Exec("INSERT INTO diva_interception_legacy_events(event_id) VALUES($1)", event.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDivaInterceptionPoints(charID, event.ID, 58043, 1, guildID, divaRunKeyOne, now, now); !errors.Is(err, ErrDivaInterceptionLegacy) {
		t.Fatalf("legacy enabled: %v", err)
	}
	progress, err := r.GetDivaInterceptionProgress(charID, event.ID)
	if err != nil || progress.Enabled || progress.Points != 0 {
		t.Fatalf("legacy progress=%+v %v", progress, err)
	}
	later := divaLifecycleExpiry(event, 2)
	next, err := r.EnsureDivaEvent(later, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.AddDivaInterceptionPoints(charID, next.ID, 58043, 123, guildID, divaRunKeyOne, later, later); err != nil {
		t.Fatal(err)
	}
	progress, err = r.GetDivaInterceptionProgress(charID, next.ID)
	if err != nil || !progress.Enabled || progress.Points != 123 {
		t.Fatalf("new round progress=%+v %v", progress, err)
	}
	if err = r.AddDivaInterceptionPoints(charID, next.ID, 58043, 999, guildID, divaRunKeyTwo, now, later); !errors.Is(err, ErrDivaInterceptionInvalid) {
		t.Fatalf("old departure leaked into new round: %v", err)
	}
}

func TestRepoDivaInterceptionConcurrentDuplicate(t *testing.T) {
	r, _, charID, guildID, event, now := setupDivaInterceptionRepoTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.AddDivaInterceptionPoints(charID, event.ID, 58043, 4294967295, guildID, divaRunKeyOne, now, now)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	progress, err := r.GetDivaInterceptionProgress(charID, event.ID)
	if err != nil || progress.Points != 4294967295 {
		t.Fatalf("duplicate or uint32 overflow: %+v %v", progress, err)
	}
}
