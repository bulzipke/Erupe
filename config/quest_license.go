package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	"erupe-ce/common/questlicense"
)

// QuestLicenseOptions enables account authority and the LPV2 preview separately
// from paid purchases. It never changes quest publication, native entry checks,
// character completion, or the lifetime of previously purchased rights.
type QuestLicenseOptions struct {
	Enabled          bool
	CatalogFile      string
	DisplayFile      string
	PurchasesEnabled bool
}

func readQuestLicenseFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("quest license file path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > questlicense.MaxDocumentBytes {
		return nil, fmt.Errorf("quest license input must be a regular file no larger than 4 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, questlicense.MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > questlicense.MaxDocumentBytes {
		return nil, fmt.Errorf("quest license input exceeds 4 MiB")
	}
	return data, nil
}

// LoadQuestLicenses publishes both trusted documents only after full validation.
// Disabled configurations do not read files, and failed reloads discard old data.
func (c *Config) LoadQuestLicenses() error {
	c.QuestLicenseCatalog, c.QuestLicenseDisplay = nil, nil
	if !c.QuestLicenses.Enabled {
		if c.QuestLicenses.PurchasesEnabled {
			return fmt.Errorf("QuestLicenses.PurchasesEnabled requires QuestLicenses.Enabled")
		}
		return nil
	}
	if c.RealClientMode != ZZ {
		return fmt.Errorf("QuestLicenses requires the ZZ client with the matching LPV2 patch")
	}
	data, err := readQuestLicenseFile(c.QuestLicenses.CatalogFile)
	if err != nil {
		return fmt.Errorf("read quest license catalog: %w", err)
	}
	catalog, err := questlicense.Parse(data)
	if err != nil {
		return fmt.Errorf("parse quest license catalog: %w", err)
	}
	data, err = readQuestLicenseFile(c.QuestLicenses.DisplayFile)
	if err != nil {
		return fmt.Errorf("read quest license display: %w", err)
	}
	display, err := questlicense.ParseDisplay(data, catalog)
	if err != nil {
		return fmt.Errorf("parse quest license display: %w", err)
	}
	// Publishing a quote must never sell a starter that native collection/save
	// cannot yet deliver. This gate can be removed only with the verified flow.
	if c.QuestLicenses.PurchasesEnabled && len(catalog.Starters()) > 0 {
		return fmt.Errorf("quest license purchases with starters require native collection/save integration; keep PurchasesEnabled=false")
	}
	c.QuestLicenseCatalog, c.QuestLicenseDisplay = catalog, display
	return nil
}
