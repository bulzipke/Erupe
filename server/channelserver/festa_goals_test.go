package channelserver

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"
)

func TestFestaGoalSnapshotAndClaims(t *testing.T) {
	repo, db, charID, guildID := setupFestaRepo(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}

	var userID uint32
	if err := db.Get(&userID, "SELECT user_id FROM characters WHERE id=$1", charID); err != nil {
		t.Fatal(err)
	}
	altID := CreateTestCharacter(t, db, userID, "FestaAlt")
	memberID := CreateTestCharacter(t, db, CreateTestUser(t, db, "festa_member"), "FestaMember")
	leftID := CreateTestCharacter(t, db, CreateTestUser(t, db, "festa_left"), "FestaLeft")
	quietID := CreateTestCharacter(t, db, CreateTestUser(t, db, "festa_quiet"), "FestaQuiet")
	newID := CreateTestCharacter(t, db, CreateTestUser(t, db, "festa_new"), "FestaNew")
	lateID := CreateTestCharacter(t, db, CreateTestUser(t, db, "festa_late"), "FestaLate")
	quietGuild := CreateTestGuild(t, db, quietID, "QuietGuild")
	exec("INSERT INTO guild_characters(guild_id,character_id) VALUES($1,$2),($1,$3)", guildID, altID, memberID)

	seed, err := os.ReadFile("../migrations/seed/FestaDefaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(seed))
	exec("INSERT INTO festa_trials(objective,goal_id,times_req,reward) VALUES(5,63303,0,99),(5,53226,0,200)")

	now := time.Now().UTC().Truncate(time.Second)
	currentStart := now.Add(-8 * 24 * time.Hour)
	gathering := currentStart.Add(7 * 24 * time.Hour)
	exec("UPDATE guild_characters SET joined_at=$1", gathering.Add(-time.Second))
	exec("INSERT INTO guild_characters(guild_id,character_id,joined_at) VALUES($1,$2,$3),($1,$4,$5)",
		guildID, newID, gathering.Add(-time.Second), lateID, gathering)
	previousStart := currentStart.Add(-21 * 24 * time.Hour)
	var previous, current uint32
	for _, round := range []struct {
		id    *uint32
		start time.Time
	}{{&previous, previousStart}, {&current, currentStart}} {
		if err := db.Get(round.id, "INSERT INTO events(event_type,start_time,festa_continuous) VALUES('festa',$1,true) RETURNING id", round.start); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO festa_event_lifecycle(event_id) VALUES($1)", current)
	exec("INSERT INTO festa_registrations(event_id,guild_id,team) VALUES($1,$2,'blue'),($1,$3,'red')", current, guildID, quietGuild)
	exec("INSERT INTO festa_goal_rounds(event_id) VALUES($1)", previous) // Existing rounds keep their 500-soul rules.
	submit := func(event, guild, char uint32, souls int, at time.Time) {
		t.Helper()
		exec("INSERT INTO festa_submissions(event_id,guild_id,character_id,trial_type,souls,timestamp) VALUES($1,$2,$3,1,$4,$5)",
			event, guild, char, souls, at)
	}
	previousGathering := previousStart.Add(7 * 24 * time.Hour)
	for _, id := range []uint32{charID, altID, memberID, leftID} {
		submit(previous, guildID, id, 99, previousGathering)
	}
	submit(previous, quietGuild, quietID, 0, previousGathering)
	submit(previous, quietGuild, quietID, 99, previousStart.Add(14*24*time.Hour))
	submit(current, guildID, charID, 2999, gathering)
	submit(current, guildID, memberID, 3000, gathering)
	submit(current, quietGuild, quietID, 3000, gathering)

	firstPersonal, err := repo.ForFestaEvent(current).ListPrizes(charID, "personal")
	if err != nil || len(firstPersonal) == 0 || firstPersonal[len(firstPersonal)-1].SoulsReq != 10000 {
		t.Fatalf("uninitialized table did not use the legacy cut: %v %v", firstPersonal, err)
	}
	exec("INSERT INTO festa_prizes_accepted(event_id,character_id,prize_id) VALUES($1,$2,$3)",
		current, charID, firstPersonal[0].ID)
	if event, err := repo.EnsureFestaEvent(now); err != nil || event.ID != current {
		t.Fatalf("current gathering was not initialized: %+v %v", event, err)
	}
	scoped := repo.ForFestaEvent(current)
	legacy := repo.ForFestaEvent(previous)
	legacyList, err := legacy.ListPrizes(charID, "personal")
	if err != nil || len(legacyList) != 63 || legacyList[len(legacyList)-1].SoulsReq != 500 {
		t.Fatalf("legacy round cut changed: %v %v", legacyList, err)
	}
	for _, round := range []struct {
		repo FestaRepo
		want map[uint32]uint16
	}{
		{legacy, map[uint32]uint16{53034: 400}},
		{scoped, map[uint32]uint16{53034: 400, 53226: 200, 63303: 99}},
	} {
		trials, err := round.repo.GetTrialsWithMonopoly(false)
		if err != nil {
			t.Fatal(err)
		}
		for _, trial := range trials {
			if trial.Objective != 5 {
				continue
			}
			if round.want[trial.GoalID] != trial.Reward || trial.Unk != 2 {
				t.Errorf("unexpected daily trial: %+v", trial)
			}
			delete(round.want, trial.GoalID)
		}
		if len(round.want) != 0 {
			t.Errorf("missing daily trials: %v", round.want)
		}
	}
	personal, err := scoped.ListPrizes(charID, "personal")
	if err != nil || len(personal) != 63 || personal[0].Claimed != 1 {
		t.Fatalf("personal table did not preserve the first receipt: %v %v", personal, err)
	}
	for i, item := range personal {
		original := firstPersonal[i]
		wantCut := uint32(i / 3 * 150)
		if wantCut == 0 {
			wantCut = 1
		}
		if item.SoulsReq != wantCut || item.ID != original.ID || item.ItemID != original.ItemID || item.NumItem != original.NumItem {
			t.Errorf("personal row %d: got %+v, original %+v, want %d souls", i, item, original, wantCut)
		}
	}
	assertGuildGoal := func(event, char, want uint32) []Prize {
		t.Helper()
		prizes, err := repo.ForFestaEvent(event).ListPrizes(char, "guild")
		if err != nil || len(prizes) != 57 || prizes[len(prizes)-1].SoulsReq != want {
			t.Fatalf("guild goal for event %d character %d: %v %v", event, char, prizes, err)
		}
		return prizes
	}
	guild := assertGuildGoal(current, charID, 6000) // Leader and alt count as one account.
	assertGuildGoal(current, quietID, 3000)
	exec("DELETE FROM guild_characters WHERE character_id=$1", memberID)
	if _, err := repo.EnsureFestaEvent(now); err != nil {
		t.Fatal(err)
	}
	assertGuildGoal(current, charID, 6000) // Membership changes do not move a published goal.

	lastPersonal, lastGuild := personal[len(personal)-3].ID, guild[len(guild)-6].ID
	for kind, id := range map[string]uint32{"personal": lastPersonal, "guild": lastGuild} {
		if err := scoped.ClaimGoalPrize(id, charID, kind); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s goal was accepted one soul early: %v", kind, err)
		}
	}
	if err := scoped.SubmitSouls(charID, guildID, []uint16{1}); err != nil {
		t.Fatal(err)
	}
	if err := scoped.ClaimGoalPrize(lastGuild, charID, "personal"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("guild goal was accepted through the personal menu: %v", err)
	}
	for kind, id := range map[string]uint32{"personal": lastPersonal, "guild": lastGuild} {
		if err := scoped.ClaimGoalPrize(id, charID, kind); err != nil {
			t.Errorf("%s goal was refused at the displayed cut: %v", kind, err)
		}
	}
	if err := scoped.SubmitSouls(altID, quietGuild, []uint16{1}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("contribution to another guild: %v", err)
	}
	submit(current, quietGuild, newID, 3000, gathering) // Souls credited to a former guild.
	for _, id := range []uint32{altID, newID, lateID} {
		if id != newID {
			submit(current, guildID, id, 3000, gathering)
		}
		if err := scoped.ClaimGoalPrize(lastGuild, id, "guild"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("ineligible character %d claimed the guild reward: %v", id, err)
		}
		if err := scoped.ClaimGoalPrize(lastPersonal, id, "personal"); err != nil {
			t.Errorf("personal reward refused for character %d: %v", id, err)
		}
	}
	if prizes, err := scoped.ListPrizes(lateID, "guild"); len(prizes) != 0 || err != nil {
		t.Fatalf("late member received guild offers: %v %v", prizes, err)
	}
	submit(current, guildID, newID, 2999, gathering)
	if err := scoped.ClaimGoalPrize(lastGuild, newID, "guild"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("account outside the snapshot claimed before meeting its share: %v", err)
	}
	if err := scoped.SubmitSouls(newID, guildID, []uint16{1}); err != nil {
		t.Fatal(err)
	}
	if err := scoped.ClaimGoalPrize(lastGuild, newID, "guild"); err != nil {
		t.Fatalf("account outside the snapshot could not claim after pledging its share: %v", err)
	}
	next, err := repo.EnsureFestaEvent(currentStart.Add(21 * 24 * time.Hour))
	if err != nil || next.ID == current {
		t.Fatalf("next round was not initialized: %+v %v", next, err)
	}
	assertGuildGoal(next.ID, charID, 9000) // Departed member is excluded and the two alts count once.
	assertGuildGoal(next.ID, lateID, 9000) // The late joiner is eligible in the following round.
	assertGuildGoal(current, charID, 6000)
}
