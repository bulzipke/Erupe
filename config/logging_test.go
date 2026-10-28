package config

import (
	"os"
	"testing"

	"erupe-ce/common/logging"
	"github.com/spf13/viper"
)

func TestLoadLoggingOptions(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		wantErr      bool
	}{
		{name: "missing"},
		{name: "legacy", config: `{"Host":"127.0.0.1"}`},
		{name: "custom", config: `{"Logging":{"Directory":"custom-logs","MaxSizeMB":2}}`},
		{name: "disabled", config: `{"Logging":{"Enabled":false}}`},
		{name: "invalid retention", config: `{"Logging":{"MaxBackups":0}}`, wantErr: true},
		{name: "malformed", config: `{`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tc.config != "" {
				if err := os.WriteFile("config.json", []byte(tc.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			o, err := LoadLoggingOptions()
			if (err != nil) != tc.wantErr {
				t.Fatalf("logging config error: %v", err)
			}
			if tc.wantErr {
				return
			}
			want := logging.DefaultOptions()
			if tc.name == "custom" {
				want.Directory, want.MaxSizeMB = "custom-logs", 2
			}
			if tc.name == "disabled" {
				want.Enabled = false
			}
			if o != want {
				t.Fatalf("got %+v, want %+v", o, want)
			}
			// Full-config defaults must agree with the early bootstrap loader.
			v := viper.New()
			registerLoggingDefaults(v)
			if v.GetInt("Logging.MaxSizeMB") != logging.DefaultOptions().MaxSizeMB {
				t.Fatal("logging defaults diverged")
			}
		})
	}
}
