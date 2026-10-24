package channelserver

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
	"github.com/jmoiron/sqlx"
)

func starterTestPolicy() QuestLicenseStarterPolicy {
	return QuestLicenseStarterPolicy{Key: "visa_veil_starter", LicenseKeys: []string{"visa_veil"},
		ItemID: 1859, Quantity: 1, LegacyCampaignIDs: []uint32{492821212}}
}

func setupStarterRepo(t *testing.T) (*QuestLicenseStarterRepository, *sqlx.DB, uint32, uint32, QuestLicenseStarterPolicy) {
	t.Helper()
	licenses, db, user, char := setupQuestLicenseRepo(t)
	if err := licenses.BindDefinitions(context.Background(), []questlicense.Definition{{Key: "visa_veil", QuestIDs: []int{40142}}}); err != nil {
		t.Fatal(err)
	}
	repo := NewQuestLicenseStarterRepository(db)
	policy := starterTestPolicy()
	if err := repo.BindPolicies(context.Background(), []QuestLicenseStarterPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,'visa_veil')`, user)
	return repo, db, user, char, policy
}

func assertStarterState(t *testing.T, db *sqlx.DB, user uint32, pending, claimed, legacy int) {
	t.Helper()
	var balance, trial, gotPending, gotClaimed, gotLegacy int
	if err := db.QueryRow(`SELECT gacha_premium,gacha_trial FROM users WHERE id=$1`, user).Scan(&balance, &trial); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='claimed'),
		count(*) FILTER(WHERE state='legacy_claimed') FROM quest_license_starters WHERE user_id=$1`, user).
		Scan(&gotPending, &gotClaimed, &gotLegacy); err != nil {
		t.Fatal(err)
	}
	if balance != 200 || trial != 77 || gotPending != pending || gotClaimed != claimed || gotLegacy != legacy {
		t.Fatalf("starter balance/trial/pending/claimed/legacy=%d/%d/%d/%d/%d want 200/77/%d/%d/%d",
			balance, trial, gotPending, gotClaimed, gotLegacy, pending, claimed, legacy)
	}
}

func starterLegacyCatalog(t *testing.T, db *sqlx.DB, campaign uint32, item uint16, stamps int) {
	t.Helper()
	db.MustExec(`INSERT INTO campaigns(id,stamps,reward_type,receive_type,start_time,end_time)
		VALUES($1,$2,1,1,now()-interval '2 days',now()-interval '1 day')`, campaign, stamps)
	db.MustExec(`INSERT INTO campaign_rewards(campaign_id,item_type,item_id,quantity) VALUES($1,7,$2,110),($1,7,$2,110)`, campaign, item)
}

