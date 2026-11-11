package channelserver

import (
	"testing"
	"time"
)

func TestGuildContentMutationsRejectAnotherGuild(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	userA := CreateTestUser(t, db, "content_scope_user_a")
	charA := CreateTestCharacter(t, db, userA, "ContentScopeA")
	guildA := CreateTestGuild(t, db, charA, "ContentScopeGuildA")
	userB := CreateTestUser(t, db, "content_scope_user_b")
	charB := CreateTestCharacter(t, db, userB, "ContentScopeB")
	guildB := CreateTestGuild(t, db, charB, "ContentScopeGuildB")
	repo := NewGuildRepository(db)

	if err := repo.CreateAlliance("ContentScopeAlliance", guildB); err != nil {
		t.Fatalf("create alliance: %v", err)
	}
	var allianceID uint32
	if err := db.QueryRow(`UPDATE guild_alliances SET sub1_id=$1 WHERE parent_id=$2 RETURNING id`, guildA, guildB).Scan(&allianceID); err != nil {
		t.Fatalf("prepare alliance: %v", err)
	}
	if err := repo.CreateAllianceForMember("ForgedAlliance", guildB, charA); err == nil {
		t.Fatal("another guild's member created an alliance")
	}
	if err := repo.LeaveAlliance(allianceID, guildB, charA); err == nil {
		t.Fatal("character left another guild from an alliance")
	}
	if err := repo.KickGuildFromAlliance(allianceID, guildA, charA); err == nil {
		t.Fatal("non-parent leader kicked a guild from an alliance")
	}

	mealID, err := repo.CreateMeal(guildB, 10, 1, time.Now())
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	if err := repo.UpdateMealForGuild(guildA, charA, mealID, 20, 2, time.Now()); err == nil {
		t.Fatal("updated another guild's meal")
	}
	if _, err := repo.CreateMealForGuild(guildB, charA, 30, 3, time.Now()); err == nil {
		t.Fatal("created a meal for another guild")
	}

	if err := repo.CreateAdventure(guildB, 1, 100, 200); err != nil {
		t.Fatalf("create adventure: %v", err)
	}
	var adventureID uint32
	if err := db.QueryRow(`SELECT id FROM guild_adventures WHERE guild_id=$1 ORDER BY id DESC LIMIT 1`, guildB).Scan(&adventureID); err != nil {
		t.Fatalf("load adventure: %v", err)
	}
	if err := repo.CollectAdventureForGuild(guildA, adventureID, charA); err == nil {
		t.Fatal("collected another guild's adventure")
	}
	if err := repo.ChargeAdventureForGuild(guildA, adventureID, charA, 50); err == nil {
		t.Fatal("charged another guild's adventure")
	}
	if err := repo.CreateAdventureForGuild(guildB, charA, 2, 100, 200); err == nil {
		t.Fatal("created an adventure for another guild")
	}
	if err := repo.CreateAdventureWithChargeForGuild(guildB, charA, 2, 50, 100, 200); err == nil {
		t.Fatal("created a charged adventure for another guild")
	}

	if err := repo.CreateHunt(guildB, charB, 1, 1, []byte{}, ""); err != nil {
		t.Fatalf("create hunt: %v", err)
	}
	var huntID uint32
	if err := db.QueryRow(`SELECT id FROM guild_hunts WHERE guild_id=$1 ORDER BY id DESC LIMIT 1`, guildB).Scan(&huntID); err != nil {
		t.Fatalf("load hunt: %v", err)
	}
	if err := repo.AcquireHuntForGuild(guildA, huntID, charA); err == nil {
		t.Fatal("acquired another guild's hunt")
	}
	if err := repo.RegisterHuntReportForGuild(guildA, huntID, charA); err == nil {
		t.Fatal("registered another guild's hunt")
	}
	if err := repo.CollectHuntForGuild(guildA, huntID, charA); err == nil {
		t.Fatal("collected another guild's hunt")
	}
	if err := repo.ClaimHuntRewardForGuild(guildA, huntID, charA); err == nil {
		t.Fatal("claimed another guild's hunt")
	}
	if err := repo.CreateHuntForGuild(guildB, charA, 2, 1, []byte{}, ""); err == nil {
		t.Fatal("created a hunt for another guild")
	}

	if err := repo.AddWeeklyBonusUsersForMember(guildB, charA, 1); err == nil {
		t.Fatal("updated another guild's weekly bonus")
	}
}

func TestGuildContentMutationsAcceptOwnGuild(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	userID := CreateTestUser(t, db, "guild_content_owner")
	charID := CreateTestCharacter(t, db, userID, "ContentOwner")
	guildID := CreateTestGuild(t, db, charID, "GuildContent")
	repo := NewGuildRepository(db)

	if err := repo.CreateAllianceForMember("ContentAlliance", guildID, charID); err != nil {
		t.Fatalf("create alliance: %v", err)
	}
	if err := repo.CreateAllianceForMember("DuplicateAlliance", guildID, charID); err == nil {
		t.Fatal("created a second alliance for the same guild")
	}
	guild, err := repo.GetByID(guildID)
	if err != nil || guild == nil || guild.AllianceID == 0 {
		t.Fatalf("load guild with alliance: %+v, %v", guild, err)
	}
	alliance, err := repo.GetAllianceByID(guild.AllianceID)
	if err != nil || alliance == nil || alliance.ParentGuildID != guildID || alliance.SubGuild1ID != 0 || alliance.SubGuild2ID != 0 {
		t.Fatalf("load alliance: %+v, %v", alliance, err)
	}

	if err := repo.CreateHuntForGuild(guildID, charID, 1, 2, []byte{1}, ""); err != nil {
		t.Fatalf("create hunt: %v", err)
	}
	hunt, err := repo.GetPendingHunt(charID)
	if err != nil || hunt == nil || hunt.HostID != charID {
		t.Fatalf("load pending hunt: %+v, %v", hunt, err)
	}
	if err := repo.AcquireHuntForGuild(guildID, hunt.HuntID, charID); err != nil {
		t.Fatalf("acquire hunt: %v", err)
	}
	if err := repo.RegisterHuntReportForGuild(guildID, hunt.HuntID, charID); err != nil {
		t.Fatalf("register hunt report: %v", err)
	}
	if err := repo.CollectHuntForGuild(guildID, hunt.HuntID, charID); err != nil {
		t.Fatalf("collect hunt: %v", err)
	}
	if err := repo.ClaimHuntRewardForGuild(guildID, hunt.HuntID, charID); err != nil {
		t.Fatalf("claim hunt reward: %v", err)
	}
	hunts, err := repo.ListGuildHunts(guildID, charID)
	if err != nil || len(hunts) != 1 || !hunts[0].Collected || !hunts[0].Claimed {
		t.Fatalf("load collected hunt: %+v, %v", hunts, err)
	}
}
