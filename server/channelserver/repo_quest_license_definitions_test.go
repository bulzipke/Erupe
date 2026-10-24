package channelserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
	"github.com/jmoiron/sqlx"
)

func TestQuestLicenseDefinitionsCanonical(t *testing.T) {
	input := []questlicense.Definition{{Key: "veil", QuestIDs: []int{40142, 40081}}, {Key: "gothic", QuestIDs: []int{40079}}}
	got, err := canonicalQuestLicenseDefinitions(input)
	if err != nil || len(got) != 2 || got[0].Key != "gothic" || got[1].Key != "veil" ||
		!slices.Equal([]int64(got[1].QuestIDs), []int64{40081, 40142}) || len(got[1].Hash) != 64 {
		t.Fatalf("canonical definitions: %+v %v", got, err)
	}
	if input[0].Key != "veil" || !slices.Equal(input[0].QuestIDs, []int{40142, 40081}) {
		t.Fatal("canonical binding mutated caller catalog")
	}
	reordered, err := canonicalQuestLicenseDefinitions([]questlicense.Definition{
		{Key: "gothic", QuestIDs: []int{40079}}, {Key: "veil", QuestIDs: []int{40081, 40142}},
	})
	if err != nil || reordered[1].Hash != got[1].Hash {
		t.Fatal("definition order changed binding identity")
	}
	changed, err := canonicalQuestLicenseDefinitions([]questlicense.Definition{{Key: "veil", QuestIDs: []int{40081, 40143}}})
	if err != nil || changed[0].Hash == got[1].Hash {
		t.Fatal("mapping change did not change binding identity")
	}
	maxDefinitions := make([]questlicense.Definition, questlicense.MaxDefinitions)
	for i := range maxDefinitions {
		maxDefinitions[i] = questlicense.Definition{Key: fmt.Sprintf("key_%d", i), QuestIDs: []int{1}}
	}
	if _, err := canonicalQuestLicenseDefinitions(maxDefinitions); err != nil {
		t.Fatal("maximum definitions rejected", err)
	}
	maxIDs := make([]int, questlicense.MaxQuestMappings)
	for i := range maxIDs {
		maxIDs[i] = i + 1
	}
	if _, err := canonicalQuestLicenseDefinitions([]questlicense.Definition{{Key: strings.Repeat("a", 64), QuestIDs: maxIDs}}); err != nil {
		t.Fatal("maximum mapping count/key length rejected", err)
	}
}