func TestQuestLicenseStarterPolicyCanonical(t *testing.T) {
	policy := starterTestPolicy()
	policy.LicenseKeys = []string{"veil_second", "visa_veil"}
	policy.LegacyCampaignIDs = []uint32{492821212, 123}
	a, err := canonicalQuestLicenseStarter(policy)
	if err != nil || len(a.Hash) != 64 || !slices.Equal([]string(a.Keys), []string{"veil_second", "visa_veil"}) ||
		!slices.Equal([]int64(a.Campaigns), []int64{123, 492821212}) {
		t.Fatal(a, err)
	}
	if policy.LegacyCampaignIDs[0] != 492821212 {
		t.Fatal("caller policy mutated")
	}
	policy.LegacyCampaignIDs = []uint32{123, 492821212}
	b, err := canonicalQuestLicenseStarter(policy)
	if err != nil || !sameQuestLicenseStarter(a, b) {
		t.Fatal("ordering changed policy identity")
	}
	policy.Quantity++
	b, err = canonicalQuestLicenseStarter(policy)
	if err != nil || sameQuestLicenseStarter(a, b) {
		t.Fatal("benefit drift preserved policy identity")
	}
	for _, test := range []struct {
		name string
		edit func(*QuestLicenseStarterPolicy)
	}{
		{"key", func(p *QuestLicenseStarterPolicy) { p.Key = "Invalid" }},
		{"item", func(p *QuestLicenseStarterPolicy) { p.ItemID = 0 }},
		{"quantity", func(p *QuestLicenseStarterPolicy) { p.Quantity = 0 }},
		{"no-license", func(p *QuestLicenseStarterPolicy) { p.LicenseKeys = nil }},
		{"duplicate-license", func(p *QuestLicenseStarterPolicy) { p.LicenseKeys = []string{"veil", "veil"} }},
		{"invalid-license", func(p *QuestLicenseStarterPolicy) { p.LicenseKeys = []string{"veil.key"} }},
		{"zero-campaign", func(p *QuestLicenseStarterPolicy) { p.LegacyCampaignIDs = []uint32{0} }},
		{"large-campaign", func(p *QuestLicenseStarterPolicy) { p.LegacyCampaignIDs = []uint32{0xffffffff} }},
		{"duplicate-campaign", func(p *QuestLicenseStarterPolicy) { p.LegacyCampaignIDs = []uint32{1, 1} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := starterTestPolicy()
			test.edit(&p)
			if _, err := canonicalQuestLicenseStarter(p); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func TestQuestLicenseStarterRepoPolicyBinding(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	if err := repo.BindPolicies(ctx, []QuestLicenseStarterPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	changed := policy
	changed.Quantity = 2
	if err := repo.BindPolicies(ctx, []QuestLicenseStarterPolicy{changed}); err == nil {
		t.Fatal("bound benefit changed")
	}
	if _, err := repo.Ensure(ctx, user, char, changed); err == nil {
		t.Fatal("ensure accepted changed policy")
	}
	if err := repo.BindPolicies(ctx, nil); err == nil {
		t.Fatal("registered policy disappeared")
	}
	unknown := policy
	unknown.Key, unknown.LicenseKeys = "another", []string{"unbound_license"}
	if err := repo.BindPolicies(ctx, []QuestLicenseStarterPolicy{policy, unknown}); err == nil {
		t.Fatal("policy with unregistered license accepted")
	}
	first, failure := policy, policy
	first.Key, failure.Key = "a_new", "z_failure"
	db.MustExec(`ALTER TABLE quest_license_starter_policies ADD CONSTRAINT starter_test_rollback CHECK(starter_key<>'z_failure')`)
	t.Cleanup(func() {
		db.MustExec(`ALTER TABLE quest_license_starter_policies DROP CONSTRAINT IF EXISTS starter_test_rollback`)
	})
	if err := repo.BindPolicies(ctx, []QuestLicenseStarterPolicy{policy, first, failure}); err == nil {
		t.Fatal("forced policy insert failure accepted")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM quest_license_starter_policies`); err != nil || count != 1 {
		t.Fatalf("partial policy binding: count=%d err=%v", count, err)
	}
	assertStarterState(t, db, user, 0, 0, 0)
}

func TestQuestLicenseStarterRepoOncePerCharacterAndAtomicClaim(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	first, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || first.Status != StarterPending || first.RewardID < campaignCoinRewardBase {
		t.Fatal(first, err)
	}
	secondChar := CreateTestCharacter(t, db, user, "startersecond")
	second, err := repo.Ensure(ctx, user, secondChar, policy)
	if err != nil || second.Status != StarterPending || second.RewardID == first.RewardID {
		t.Fatal("account starter incorrectly blocked another character", second, err)
	}
	items, err := repo.Pending(ctx, user, char, policy.Key)
	if err != nil || len(items) != 1 || items[0].ID != first.RewardID || items[0].ItemType != 7 || items[0].ItemID != 1859 || items[0].Quantity != 1 {
		t.Fatal("starter delivery snapshot", items, err)
	}
	if err := repo.Acquire(ctx, user, char, []uint32{first.RewardID, second.RewardID}); err == nil {
		t.Fatal("mixed-character batch accepted")
	}
	assertStarterState(t, db, user, 2, 0, 0)
	if err := repo.Acquire(ctx, user, secondChar, []uint32{first.RewardID}); err == nil {
		t.Fatal("wrong character claimed starter")
	}
	if err := repo.Acquire(ctx, user, char, []uint32{first.RewardID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Acquire(ctx, user, char, []uint32{first.RewardID}); err != nil {
		t.Fatal("claim acknowledgement retry failed", err)
	}
	replay, err := repo.Ensure(ctx, user, char, policy)
	if err != nil || replay.Status != StarterClaimed || replay.RewardID != first.RewardID {
		t.Fatal("claimed receipt reissued starter", replay, err)
	}
	items, err = repo.Pending(ctx, user, char, policy.Key)
	if err != nil || len(items) != 0 {
		t.Fatal("claimed item still pending", items, err)
	}
	assertStarterState(t, db, user, 1, 1, 0)
}

func TestQuestLicenseStarterRepoAuthenticationAndOwnership(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx := context.Background()
	other := CreateTestUser(t, db, "starter_other")
	foreign := CreateTestCharacter(t, db, other, "starterforeign")
	for _, pair := range [][2]uint32{{user, foreign}, {other, char}, {0, char}, {user, 0}} {
		got, err := repo.Ensure(ctx, pair[0], pair[1], policy)
		if err != nil || got.Status != StarterUnavailable {
			t.Fatal("unauthenticated ensure", got, err)
		}
	}
	got, err := repo.Ensure(ctx, other, foreign, policy)
	if err != nil || got.Status != StarterNotOwned {
		t.Fatal("unlicensed account got starter", got, err)
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, char)
	got, err = repo.Ensure(ctx, user, char, policy)
	if err != nil || got.Status != StarterUnavailable {
		t.Fatal("deleted character got starter", got, err)
	}
	if _, err := repo.Pending(ctx, user, char, policy.Key); err == nil {
		t.Fatal("deleted character fetched pending items")
	}
	assertStarterState(t, db, user, 0, 0, 0)
}

func TestQuestLicenseStarterRepoFailedGrantRollsBack(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	db.MustExec(`ALTER TABLE quest_license_starters ADD CONSTRAINT starter_grant_test_rollback CHECK(item_id<>1859)`)
	t.Cleanup(func() {
		db.MustExec(`ALTER TABLE quest_license_starters DROP CONSTRAINT IF EXISTS starter_grant_test_rollback`)
	})
	if got, err := repo.Ensure(context.Background(), user, char, policy); err == nil || got.Status != StarterUnavailable {
		t.Fatal("failed durable starter insert accepted", got, err)
	}
	assertStarterState(t, db, user, 0, 0, 0)
	db.MustExec(`ALTER TABLE quest_license_starters DROP CONSTRAINT starter_grant_test_rollback`)
	if got, err := repo.Ensure(context.Background(), user, char, policy); err != nil || got.Status != StarterPending {
		t.Fatal("retry after rollback failed", got, err)
	}
	assertStarterState(t, db, user, 1, 0, 0)
}

func TestQuestLicenseStarterRepoLegacyClaimDoesNotRequireRemainingAuth(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	starterLegacyCatalog(t, db, 492821212, 1859, 0)
	db.MustExec(`INSERT INTO campaign_rewards_claimed(character_id,reward_id)
		SELECT $1,min(id) FROM campaign_rewards WHERE campaign_id=492821212`, char)
	got, err := repo.Ensure(context.Background(), user, char, policy)
	if err != nil || got.Status != StarterLegacyClaimed {
		t.Fatal("missing authentication history duplicated a claimed legacy item", got, err)
	}
	assertStarterState(t, db, user, 0, 0, 1)
}

func TestQuestLicenseStarterRepoLegacyPendingThenClaimed(t *testing.T) {
	for _, coin := range []bool{false, true} {
		t.Run(fmt.Sprintf("coin=%t", coin), func(t *testing.T) {
			repo, db, user, char, policy := setupStarterRepo(t)
			ctx := context.Background()
			starterLegacyCatalog(t, db, 492821212, 1859, 0)
			var rewardID uint32
			if coin {
				var purchase int64
				if err := db.QueryRow(`INSERT INTO campaign_coin_purchases(user_id,character_id,campaign_id,request_code,price,balance_after)
					VALUES($1,$2,492821212,'test-purchase',1,200) RETURNING id`, user, char).Scan(&purchase); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`INSERT INTO campaign_coin_rewards(purchase_id,item_type,item_id,quantity)
					VALUES($1,7,1859,110) RETURNING id`, purchase).Scan(&rewardID); err != nil {
					t.Fatal(err)
				}
			} else {
				db.MustExec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES(492821212,$1,'test-auth')`, char)
				if err := db.Get(&rewardID, `SELECT min(id) FROM campaign_rewards WHERE campaign_id=492821212`); err != nil {
					t.Fatal(err)
				}
			}
			got, err := repo.Ensure(ctx, user, char, policy)
			if err != nil || got.Status != StarterLegacyPending || got.RewardID != 0 {
				t.Fatal("expired but enumerable legacy reward duplicated", got, err)
			}
			assertStarterState(t, db, user, 0, 0, 0)
			if coin {
				db.MustExec(`UPDATE campaign_coin_rewards SET claimed=true WHERE id=$1`, rewardID)
			} else {
				db.MustExec(`INSERT INTO campaign_rewards_claimed(character_id,reward_id) VALUES($1,$2)`, char, rewardID)
			}
			got, err = repo.Ensure(ctx, user, char, policy)
			if err != nil || got.Status != StarterLegacyClaimed || got.RewardID != 0 {
				t.Fatal("legacy claim not durably suppressed", got, err)
			}
			assertStarterState(t, db, user, 0, 0, 1)
			// The persisted decision prevents a later catalog/history cleanup
			// from manufacturing a fresh one-item benefit for this character.
			db.MustExec(`DELETE FROM campaigns WHERE id=492821212`)
			got, err = repo.Ensure(ctx, user, char, policy)
			if err != nil || got.Status != StarterLegacyClaimed {
				t.Fatal("history cleanup reissued starter", got, err)
			}
			items, err := repo.Pending(ctx, user, char, policy.Key)
			if err != nil || len(items) != 0 {
				t.Fatal(items, err)
			}
			var suppressionID uint32
			if err := db.Get(&suppressionID, `SELECT id FROM quest_license_starters WHERE character_id=$1`, char); err != nil {
				t.Fatal(err)
			}
			if err := repo.Acquire(ctx, user, char, []uint32{suppressionID}); err == nil {
				t.Fatal("suppression row became collectible reward")
			}
		})
	}
}

func TestQuestLicenseStarterRepoIgnoresUnrelatedLegacyItems(t *testing.T) {
	for _, test := range []struct {
		name     string
		campaign uint32
		item     uint16
		stamps   int
		auth     bool
	}{
		{"different-item", 492821212, 1860, 0, true},
		{"different-campaign", 999, 1859, 0, true},
		{"not-authenticated", 492821212, 1859, 0, false},
		{"insufficient-stamps", 492821212, 1859, 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, user, char, policy := setupStarterRepo(t)
			starterLegacyCatalog(t, db, test.campaign, test.item, test.stamps)
			if test.auth {
				db.MustExec(`INSERT INTO campaign_state(campaign_id,character_id,code) VALUES($1,$2,'test-auth')`, test.campaign, char)
			}
			got, err := repo.Ensure(context.Background(), user, char, policy)
			if err != nil || got.Status != StarterPending || got.RewardID == 0 {
				t.Fatal("unrelated/uncollectible reward suppressed starter", got, err)
			}
			assertStarterState(t, db, user, 1, 0, 0)
		})
	}
}

