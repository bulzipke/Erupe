package config

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestHunterGemPointMultiplierConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, body, wantError string
		want                  float32
	}{
		{"omitted", `{"Host":"127.0.0.1","ClientMode":"ZZ"}`, "", 1},
		{"fractional", `{"Host":"127.0.0.1","ClientMode":"ZZ","GameplayOptions":{"HunterGemPointMultiplier":2.5}}`, "", 2.5},
		{"disabled gains", `{"Host":"127.0.0.1","ClientMode":"ZZ","GameplayOptions":{"HunterGemPointMultiplier":0}}`, "", 0},
		{"maximum", `{"Host":"127.0.0.1","ClientMode":"ZZ","GameplayOptions":{"HunterGemPointMultiplier":100}}`, "", 100},
		{"negative", `{"Host":"127.0.0.1","ClientMode":"ZZ","GameplayOptions":{"HunterGemPointMultiplier":-1}}`, "between 0 and 100", 0},
		{"too large", `{"Host":"127.0.0.1","ClientMode":"ZZ","GameplayOptions":{"HunterGemPointMultiplier":100.01}}`, "between 0 and 100", 0},
		{"older default", `{"Host":"127.0.0.1","ClientMode":"Z2"}`, "", 1},
		{"older modified", `{"Host":"127.0.0.1","ClientMode":"Z2","GameplayOptions":{"HunterGemPointMultiplier":5}}`, "requires the ZZ client", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			writeMinimalConfig(t, dir, tt.body)
			loaded, err := LoadConfig()
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := loaded.GameplayOptions.HunterGemPointMultiplier; got != tt.want {
				t.Fatalf("multiplier = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHunterGemPointMultiplierRejectsNonFinite(t *testing.T) {
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if err := ValidateHunterGemPointMultiplier(value, ZZ); err == nil {
			t.Errorf("accepted %v", value)
		}
	}
}
