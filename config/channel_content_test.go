package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"golang.org/x/text/encoding/korean"
)

func TestChannelCollabEventInheritance(t *testing.T) {
	for _, world := range []string{"", "none", "random", "kaiji"} {
		for _, override := range []string{"", "none", "random", "kaiji", "higanjima", "nier", "evangelion", "pso2"} {
			channel := EntranceChannelInfo{CollabEvent: override}
			want := override
			if want == "" {
				want = world
			}
			if got := channel.EffectiveCollabEvent(world); got != want {
				t.Errorf("world=%q channel=%q: got %q, want %q", world, override, got, want)
			}
		}
	}
}

func TestEntranceChannelContentConfigLoad(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()
	writeMinimalConfig(t, dir, `{
		"Host":"127.0.0.1", "Database":{"Password":"test"}, "ClientMode":"ZZ",
		"Entrance":{"Entries":[
			{"Name":"Open", "Type":1, "CollabEvent":"random", "Channels":[
				{"Port":54001, "CollabEvent":"none"},
				{"Port":54002, "CollabEvent":"kaiji"},
				{"Port":54003}
			]},
			{"Name":"MezFes", "Type":6, "CollabEvent":"none", "Channels":[
				{"Port":54004, "MezFesMinigame":2},
				{"Port":54005, "MezFesMinigame":4},
				{"Port":54006, "MezFesMinigame":0},
				{"Port":54007}
			]}
		]}
	}`)
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entrance.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(c.Entrance.Entries))
	}
	world := c.Entrance.Entries[0]
	for i, want := range []string{"none", "kaiji", "random"} {
		if got := world.Channels[i].EffectiveCollabEvent(world.CollabEvent); got != want {
			t.Errorf("channel %d event = %q, want %q", i, got, want)
		}
	}
	for i, want := range []uint16{2, 4, 0, 0} {
		channel := c.Entrance.Entries[1].Channels[i]
		if channel.MezFesMinigame != want || !channel.IsEnabled() {
			t.Errorf("MezFes channel %d was not loaded correctly: %#v", i, channel)
		}
	}
	if !c.UsesMezFesMinigameTune() || c.GameplayOptions.MezFesRandomMinigame {
		t.Fatal("fixed channels must enable the wire signal independently of random mode")
	}
}

func TestEntranceChannelContentValidation(t *testing.T) {
	for _, tt := range []struct {
		name, event string
		world       uint8
		game        uint16
		mode        Mode
		valid       bool
	}{
		{"legacy", "", 1, 0, ZZ, true},
		{"none", "none", 1, 0, ZZ, true},
		{"random", "random", 1, 0, ZZ, true},
		{"fixed-collab", "pso2", 1, 0, ZZ, true},
		{"invalid-collab", "randmo", 1, 0, ZZ, false},
		{"tokotoko", "", 6, 2, ZZ, true},
		{"volpakkun", "", 6, 4, ZZ, true},
		{"z2", "", 6, 2, Z2, true},
		{"invalid-game", "", 6, 1, ZZ, false},
		{"other-game", "", 6, 3, ZZ, false},
		{"large-game", "", 6, 65535, ZZ, false},
		{"wrong-world", "", 1, 2, ZZ, false},
		{"old-client", "", 6, 4, Z1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			world := EntranceServerInfo{Name: "Test", Type: tt.world, CollabEvent: "none", Channels: []EntranceChannelInfo{{CollabEvent: tt.event, MezFesMinigame: tt.game}}}
			err := world.ValidateChannelContent(tt.mode)
			if (err == nil) != tt.valid {
				t.Fatalf("validation error = %v, valid=%t", err, tt.valid)
			}
			if err != nil && !strings.Contains(err.Error(), "Channels[0]") {
				t.Fatalf("error does not identify the bad channel: %v", err)
			}
		})
	}
}

func TestEntranceChannelContentLoadRejectsInvalidSettings(t *testing.T) {
	for _, setting := range []string{`"CollabEvent":"randmo"`, `"MezFesMinigame":1`} {
		t.Run(setting, func(t *testing.T) {
			viper.Reset()
			defer viper.Reset()
			dir := t.TempDir()
			previous, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(previous) }()
			writeMinimalConfig(t, dir, fmt.Sprintf(`{"Host":"127.0.0.1", "Database":{"Password":"test"}, "Entrance":{"Entries":[{"Name":"MezFes", "Type":6, "Channels":[{%s}]}]}}`, setting))
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "Channels[0]") {
				t.Fatalf("invalid channel configuration was not rejected clearly: %v", err)
			}
		})
	}
}

func TestUsesMezFesMinigameTune(t *testing.T) {
	disabled := false
	for _, tt := range []struct {
		name   string
		random bool
		world  uint8
		game   uint16
		on     *bool
		want   bool
	}{
		{"legacy", false, 6, 0, nil, false},
		{"random", true, 6, 0, nil, true},
		{"fixed2", false, 6, 2, nil, true},
		{"fixed4", false, 6, 4, nil, true},
		{"disabled", false, 6, 2, &disabled, false},
		{"other-world", false, 1, 2, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{GameplayOptions: GameplayOptions{MezFesRandomMinigame: tt.random}, Entrance: Entrance{Entries: []EntranceServerInfo{{Type: tt.world, Channels: []EntranceChannelInfo{{MezFesMinigame: tt.game, Enabled: tt.on}}}}}}
			if got := c.UsesMezFesMinigameTune(); got != tt.want {
				t.Fatalf("UsesMezFesMinigameTune = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestChannelContentRecommendedExample(t *testing.T) {
	data, err := os.ReadFile("../docs/examples/channel-content.json")
	if err != nil {
		t.Fatal(err)
	}
	var example Config
	if err := json.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	if len(example.Entrance.Entries) != 2 || example.GameplayOptions.MezFesRandomMinigame {
		t.Fatal("recommended example should contain the two fixed-content worlds")
	}
	ports := map[uint16]bool{}
	for _, world := range example.Entrance.Entries {
		if err := world.ValidateChannelContent(ZZ); err != nil {
			t.Fatal(err)
		}
		name, err := korean.EUCKR.NewEncoder().String(world.Name)
		if err != nil {
			t.Fatalf("world name is not CP949: %v", err)
		}
		description, err := korean.EUCKR.NewEncoder().String(world.Description)
		if err != nil {
			t.Fatalf("description is not CP949: %v", err)
		}
		if size := len(name) + len(description) + 2; size > 65 {
			t.Fatalf("%q world text exceeds the wire budget: %d/65 bytes", world.Name, size)
		}
		for _, channel := range world.Channels {
			if channel.Port == 0 || ports[channel.Port] {
				t.Fatalf("missing/duplicate example port: %d", channel.Port)
			}
			ports[channel.Port] = true
		}
	}
	open, mezfes := example.Entrance.Entries[0], example.Entrance.Entries[1]
	if open.Type != 1 || open.CollabEvent != "none" || len(open.Channels) != 6 {
		t.Fatal("open world should default to ordinary content and have six fixed channels")
	}
	for i, want := range []string{"none", "kaiji", "higanjima", "nier", "evangelion", "pso2"} {
		if got := open.Channels[i].EffectiveCollabEvent(open.CollabEvent); got != want {
			t.Errorf("open channel %d = %q, want %q", i+1, got, want)
		}
	}
	if mezfes.Type != 6 || len(mezfes.Channels) != 2 || mezfes.Channels[0].MezFesMinigame != 2 || mezfes.Channels[1].MezFesMinigame != 4 {
		t.Fatal("MezFes example must fix the two group games to different channels")
	}
}
