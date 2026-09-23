package channelserver

import (
	"errors"
	"testing"
	"time"
)

var _ DivaSpecialPugiRepository = (*DivaRepository)(nil)

func TestRepoDivaSpecialPugiIndependentPersistentState(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 1)
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude) * time.Second)
	if _, err := db.Exec(`UPDATE guilds SET pugi_outfit_1=12,pugi_outfit_2=13,pugi_outfit_3=14,pugi_outfits=255 WHERE id=$1`, guild); err != nil {
		t.Fatal(err)
	}
	gr := NewGuildRepository(db)
	stale, err := gr.GetByID(guild)
	if err != nil || stale == nil {
		t.Fatal(stale, err)
	}
	if err = r.changeDivaSpecialPugiAt(char, guild, 1, 9, 3, now); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("forced welcome bypassed actual phase", err)
	}
	for i, outfit := range []uint32{3, 5, 9} {
		for range 2 {
			if err = r.changeDivaSpecialPugiAt(char, guild, uint8(i+1), outfit, 3, welcome); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A stale ordinary save includes zero-valued special clothes in memory.
	// It must update only ordinary fields, preserving the three new choices.
	stale.Comment = "ordinary metadata changed"
	if err = gr.Save(stale); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		g, e := gr.GetByID(guild)
		if e != nil || g == nil {
			t.Fatal(g, e)
		}
		if g.DivaPugiOutfit1 != 3 || g.DivaPugiOutfit2 != 5 || g.DivaPugiOutfit3 != 9 ||
			g.PugiOutfit1 != 12 || g.PugiOutfit2 != 13 || g.PugiOutfit3 != 14 || g.PugiOutfits != 255 {
			t.Fatal(g)
		}
	}
	check()
	end := time.Unix(int64(event.StartTime)+divaPhaseDuration+2*divaWeekDuration, 0)
	if err = r.changeDivaSpecialPugiAt(char, guild, 1, 1, 3, end); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("changed after closing", err)
	}
	check()
	for _, v := range []struct {
		slot   uint8
		outfit uint32
	}{{0, 0}, {4, 0}, {1, 10}, {1, 256}} {
		if err = r.changeDivaSpecialPugiAt(char, guild, v.slot, v.outfit, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
			t.Fatal(v, err)
		}
	}
	for _, mode := range []int{0, 1, 2, 4} {
		if err = r.changeDivaSpecialPugiAt(char, guild, 1, 1, mode, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
			t.Fatal(mode, err)
		}
	}
	check()
}

func TestRepoDivaSpecialPugiLeaderMembershipAndUnlock(t *testing.T) {
	r, db, char, guild, event, now := setupDivaGuildRewardRepoTest(t, 0)
	_, phaseEnd := divaInterceptionWindow(event)
	welcome := phaseEnd.Add(time.Duration(divaInterlude)*time.Second + time.Minute)
	if err := r.changeDivaSpecialPugiAt(char, guild, 1, 9, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("unearned hall", err)
	}
	setDivaGuildRewardTestAreas(t, r, db, char, guild, event.ID, now, 1)
	var user uint32
	if err := db.Get(&user, `SELECT user_id FROM characters WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	member := CreateTestCharacter(t, db, user, "PugiMember")
	if _, err := db.Exec(`INSERT INTO guild_characters(guild_id,character_id,joined_at,order_index) VALUES($1,$2,$3,2)`, guild, member, now); err != nil {
		t.Fatal(err)
	}
	if err := r.changeDivaSpecialPugiAt(member, guild, 1, 9, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("subleader changed clothes", err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET joined_at=NULL WHERE character_id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if err := r.changeDivaSpecialPugiAt(char, guild, 1, 9, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("nonaccepted leader changed clothes", err)
	}
	if _, err := db.Exec(`UPDATE guild_characters SET joined_at=$2 WHERE character_id=$1`, char, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE guilds SET leader_id=$2 WHERE id=$1`, guild, member); err != nil {
		t.Fatal(err)
	}
	if err := r.changeDivaSpecialPugiAt(char, guild, 1, 9, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("stale leader authorized", err)
	}
	// A newly appointed accepted leader may use the earned guild entitlement
	// without a personal interception report. No new reward is granted here.
	for outfit := uint32(0); outfit < 10; outfit++ {
		if err := r.changeDivaSpecialPugiAt(member, guild, 1, outfit, 3, welcome); err != nil {
			t.Fatal(outfit, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if err := r.changeDivaSpecialPugiAt(member, guild, 1, 1, 3, welcome); !errors.Is(err, errDivaSpecialPugiUnavailable) {
		t.Fatal("former member changed clothes", err)
	}
	var saved int
	if err := db.Get(&saved, `SELECT diva_pugi_outfit_1 FROM guilds WHERE id=$1`, guild); err != nil || saved != 9 {
		t.Fatal(saved, err)
	}
}
