package channelserver

import "testing"

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
