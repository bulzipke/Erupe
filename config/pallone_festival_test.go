package config

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestPalloneFestivalSelectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options PalloneFestivalOptions
		mode    Mode
		valid   bool
	}{
		{"disabled", PalloneFestivalOptions{}, ZZ, true},
		{"disabled ignores stored ID", PalloneFestivalOptions{EventID: -1}, G10, true},
		{"independent", PalloneFestivalOptions{Enabled: true, EventID: 1001}, ZZ, true},
		{"automatic without ID", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 21}, ZZ, true},
		{"missing durations", PalloneFestivalOptions{Enabled: true}, ZZ, false},
		{"weekly claims", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 7}, ZZ, true},
		{"claims overlap two old rounds", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 22}, ZZ, false},
		{"nonweekly cycle", PalloneFestivalOptions{Enabled: true, ActiveDays: 20, ClaimDays: 7}, ZZ, false},
		{"zero claims", PalloneFestivalOptions{Enabled: true, ActiveDays: 21}, ZZ, false},
		{"Monday KST", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 21, StartAt: "2026-10-26T03:00:00Z"}, ZZ, true},
		{"wrong weekday", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 21, StartAt: "2026-10-27T12:00:00+09:00"}, ZZ, false},
		{"midnight", PalloneFestivalOptions{Enabled: true, ActiveDays: 21, ClaimDays: 21, StartAt: "2026-10-26T00:00:00+09:00"}, ZZ, false},
		{"negative ID", PalloneFestivalOptions{Enabled: true, EventID: -1}, ZZ, false},
		{"unsupported client", PalloneFestivalOptions{Enabled: true, EventID: 1001}, G10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.options.Validate(tc.mode); (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestLoadConfigIndependentPallonePreservesTowerSettings(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous); viper.Reset() }()
	for _, tc := range []struct {
		name, body, wantError string
		enabled               bool
	}{
		{"default off", `{"Host":"127.0.0.1","ClientMode":"ZZ"}`, "", false},
		{"independent", `{"Host":"127.0.0.1","ClientMode":"ZZ","EarthStatus":21,"EarthID":1,"TowerRotation":{"Enabled":true,"StartAt":"2026-10-26T12:00:00+09:00","ActiveDays":21,"CycleDays":21},"PalloneFestival":{"Enabled":true,"EventID":777}}`, "", true},
		{"automatic without ID", `{"Host":"127.0.0.1","ClientMode":"ZZ","PalloneFestival":{"Enabled":true}}`, "", true},
		{"bad durations", `{"Host":"127.0.0.1","ClientMode":"ZZ","PalloneFestival":{"Enabled":true,"ActiveDays":20}}`, "ActiveDays", false},
		{"old client", `{"Host":"127.0.0.1","ClientMode":"G10","PalloneFestival":{"Enabled":true,"EventID":777}}`, "requires the ZZ client", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			writeMinimalConfig(t, dir, tc.body)
			loaded, err := LoadConfig()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("load error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loaded.PalloneFestival.Enabled != tc.enabled || !loaded.TowerRotation.Enabled || loaded.TowerRotation.ActiveDays != 21 || loaded.TowerRotation.CycleDays != 21 {
				t.Fatal("festival selection changed the existing Tower rotation")
			}
			if tc.name == "independent" && (loaded.PalloneFestival.EventID != 777 || loaded.EarthStatus != 21 || loaded.EarthID != 1 || loaded.TowerRotation.StartAt != "2026-10-26T12:00:00+09:00") {
				t.Fatal("changed either event's independent selection")
			}
			if tc.name == "automatic without ID" && (loaded.PalloneFestival.EventID != 0 || loaded.PalloneFestival.ActiveDays != 21 || loaded.PalloneFestival.ClaimDays != 21) {
				t.Fatal("automatic rounds require no operator ID")
			}
		})
	}
}