func TestQuestLicenseDefinitionsValidation(t *testing.T) {
	tooManyDefinitions := make([]questlicense.Definition, questlicense.MaxDefinitions+1)
	tooManyMappings := make([]int, questlicense.MaxQuestMappings+1)
	for i := range tooManyMappings {
		tooManyMappings[i] = i + 1
	}
	for _, test := range []struct {
		name string
		defs []questlicense.Definition
	}{
		{"empty-key", []questlicense.Definition{{Key: "", QuestIDs: []int{1}}}},
		{"uppercase", []questlicense.Definition{{Key: "Veil", QuestIDs: []int{1}}}},
		{"leading-digit", []questlicense.Definition{{Key: "1veil", QuestIDs: []int{1}}}},
		{"dot", []questlicense.Definition{{Key: "visa.veil", QuestIDs: []int{1}}}},
		{"colon", []questlicense.Definition{{Key: "visa:veil", QuestIDs: []int{1}}}},
		{"large-key", []questlicense.Definition{{Key: strings.Repeat("a", 65), QuestIDs: []int{1}}}},
		{"duplicate-key", []questlicense.Definition{{Key: "veil", QuestIDs: []int{1}}, {Key: "veil", QuestIDs: []int{2}}}},
		{"empty-mapping", []questlicense.Definition{{Key: "veil"}}},
		{"zero-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{0}}}},
		{"negative-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{-1}}}},
		{"large-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{65536}}}},
		{"duplicate-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{1, 1}}}},
		{"too-many-definitions", tooManyDefinitions},
		{"too-many-mappings", []questlicense.Definition{{Key: "veil", QuestIDs: tooManyMappings}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := canonicalQuestLicenseDefinitions(test.defs); err == nil {
				t.Fatal("invalid definition accepted")
			}
			var unavailable *QuestLicenseRepository
			if err := unavailable.BindDefinitions(context.Background(), test.defs); err == nil {
				t.Fatal("invalid definition accepted through repository")
			}
		})
	}
	for _, repo := range []*QuestLicenseRepository{nil, NewQuestLicenseRepository(nil)} {
		if err := repo.BindDefinitions(context.Background(), nil); err == nil {
			t.Fatal("unavailable repository accepted")
		}
	}
}

func readQuestLicenseDefinitionBindings(t *testing.T, db *sqlx.DB) []questLicenseDefinitionBinding {
	t.Helper()
	var bindings []questLicenseDefinitionBinding
	if err := db.Select(&bindings, `SELECT license_key,quest_ids,mapping_hash FROM quest_license_definitions ORDER BY license_key`); err != nil {
		t.Fatal(err)
	}
	return bindings
}

func TestQuestLicenseDefinitionsRepoAppendOnly(t *testing.T) {
	repo, db, user, _ := setupQuestLicenseRepo(t)
	ctx := context.Background()
	base := []questlicense.Definition{{Key: "veil", QuestIDs: []int{40142, 40081}}}
	if err := repo.BindDefinitions(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := repo.BindDefinitions(ctx, []questlicense.Definition{{Key: "veil", QuestIDs: []int{40081, 40142}}}); err != nil {
		t.Fatal("identical canonical binding rejected", err)
	}
	for _, test := range []struct {
		name string
		defs []questlicense.Definition
	}{
		{"missing", nil},
		{"removed-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{40081}}}},
		{"added-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{40081, 40142, 40143}}}},
		{"changed-id", []questlicense.Definition{{Key: "veil", QuestIDs: []int{40081, 40143}}}},
		{"new-key-with-missing-old", []questlicense.Definition{{Key: "new_key", QuestIDs: []int{1}}}},
		{"new-key-with-changed-old", []questlicense.Definition{{Key: "new_key", QuestIDs: []int{1}}, {Key: "veil", QuestIDs: []int{2}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := repo.BindDefinitions(ctx, test.defs); err == nil {
				t.Fatal("registered mapping changed or disappeared")
			}
			registered := readQuestLicenseDefinitionBindings(t, db)
			if len(registered) != 1 || registered[0].Key != "veil" || !slices.Equal([]int64(registered[0].QuestIDs), []int64{40081, 40142}) {
				t.Fatalf("rejected bind changed registry: %+v", registered)
			}
		})
	}
	appended := append(slices.Clone(base), questlicense.Definition{Key: "gothic", QuestIDs: []int{40079}})
	if err := repo.BindDefinitions(ctx, appended); err != nil {
		t.Fatal("new independent key rejected", err)
	}
	if len(readQuestLicenseDefinitionBindings(t, db)) != 2 {
		t.Fatal("new key not registered")
	}
	// Registering definitions is not an entitlement or a currency operation.
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
}

func TestQuestLicenseDefinitionsRepoRollback(t *testing.T) {
	repo, db, user, _ := setupQuestLicenseRepo(t)
	ctx := context.Background()
	base := []questlicense.Definition{{Key: "base", QuestIDs: []int{1}}}
	if err := repo.BindDefinitions(ctx, base); err != nil {
		t.Fatal(err)
	}
	db.MustExec(`ALTER TABLE quest_license_definitions ADD CONSTRAINT quest_license_definition_test_rollback CHECK (license_key <> 'z_failure')`)
	t.Cleanup(func() {
		db.MustExec(`ALTER TABLE quest_license_definitions DROP CONSTRAINT IF EXISTS quest_license_definition_test_rollback`)
	})
	definitions := append(slices.Clone(base), questlicense.Definition{Key: "a_new", QuestIDs: []int{2}}, questlicense.Definition{Key: "z_failure", QuestIDs: []int{3}})
	if err := repo.BindDefinitions(ctx, definitions); err == nil {
		t.Fatal("forced second-insert failure accepted")
	}
	registered := readQuestLicenseDefinitionBindings(t, db)
	if len(registered) != 1 || registered[0].Key != "base" {
		t.Fatalf("failed binding retained a partial registration: %+v", registered)
	}
	db.MustExec(`ALTER TABLE quest_license_definitions DROP CONSTRAINT quest_license_definition_test_rollback`)
	if err := repo.BindDefinitions(ctx, definitions); err != nil {
		t.Fatal("retry after rollback failed", err)
	}
	if len(readQuestLicenseDefinitionBindings(t, db)) != 3 {
		t.Fatal("rolled-back keys could not be registered later")
	}
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
}

func TestQuestLicenseDefinitionsRepoConcurrentBind(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%t", conflict), func(t *testing.T) {
			repo, db, user, _ := setupQuestLicenseRepo(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			errors := make(chan error, 2)
			var workers sync.WaitGroup
			for i := range 2 {
				definitions := []questlicense.Definition{{Key: "shared", QuestIDs: []int{1}}}
				if conflict {
					definitions[0].QuestIDs = []int{i + 1}
					definitions = append(definitions, questlicense.Definition{Key: fmt.Sprintf("extra_%d", i), QuestIDs: []int{3}})
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					errors <- repo.BindDefinitions(ctx, definitions)
				}()
			}
			workers.Wait()
			close(errors)
			failures := 0
			for err := range errors {
				if err != nil {
					failures++
				}
			}
			wantFailures, wantDefinitions := 0, 1
			if conflict {
				wantFailures, wantDefinitions = 1, 2
			}
			if failures != wantFailures || len(readQuestLicenseDefinitionBindings(t, db)) != wantDefinitions {
				t.Fatalf("concurrent registry: failures=%d want %d, registry=%+v", failures, wantFailures, readQuestLicenseDefinitionBindings(t, db))
			}
			assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
		})
	}
}
