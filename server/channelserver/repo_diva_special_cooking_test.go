package channelserver

import (
	"testing"
	"time"
)

func TestRepoDivaSpecialCookingSharedStorageAndOwnership(t *testing.T) {
	cfg := DefaultTestDBConfig()
	if (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") || cfg.Port != "5433" || cfg.DBName != "erupe_test" {
		t.Fatal("special cooking tests require isolated localhost:5433/erupe_test before schema reset")
	}
	repo, db, guild, leader := setupGuildRepo(t)
	user := CreateTestUser(t, db, "diva_cook_member")
	member := CreateTestCharacter(t, db, user, "CookMember")
	if _, err := db.Exec(`INSERT INTO guild_characters(guild_id,character_id,order_index) VALUES($1,$2,2)`, guild, member); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	id, err := repo.CreateMealForGuild(guild, leader, 5, 1, created)
	if err != nil || id == 0 {
		t.Fatalf("accepted member could not create a meal: %d %v", id, err)
	}
	meals, err := repo.ListMeals(guild)
	if err != nil || len(meals) != 1 || meals[0].ID != id || meals[0].MealID != 5 || meals[0].Level != 1 || !meals[0].CreatedAt.Equal(created) {
		t.Fatalf("shared meal changed: %+v %v", meals, err)
	}
	replaced := created.Add(10 * time.Minute)
	if err = repo.UpdateMealForGuild(guild, member, id, 6, 3, replaced); err != nil {
		t.Fatalf("accepted member could not replace shared meal: %v", err)
	}
	meals, err = repo.ListMeals(guild)
	if err != nil || len(meals) != 1 || meals[0].ID != id || meals[0].MealID != 6 || meals[0].Level != 3 || !meals[0].CreatedAt.Equal(replaced) {
		t.Fatalf("replacement changed: %+v %v", meals, err)
	}
	if _, err = db.Exec(`DELETE FROM guild_characters WHERE character_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateMealForGuild(guild, member, 7, 3, replaced); err == nil {
		t.Fatal("departed member created guild meal")
	}
	if err = repo.UpdateMealForGuild(guild, member, id, 7, 3, replaced); err == nil {
		t.Fatal("departed member overwrote guild meal")
	}
	meals, err = repo.ListMeals(guild)
	if err != nil || len(meals) != 1 || meals[0].MealID != 6 {
		t.Fatalf("rejected mutation changed shared storage: %+v %v", meals, err)
	}
}
