package channelserver

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
	cfg "erupe-ce/config"
	"github.com/jmoiron/sqlx"
)

func runtimeLicenseServer(t *testing.T, db *sqlx.DB, catalog *questlicense.Catalog, purchases bool) *Server {
	t.Helper()
	display := shopDisplay(t, catalog, 123)
	return &Server{db: db, erupeConfig: &cfg.Config{
		RealClientMode: cfg.ZZ,
		QuestLicenses: cfg.QuestLicenseOptions{
			Enabled: true, PurchasesEnabled: purchases,
		},
		QuestLicenseCatalog: catalog, QuestLicenseDisplay: display,
	}}
}

func TestQuestLicenseRuntimeDisabledInitializationDoesNotUseDatabase(t *testing.T) {
	ctx := context.Background()
	// Neither invalid/missing documents nor an unusable DB may be touched while
	// the feature is disabled. A zero sqlx.DB would fail if any query were made.
	for _, server := range []*Server{nil, {}, {db: &sqlx.DB{}, erupeConfig: &cfg.Config{}}} {
		if err := server.initializeQuestLicenses(ctx); err != nil {
			t.Fatal("disabled initialization reached a dependency", err)
		}
		if server != nil && (server.questLicenses != nil || server.questLicenseShop != nil || server.questAccess != nil) {
			t.Fatal("disabled initialization published a runtime")
		}
	}
}

func TestQuestLicenseRuntimeWiresImmutableServicesAndAuthorityTargets(t *testing.T) {
	_, db, user, character := setupQuestLicenseRepo(t)
	catalog, err := questlicense.NewVisaCatalog(123)
	if err != nil {
		t.Fatal(err)
	}
	// One legacy Type9 quest is outside the new account catalog, and another
	// overlaps it. Ordinary quest types must not enter the protected roster.
	db.MustExec(`INSERT INTO event_quests(quest_type,quest_id) VALUES(9,49999),(9,40081),(8,49998)`)
	server := runtimeLicenseServer(t, db, catalog, false)
	if err := server.initializeQuestLicenses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if server.questLicenses == nil || server.questLicenseShop == nil || server.questAccess == nil ||
		!server.questLicenses.enabled || server.questLicenses.catalog != catalog ||
		server.questLicenseShop.licenses != server.questLicenses || server.questLicenseShop.display != server.erupeConfig.QuestLicenseDisplay {
		t.Fatal("initialization did not publish one immutable backend")
	}
	store, ok := server.questLicenses.store.(*QuestLicenseRepository)
	if !ok || store.db != db {
		t.Fatal("account authority store uses a different database")
	}
	starters, ok := server.questLicenses.starters.(*QuestLicenseStarterRepository)
	if !ok || starters.db != db {
		t.Fatal("starter store uses a different database")
	}
	ownership, ok := server.questLicenseShop.ownership.(*QuestLicensePreviewRepository)
	if !ok || ownership.db != db {
		t.Fatal("preview ownership uses a different database")
	}
	access, ok := server.questAccess.loader.(*QuestAccessService)
	if !ok || access.licenses != server.questLicenses {
		t.Fatal("gameplay authority uses a different license service")
	}
	want := append(catalog.QuestIDsForOffer(123), 49999)
	slices.Sort(want)
	if !slices.Equal(server.questAccess.questIDs, want) || server.questAccess.requires(49998) {
		t.Fatal("legacy Type9 and catalog targets were not combined exactly", server.questAccess.questIDs)
	}
	for _, id := range want {
		if !server.questAccess.requires(id) {
			t.Fatal("protected authority target missing", id)
		}
	}
	if server.questLicenseEvents(time.Now()) != nil {
		t.Fatal("sale-off runtime exposed a native purchase row")
	}
	var definitions, policies, receipts int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM quest_license_definitions),
		(SELECT count(*) FROM quest_license_starter_policies),(SELECT count(*) FROM quest_license_purchases)`).
		Scan(&definitions, &policies, &receipts); err != nil || definitions != 0 || policies != 0 || receipts != 0 {
		t.Fatal("runtime initialization wrote a permanent contract or purchase", definitions, policies, receipts, err)
	}
	// Returned accessors cannot alter the retained catalog or display document.
	returned := catalog.Definitions()
	returned[0].QuestIDs[0] = 1
	products := server.erupeConfig.QuestLicenseDisplay.Products()
	products[0].Quests[0].Name = "changed"
	if !server.questAccess.requires(40081) || server.questAccess.requires(1) || catalog.Definitions()[0].QuestIDs[0] != 40081 {
		t.Fatal("caller mutation altered the runtime contract")
	}
	fresh, _ := server.questLicenseShop.display.Offer(123)
	if fresh.Quests[0].Name != "VISA quest" {
		t.Fatal("caller mutation reached runtime display text")
	}
	// Verify the installed gameplay loader actually combines legacy character
	// authentication with account authority; matching roster counts alone would
	// miss wiring a loader to the wrong license service or database.
	var now time.Time
	if err := db.Get(&now, `SELECT clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	insertQuestAccessCampaign(t, db, 900, 49999, 9, 1, now.Add(-time.Hour), now.Add(time.Hour), character, 1)
	db.MustExec(`INSERT INTO quest_licenses(user_id,license_key) VALUES($1,$2)`, user, questlicense.VisaLicenseKey)
	session := &Session{server: server, userID: user, charID: character}
	if err := session.refreshQuestAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := session.questAccessSnapshot.Load()
	if snapshot == nil || snapshot.AccountReadError() != nil || snapshot.ObservedAt().IsZero() {
		t.Fatal("runtime loader returned no verified authority", snapshot)
	}
	for _, id := range want {
		if !session.allowsQuestAccess(id, snapshot.ObservedAt()) {
			t.Fatal("installed legacy/account authority was not applied", id)
		}
	}
	// Runtime Type9 edits are deliberately deferred to the next server startup.
	db.MustExec(`INSERT INTO event_quests(quest_type,quest_id) VALUES(9,50000)`)
	if server.questAccess.requires(50000) || !slices.Equal(server.questAccess.questIDs, want) {
		t.Fatal("startup-frozen roster changed after a live DB edit")
	}
}

