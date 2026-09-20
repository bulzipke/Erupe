package config

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDivaBonusRandomConfigLoad(t *testing.T) {
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
		wantRandom            bool
	}{
		{name: "default off", body: `{"ClientMode":"ZZ"}`},
		{name: "enabled", body: `{"ClientMode":"ZZ","GameplayOptions":{"DivaBonusRandom":true}}`, wantRandom: true},
		{name: "explicit empty manual list", body: `{"ClientMode":"ZZ","GameplayOptions":{"DivaBonusRandom":true,"DivaBonusTargets":[]}}`, wantRandom: true},
		{name: "unverified client", body: `{"ClientMode":"Z2","GameplayOptions":{"DivaBonusRandom":true}}`, wantError: "ZZ client"},
		{name: "off permits older client", body: `{"ClientMode":"Z2","GameplayOptions":{"DivaBonusRandom":false}}`},
		{name: "shifted clock", body: `{"ClientMode":"ZZ","DebugOptions":{"InGameTimeOverrideHour":12},"GameplayOptions":{"DivaBonusRandom":true}}`, wantError: "InGameTimeOverrideHour=null"},
		{name: "midnight override also rejected", body: `{"ClientMode":"ZZ","DebugOptions":{"InGameTimeOverrideHour":0},"GameplayOptions":{"DivaBonusRandom":true}}`, wantError: "InGameTimeOverrideHour=null"},
		{name: "null clock", body: `{"ClientMode":"ZZ","DebugOptions":{"InGameTimeOverrideHour":null},"GameplayOptions":{"DivaBonusRandom":true}}`, wantRandom: true},
		{name: "manual conflict", body: `{"ClientMode":"ZZ","GameplayOptions":{"DivaBonusRandom":true,"DivaBonusTargets":[{"Color":1,"TargetType":"monster","TargetID":1,"StartOffsetSeconds":0,"EndOffsetSeconds":60,"MultiplierPercent":200}]}}`, wantError: "cannot be enabled together"},
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
			if loaded.GameplayOptions.DivaBonusRandom != tt.wantRandom {
				t.Fatalf("random = %t, want %t", loaded.GameplayOptions.DivaBonusRandom, tt.wantRandom)
			}
		})
	}
}
