package channelserver

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var _ DivaBattleSongRepository = (*DivaRepository)(nil)

func TestRepoDivaBattleSongLifecycleAndConsumption(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "battle_song")
	char := CreateTestCharacter(t, db, user, "BattleSong")
	now := divaTestTime(21, 12, 0)
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	prayer := time.Unix(int64(event.StartTime)+60, 0)
	if _, err = db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
		VALUES($1,$2,$3,1,60,0,$4)`, char, event.ID, divaNoon(prayer), prayer); err != nil {
		t.Fatal(err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, now); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("below threshold activated", err)
	}
	if err = r.AddPoints(char, event.ID, 500000, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	states := make(chan DivaBattleSongState, 2)
	errorsCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := r.useDivaBattleSongAt(char, event.ID, now)
			states <- state
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(states)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first DivaBattleSongState
	for state := range states {
		if state.Used != 1 || state.StartedAt != uint32(now.Unix()) || state.ActivationID == 0 || len(state.Effects) == 0 {
			t.Fatalf("bad state %+v", state)
		}
		if first.ActivationID != 0 && first.ActivationID != state.ActivationID {
			t.Fatal("duplicate consumed")
		}
		first = state
	}
	effect := first.Effects[0].ID
	run := divaBattleSongRun{Key: divaRunKeyOne, QuestID: 123, EventID: event.ID, ActivationID: first.ActivationID, StartedAt: now.Add(time.Minute)}
	for i := 0; i < 2; i++ {
		if err = r.ConsumeDivaBattleSongEffects(char, run, []uint16{effect}, now.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := r.GetDivaBattleSong(char, event.ID)
	if err != nil || loaded.Used != 1 || loaded.ActivationID != first.ActivationID || loaded.Effects[0].Used != 1 {
		t.Fatalf("lost receipt %+v %v", loaded, err)
	}
	if err = r.ConsumeDivaBattleSongEffects(char, run, []uint16{25}, now.Add(2*time.Minute)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("changed replay accepted", err)
	}
	run.Key = divaRunKeyTwo
	// Completing after the hour is allowed when departure was during it.
	if err = r.ConsumeDivaBattleSongEffects(char, run, []uint16{effect}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	loaded, err = r.GetDivaBattleSong(char, event.ID)
	if err != nil || loaded.Effects[0].Used != 2 {
		t.Fatal(loaded, err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, now.Add(time.Hour)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("exhausted activated", err)
	}
	if err = r.AddPoints(char, event.ID, 500000, 0); err != nil {
		t.Fatal(err)
	}
	second, err := r.useDivaBattleSongAt(char, event.ID, now.Add(time.Hour))
	if err != nil || second.Used != 2 || second.ActivationID == first.ActivationID || second.Effects[0].Used != 0 {
		t.Fatal(second, err)
	}
	run.Key = "12345678-1234-4567-89ab-123456789abe"
	if err = r.ConsumeDivaBattleSongEffects(char, run, []uint16{effect}, now.Add(2*time.Hour)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("stale activation accepted", err)
	}
	run.ActivationID = second.ActivationID
	if err = r.ConsumeDivaBattleSongEffects(char, run, []uint16{effect}, now.Add(2*time.Hour)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("departure predating activation accepted", err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, now.Add(2*time.Hour)); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("third use accepted", err)
	}
}

func TestRepoDivaBattleSongCannotFabricateParticipation(t *testing.T) {
	r, db := setupDivaRepo(t)
	user := CreateTestUser(t, db, "battle_song_missing")
	char := CreateTestCharacter(t, db, user, "MissingSong")
	now := divaTestTime(21, 12, 0)
	event, err := r.EnsureDivaEvent(now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.AddPoints(char, event.ID, 100000000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, now); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("forced battle without journal accepted", err)
	}
	prayer := time.Unix(int64(event.StartTime)+60, 0)
	if _, err = db.Exec(`INSERT INTO diva_song_records(char_id,event_id,day_start,bead_index,quest_points,bonus_points,submitted_at)
		VALUES($1,$2,$3,0,60,0,$4)`, char, event.ID, divaNoon(prayer), prayer); err != nil {
		t.Fatal(err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, now); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("no selected bead accepted", err)
	}
	if _, err = db.Exec(`UPDATE diva_song_records SET bead_index=1 WHERE char_id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if _, err = r.useDivaBattleSongAt(char, event.ID, prayer); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("prayer phase accepted", err)
	}
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+divaWeekDuration, 0)
	if _, err = r.useDivaBattleSongAt(char, event.ID, end); !errors.Is(err, errDivaBattleSongUnavailable) {
		t.Fatal("battle end accepted", err)
	}
	state, err := r.GetDivaBattleSong(char, event.ID)
	if err != nil || state.Used != 0 || state.ActivationID != 0 {
		t.Fatal(state, err)
	}
}
