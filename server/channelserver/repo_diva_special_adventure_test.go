package channelserver

import (
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

var _ DivaSpecialAdventureRepository = (*DivaRepository)(nil)

func divaSpecialAdventureTestRows(t *testing.T, db *sqlx.DB, guild uint32) []*GuildAdventure {
	t.Helper()
	rows, err := NewGuildRepository(db).ListAdventures(guild)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestRepoDivaSpecialAdventurePeriodAndDuration(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 1)
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude) * time.Second)
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	for _, at := range []time.Time{now, welcome.Add(-time.Microsecond), end, end.Add(time.Second)} {
		if err := r.registerDivaSpecialAdventureAt(char, 3, 200, 3, at); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
			t.Fatalf("registered outside actual welcome period at %v: %v", at, err)
		}
	}
	if rows := divaSpecialAdventureTestRows(t, db, guild); len(rows) != 0 {
		t.Fatal(rows)
	}
	for i, at := range []time.Time{welcome, end.Add(-time.Microsecond)} {
		// The final valid departure can return after the hall closes. Do not
		// cancel it or shorten its one-hour duration at the closing boundary.
		if err := r.registerDivaSpecialAdventureAt(char, uint32(i+1), uint32(200+i), 3, at); err != nil {
			t.Fatal(err)
		}
	}
	rows := divaSpecialAdventureTestRows(t, db, guild)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.Return-row.Depart != uint32(time.Hour/time.Second) || row.CollectedBy != "" {
			t.Fatal(row)
		}
		if row.Destination == 1 && (row.Charge != 200 || row.Depart != uint32(welcome.Unix())) {
			t.Fatal(row)
		}
		if row.Destination == 2 && (row.Charge != 201 || row.Depart != uint32(end.Unix()-1) || row.Return <= uint32(end.Unix())) {
			t.Fatal(row)
		}
	}
	// Existing normal registration remains a six-hour expedition and does
	// not require the Diva period. Existing accepted expeditions remain
	// collectable by their guild even after the special hall has closed.
	gr := NewGuildRepository(db)
	if err := gr.CreateAdventureForGuild(guild, char, 9, end.Unix(), end.Add(6*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := gr.CollectAdventureForGuild(guild, rows[0].ID, char); err != nil {
		t.Fatal(err)
	}
	rows = divaSpecialAdventureTestRows(t, db, guild)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.Destination == 9 && (row.Return-row.Depart != uint32(6*time.Hour/time.Second) || row.Charge != 0) {
			t.Fatal(row)
		}
	}
}

func TestRepoDivaSpecialAdventureCurrentMembersAndEarnedAccess(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 0)
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude)*time.Second + time.Minute)
	if err := r.registerDivaSpecialAdventureAt(char, 3, 200, 3, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
		t.Fatal("unearned hall", err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, 1)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	member := CreateTestCharacter(t, db, user, "Adventurer")
	if _, err := db.Exec(`INSERT INTO guild_characters(guild_id,character_id,joined_at,order_index) VALUES($1,$2,$3,2)`, guild, member, now); err != nil {
		t.Fatal(err)
	}
	// The hall is a guild-wide privilege, not a leader or individual-score
	// reward. A newly joined accepted member does not need an old report.
	if err := r.registerDivaSpecialAdventureAt(member, 7, 0, 3, welcome); err != nil {
		t.Fatal("accepted member with no personal report", err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET joined_at=NULL WHERE character_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if err := r.registerDivaSpecialAdventureAt(member, 7, 0, 3, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
		t.Fatal("unaccepted member", err)
	}
	if _, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if err := r.registerDivaSpecialAdventureAt(member, 7, 0, 3, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
		t.Fatal("former member", err)
	}
	otherGuild := CreateTestGuild(t, db, member, "OtherGuild")
	if err := r.registerDivaSpecialAdventureAt(member, 7, 0, 3, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
		t.Fatal("transferred member reused old hall", err)
	}
	if rows := divaSpecialAdventureTestRows(t, db, otherGuild); len(rows) != 0 {
		t.Fatal(rows)
	}
	rows := divaSpecialAdventureTestRows(t, db, guild)
	if len(rows) != 1 || rows[0].Destination != 7 || rows[0].Charge != 0 {
		t.Fatal(rows)
	}
}

func TestRepoDivaSpecialAdventureFailureDoesNotRegister(t *testing.T) {
	r, db, char, guild, event, _ := setupDivaGuildRewardRepoTest(t, 1)
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude) * time.Second)
	for _, mode := range []int{0, 1, 2, 4} {
		if err := r.registerDivaSpecialAdventureAt(char, 1, 0, mode, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
			t.Fatal(mode, err)
		}
	}
	for _, absent := range []uint32{0, 0x7fffffff} {
		if err := r.registerDivaSpecialAdventureAt(absent, 1, 0, 3, welcome); !errors.Is(err, errDivaSpecialAdventureUnavailable) {
			t.Fatal(absent, err)
		}
	}
	// Storage failure must abort the entire registration, without wrapping
	// an invalid uint32 into the signed database charge field.
	if err := r.registerDivaSpecialAdventureAt(char, 1, ^uint32(0), 3, welcome); err == nil {
		t.Fatal("out-of-storage-range charge was silently accepted")
	}
	if rows := divaSpecialAdventureTestRows(t, db, guild); len(rows) != 0 {
		t.Fatal(rows)
	}
	if err := r.registerDivaSpecialAdventureAt(char, 1, 0, 3, welcome); err != nil {
		t.Fatal("valid retry after rollback", err)
	}
	if rows := divaSpecialAdventureTestRows(t, db, guild); len(rows) != 1 {
		t.Fatal(rows)
	}
}
