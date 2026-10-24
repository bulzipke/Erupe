package channelserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func setupLegacyCampaignRepository(t *testing.T) (*LegacyCampaignRepository, *sqlx.DB, uint32, uint32, []uint32, uint32) {
	t.Helper()
	_, db, user, character := setupQuestLicenseRepo(t)
	db.MustExec(`INSERT INTO campaigns(id,stamps,reward_type,receive_type,start_time,end_time)
		VALUES(8001,1,1,1,now()-interval '2 days',now()-interval '1 day');
		INSERT INTO campaign_codes(code,campaign_id,multi) VALUES('LEGACY-MULTI',8001,true),('LEGACY-SINGLE',8001,false);
		INSERT INTO campaign_rewards(campaign_id,item_type,item_id,quantity) VALUES(8001,7,1859,110),(8001,7,100,1),(8001,9,40081,1)`)
	var ids []uint32
	if err := db.Select(&ids, `SELECT id FROM campaign_rewards WHERE campaign_id=8001 ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	var eventID uint32
	if err := db.QueryRow(`INSERT INTO event_quests(quest_id,quest_type) VALUES(40081,9) RETURNING id`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return NewLegacyCampaignRepository(db), db, user, character, ids, eventID
}

func TestLegacyCampaignApplyPreservesLegacyRules(t *testing.T) {
	repo, db, user, character, _, _ := setupLegacyCampaignRepository(t)
	ctx := context.Background()
	// Expiration is intentionally not introduced as a new code-entry rule.
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI"); err != nil {
		t.Fatal("existing legacy code rule changed", err)
	}
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI"); err == nil {
		t.Fatal("same character reused multi code")
	}
	otherCharacter := CreateTestCharacter(t, db, user, "LegacyOther")
	if err := repo.Apply(ctx, user, otherCharacter, 8001, "LEGACY-MULTI"); err != nil {
		t.Fatal("multi code lost its per-character scope", err)
	}
	if err := repo.Apply(ctx, user, character, 8002, "LEGACY-SINGLE"); err == nil {
		t.Fatal("code accepted for another campaign")
	}
	db.MustExec(`INSERT INTO campaign_coin_prices(campaign_id,price) VALUES(8001,10)`)
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-SINGLE"); err == nil {
		t.Fatal("priced kit accepted reusable code")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM campaign_state`); err != nil || count != 2 {
		t.Fatal("rejected authentication wrote state", count, err)
	}
}

func TestLegacyCampaignSingleUseCodeIsGlobalUnderConcurrency(t *testing.T) {
	repo, db, user, character, _, _ := setupLegacyCampaignRepository(t)
	other := CreateTestUser(t, db, "legacy_other")
	otherCharacter := CreateTestCharacter(t, db, other, "LegacyOther")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	codeBlocker, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = codeBlocker.Rollback() }()
	var lockedCode string
	if err := codeBlocker.Get(&lockedCode, `SELECT code FROM campaign_codes WHERE code='LEGACY-SINGLE' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, pair := range [][2]uint32{{user, character}, {other, otherCharacter}} {
		go func(pair [2]uint32) {
			<-start
			results <- repo.Apply(ctx, pair[0], pair[1], 8001, "LEGACY-SINGLE")
		}(pair)
	}
	close(start)
	// Both different-account requests must reach the same locked code row.
	// Without the code-row lock both can read "unused" and commit independently.
	waitForLegacyBlockedQueries(t, db, results, "SELECT multi FROM campaign_codes", 2)
	if err := codeBlocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		}
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM campaign_state WHERE code='LEGACY-SINGLE'`); err != nil || success != 1 || count != 1 {
		t.Fatal("single-use code succeeded for multiple accounts", success, count, err)
	}
}

func waitForLegacyAccountLock(t *testing.T, db *sqlx.DB, done <-chan error, queryFragment ...string) {
	t.Helper()
	fragment := "SELECT id FROM users WHERE id=$1 FOR UPDATE"
	if len(queryFragment) != 0 {
		fragment = queryFragment[0]
	}
	waitForLegacyBlockedQueries(t, db, done, fragment, 1)
}

func waitForLegacyBlockedQueries(t *testing.T, db *sqlx.DB, done <-chan error, fragment string, expected int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		var blocked int
		if err := db.Get(&blocked, `SELECT count(*) FROM pg_stat_activity
			WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'
			AND query LIKE $1`, "%"+fragment+"%"); err != nil {
			t.Fatal(err)
		}
		if blocked >= expected {
			return
		}
		select {
		case err := <-done:
			t.Fatal("legacy writer completed before its account lock was released", err)
		case <-timeout.C:
			t.Fatal("legacy writer did not wait for its account lock")
		case <-ticker.C:
		}
	}
}

