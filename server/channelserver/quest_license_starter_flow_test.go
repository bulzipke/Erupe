package channelserver

import (
	"context"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

// This exercises durable server receipts, not the native client's inventory or
// its save/ack ordering. The production Event House path remains disconnected.
func TestQuestLicenseStarterFlowTenGoldGAndOnePerCharacter(t *testing.T) {
	repo, db, user, character := setupQuestLicenseRepo(t)
	now := repo.now()
	starterRepo := NewQuestLicenseStarterRepository(db)
	licenses, err := NewQuestLicenseServiceWithStarters(true, starterLicenseCatalog(t, now, true), repo, starterRepo)
	if err != nil {
		t.Fatal(err)
	}
	licenses.quotes.now = func() time.Time { return now }
	ctx := context.Background()
	if result, err := licenses.EnsureStarter(ctx, user, character, questlicense.VisaStarterKey); err != nil || result.Status != StarterNotOwned {
		t.Fatal("unowned character received starter", result, err)
	}
	quote, ok, err := licenses.Quote(ctx, user, character, 123)
	if err != nil || !ok || quote.Price != 10 || len(quote.QuestIDs) != 8 || len(quote.Starters) != 1 || quote.Starters[0].ItemID != 1859 || quote.Starters[0].Quantity != 1 {
		t.Fatal(quote, ok, err)
	}
	quests := make([]QuestLicensePreviewQuest, len(quote.QuestIDs))
	for i, id := range quote.QuestIDs {
		quests[i] = QuestLicensePreviewQuest{ID: id, Name: "VISA quest", Conditions: "Original equipment and rank", Publication: "Independent publication"}
	}
	preview, err := encodeQuestLicensePreview(QuestLicensePreview{Quote: quote, Title: "VISA permanent license", Quests: quests})
	if err != nil || string(preview[:4]) != "LPV2" {
		t.Fatal("starter benefit was not included in preview", len(preview), err)
	}
	purchase, err := licenses.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode)
	if err != nil || purchase.Status != QuestLicensePurchaseOK || purchase.BalanceAfter != 190 || purchase.Replay {
		t.Fatal(purchase, err)
	}
	first, err := licenses.EnsureStarter(ctx, user, character, questlicense.VisaStarterKey)
	if err != nil || first.Status != StarterPending || first.RewardID < campaignCoinRewardBase {
		t.Fatal(first, err)
	}
	for range 3 {
		retry, err := licenses.EnsureStarter(ctx, user, character, questlicense.VisaStarterKey)
		if err != nil || retry != first {
			t.Fatal("retry duplicated starter receipt", retry, first, err)
		}
	}
	items, err := licenses.PendingStarter(ctx, user, character, questlicense.VisaStarterKey)
	if err != nil || len(items) != 1 || items[0].ID != first.RewardID || items[0].ItemType != 7 || items[0].ItemID != 1859 || items[0].Quantity != 1 {
		t.Fatal(items, err)
	}
	// Ordinary coin collection may not consume a starter just because both use
	// the same native ID range. Future integration needs a common dispatcher.
	if err := acquireCampaignCoins(ctx, db, user, character, []uint32{first.RewardID}); err == nil {
		t.Fatal("ordinary coin route consumed a starter receipt")
	}
	if err := licenses.AcquireStarters(ctx, user, character, []uint32{first.RewardID}); err != nil {
		t.Fatal(err)
	}
	if err := licenses.AcquireStarters(ctx, user, character, []uint32{first.RewardID}); err != nil {
		t.Fatal("lost ACK replay failed", err)
	}
	claimed, err := licenses.EnsureStarter(ctx, user, character, questlicense.VisaStarterKey)
	if err != nil || claimed.Status != StarterClaimed || claimed.RewardID != first.RewardID {
		t.Fatal("claimed starter was queued again", claimed, err)
	}
	if items, err := licenses.PendingStarter(ctx, user, character, questlicense.VisaStarterKey); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	second := CreateTestCharacter(t, db, user, "visa_second")
	secondGrant, err := licenses.EnsureStarter(ctx, user, second, questlicense.VisaStarterKey)
	if err != nil || secondGrant.Status != StarterPending || secondGrant.RewardID == first.RewardID {
		t.Fatal("second character did not get independent starter", secondGrant, err)
	}
	// Removing the sale preserves the right and starter definition, and does
	// not require a second purchase to queue on a later account character.
	removed, err := NewQuestLicenseServiceWithStarters(true, starterLicenseCatalog(t, now, false), repo, starterRepo)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := removed.PendingStarter(ctx, user, second, questlicense.VisaStarterKey); err != nil || len(rows) != 1 || rows[0].ID != secondGrant.RewardID {
		t.Fatal("sale removal lost pending material", rows, err)
	}
	third := CreateTestCharacter(t, db, user, "visa_third")
	if grant, err := removed.EnsureStarter(ctx, user, third, questlicense.VisaStarterKey); err != nil || grant.Status != StarterPending {
		t.Fatal("sale removal revoked character starter entitlement", grant, err)
	}
	if replay, err := removed.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode); err != nil || !replay.Replay || replay.PurchaseID != purchase.PurchaseID {
		t.Fatal("purchase receipt replay failed", replay, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 190, 1, 1)
	var pending, claims, total int
	if err := db.QueryRow(`SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='claimed'),count(*) FROM quest_license_starters`).Scan(&pending, &claims, &total); err != nil || pending != 2 || claims != 1 || total != 3 {
		t.Fatal(pending, claims, total, err)
	}
	// A later catalog cannot omit the entire starter registry while keeping
	// the same paid right. Its empty policy set must still hit the DB guard,
	// even when constructed with no delivery store through the old API.
	price := uint32(10)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	stripped, err := questlicense.New(questlicense.Document{Version: "stripped-starter", Licenses: licenses.catalog.Definitions(),
		Products: []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{questlicense.VisaLicenseKey}}}})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := NewQuestLicenseService(true, stripped, repo)
	if err != nil {
		t.Fatal(err)
	}
	invalid.quotes.now = func() time.Time { return now }
	if _, ok, err := invalid.Quote(ctx, user, character, 123); err == nil || ok {
		t.Fatal("removing every starter bypassed immutable policy guard", ok, err)
	}
	if owned, err := invalid.HasQuestLicense(ctx, user, 40081); err != nil || !owned {
		t.Fatal("removed starter policy revoked an earned quest right", owned, err)
	}
	if replay, err := invalid.PurchaseQuoted(ctx, user, character, 123, quote.PaymentCode); err != nil || !replay.Replay || replay.PurchaseID != purchase.PurchaseID {
		t.Fatal("removed starter policy blocked committed purchase replay", replay, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 190, 1, 1)
}
