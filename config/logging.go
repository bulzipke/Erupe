package config

import (
	"errors"

	"erupe-ce/common/logging"
	"github.com/spf13/viper"
)

func registerLoggingDefaults(v *viper.Viper) {
	o := logging.DefaultOptions()
	v.SetDefault("Logging.Enabled", o.Enabled)
	v.SetDefault("Logging.Directory", o.Directory)
	v.SetDefault("Logging.MaxSizeMB", o.MaxSizeMB)
	v.SetDefault("Logging.MaxBackups", o.MaxBackups)
	v.SetDefault("Logging.MaxAgeDays", o.MaxAgeDays)
	v.SetDefault("Logging.Compress", o.Compress)
	v.SetDefault("Logging.CaptureCrashes", o.CaptureCrashes)
}

// LoadLoggingOptions reads logging before full config validation, so startup
// failures and setup-wizard messages are also retained. A missing config uses
// defaults; the regular loader/setup wizard still decides how startup proceeds.
func LoadLoggingOptions() (logging.Options, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath(".")
	registerLoggingDefaults(v)
	err := v.ReadInConfig()
	var missing viper.ConfigFileNotFoundError
	if err != nil && !errors.As(err, &missing) {
		return logging.DefaultOptions(), err
	}
	var c struct{ Logging logging.Options }
	if err := v.Unmarshal(&c); err != nil {
		return logging.DefaultOptions(), err
	}
	return c.Logging, c.Logging.Validate()
}
