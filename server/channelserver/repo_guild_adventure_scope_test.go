package channelserver

import (
	"fmt"
	"reflect"
	"testing"
)

func TestGuildAdventureScopedRegistration(t *testing.T) {
	repo, db, guild, char := setupGuildRepo(t)
	// guild_adventures.guild_id is integer but guild_characters.guild_id is
	// bigint. Exercise successful INSERT ... SELECT as well as denial paths:
	// an SQL type-inference failure must not masquerade as authorization.
	if err := repo.CreateAdventureForGuild(guild, char, 3, 1000, 22600); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAdventureWithChargeForGuild(guild, char, 4, 200, 1000, 4600); err != nil {
		t.Fatal(err)
	}
	user := CreateTestUser(t, db, "adv_outsider")
	outsider := CreateTestCharacter(t, db, user, "Outsider")
	if err := repo.CreateAdventureForGuild(guild, outsider, 5, 1000, 22600); err == nil {
		t.Fatal("outsider registered an adventure")
	}
	if err := repo.CreateAdventureWithChargeForGuild(guild, outsider, 5, 200, 1000, 4600); err == nil {
		t.Fatal("outsider registered a charged adventure")
	}
	rows, err := repo.ListAdventures(guild)
	if err != nil || len(rows) != 2 {
		t.Fatalf("registrations=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		switch row.Destination {
		case 3:
			if row.Charge != 0 || row.Return-row.Depart != 21600 {
				t.Fatal(row)
			}
		case 4:
			if row.Charge != 200 || row.Return-row.Depart != 3600 {
				t.Fatal(row)
			}
		default:
			t.Fatal(row)
		}
	}
}

func TestGuildAdventureVisibleSlots(t *testing.T) {
	repo, db, guild, hunter := setupGuildRepo(t)
	member := CreateTestCharacter(t, db, CreateTestUser(t, db, "adv_member"), "GuildMember")
	if err := repo.AddMember(guild, member); err != nil {
		t.Fatal(err)
	}
	other := CreateTestCharacter(t, db, CreateTestUser(t, db, "adv_other"), "OtherHunter")
	otherGuild := CreateTestGuild(t, db, other, "OtherGuild")
	const now = 10000
	_, err := db.Exec(`
		INSERT INTO guild_adventures (guild_id, destination, charge, depart, "return", collected_by)
		VALUES ($1, 1, 0, 100, 200, $3),
		       ($1, 2, 0, 200, 300, ''),
		       ($1, 3, 0, 8000, 9000, ''),
		       ($1, 4, 0, 8100, 9100, $3),
		       ($1, 5, 0, 9200, 10800, ''),
		       ($1, 6, 700, $4, 12000, ''),
		       ($1, 7, 0, 11000, 13000, ''),
		       ($2, 8, 0, 9700, 10100, '')
	`, guild, otherGuild, fmt.Sprint(hunter), now)
	if err != nil {
		t.Fatal(err)
	}
	checkSlots := func(charID uint32, at int64, want []uint32) []GuildAdventure {
		t.Helper()
		got, err := repo.ListVisibleAdventures(charID, at)
		if err != nil {
			t.Fatal(err)
		}
		destinations := make([]uint32, 0, len(got))
		for _, adventure := range got {
			destinations = append(destinations, adventure.Destination)
		}
		if !reflect.DeepEqual(destinations, want) {
			t.Fatalf("visible adventures at %d = %v, want %v", at, destinations, want)
		}
		return got
	}
	slots := checkSlots(hunter, now, []uint32{6, 3})
	if slots[0].Charge != 700 {
		t.Fatalf("initial support = %d, want 700", slots[0].Charge)
	}
	if err := repo.ChargeAdventureForGuild(guild, slots[0].ID, hunter, 100); err != nil {
		t.Fatal(err)
	}
	if got := checkSlots(hunter, now, []uint32{6, 3})[0].Charge; got != 800 {
		t.Fatalf("support after reloading = %d, want 800", got)
	}
	if err := repo.CollectAdventureForGuild(guild, slots[1].ID, hunter); err != nil {
		t.Fatal(err)
	}
	checkSlots(hunter, now, []uint32{6, 2})
	checkSlots(member, now, []uint32{6, 4})
	checkSlots(hunter, 12000, []uint32{7, 6})
	checkSlots(other, now, []uint32{8})
}