func TestLegacyCampaignAuthenticationAndStarterDecisionSerialize(t *testing.T) {
	starter, db, user, character, policy := setupStarterRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	queued, err := starter.Ensure(ctx, user, character, policy)
	if err != nil || queued.Status != StarterPending {
		t.Fatal(queued, err)
	}
	starterLegacyCatalog(t, db, 492821212, 1859, 0)
	db.MustExec(`INSERT INTO campaign_codes(code,campaign_id,multi) VALUES('RACING-LEGACY',492821212,true)`)
	codeBlocker, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = codeBlocker.Rollback() }()
	var code string
	if err := codeBlocker.Get(&code, `SELECT code FROM campaign_codes WHERE code='RACING-LEGACY' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	legacyDone := make(chan error, 1)
	go func() { legacyDone <- NewLegacyCampaignRepository(db).Apply(ctx, user, character, 492821212, code) }()
	// The real Apply method now holds the account lock while it waits for the
	// code row. The competing starter decision must wait behind that method.
	waitForLegacyAccountLock(t, db, legacyDone, "SELECT multi FROM campaign_codes")
	starterDone := make(chan error, 1)
	starterResult := make(chan QuestLicenseStarterResult, 1)
	go func() {
		result, err := starter.Ensure(ctx, user, character, policy)
		starterResult <- result
		starterDone <- err
	}()
	waitForLegacyAccountLock(t, db, starterDone)
	if err := codeBlocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-legacyDone; err != nil {
		t.Fatal(err)
	}
	if err := <-starterDone; err != nil {
		t.Fatal(err)
	}
	if result := <-starterResult; result.Status != StarterLegacyPending || result.RewardID != 0 {
		t.Fatal("starter decision missed the earlier legacy authentication", result)
	}
	if _, err := starter.PrepareSave(ctx, user, character, []uint32{queued.RewardID}); err == nil {
		t.Fatal("new native claim bypassed serialized legacy eligibility")
	}
	assertStarterState(t, db, user, 1, 0, 0)
}

func TestLegacyCampaignWritersShareStarterAccountLock(t *testing.T) {
	for _, operation := range []string{"apply", "acquire", "complete"} {
		t.Run(operation, func(t *testing.T) {
			repo, db, user, character, ids, eventID := setupLegacyCampaignRepository(t)
			if operation == "acquire" {
				if err := repo.Apply(context.Background(), user, character, 8001, "LEGACY-MULTI"); err != nil {
					t.Fatal(err)
				}
			}
			blocker, err := db.Beginx()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback() }()
			var locked uint32
			if err := blocker.Get(&locked, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "apply":
					done <- repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI")
				case "acquire":
					done <- repo.Acquire(ctx, user, character, ids[:2])
				case "complete":
					done <- repo.CompleteQuest(ctx, user, character, eventID)
				}
			}()
			waitForLegacyAccountLock(t, db, done)
			var before int
			if err := db.Get(&before, `SELECT (SELECT count(*) FROM campaign_rewards_claimed)+(SELECT count(*) FROM campaign_quest)`); err != nil || before != 0 {
				t.Fatal("blocked writer committed eligibility state", before, err)
			}
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal("writer failed after account lock release", err)
			}
		})
	}
}

func TestLegacyCampaignAcquireBatchRollsBackOnLaterWriteFailure(t *testing.T) {
	repo, db, user, character, ids, _ := setupLegacyCampaignRepository(t)
	ctx := context.Background()
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI"); err != nil {
		t.Fatal(err)
	}
	constraint := fmt.Sprintf(`ALTER TABLE campaign_rewards_claimed ADD CONSTRAINT legacy_acquire_rollback CHECK(reward_id<>%d)`, ids[1])
	db.MustExec(constraint)
	t.Cleanup(func() {
		db.MustExec(`ALTER TABLE campaign_rewards_claimed DROP CONSTRAINT IF EXISTS legacy_acquire_rollback`)
	})
	if err := repo.Acquire(ctx, user, character, ids[:2]); err == nil {
		t.Fatal("forced later claim failure accepted")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM campaign_rewards_claimed WHERE character_id=$1`, character); err != nil || count != 0 {
		t.Fatal("later failure left a partial claim batch", count, err)
	}
	db.MustExec(`ALTER TABLE campaign_rewards_claimed DROP CONSTRAINT legacy_acquire_rollback`)
	if err := repo.Acquire(ctx, user, character, ids[:2]); err != nil {
		t.Fatal("rolled-back reward batch was not recoverable", err)
	}
}

