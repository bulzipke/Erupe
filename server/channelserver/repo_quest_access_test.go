package channelserver

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

// These fixtures must never run against a NAS/production database. Like the
// purchase repository tests, they use the disposable local PostgreSQL fixture.
func setupQuestAccessRepo(t *testing.T) (*QuestAccessRepository, *sqlx.DB, uint32, uint32, time.Time) {
	t.Helper()
	c := DefaultTestDBConfig()
	if (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Port != "5433" || c.DBName != "erupe_test" {
		t.Fatal("requires isolated localhost:5433/erupe_test")
	}
	db := SetupTestDB(t)
	user := CreateTestUser(t, db, "quest_access_repo")
	char := CreateTestCharacter(t, db, user, "access")
	var now time.Time
	if err := db.Get(&now, `SELECT transaction_timestamp()`); err != nil {
		t.Fatal(err)
	}
	return NewQuestAccessRepository(db), db, user, char, now.UTC()
}

type questAccessFixtureExecutor interface {
	Exec(string, ...any) (sql.Result, error)
}

func insertQuestAccessCampaign(t *testing.T, db questAccessFixtureExecutor, campaignID, questID, itemType, stamps int, start, end time.Time, characterID uint32, redeemed int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO campaigns(id,stamps,start_time,end_time) VALUES($1,$2,$3,$4)`, campaignID, stamps, start, end); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO campaign_rewards(campaign_id,item_type,item_id,quantity) VALUES($1,$2,$3,1)`, campaignID, itemType, questID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < redeemed; i++ {
		if _, err := db.Exec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES($1,$2,'fixture')`, campaignID, characterID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuestAccessRepoCombinesExactLegacyAndAccountRights(t *testing.T) {
	repo, db, user, char, now := setupQuestAccessRepo(t)
	secondChar := CreateTestCharacter(t, db, user, "sameaccount")
	otherUser := CreateTestUser(t, db, "quest_access_other")
	otherChar := CreateTestCharacter(t, db, otherUser, "other")
	start := now.Add(-time.Hour)
	maxValidEnd := now.Add(2 * time.Hour)
	// A first expired campaign must not shadow a later valid one. The maximum
	// deadline must consider valid matching campaigns only.
	insertQuestAccessCampaign(t, db, 100, 40079, 9, 1, start, now.Add(-time.Hour), char, 1)
	insertQuestAccessCampaign(t, db, 101, 40079, 9, 1, start, now.Add(time.Hour), char, 1)
	insertQuestAccessCampaign(t, db, 102, 40079, 9, 2, start, maxValidEnd, char, 2)
	insertQuestAccessCampaign(t, db, 103, 40079, 9, 2, start, now.Add(8*time.Hour), char, 1)
	// Duplicating reward mappings must not turn one stamp into two.
	if _, err := db.Exec(`INSERT INTO campaign_rewards(campaign_id,item_type,item_id,quantity)
		VALUES(103,9,40079,1),(103,9,40079,1),(103,9,40079,1)`); err != nil {
		t.Fatal(err)
	}
	insertQuestAccessCampaign(t, db, 104, 40079, 9, 1, start, now.Add(9*time.Hour), secondChar, 1)
	insertQuestAccessCampaign(t, db, 105, 40079, 8, 1, start, now.Add(10*time.Hour), char, 1)
	// The legacy policy checks only end_time, not a future campaign start.
	insertQuestAccessCampaign(t, db, 106, 40142, 9, 0, now.Add(24*time.Hour), now.Add(48*time.Hour), char, 1)
	insertQuestAccessCampaign(t, db, 107, 40081, 9, 0, start, maxValidEnd, char, 0)
	insertQuestAccessCampaign(t, db, 108, 40143, 9, -3, start, maxValidEnd, char, 1)
	insertQuestAccessCampaign(t, db, 109, 40161, 9, 1, start, now.Add(-time.Second), char, 1)
	insertQuestAccessCampaign(t, db, 110, 40201, 9, 1, start, maxValidEnd, char, 0)
	if _, err := db.Exec(`INSERT INTO campaign_quest(campaign_id,character_id) VALUES(110,$1)`, char); err != nil {
		t.Fatal(err)
	}
	insertQuestAccessCampaign(t, db, 111, 65500, 9, 1, start, maxValidEnd, char, 1)
	insertQuestAccessCampaign(t, db, 112, 40218, 9, 1, start, maxValidEnd, otherChar, 1)
	if _, err := db.Exec(`INSERT INTO quest_licenses(user_id,license_key)
		VALUES($1,'account_owned'),($1,'alternative_owned'),($2,'other_owned')`, user, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET gacha_premium=200,gacha_trial=77 WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	var beforeStates, beforeCompleted, beforeLicenses int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM campaign_state),
		(SELECT count(*) FROM campaign_quest),(SELECT count(*) FROM quest_licenses)`).Scan(&beforeStates, &beforeCompleted, &beforeLicenses); err != nil {
		t.Fatal(err)
	}
	request := QuestAccessLoadRequest{
		UserID: user, CharacterID: char, AccountLicensesEnabled: true,
		QuestIDs: []int{40079, 40081, 40142, 40143, 40161, 40173, 40174, 40201, 40218, 55555},
		Catalog: questAccessMapCatalog{
			40173: {"account_owned"}, 40174: {"alternative_missing", "alternative_owned"},
			40218: {"other_owned"}, 65500: {"account_owned"},
		},
	}
	snapshot, err := repo.Load(context.Background(), request)
	if err != nil || snapshot == nil || snapshot.ObservedAt().IsZero() || snapshot.userID != user || snapshot.characterID != char {
		t.Fatalf("load: %#v %v", snapshot, err)
	}
	for id, want := range map[int]bool{
		40079: true, 40081: false, 40142: true, 40143: true, 40161: false,
		40173: true, 40174: true, 40201: false, 40218: false, 55555: false, 65500: false,
	} {
		if got := snapshot.Allows(id, snapshot.ObservedAt()); got != want {
			t.Errorf("quest %d access=%t want %t", id, got, want)
		}
	}
	if got := snapshot.grants[40079].legacyExpiry; !got.Equal(maxValidEnd) {
		t.Fatalf("valid maximum legacy deadline=%s want %s", got, maxValidEnd)
	}
	if snapshot.Allows(40079, maxValidEnd) || !snapshot.Allows(40079, maxValidEnd.Add(-time.Nanosecond)) {
		t.Fatal("database legacy expiry did not use an exclusive end")
	}
	if !snapshot.Allows(40173, now.AddDate(20, 0, 0)) || snapshot.Allows(40173, snapshot.ObservedAt().Add(-time.Nanosecond)) {
		t.Fatal("permanent account right has an expiry or retroactively authorizes")
	}
	// Account rights apply to another active character, while character-specific
	// legacy state must not be promoted to account ownership.
	request.CharacterID = secondChar
	second, err := repo.Load(context.Background(), request)
	if err != nil || !second.Allows(40173, second.ObservedAt()) || !second.Allows(40174, second.ObservedAt()) || second.Allows(40142, second.ObservedAt()) {
		t.Fatalf("account sharing/character isolation: %#v %v", second, err)
	}
	var afterStates, afterCompleted, afterLicenses, premium, trial int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM campaign_state),
		(SELECT count(*) FROM campaign_quest),(SELECT count(*) FROM quest_licenses),
		gacha_premium,gacha_trial FROM users WHERE id=$1`, user).
		Scan(&afterStates, &afterCompleted, &afterLicenses, &premium, &trial); err != nil {
		t.Fatal(err)
	}
	if beforeStates != afterStates || beforeCompleted != afterCompleted || beforeLicenses != afterLicenses || premium != 200 || trial != 77 {
		t.Fatal("read-only access load changed rights, completion, or currency")
	}
}

func TestQuestAccessRepoLegacyDoesNotDependOnAccountTables(t *testing.T) {
	repo, db, user, char, now := setupQuestAccessRepo(t)
	insertQuestAccessCampaign(t, db, 100, 40079, 9, 0, now.Add(-time.Hour), now.Add(time.Hour), char, 1)
	// Hide both new tables in this disposable schema, restoring them before any
	// later fixture. An accidental account query would now fail immediately.
	if _, err := db.Exec(`ALTER TABLE quest_licenses RENAME TO quest_access_test_hidden_licenses;
		ALTER TABLE quest_license_purchases RENAME TO quest_access_test_hidden_purchases`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`ALTER TABLE quest_access_test_hidden_licenses RENAME TO quest_licenses;
			ALTER TABLE quest_access_test_hidden_purchases RENAME TO quest_license_purchases`); err != nil {
			t.Errorf("restore account fixture tables: %v", err)
		}
	})
	request := QuestAccessLoadRequest{UserID: user, CharacterID: char, QuestIDs: []int{40079}}
	snapshot, err := repo.Load(context.Background(), request)
	if err != nil || snapshot == nil || !snapshot.Allows(40079, snapshot.ObservedAt()) {
		t.Fatalf("legacy-only access without account tables: %#v %v", snapshot, err)
	}
	// Even with the account branch enabled, no key mapping means legacy only.
	request.AccountLicensesEnabled, request.Catalog = true, questAccessMapCatalog{}
	snapshot, err = repo.Load(context.Background(), request)
	if err != nil || snapshot == nil || !snapshot.Allows(40079, snapshot.ObservedAt()) {
		t.Fatalf("unmapped account target: %#v %v", snapshot, err)
	}
	// A mapped account query fails, but savepoint recovery must preserve valid
	// legacy rights and clearly mark the missing source without granting it.
	request.QuestIDs = []int{40079, 40142}
	request.Catalog = questAccessMapCatalog{40079: {"mapped"}, 40142: {"mapped"}}
	got, err := repo.Load(context.Background(), request)
	if err != nil || got == nil || got.AccountReadError() == nil || !got.Allows(40079, got.ObservedAt()) || got.grants[40079].accountPermanent || got.Allows(40142, got.ObservedAt()) {
		t.Fatalf("legacy was revoked or missing account source granted rights: %#v %v", got, err)
	}
}

func TestQuestAccessRepoValidatesActiveCharacterOwnership(t *testing.T) {
	repo, db, user, char, _ := setupQuestAccessRepo(t)
	otherUser := CreateTestUser(t, db, "quest_access_wrong_owner")
	otherChar := CreateTestCharacter(t, db, otherUser, "other")
	request := QuestAccessLoadRequest{UserID: user, CharacterID: char, QuestIDs: []int{40079}}
	for _, invalid := range []QuestAccessLoadRequest{
		{UserID: otherUser, CharacterID: char, QuestIDs: request.QuestIDs},
		{UserID: user, CharacterID: otherChar, QuestIDs: request.QuestIDs},
		{UserID: user, CharacterID: 2147483647, QuestIDs: request.QuestIDs},
	} {
		if got, err := repo.Load(context.Background(), invalid); got != nil || !errors.Is(err, ErrQuestAccessCharacter) {
			t.Fatalf("unowned character: %#v %v", got, err)
		}
	}
	if _, err := db.Exec(`UPDATE characters SET deleted=true WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Load(context.Background(), request); got != nil || !errors.Is(err, ErrQuestAccessCharacter) {
		t.Fatalf("deleted character: %#v %v", got, err)
	}
	if _, err := db.Exec(`UPDATE characters SET deleted=false WHERE id=$1`, char); err != nil {
		t.Fatal(err)
	}
	request.QuestIDs = nil
	empty, err := repo.Load(context.Background(), request)
	if err != nil || empty == nil || empty.ObservedAt().IsZero() || empty.Allows(40079, empty.ObservedAt()) {
		t.Fatalf("authenticated empty scope: %#v %v", empty, err)
	}
}

