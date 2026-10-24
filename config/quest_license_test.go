package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"erupe-ce/common/questlicense"
)

func licenseConfigFixture(t *testing.T, starter bool) (*Config, string, string) {
	t.Helper()
	price := uint32(10)
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	doc := questlicense.Document{Version: "config-license", Licenses: []questlicense.Definition{{Key: "visa_veil", QuestIDs: []int{40081}}},
		Products: []questlicense.Product{{CampaignID: 123, Enabled: true, Price: &price, StartsAt: &start, EndsAt: &end, LicenseKeys: []string{"visa_veil"}}}}
	if starter {
		doc.Starters = []questlicense.StarterDefinition{{Key: "visa_starter", LicenseKeys: []string{"visa_veil"}, ItemID: 1859, Quantity: 1}}
		doc.Products[0].StarterKeys = []string{"visa_starter"}
	}
	display := questlicense.DisplayDocument{CatalogVersion: doc.Version, Products: []questlicense.DisplayProduct{{CampaignID: 123, Title: "VISA license", Quests: []questlicense.DisplayQuest{{ID: 40081, Name: "VISA quest", Conditions: "Original equipment", Publication: "Independent publication"}}}}}
	root := t.TempDir()
	catalogPath, displayPath := filepath.Join(root, "catalog.json"), filepath.Join(root, "display.json")
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(catalogPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(display)
	if err := os.WriteFile(displayPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return &Config{RealClientMode: ZZ, QuestLicenses: QuestLicenseOptions{Enabled: true, CatalogFile: catalogPath, DisplayFile: displayPath}}, catalogPath, displayPath
}

func TestLoadQuestLicensesOffNeverReadsFiles(t *testing.T) {
	c := &Config{QuestLicenses: QuestLicenseOptions{CatalogFile: "missing", DisplayFile: "missing"}}
	if err := c.LoadQuestLicenses(); err != nil || c.QuestLicenseCatalog != nil || c.QuestLicenseDisplay != nil {
		t.Fatal(err)
	}
	c.QuestLicenses.PurchasesEnabled = true
	if err := c.LoadQuestLicenses(); err == nil {
		t.Fatal("purchases accepted with disabled authority")
	}
}

func TestLoadQuestLicensesDocumentsAreImmutableAndPublicationIndependent(t *testing.T) {
	c, _, p := licenseConfigFixture(t, true)
	if err := c.LoadQuestLicenses(); err != nil || c.QuestLicenseCatalog == nil || c.QuestLicenseDisplay == nil || c.QuestDelivery.Enabled {
		t.Fatal(err)
	}
	old := c.QuestLicenseDisplay
	if err := os.WriteFile(p, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if rows, ok := old.Offer(123); !ok || len(rows.Quests) != 1 {
		t.Fatal("loaded display changed with its source file")
	}
	if err := c.LoadQuestLicenses(); err == nil || c.QuestLicenseCatalog != nil || c.QuestLicenseDisplay != nil {
		t.Fatal("failed reload kept stale documents", err)
	}
}

func TestLoadQuestLicensesPreventsUnverifiedStarterSales(t *testing.T) {
	c, _, _ := licenseConfigFixture(t, true)
	c.QuestLicenses.PurchasesEnabled = true
	if err := c.LoadQuestLicenses(); err == nil || c.QuestLicenseCatalog != nil {
		t.Fatal("starter sold without native save integration", err)
	}
	c, _, _ = licenseConfigFixture(t, false)
	c.QuestLicenses.PurchasesEnabled = true
	if err := c.LoadQuestLicenses(); err != nil {
		t.Fatal("verified right-only backend configuration rejected", err)
	}
}

func TestLoadQuestLicensesRejectsUnsupportedAndOversizedInputs(t *testing.T) {
	for _, kind := range []string{"mode", "missing", "directory", "oversize", "invalid_display", "version"} {
		t.Run(kind, func(t *testing.T) {
			c, p, d := licenseConfigFixture(t, false)
			switch kind {
			case "mode":
				c.RealClientMode = Z2
			case "missing":
				c.QuestLicenses.CatalogFile = "missing"
			case "directory":
				c.QuestLicenses.CatalogFile = t.TempDir()
			case "oversize":
				if err := os.WriteFile(p, make([]byte, questlicense.MaxDocumentBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid_display":
				if err := os.WriteFile(d, []byte(`{"catalog_version":"config-license","products":[],"bad":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "version":
				if err := os.WriteFile(d, []byte(`{"catalog_version":"other","products":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.LoadQuestLicenses(); err == nil || c.QuestLicenseCatalog != nil || c.QuestLicenseDisplay != nil {
				t.Fatal("unsafe config accepted", err)
			}
		})
	}
}
