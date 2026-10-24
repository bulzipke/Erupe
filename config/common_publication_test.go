package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommonPublicationConfigPreparedSameCatalogAndRealLocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(testDeliveryManifest), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{RealClientMode: ZZ, QuestDelivery: QuestDeliveryOptions{Enabled: true, ManifestFile: path}, Entrance: Entrance{Entries: []EntranceServerInfo{{Name: "Brave", Channels: []EntranceChannelInfo{{}}}}}}
	if err := c.LoadQuestDelivery(); err != nil || !c.QuestDeliveryPublication.BoundTo(c.QuestDeliveryCatalog) {
		t.Fatal("catalog/cohort not prepared together", err)
	}
	c.Entrance.Entries[0].Channels = append(c.Entrance.Entries[0].Channels, EntranceChannelInfo{})
	if err := c.LoadQuestDelivery(); err == nil || c.QuestDeliveryCatalog != nil || c.QuestDeliveryPublication != nil {
		t.Fatal("missing enabled channel allowed or stale state kept")
	}
	disabled := false
	c.Entrance.Entries[0].Channels[1].Enabled = &disabled
	if err := c.LoadQuestDelivery(); err != nil || len(c.QuestDeliveryEnabledLocations()) != 1 {
		t.Fatal("disabled channel added to actual location set", err)
	}
}
