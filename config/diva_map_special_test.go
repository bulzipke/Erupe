package config

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDivaMapRedTreasureConfig(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous); viper.Reset() }()
	for _, tc := range []struct{ name, body, want, fail string }{
		{"default", `{"ClientMode":"ZZ"}`, "off", ""},
		{"empty", `{"ClientMode":"ZZ","GameplayOptions":{"DivaMapRedTreasureMode":""}}`, "off", ""},
		{"random", `{"ClientMode":"ZZ","GameplayOptions":{"DivaMapRedTreasureMode":"random-one"}}`, "random-one", ""},
		{"all", `{"ClientMode":"ZZ","GameplayOptions":{"DivaMapRedTreasureMode":"all"}}`, "all", ""},
		{"unknown", `{"ClientMode":"ZZ","GameplayOptions":{"DivaMapRedTreasureMode":"maybe"}}`, "", "must be off"},
		{"old client off", `{"ClientMode":"Z2"}`, "off", ""},
		{"old client enabled", `{"ClientMode":"Z2","GameplayOptions":{"DivaMapRedTreasureMode":"all"}}`, "", "ZZ client"},
		{"shifted clock", `{"ClientMode":"ZZ","DebugOptions":{"InGameTimeOverrideHour":0},"GameplayOptions":{"DivaMapRedTreasureMode":"random-one"}}`, "", "InGameTimeOverrideHour=null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			writeMinimalConfig(t, dir, tc.body)
			got, err := LoadConfig()
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("error=%v want=%s", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.GameplayOptions.DivaMapRedTreasureMode != tc.want {
				t.Fatalf("mode=%q want=%q", got.GameplayOptions.DivaMapRedTreasureMode, tc.want)
			}
		})
	}
}