func TestQuestLicenseStarterRepoConcurrentAndSharedReceiptSequence(t *testing.T) {
	repo, db, user, char, policy := setupStarterRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := make(chan QuestLicenseStarterResult, 12)
	errors := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := repo.Ensure(ctx, user, char, policy)
			results <- got
			errors <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uint32
	for got := range results {
		if id == 0 {
			id = got.RewardID
		}
		if got.Status != StarterPending || got.RewardID != id || id < campaignCoinRewardBase {
			t.Fatal("duplicate concurrent receipt", got)
		}
	}
	starterLegacyCatalog(t, db, 999, 100, 0)
	var purchase int64
	if err := db.QueryRow(`INSERT INTO campaign_coin_purchases(user_id,character_id,campaign_id,request_code,price,balance_after)
		VALUES($1,$2,999,'ordinary-test',1,200) RETURNING id`, user, char).Scan(&purchase); err != nil {
		t.Fatal(err)
	}
	var ordinaryID uint32
	if err := db.QueryRow(`INSERT INTO campaign_coin_rewards(purchase_id,item_type,item_id,quantity)
		VALUES($1,7,100,1) RETURNING id`, purchase).Scan(&ordinaryID); err != nil {
		t.Fatal(err)
	}
	if ordinaryID == id || ordinaryID < campaignCoinRewardBase {
		t.Fatal("shared sequence collided with ordinary campaign reward")
	}
	var automaticOwners int
	if err := db.Get(&automaticOwners, `SELECT count(*) FROM pg_depend
		WHERE classid='pg_class'::regclass AND objid='campaign_coin_reward_id_seq'::regclass AND deptype='a'`); err != nil || automaticOwners != 0 {
		t.Fatalf("shared receipt sequence still owned by one ledger: owners=%d err=%v", automaticOwners, err)
	}
	if err := repo.Acquire(ctx, user, char, []uint32{id, ordinaryID}); err == nil {
		t.Fatal("mixed ordinary/starter receipt batch accepted")
	}
	assertStarterState(t, db, user, 1, 0, 0)
	var claims sync.WaitGroup
	claimErrors := make(chan error, 12)
	for i := 0; i < cap(claimErrors); i++ {
		claims.Add(1)
		go func() {
			defer claims.Done()
			claimErrors <- repo.Acquire(ctx, user, char, []uint32{id})
		}()
	}
	claims.Wait()
	close(claimErrors)
	for err := range claimErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertStarterState(t, db, user, 0, 1, 0)
}
