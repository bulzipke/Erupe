package channelserver

import (
	"sync"
	"testing"
)

func TestWeaponUsageRepositoryRecordsPersistedWeaponAtomically(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	userID := CreateTestUser(t, db, "weapon_usage_user")
	charID := CreateTestCharacter(t, db, userID, "WeaponUsageHunter")
	if _, err := db.Exec(`UPDATE characters SET weapon_type=12 WHERE id=$1`, charID); err != nil {
		t.Fatalf("set weapon type: %v", err)
	}

	repo := NewWeaponUsageRepository(db)
	const departures = 16
	var wg sync.WaitGroup
	errs := make(chan error, departures)
	for i := 0; i < departures; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			weaponType, ok, err := repo.RecordQuestDeparture(charID)
			if err != nil {
				errs <- err
				return
			}
			if !ok || weaponType != 12 {
				errs <- &unexpectedWeaponUsageResult{weaponType: weaponType, ok: ok}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("record departure: %v", err)
	}

	var count int64
	if err := db.QueryRow(`SELECT usage_count FROM weapon_usage_stats WHERE weapon_type=12`).Scan(&count); err != nil {
		t.Fatalf("read weapon usage: %v", err)
	}
	if count != departures {
		t.Fatalf("usage count = %d, want %d", count, departures)
	}
}

type unexpectedWeaponUsageResult struct {
	weaponType uint8
	ok         bool
}

func (e *unexpectedWeaponUsageResult) Error() string {
	return "unexpected weapon type or unavailable character"
}

func TestWeaponUsageRepositoryRejectsInvalidOrUnavailableCharacter(t *testing.T) {
	db := SetupTestDB(t)
	defer TeardownTestDB(t, db)

	userID := CreateTestUser(t, db, "invalid_weapon_usage_user")
	charID := CreateTestCharacter(t, db, userID, "InvalidWeaponHunter")
	if _, err := db.Exec(`UPDATE characters SET weapon_type=14 WHERE id=$1`, charID); err != nil {
		t.Fatalf("set invalid weapon type: %v", err)
	}

	repo := NewWeaponUsageRepository(db)
	if weaponType, ok, err := repo.RecordQuestDeparture(charID); err != nil || ok || weaponType != 0 {
		t.Fatalf("invalid weapon result = (%d, %t, %v), want (0, false, nil)", weaponType, ok, err)
	}
	if weaponType, ok, err := repo.RecordQuestDeparture(charID + 1); err != nil || ok || weaponType != 0 {
		t.Fatalf("missing character result = (%d, %t, %v), want (0, false, nil)", weaponType, ok, err)
	}

	var invalidRows int
	if err := db.Get(&invalidRows, `SELECT COUNT(*) FROM weapon_usage_stats WHERE weapon_type NOT BETWEEN 0 AND 13`); err != nil {
		t.Fatalf("count invalid rows: %v", err)
	}
	if invalidRows != 0 {
		t.Fatalf("invalid weapon rows = %d, want 0", invalidRows)
	}
}

func TestWeaponUsageRepositoryWithoutDatabaseIsUnavailable(t *testing.T) {
	repo := NewWeaponUsageRepository(nil)
	if weaponType, ok, err := repo.RecordQuestDeparture(1); err != nil || ok || weaponType != 0 {
		t.Fatalf("nil DB result = (%d, %t, %v), want (0, false, nil)", weaponType, ok, err)
	}
}