func TestLegacyCampaignAcquireRetainsCompletionRewardBranch(t *testing.T) {
	repo, db, user, character, ids, eventID := setupLegacyCampaignRepository(t)
	ctx := context.Background()
	db.MustExec(`UPDATE campaigns SET reward_type=2 WHERE id=8001`)
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Acquire(ctx, user, character, ids[:2]); err == nil {
		t.Fatal("completion materials acquired before quest completion")
	}
	if err := repo.Acquire(ctx, user, character, ids[2:]); err != nil {
		t.Fatal("original Type9 reward unavailable before completion", err)
	}
	if err := repo.CompleteQuest(ctx, user, character, eventID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Acquire(ctx, user, character, ids[:2]); err != nil {
		t.Fatal("original materials unavailable after completion", err)
	}
	if err := repo.CompleteQuest(ctx, user, character, eventID+10000); err != nil {
		t.Fatal("unmapped transfer changed its success acknowledgement", err)
	}
}

func TestLegacyCampaignRejectsForeignDeletedAndInvalidBatches(t *testing.T) {
	repo, db, user, character, ids, eventID := setupLegacyCampaignRepository(t)
	ctx := context.Background()
	other := CreateTestUser(t, db, "legacy_foreign")
	for _, operation := range []func() error{
		func() error { return repo.Apply(ctx, other, character, 8001, "LEGACY-MULTI") },
		func() error { return repo.Acquire(ctx, other, character, ids[:1]) },
		func() error { return repo.CompleteQuest(ctx, other, character, eventID) },
	} {
		if err := operation(); err == nil {
			t.Fatal("foreign character accepted")
		}
	}
	for _, invalid := range [][]uint32{{0}, {ids[0], ids[0]}, {ids[0], campaignCoinRewardBase}, make([]uint32, 513)} {
		if err := repo.Acquire(ctx, user, character, invalid); err == nil {
			t.Fatal("invalid claim batch accepted")
		}
	}
	if err := repo.Acquire(ctx, user, character, ids[:1]); err == nil {
		t.Fatal("unauthenticated reward accepted")
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, character)
	if err := repo.Apply(ctx, user, character, 8001, "LEGACY-MULTI"); err == nil {
		t.Fatal("deleted character authenticated")
	}
	if err := (*LegacyCampaignRepository)(nil).Acquire(ctx, 0, 0, nil); err != nil {
		t.Fatal("empty native claim lost its no-op", err)
	}
	if err := (*LegacyCampaignRepository)(nil).Apply(ctx, user, character, 8001, "LEGACY-MULTI"); !errors.Is(err, errLegacyCampaignUnavailable) {
		t.Fatal("nil repository accepted mutation", err)
	}
}

func TestLegacyCampaignWritersSuppressPreviouslyQueuedNewStarter(t *testing.T) {
	starter, db, user, character, policy := setupStarterRepo(t)
	ctx := context.Background()
	queued, err := starter.Ensure(ctx, user, character, policy)
	if err != nil || queued.Status != StarterPending {
		t.Fatal(queued, err)
	}
	starterLegacyCatalog(t, db, 492821212, 1859, 0)
	db.MustExec(`INSERT INTO campaign_codes(code,campaign_id,multi) VALUES('STARTER-LEGACY',492821212,true)`)
	legacy := NewLegacyCampaignRepository(db)
	if err := legacy.Apply(ctx, user, character, 492821212, "STARTER-LEGACY"); err != nil {
		t.Fatal(err)
	}
	if _, err := starter.PrepareSave(ctx, user, character, []uint32{queued.RewardID}); err == nil {
		t.Fatal("new starter bypassed later committed legacy authentication")
	}
	var rewardID uint32
	if err := db.Get(&rewardID, `SELECT min(id) FROM campaign_rewards WHERE campaign_id=492821212`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Acquire(ctx, user, character, []uint32{rewardID}); err != nil {
		t.Fatal(err)
	}
	if state, err := starter.Ensure(ctx, user, character, policy); err != nil || state.Status != StarterLegacyClaimed {
		t.Fatal("committed legacy claim did not suppress pending new starter", state, err)
	}
	assertStarterState(t, db, user, 0, 0, 1)
}
