package channelserver

import (
	"database/sql"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setupSessionRepo(t *testing.T) (*SessionRepository, *sqlx.DB, uint32, uint32, uint32, string) {
	t.Helper()
	db := SetupTestDB(t)
	userID := CreateTestUser(t, db, "session_test_user")
	charID := CreateTestCharacter(t, db, userID, "SessionChar")
	token := "test_token_12345"
	sessionID := CreateTestSignSession(t, db, userID, token)
	repo := NewSessionRepository(db)
	t.Cleanup(func() { TeardownTestDB(t, db) })
	return repo, db, userID, charID, sessionID, token
}

func TestRepoSessionValidateLoginToken(t *testing.T) {
	repo, _, _, charID, sessionID, token := setupSessionRepo(t)

	err := repo.ValidateLoginToken(token, sessionID, charID)
	if err != nil {
		t.Fatalf("ValidateLoginToken failed: %v", err)
	}
}

func TestRepoSessionValidateLoginTokenInvalidToken(t *testing.T) {
	repo, _, _, charID, sessionID, _ := setupSessionRepo(t)

	err := repo.ValidateLoginToken("wrong_token", sessionID, charID)
	if err == nil {
		t.Fatal("Expected error for invalid token, got nil")
	}
}

func TestRepoSessionValidateLoginTokenWrongChar(t *testing.T) {
	repo, _, _, _, sessionID, token := setupSessionRepo(t)

	err := repo.ValidateLoginToken(token, sessionID, 999999)
	if err == nil {
		t.Fatal("Expected error for wrong char ID, got nil")
	}
}

func TestRepoSessionValidateLoginTokenWrongSession(t *testing.T) {
	repo, _, _, charID, _, token := setupSessionRepo(t)

	err := repo.ValidateLoginToken(token, 999999, charID)
	if err == nil {
		t.Fatal("Expected error for wrong session ID, got nil")
	}
}

func TestRepoSessionRejectsExpiredUnboundToken(t *testing.T) {
	_, db, _, charID, sessionID, token := setupSessionRepo(t)
	repo := NewSessionRepository(db, time.Hour)

	if _, err := db.Exec("UPDATE sign_sessions SET last_used_at=NOW()-INTERVAL '2 hours' WHERE id=$1", sessionID); err != nil {
		t.Fatalf("Failed to age sign session: %v", err)
	}
	if err := repo.ValidateLoginToken(token, sessionID, charID); err != sql.ErrNoRows {
		t.Fatalf("ValidateLoginToken error = %v, want sql.ErrNoRows", err)
	}
}

func TestRepoSessionRejectsExpiredBoundTokenForNewAuthentication(t *testing.T) {
	_, db, _, charID, sessionID, token := setupSessionRepo(t)
	repo := NewSessionRepository(db, time.Hour)
	CreateTestServer(t, db, 1)

	if err := repo.BindSession(token, 1, charID); err != nil {
		t.Fatalf("BindSession failed: %v", err)
	}
	if _, err := db.Exec("UPDATE sign_sessions SET last_used_at=NOW()-INTERVAL '2 hours' WHERE id=$1", sessionID); err != nil {
		t.Fatalf("Failed to age sign session: %v", err)
	}
	if err := repo.ValidateLoginToken(token, sessionID, charID); err != sql.ErrNoRows {
		t.Fatalf("ValidateLoginToken error = %v, want sql.ErrNoRows", err)
	}
}

func TestRepoSessionBindSession(t *testing.T) {
	repo, db, _, charID, _, token := setupSessionRepo(t)

	CreateTestServer(t, db, 1)

	if err := repo.BindSession(token, 1, charID); err != nil {
		t.Fatalf("BindSession failed: %v", err)
	}

	var serverID *uint16
	var boundCharID *uint32
	if err := db.QueryRow("SELECT server_id, char_id FROM sign_sessions WHERE token=$1", token).Scan(&serverID, &boundCharID); err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if serverID == nil || *serverID != 1 {
		t.Errorf("Expected server_id=1, got: %v", serverID)
	}
	if boundCharID == nil || *boundCharID != charID {
		t.Errorf("Expected char_id=%d, got: %v", charID, boundCharID)
	}
}

func TestRepoSessionClearSession(t *testing.T) {
	repo, db, _, charID, _, token := setupSessionRepo(t)

	CreateTestServer(t, db, 1)

	if err := repo.BindSession(token, 1, charID); err != nil {
		t.Fatalf("BindSession failed: %v", err)
	}

	if err := repo.ClearSession(token); err != nil {
		t.Fatalf("ClearSession failed: %v", err)
	}

	var serverID, boundCharID *int
	if err := db.QueryRow("SELECT server_id, char_id FROM sign_sessions WHERE token=$1", token).Scan(&serverID, &boundCharID); err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if serverID != nil {
		t.Errorf("Expected server_id=NULL, got: %v", *serverID)
	}
	if boundCharID != nil {
		t.Errorf("Expected char_id=NULL, got: %v", *boundCharID)
	}
}

func TestRepoSessionBindWaitsForCurrencyAccountLock(t *testing.T) {
	repo, db, userID, charID, _, token := setupSessionRepo(t)
	CreateTestServer(t, db, 1)
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var lockedID uint32
	if err := tx.QueryRow(`SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- repo.BindSession(token, 1, charID)
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("login bypassed the currency grant lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not resume after the account lock was released")
	}
}

func TestRepoSessionUpdatePlayerCount(t *testing.T) {
	repo, db, _, _, _, _ := setupSessionRepo(t)

	CreateTestServer(t, db, 1)

	if err := repo.UpdatePlayerCount(1, 42); err != nil {
		t.Fatalf("UpdatePlayerCount failed: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT current_players FROM servers WHERE server_id=1").Scan(&count); err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if count != 42 {
		t.Errorf("Expected current_players=42, got: %d", count)
	}
}

func TestRepoSessionUpdatePlayerCountTwice(t *testing.T) {
	repo, db, _, _, _, _ := setupSessionRepo(t)

	CreateTestServer(t, db, 1)

	if err := repo.UpdatePlayerCount(1, 10); err != nil {
		t.Fatalf("First UpdatePlayerCount failed: %v", err)
	}
	if err := repo.UpdatePlayerCount(1, 25); err != nil {
		t.Fatalf("Second UpdatePlayerCount failed: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT current_players FROM servers WHERE server_id=1").Scan(&count); err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if count != 25 {
		t.Errorf("Expected current_players=25, got: %d", count)
	}
}
