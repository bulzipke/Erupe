package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/questdelivery"
)

// These ASCII worlds/channels are synthetic inputs, never operating settings.
func TestQuestDeliveryCommonAllExistingChannelsContract(t *testing.T) {
	anchor := time.Date(2026, 10, 24, 0, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	for _, field := range []string{"", `,"channels":[]`} {
		name := "omitted"
		if field != "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			text := fmt.Sprintf(`{"version":"not-live-common-contract","profiles":[{"id":"common","worlds":["One","Two"]%s}],"schedules":[{"id":"always"},{"id":"window","starts_at":"2026-10-24T00:00:00+09:00","ends_at":"2026-10-25T00:00:00+09:00"}],"quests":[{"quest_id":53140,"profiles":["common"],"schedule":"window","requirements":["Original ticket and equipment remain required"]},{"quest_id":53141,"profiles":["common"],"schedule":"always"},{"quest_id":61067,"disabled":true}]}`, field)
			path := filepath.Join(t.TempDir(), "common.json")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			c := Config{RealClientMode: ZZ, QuestDelivery: QuestDeliveryOptions{Enabled: true, ManifestFile: path}, Entrance: Entrance{Entries: []EntranceServerInfo{
				{Name: "One", Channels: []EntranceChannelInfo{{}, {}}},
				{Name: "Two", Channels: []EntranceChannelInfo{{}, {}}},
			}}}
			if err := c.LoadQuestDelivery(); err != nil {
				t.Fatal(err)
			}
			catalog := c.QuestDeliveryCatalog
			for _, world := range []string{"One", "Two"} {
				for _, ch := range []int{1, 2, 65535} {
					if managed, allowed := catalog.Allows(53140, world, ch, anchor); !managed || !allowed {
						t.Fatal("empty channel set did not match the named world", world, ch)
					}
				}
			}
			// Catalog matching does not assert that channel65535 is configured.
			for _, probe := range []struct {
				id, channel int
				world       string
				at          time.Time
			}{{53140, 0, "One", anchor}, {53140, 65536, "One", anchor}, {53140, 1, "Other", anchor},
				{61067, 1, "One", anchor}, {53140, 1, "One", anchor.Add(-time.Nanosecond)},
				{53140, 1, "One", anchor.Add(24 * time.Hour)}} {
				if managed, allowed := catalog.Allows(probe.id, probe.world, probe.channel, probe.at); !managed || allowed {
					t.Fatal("common profile removed an original range/disabled guard", probe)
				}
			}
			doc, err := catalog.PublicDocument(anchor, anchor.Add(30*24*time.Hour))
			if err != nil || doc.Coverage != "managed_quests_only" || !doc.OriginalEntryConditionsApply || len(doc.Occurrences) != 2 {
				t.Fatal("invalid common public document", err)
			}
			for _, row := range doc.Occurrences {
				if len(row.Profiles) != 1 || len(row.Profiles[0].Channels) != 0 || !reflect.DeepEqual(row.Profiles[0].Worlds, []string{"One", "Two"}) {
					t.Fatal("public output invented channel-specific placement")
				}
				if row.QuestID == 53140 && (!reflect.DeepEqual(row.Requirements, []string{"Original ticket and equipment remain required"}) || row.Permanent || !row.StartsAt.Equal(anchor) || !row.EndsAt.Equal(anchor.Add(24*time.Hour))) {
					t.Fatal("common profile changed original conditions or dates")
				}
				if row.QuestID == 53141 && !row.Permanent {
					t.Fatal("permanent schedule was changed")
				}
			}
			if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, allowed := catalog.Allows(53140, "Two", 2, anchor); !allowed {
				t.Fatal("loaded common snapshot changed after a file edit")
			}
		})
	}
	t.Run("original_channel1_counterexample", func(t *testing.T) {
		catalog, err := questdelivery.Parse([]byte(testDeliveryManifest))
		if err != nil {
			t.Fatal(err)
		}
		if _, allowed := catalog.Allows(53140, "Brave", 2, anchor); allowed {
			t.Fatal("counterexample unexpectedly matched channel2")
		}
	})
}
