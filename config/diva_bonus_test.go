package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestValidateDivaBonusTargets(t *testing.T) {
	base := DivaBonusTarget{Color: 1, TargetType: "monster", TargetID: 1,
		StartOffsetSeconds: 3600, EndOffsetSeconds: 7200, MultiplierPercent: 200}
	for _, tt := range []struct {
		name   string
		change func(*DivaBonusTarget)
	}{
		{"color zero", func(r *DivaBonusTarget) { r.Color = 0 }},
		{"color high", func(r *DivaBonusTarget) { r.Color = 5 }},
		{"unknown kind", func(r *DivaBonusTarget) { r.TargetType = "unknown" }},
		{"missing target", func(r *DivaBonusTarget) { r.TargetID = 0 }},
		{"negative target", func(r *DivaBonusTarget) { r.TargetID = -1 }},
		{"monster high", func(r *DivaBonusTarget) { r.TargetID = 177 }},
		{"monster alias", func(r *DivaBonusTarget) { r.TargetID = 0xac }},
		{"quest high", func(r *DivaBonusTarget) { r.TargetType = "quest"; r.TargetID = 65536 }},
		{"negative start", func(r *DivaBonusTarget) { r.StartOffsetSeconds = -1 }},
		{"empty window", func(r *DivaBonusTarget) { r.EndOffsetSeconds = r.StartOffsetSeconds }},
		{"past song phase", func(r *DivaBonusTarget) { r.EndOffsetSeconds = DivaBonusPhaseSeconds + 1 }},
		{"percent not multiplier", func(r *DivaBonusTarget) { r.MultiplierPercent = 2 }},
		{"unsafe percent", func(r *DivaBonusTarget) { r.MultiplierPercent = MaxDivaBonusPercent + 1 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			tt.change(&r)
			if err := ValidateDivaBonusTargets([]DivaBonusTarget{r}); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	for _, rows := range [][]DivaBonusTarget{nil, {base}, {
		base, {Color: 2, TargetType: "quest", TargetID: 40217, StartOffsetSeconds: 3600, EndOffsetSeconds: 7200, MultiplierPercent: 200},
	}, {
		base, {Color: 1, TargetType: "monster", TargetID: 1, StartOffsetSeconds: 7200, EndOffsetSeconds: 10800, MultiplierPercent: 100},
	}} {
		if err := ValidateDivaBonusTargets(rows); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateDivaBonusTargets([]DivaBonusTarget{base, base}); err == nil {
		t.Fatal("overlapping same-color windows accepted")
	}
	if err := ValidateDivaBonusTargets(make([]DivaBonusTarget, MaxDivaBonusTargets+1)); err == nil {
		t.Fatal("oversized schedule accepted")
	}
}

func TestDivaBonusConfigLoad(t *testing.T) {
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
	writeMinimalConfig(t, dir, `{
		"ClientMode": "ZZ",
		"GameplayOptions": {"DivaBonusTargets": [{
			"Color": 4, "TargetType": "monster", "TargetID": 111,
			"StartOffsetSeconds": 72000, "EndOffsetSeconds": 86400, "MultiplierPercent": 200
		}]}
	}`)
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	rows := loaded.GameplayOptions.DivaBonusTargets
	if len(rows) != 1 || rows[0].Color != 4 || rows[0].TargetID != 111 || rows[0].MultiplierPercent != 200 {
		t.Fatalf("bonus configuration not loaded: %+v", rows)
	}
	writeMinimalConfig(t, dir, `{"ClientMode":"ZZ", "GameplayOptions":{"DivaBonusTargets":[{"Color":4,"TargetType":"monster","TargetID":111,"StartOffsetSeconds":0,"EndOffsetSeconds":60,"MultiplierPercent":2}]}}`)
	viper.Reset()
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid percent accepted by LoadConfig")
	}
	writeMinimalConfig(t, dir, `{"ClientMode":"Z2", "GameplayOptions":{"DivaBonusTargets":[{"Color":4,"TargetType":"monster","TargetID":111,"StartOffsetSeconds":0,"EndOffsetSeconds":60,"MultiplierPercent":200}]}}`)
	viper.Reset()
	if _, err := LoadConfig(); err == nil {
		t.Fatal("unverified client accepted with active schedule")
	}
	writeMinimalConfig(t, dir, `{"ClientMode":"ZZ", "DebugOptions":{"InGameTimeOverrideHour":12}, "GameplayOptions":{"DivaBonusTargets":[{"Color":4,"TargetType":"monster","TargetID":111,"StartOffsetSeconds":0,"EndOffsetSeconds":60,"MultiplierPercent":200}]}}`)
	viper.Reset()
	if _, err := LoadConfig(); err == nil {
		t.Fatal("bonus windows accepted with a shifted client clock")
	}
}