func TestQuestLicenseRuntimeRejectsLegacyIDCollisionsIncludingDisabledOffers(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			_, db, _, _ := setupQuestLicenseRepo(t)
			catalog := serviceLicenseCatalog(t, enabled)
			db.MustExec(`INSERT INTO campaigns(id) VALUES(123)`)
			server := runtimeLicenseServer(t, db, catalog, false)
			err := server.initializeQuestLicenses(context.Background())
			if err == nil || !strings.Contains(err.Error(), "collides") {
				t.Fatal("legacy campaign ID collision was not rejected", enabled, err)
			}
			if server.questLicenses != nil || server.questLicenseShop != nil || server.questAccess != nil {
				t.Fatal("failed initialization published part of the runtime")
			}
		})
	}
}

func TestQuestLicenseRuntimeSaleRequiresCategorySixTypeOne(t *testing.T) {
	for _, test := range []struct {
		name         string
		categoryID   int
		categoryType int
		allowed      bool
	}{
		{"missing", 0, 0, false},
		{"other category", 7, 1, false},
		{"special tent category six", 6, 0, false},
		{"premium category six", 6, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, db, _, _ := setupQuestLicenseRepo(t)
			if test.categoryID != 0 {
				db.MustExec(`INSERT INTO campaign_categories(id,type,title,description) VALUES($1,$2,'Fixture','Fixture')`, test.categoryID, test.categoryType)
			}
			server := runtimeLicenseServer(t, db, serviceLicenseCatalog(t, true), true)
			err := server.initializeQuestLicenses(context.Background())
			if test.allowed {
				if err != nil || server.questLicenses == nil || server.questLicenseShop == nil || server.questAccess == nil {
					t.Fatal("existing premium category six did not allow initialization", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "category6") {
				t.Fatal("purchase runtime accepted the wrong category", err)
			}
		})
	}
}

func TestQuestLicenseRuntimeRejectsStarterSalesBeforeDatabaseRead(t *testing.T) {
	catalog, err := questlicense.NewVisaCatalog(123)
	if err != nil {
		t.Fatal(err)
	}
	server := runtimeLicenseServer(t, &sqlx.DB{}, catalog, true)
	if err := server.initializeQuestLicenses(context.Background()); err == nil || !strings.Contains(err.Error(), "collection/save") {
		t.Fatal("native starter delivery gate was not enforced before querying DB", err)
	}
}
