package config

import (
	"os"
	"path/filepath"
	"testing"
)

const testDeliveryManifest = `{"version":"test","profiles":[{"id":"general","worlds":["Brave"],"channels":[1]}],"schedules":[{"id":"always"}],"quests":[{"quest_id":53140,"profiles":["general"],"schedule":"always"}]}`

func TestLoadQuestDeliveryOptInAndExistingWorlds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "delivery.json")
	if err := os.WriteFile(p, []byte(testDeliveryManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	worlds := []EntranceServerInfo{{Name: "Brave", Channels: []EntranceChannelInfo{{}}}}
	c := &Config{RealClientMode: ZZ, Entrance: Entrance{Entries: worlds}, QuestDelivery: QuestDeliveryOptions{ManifestFile: "does-not-exist"}}
	if err := c.LoadQuestDelivery(); err != nil || c.QuestDeliveryCatalog != nil {
		t.Fatalf("disabled feature changed legacy config: %v", err)
	}
	c.QuestDelivery = QuestDeliveryOptions{Enabled: true, ManifestFile: p}
	if err := c.LoadQuestDelivery(); err != nil || c.QuestDeliveryCatalog == nil {
		t.Fatalf("load: %v", err)
	}
	if c.QuestDeliveryCatalog.Version() != "test" {
		t.Fatal("wrong snapshot")
	}
	if err := os.WriteFile(p, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c.QuestDeliveryCatalog.Version() != "test" {
		t.Fatal("loaded manifest was not immutable")
	}
	if err := c.LoadQuestDelivery(); err == nil || c.QuestDeliveryCatalog != nil {
		t.Fatal("invalid manifest kept a stale catalog")
	}
}

func TestQuestDeliveryRejectsUnavailableLocations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "delivery.json")
	if err := os.WriteFile(p, []byte(testDeliveryManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	disabled := false
	cases := []struct {
		name   string
		mode   Mode
		worlds []EntranceServerInfo
	}{
		{"unknown world", ZZ, []EntranceServerInfo{{Name: "Other", Channels: []EntranceChannelInfo{{}}}}},
		{"no channel", ZZ, []EntranceServerInfo{{Name: "Brave"}}},
		{"disabled channel", ZZ, []EntranceServerInfo{{Name: "Brave", Channels: []EntranceChannelInfo{{Enabled: &disabled}}}}},
		{"duplicate world", ZZ, []EntranceServerInfo{{Name: "Brave", Channels: []EntranceChannelInfo{{}}}, {Name: "Brave", Channels: []EntranceChannelInfo{{}}}}},
		{"unverified client mode", Z2, []EntranceServerInfo{{Name: "Brave", Channels: []EntranceChannelInfo{{}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{RealClientMode: tc.mode, Entrance: Entrance{Entries: tc.worlds}, QuestDelivery: QuestDeliveryOptions{Enabled: true, ManifestFile: p}}
			if err := c.LoadQuestDelivery(); err == nil {
				t.Fatal("expected unavailable configuration to be rejected")
			}
		})
	}
}
