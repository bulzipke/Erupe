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
	quietGuild := CreateTestGuild(t, db, quietID, "QuietGuild")
	exec("INSERT INTO guild_characters(guild_id,character_id) VALUES($1,$2),($1,$3)", guildID, altID, memberID)

	seed, err := os.ReadFile("../migrations/seed/FestaDefaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(seed))

	now := time.Now().UTC().Truncate(time.Second)
	currentStart := now.Add(-8 * 24 * time.Hour)
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
	exec("INSERT INTO festa_registrations(event_id,guild_id,team) VALUES($1,$2,'blue')", current, guildID)
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
	submit(current, guildID, charID, 499, currentStart.Add(7*24*time.Hour))
	submit(current, guildID, memberID, 500, currentStart.Add(7*24*time.Hour))

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
	personal, err := scoped.ListPrizes(charID, "personal")
	if err != nil || len(personal) != 63 || personal[0].Claimed != 1 {
		t.Fatalf("personal table did not preserve the first receipt: %v %v", personal, err)
	}
	for i, item := range personal {
		original := firstPersonal[i]
		wantCut := uint32(i / 3 * 25)
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
	guild := assertGuildGoal(current, charID, 1000) // Leader and alt count as one account.
	assertGuildGoal(current, quietID, 500)
	exec("DELETE FROM guild_characters WHERE character_id=$1", memberID)
	if _, err := repo.EnsureFestaEvent(now); err != nil {
		t.Fatal(err)
	}
	assertGuildGoal(current, charID, 1000) // Membership changes do not move a published goal.

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

	next, err := repo.EnsureFestaEvent(currentStart.Add(21 * 24 * time.Hour))
	if err != nil || next.ID == current {
		t.Fatalf("next round was not initialized: %+v %v", next, err)
	}
	assertGuildGoal(next.ID, charID, 500) // Only one of that round's contributors is still a member.
	assertGuildGoal(current, charID, 1000)
}