func TestQuestAccessRepoUsesOneConsistentReadonlySnapshot(t *testing.T) {
	repo, db, user, char, now := setupQuestAccessRepo(t)
	insertQuestAccessCampaign(t, db, 100, 40079, 9, 1, now.Add(-time.Hour), now.Add(time.Hour), char, 0)
	request := QuestAccessLoadRequest{
		UserID: user, CharacterID: char, QuestIDs: []int{40079},
		AccountLicensesEnabled: true, Catalog: questAccessMapCatalog{40079: {"late_right"}},
	}
	blocker, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.Exec(`LOCK TABLE campaign_rewards IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type loadResult struct {
		snapshot *QuestAccessSnapshot
		err      error
	}
	loaded := make(chan loadResult, 1)
	go func() {
		snapshot, err := repo.Load(ctx, request)
		loaded <- loadResult{snapshot, err}
	}()
	// Seeing the legacy query wait proves the identity/time statement already
	// established the reader's snapshot. This is bounded synchronization, not a
	// timing assumption about how fast the loader goroutine starts.
	waitCtx, stopWait := context.WithTimeout(ctx, 4*time.Second)
	defer stopWait()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.GetContext(waitCtx, &waiting, `SELECT EXISTS(
			SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid()
			AND state='active' AND wait_event_type='Lock' AND query LIKE $1
		)`, "%quest_access_legacy%"); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("access reader did not reach the controlled relation lock")
		case <-ticker.C:
		}
	}
	if _, err := blocker.Exec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES(100,$1,'late')`, char); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,'late_right')`, user); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	var first loadResult
	select {
	case first = <-loaded:
	case <-ctx.Done():
		t.Fatal("access reader did not finish after the writer committed")
	}
	if first.err != nil || first.snapshot == nil || first.snapshot.Allows(40079, first.snapshot.ObservedAt()) {
		t.Fatalf("mixed pre/post-commit sources: %#v %v", first.snapshot, first.err)
	}
	second, err := repo.Load(context.Background(), request)
	if err != nil || second == nil || !second.Allows(40079, second.ObservedAt()) ||
		!second.grants[40079].accountPermanent || second.grants[40079].legacyExpiry.IsZero() {
		t.Fatalf("new observation did not see both committed sources: %#v %v", second, err)
	}
	if second.Allows(40079, first.snapshot.ObservedAt()) {
		t.Fatal("later observation retroactively authorized an earlier admission")
	}
}

func TestQuestAccessRepoUnavailableAndCancelledRead(t *testing.T) {
	request := QuestAccessLoadRequest{UserID: 1, CharacterID: 2, QuestIDs: []int{40079}}
	for _, repo := range []*QuestAccessRepository{nil, NewQuestAccessRepository(nil)} {
		if got, err := repo.Load(context.Background(), request); err == nil || got != nil {
			t.Fatalf("unavailable repository: %#v %v", got, err)
		}
	}
	repo, _, user, char, _ := setupQuestAccessRepo(t)
	request.UserID, request.CharacterID = user, char
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := repo.Load(ctx, request); err == nil || got != nil {
		t.Fatalf("cancelled read published a snapshot: %#v %v", got, err)
	}
}
