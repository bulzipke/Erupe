package config

import (
	"fmt"
	"os"
	"strings"

	"erupe-ce/common/questdelivery"
)

// QuestDeliveryOptions routes existing event quests without adding worlds or
// channels. The default is disabled and retains legacy delivery behavior.
type QuestDeliveryOptions struct {
	Enabled      bool
	ManifestFile string
}

// LoadQuestDelivery loads a single immutable snapshot shared by the channel
// servers. Updating the file requires a deliberate server restart; a partial
// file edit can never change half of an enumeration or the public calendar.
func (c *Config) LoadQuestDelivery() error {
	c.QuestDeliveryCatalog = nil
	c.QuestDeliveryPublication = nil
	if _, err := c.EffectiveEventQuestCapacity(); err != nil {
		return err
	}
	if !c.QuestDelivery.Enabled {
		return nil
	}
	if c.RealClientMode != ZZ {
		return fmt.Errorf("QuestDelivery currently requires the ZZ client with validated quest departure setup")
	}
	if strings.TrimSpace(c.QuestDelivery.ManifestFile) == "" {
		return fmt.Errorf("QuestDelivery.ManifestFile is required when QuestDelivery.Enabled is true")
	}
	info, err := os.Stat(c.QuestDelivery.ManifestFile)
	if err != nil {
		return fmt.Errorf("stat quest delivery manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return fmt.Errorf("quest delivery manifest must be a regular file no larger than 16 MiB")
	}
	data, err := os.ReadFile(c.QuestDelivery.ManifestFile)
	if err != nil {
		return fmt.Errorf("read quest delivery manifest: %w", err)
	}
	catalog, err := questdelivery.Parse(data)
	if err != nil {
		return fmt.Errorf("parse quest delivery manifest: %w", err)
	}
	if err := c.ValidateQuestDeliveryWorlds(catalog); err != nil {
		return err
	}
	publication, err := questdelivery.BuildCommonPublicationCohort(catalog, c.QuestDeliveryEnabledLocations())
	if err != nil {
		return fmt.Errorf("prepare common publication content: %w", err)
	}
	c.QuestDeliveryCatalog, c.QuestDeliveryPublication = catalog, publication
	return nil
}

// ValidateQuestDeliveryWorlds rejects spelling mistakes and references to
// nonexistent/disabled channels instead of silently making quests inaccessible.
func (c *Config) ValidateQuestDeliveryWorlds(catalog *questdelivery.Catalog) error {
	worlds := make(map[string]EntranceServerInfo, len(c.Entrance.Entries))
	for _, world := range c.Entrance.Entries {
		if _, exists := worlds[world.Name]; exists {
			return fmt.Errorf("QuestDelivery requires unique world names: %q", world.Name)
		}
		worlds[world.Name] = world
	}
	for _, profile := range catalog.Profiles() {
		for _, name := range profile.Worlds {
			world, ok := worlds[name]
			if !ok {
				return fmt.Errorf("quest delivery profile %q references unknown world %q", profile.ID, name)
			}
			if len(profile.Channels) == 0 {
				enabled := false
				for _, channel := range world.Channels {
					enabled = enabled || channel.IsEnabled()
				}
				if !enabled {
					return fmt.Errorf("quest delivery profile %q references world %q with no enabled channels", profile.ID, name)
				}
			}
			for _, number := range profile.Channels {
				if number < 1 || number > len(world.Channels) || !world.Channels[number-1].IsEnabled() {
					return fmt.Errorf("quest delivery profile %q references unavailable channel %d in world %q", profile.ID, number, name)
				}
			}
		}
	}
	return nil
}

// QuestDeliveryEnabledLocations uses the actual Config, not a manifest's claims.
// The returned public tuples contain no network addresses or port information.
func (c *Config) QuestDeliveryEnabledLocations() []questdelivery.PublicationLocation {
	locations := []questdelivery.PublicationLocation{}
	for _, world := range c.Entrance.Entries {
		for index, channel := range world.Channels {
			if channel.IsEnabled() {
				locations = append(locations, questdelivery.PublicationLocation{World: world.Name, Channel: index + 1})
			}
		}
	}
	return locations
}
