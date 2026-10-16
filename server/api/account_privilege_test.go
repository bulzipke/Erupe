package api

import (
	"database/sql"
	"go.uber.org/zap"
	"testing"
	"time"
)

func TestAuthDataSeparateAccountEntitlements(t *testing.T) {
	now := time.Now()
	repo := &mockAPIUserRepo{lastLogin: now, newcomerExpiry: now.Add(30 * 24 * time.Hour)}
	s := &APIServer{logger: zap.NewNop(), erupeConfig: NewTestConfig(), userRepo: repo}
	chars := []Character{{LastLogin: 0}, {LastLogin: int32(now.Unix())}}
	a := s.newAuthData(1, 0, 1, "test", chars)
	if a.ExpiryTS != 0 || a.NewcomerExpiryTS != uint32(repo.newcomerExpiry.Unix()) {
		t.Fatal("newcomer became returning", a.ExpiryTS, a.NewcomerExpiryTS)
	}
	for _, c := range a.Characters {
		if c.Returning {
			t.Fatal("unused character became a returning account")
		}
	}
	repo.newcomerExpiry = time.Time{}
	repo.returnExpiry = now.Add(24 * time.Hour)
	a = s.newAuthData(1, 0, 1, "test", chars)
	if a.ExpiryTS == 0 || a.NewcomerExpiryTS != 0 {
		t.Fatal("returning became newcomer")
	}
	for _, c := range a.Characters {
		if !c.Returning {
			t.Fatal("account entitlement not shared")
		}
	}
	repo.newcomerExpiryErr = sql.ErrConnDone
	repo.newcomerExpiry = now.Add(30 * 24 * time.Hour)
	a = s.newAuthData(1, 0, 1, "test", chars)
	if a.NewcomerExpiryTS != 0 {
		t.Fatal("read failure invented newcomer status")
	}
}
