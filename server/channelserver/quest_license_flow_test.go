package channelserver

import (
	"context"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

// This proves the local service contracts together, not a gameplay packet, a
// client's equipment/ticket checks, or a native campaign collection flow.
func TestQuestLicenseFlowPreviewPurchaseAndIndependentAccess(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	catalog := quotedLicenseDBCatalog(t, repo.now(), true)
	licenses := quotedLicenseDBService(t, catalog, repo, repo.now())
	access, err := NewQuestAccessService(NewQuestAccessRepository(db), licenses)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := access.Load(ctx, user, character, []int{40079, 40142})
	if err != nil || before.Allows(40079, before.ObservedAt()) || before.Allows(40142, before.ObservedAt()) {
		t.Fatal("unowned account was authorized", before, err)
	}
	quote := quotedLicenseDBPreview(t, licenses, user, character)
	encoded, err := encodeQuestLicensePreview(QuestLicensePreview{Quote: quote, Title: "Local review license",
		Quests: []QuestLicensePreviewQuest{
			{ID: 40079, Name: "Quest 40079", Conditions: "Original equipment required", Publication: "Separate publication policy"},
			{ID: 40142, Name: "Quest 40142", Conditions: "Original progression required", Publication: "Separate publication policy"},
		}})
	if err != nil || len(encoded) < questLicensePreviewFixedBytes || len(encoded) > questLicensePreviewMaxBytes || string(encoded[:4]) != "LPV1" {
		t.Fatal("typed preview failed", len(encoded), err)
	}
	purchase, err := licenses.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
	if err != nil || purchase.Status != QuestLicensePurchaseOK || purchase.Replay {
		t.Fatal(purchase, err)
	}
	after, err := access.Load(ctx, user, character, []int{40079, 40142})
	if err != nil || after.AccountReadError() != nil || !after.Allows(40079, after.ObservedAt()) || !after.Allows(40142, after.ObservedAt().AddDate(5, 0, 0)) {
		t.Fatal("account license did not reach consistent access snapshot", after, err)
	}
	if before.Allows(40079, after.ObservedAt()) || after.Allows(40079, before.ObservedAt()) {
		t.Fatal("purchase rewrote an old snapshot or authorized an earlier admission")
	}
	second := CreateTestCharacter(t, db, user, "flowsecond")
	shared, err := access.Load(ctx, user, second, []int{40079, 40142})
	if err != nil || !shared.Allows(40142, shared.ObservedAt()) {
		t.Fatal("account ownership was not shared with its second character", shared, err)
	}
	// A new service may stop selling products while preserving every bound
	// permanent definition and its existing ownership. A stale committed token
	// remains replayable without native item or legacy completion records.
	removed := quotedLicenseDBService(t, quotedLicenseDBCatalog(t, repo.now(), false), repo, repo.now().Add(24*time.Hour))
	access, _ = NewQuestAccessService(NewQuestAccessRepository(db), removed)
	retained, err := access.Load(ctx, user, second, []int{40079, 40142})
	if err != nil || !retained.Allows(40142, retained.ObservedAt()) {
		t.Fatal("removing a sale revoked permanent rights", retained, err)
	}
	replay, err := removed.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
	if err != nil || !replay.Replay || replay.PurchaseID != purchase.PurchaseID {
		t.Fatal("removed sale lost its committed receipt", replay, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)

	// Reusing a purchased key for another quest cannot become a new contract.
	changed, err := questlicense.New(questlicense.Document{Version: "changed-definition", Licenses: []questlicense.Definition{
		{Key: "gothic", QuestIDs: []int{40080}}, {Key: "veil", QuestIDs: []int{40142}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	invalid, _ := NewQuestLicenseService(true, changed, repo)
	access, _ = NewQuestAccessService(NewQuestAccessRepository(db), invalid)
	blocked, err := access.Load(ctx, user, character, []int{40080, 40142})
	if err != nil || blocked.AccountReadError() == nil || blocked.Allows(40080, blocked.ObservedAt()) || blocked.Allows(40142, blocked.ObservedAt()) {
		t.Fatal("definition mismatch supplied account rights", blocked, err)
	}
	if _, err := invalid.HasQuestLicense(ctx, user, 40080); err == nil {
		t.Fatal("changed definition silently remapped an earned license")
	}
	assertQuestLicensePurchaseState(t, db, user, 150, 1, 2)
}
