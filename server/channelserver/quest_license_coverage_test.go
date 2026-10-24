package channelserver

import (
	"context"
	"sync"
	"testing"

	"erupe-ce/common/questlicense"
)

func TestQuestLicenseCoverageRejectsAlternativeOwnedRights(t *testing.T) {
	for _, test := range []struct {
		name   string
		owned  []string
		status QuestLicensePurchaseStatus
	}{
		{"fully-covered-by-other-keys", []string{"single_f", "single_fz"}, QuestLicensePurchaseAlreadyOwned},
		{"partly-covered-by-other-key", []string{"single_f"}, QuestLicensePurchasePartialOwned},
		{"unrelated-owned-right", []string{"other"}, QuestLicensePurchaseOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, user, char := setupQuestLicenseRepo(t)
			definitions := []questlicense.Definition{
				{Key: "series", QuestIDs: []int{40142, 40143}},
				{Key: "single_f", QuestIDs: []int{40142}}, {Key: "single_fz", QuestIDs: []int{40143}},
				{Key: "other", QuestIDs: []int{40161}},
			}
			if err := repo.BindDefinitions(context.Background(), definitions); err != nil {
				t.Fatal(err)
			}
			for _, key := range test.owned {
				db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,$2)`, user, key)
			}
			request, sale := questLicenseTestPurchase(user, char)
			request.LicenseKeys, sale.LicenseKeys = []string{"series"}, []string{"series"}
			got, err := repo.Purchase(context.Background(), request, sale)
			if err != nil || got.Status != test.status {
				t.Fatal("different license keys hid existing quest coverage", got, err)
			}
			if test.status == QuestLicensePurchaseOK {
				assertQuestLicensePurchaseState(t, db, user, 150, 1, len(test.owned)+1)
			} else {
				assertQuestLicensePurchaseState(t, db, user, 200, 0, len(test.owned))
			}
		})
	}
}

func TestQuestLicenseCoverageSerializesDifferentOverlappingKeys(t *testing.T) {
	repo, db, user, firstChar := setupQuestLicenseRepo(t)
	secondChar := CreateTestCharacter(t, db, user, "coverage")
	if err := repo.BindDefinitions(context.Background(), []questlicense.Definition{
		{Key: "first", QuestIDs: []int{40142, 40143}},
		{Key: "second", QuestIDs: []int{40143, 40161}},
	}); err != nil {
		t.Fatal(err)
	}
	requests := make([]QuestLicensePurchaseRequest, 2)
	sales := make([]QuestLicenseSale, 2)
	for i, key := range []string{"first", "second"} {
		requests[i], sales[i] = questLicenseTestPurchase(user, []uint32{firstChar, secondChar}[i])
		requests[i].LicenseKeys, sales[i].LicenseKeys = []string{key}, []string{key}
		requests[i].CampaignID, sales[i].CampaignID = uint32(123+i), uint32(123+i)
		requests[i].RequestCode = "coverage-" + key
	}
	type outcome struct {
		result QuestLicensePurchaseResult
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range requests {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			got, err := repo.Purchase(context.Background(), requests[index], sales[index])
			results <- outcome{got, err}
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	statuses := make(map[QuestLicensePurchaseStatus]int)
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		statuses[got.result.Status]++
	}
	if statuses[QuestLicensePurchaseOK] != 1 || statuses[QuestLicensePurchasePartialOwned] != 1 || len(statuses) != 2 {
		t.Fatal("overlapping aliases were both charged", statuses)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 1)
}

func TestQuestLicenseCoverageRejectsPartlyRegisteredContract(t *testing.T) {
	repo, db, user, char := setupQuestLicenseRepo(t)
	if err := repo.BindDefinitions(context.Background(), []questlicense.Definition{{Key: "known", QuestIDs: []int{40142}}}); err != nil {
		t.Fatal(err)
	}
	request, sale := questLicenseTestPurchase(user, char)
	request.LicenseKeys, sale.LicenseKeys = []string{"known", "unknown"}, []string{"known", "unknown"}
	got, err := repo.Purchase(context.Background(), request, sale)
	if err != nil || got.Status != QuestLicensePurchaseUnavailable {
		t.Fatal("incomplete persistent contract was sold", got, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
}
