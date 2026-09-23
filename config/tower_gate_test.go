package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestTowerGateConfig(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous); viper.Reset() }()
	for _, tc := range []struct {
		name                        string
		body                        string
		zone1, zone2, tr, trp, trp2 int32
	}{
		{"omitted", `{"ClientMode":"ZZ"}`, 0, 0, 51, 1, 1},
		{"existing floor config", `{"ClientMode":"ZZ","TowerZone1UnlockFloor":0,"TowerZone2UnlockFloor":0}`, 0, 0, 51, 1, 1},
		{"zone 1 TRP disabled", `{"ClientMode":"ZZ","TowerZone1UnlockTRP":0}`, 0, 0, 51, 0, 1},
		{"zone 2 TRP disabled", `{"ClientMode":"ZZ","TowerZone2UnlockTRP":0}`, 0, 0, 51, 1, 0},
		{"custom gates", `{"ClientMode":"ZZ","TowerZone1UnlockFloor":3,"TowerZone2UnlockFloor":100,"TowerZone2UnlockTR":60,"TowerZone1UnlockTRP":30,"TowerZone2UnlockTRP":50}`, 3, 100, 60, 30, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			writeMinimalConfig(t, dir, tc.body)
			got, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if got.TowerZone1UnlockFloor != tc.zone1 || got.TowerZone2UnlockFloor != tc.zone2 || got.TowerZone2UnlockTR != tc.tr || got.TowerZone1UnlockTRP != tc.trp || got.TowerZone2UnlockTRP != tc.trp2 {
				t.Fatalf("Tower gates = %d/%d/TR%d/TRP%d/%d, want %d/%d/TR%d/TRP%d/%d", got.TowerZone1UnlockFloor, got.TowerZone2UnlockFloor, got.TowerZone2UnlockTR, got.TowerZone1UnlockTRP, got.TowerZone2UnlockTRP, tc.zone1, tc.zone2, tc.tr, tc.trp, tc.trp2)
			}
		})
	}
}
