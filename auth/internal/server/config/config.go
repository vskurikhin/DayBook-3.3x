package config

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type Config struct {
	Address            string `mapstructure:"address"`
	Debug              bool   `mapstructure:"debug"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
	Verbose            bool   `mapstructure:"verbose"`
	ssl                bool
}

func MakeConfig(cmd *cobra.Command) Config {
	var cfg Config
	err := viper.Unmarshal(&cfg)
	if err != nil && tool.IsDebug(cmd) {
		slog.Error("Error binding flag", "error", err)
	}
	if tool.IsDebug(cmd) {
		slog.Debug("variable:", "config", fmt.Sprintf("%+v", cfg))
	}
	return cfg
}

func (c Config) Ssl() bool {
	return c.ssl
}
