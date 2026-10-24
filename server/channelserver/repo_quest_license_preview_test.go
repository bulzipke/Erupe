package channelserver

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

func TestQuestLicensePreviewRepoCoverageAndAuthentication(t *testing.T) {
	licenses, db, user, character := setupQuestLicenseRepo(t)
	ctx := context.Background()
	if err := licenses.BindDefinitions(ctx, []questlicense.Definition{
		{Key: "target", QuestIDs: []int{40081, 40142}},
		{Key: "alias_one", QuestIDs: []int{40081}},
		{Key: "alias_two", QuestIDs: []int{40142}},
	}); err != nil {
		t.Fatal(err)
	}
	repo := NewQuestLicensePreviewRepository(db)
	check := func(want QuestLicenseOwnership) {
		t.Helper()
		got, authenticated, err := repo.PreviewOwnership(ctx, user, character, []string{"target"})
		if err != nil || !authenticated || got != want {
			t.Fatalf("ownership=%v authenticated=%v err=%v want %v", got, authenticated, err, want)
		}
	}
	check(QuestLicenseOwnershipNone)
	db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,'alias_one')`, user)
	check(QuestLicenseOwnershipPartial)
	db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,'alias_two')`, user)
	check(QuestLicenseOwnershipOwned)
	other := CreateTestUser(t, db, "preview_other")
	foreign := CreateTestCharacter(t, db, other, "preview_foreign")
	for _, pair := range [][2]uint32{{user, foreign}, {other, character}, {0, character}, {user, 0}, {user, 1 << 31}} {
		got, authenticated, err := repo.PreviewOwnership(ctx, pair[0], pair[1], []string{"target"})
		if err != nil || authenticated || got != QuestLicenseOwnershipNone {
			t.Fatal("unauthenticated ownership exposed", got, authenticated, err)
		}
	}
	for _, keys := range [][]string{{"missing"}, {"target", "missing"}} {
		if _, authenticated, err := repo.PreviewOwnership(ctx, user, character, keys); err == nil || authenticated {
			t.Fatal("incomplete registry accepted", keys, authenticated, err)
		}
	}
	db.MustExec(`UPDATE characters SET deleted=true WHERE id=$1`, character)
	if _, authenticated, err := repo.PreviewOwnership(ctx, user, character, []string{"target"}); err != nil || authenticated {
		t.Fatal("deleted character authenticated", authenticated, err)
	}
	var balance, trial, rights, receipts int
	if err := db.QueryRow(`SELECT gacha_premium,gacha_trial,
		(SELECT count(*) FROM quest_licenses WHERE user_id=$1),
		(SELECT count(*) FROM quest_license_purchases WHERE user_id=$1) FROM users WHERE id=$1`, user).
		Scan(&balance, &trial, &rights, &receipts); err != nil || balance != 200 || trial != 77 || rights != 2 || receipts != 0 {
		t.Fatal("preview modified account state", balance, trial, rights, receipts, err)
	}
}

func TestQuestLicensePreviewRepoUnavailableBoundaries(t *testing.T) {
	var repo *QuestLicensePreviewRepository
	if _, authenticated, err := repo.PreviewOwnership(context.Background(), 0, 2, []string{"right"}); err != nil || authenticated {
		t.Fatal("invalid identity reached storage", authenticated, err)
	}
	if _, authenticated, err := repo.PreviewOwnership(context.Background(), 1, 2, []string{"right"}); err == nil || authenticated {
		t.Fatal("nil repository authenticated a preview", authenticated, err)
	}
}

func TestQuestLicensePreviewRepoShopFlow(t *testing.T) {
	licenses, db, user, character := setupQuestLicenseRepo(t)
	ctx := context.Background()
	now := licenses.now()
	base := starterLicenseCatalog(t, now, true)
	definitions := append(base.Definitions(), questlicense.Definition{Key: "partial_alias", QuestIDs: []int{40081}})
	price := uint32(10)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	catalog, err := questlicense.New(questlicense.Document{Version: "shop-db", Licenses: definitions, Starters: base.Starters(),
		Products: []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end,
			LicenseKeys: []string{questlicense.VisaLicenseKey}, StarterKeys: []string{questlicense.VisaStarterKey}}}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewQuestLicenseServiceWithStarters(true, catalog, licenses, NewQuestLicenseStarterRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	service.quotes.now = func() time.Time { return now }
	shop, err := NewQuestLicenseShop(service, NewQuestLicensePreviewRepository(db), shopDisplay(t, catalog, 123))
	if err != nil {
		t.Fatal(err)
	}
	preview := func(account, char uint32, want QuestLicenseOwnership) string {
		t.Helper()
		data, err := shop.Preview(ctx, account, char, 123)
		if err != nil || len(data) < 42 || string(data[:4]) != "LPV2" || data[8] != byte(want) ||
			binary.BigEndian.Uint16(data[14:16]) != 10 || binary.BigEndian.Uint16(data[16:18]) != 8 {
			t.Fatal("incorrect durable ownership preview", len(data), err)
		}
		return string(data[22:38])
	}
	code := preview(user, character, QuestLicenseOwnershipNone)
	assertQuestLicensePurchaseState(t, db, user, 200, 0, 0)
	if status, err := shop.Apply(ctx, user, character, 123, code); err != nil || status != questlicense.WireOK {
		t.Fatal(status, err)
	}
	owned := preview(user, character, QuestLicenseOwnershipOwned)
	if status, err := shop.Apply(ctx, user, character, 123, owned); err != nil || status != questlicense.WireAlreadyOwned {
		t.Fatal("fresh owned preview was charged", status, err)
	}
	if status, err := shop.Apply(ctx, user, character, 123, code); err != nil || status != questlicense.WireOK {
		t.Fatal("committed shop retry failed", status, err)
	}
	assertQuestLicensePurchaseState(t, db, user, 190, 1, 1)
	other := CreateTestUser(t, db, "shop_partial")
	otherChar := CreateTestCharacter(t, db, other, "partial_char")
	db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,'partial_alias')`, other)
	partial := preview(other, otherChar, QuestLicenseOwnershipPartial)
	if status, err := shop.Apply(ctx, other, otherChar, 123, partial); err != nil || status != questlicense.WirePartialOwned {
		t.Fatal("partial alias bundle was charged", status, err)
	}
	var receipts int
	if err := db.Get(&receipts, `SELECT count(*) FROM quest_license_purchases WHERE user_id=$1`, other); err != nil || receipts != 0 {
		t.Fatal("partial bundle created a payment receipt", receipts, err)
	}
}
