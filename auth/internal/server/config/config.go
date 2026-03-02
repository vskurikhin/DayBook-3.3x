// Package config provides functionality for configuring the DayBook
// authentication server.
//
// Configuration can be loaded from CLI flags, a configuration file,
// or environment variables using the Viper library. The package
// implements a singleton pattern for storing the global application
// configuration and provides a Config interface to access configuration
// values.
//
// Types and Functions
//
//	Config
//		Interface providing access to configuration values.
//		Methods:
//			- Values() Values — returns a Values struct with configuration settings.
//
//	Values
//		Struct containing the main configuration fields:
//			- Address: server address in host:port format
//			- Debug: enables debug mode
//			- InsecureSkipVerify: skips server certificate verification
//			- Verbose: enables verbose logging
//			- ssl: internal field indicating SSL usage
//		Methods:
//			- Ssl() bool — returns the value of the ssl field
//
//	ValuesConfig
//		Struct implementing the Config interface, containing Values.
//
//	NewConfig(cmd *cobra.Command) *ValuesConfig
//		Creates and returns the singleton ValuesConfig instance.
//		Reads configuration from Viper and CLI commands. If the debug
//		flag is enabled, logs errors and configuration values.
//
// # Features
//
// - Uses [sync.Once] to enforce the singleton pattern.
// - Supports debug logging via tool.IsDebug(cmd).
// - Configuration values are loaded via viper.Unmarshal.
// - The ssl field is unexported and accessible only via the Ssl() method.
//
// Example Usage
//
//	cmd := &cobra.Command{}
//	cmd.Flags().Bool("debug", false, "enable debug mode")
//
//	cfg := config.NewConfig(cmd)
//	fmt.Println(cfg.Values().Address)
//	if cfg.Values().Ssl() {
//		fmt.Println("SSL enabled")
//	}
package config

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

//go:generate mockgen -destination=config_mock_test.go -package=config github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config Config
type Config interface {
	Values() Values
}

type Values struct {
	Address            string `mapstructure:"address"`
	Debug              bool   `mapstructure:"debug"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
	Verbose            bool   `mapstructure:"verbose"`
	ssl                bool
}

var _ Config = (*ValuesConfig)(nil)

type ValuesConfig struct {
	values Values
}

// Values returns the configuration values stored in the Config instance.
//
// This method provides read-only access to the underlying Values struct,
// which contains all the application configuration fields such as Address,
// Debug, InsecureSkipVerify, and Verbose.
//
// Example:
//
//	cfg := config.NewConfig(cmd)
//	values := cfg.Values()
//	fmt.Println(values.Address)
//	if values.Debug {
//	    fmt.Println("Debug mode enabled")
//	}
func (v ValuesConfig) Values() Values {
	return v.values
}

var (
	cfg  *ValuesConfig
	once = new(sync.Once)
)

// NewConfig creates and returns a singleton instance of ValuesConfig.
//
// It reads configuration values from Viper and the provided Cobra command.
// If the "debug" flag is enabled on the command, any errors encountered
// during unmarshalling will be logged, and the loaded configuration will
// also be printed for debugging purposes.
//
// The returned ValuesConfig implements the Config interface and provides
// access to the configuration via the Values() method.
//
// This function uses sync.Once to ensure that only one instance of
// ValuesConfig is created (singleton pattern).
//
// Example usage:
//
//	cmd := &cobra.Command{}
//	cmd.Flags().Bool("debug", false, "enable debug mode")
//
//	cfg := config.NewConfig(cmd)
//	fmt.Println(cfg.Values().Address)
func NewConfig(cmd *cobra.Command) *ValuesConfig {
	var v, values Values
	err := viper.Unmarshal(&v)
	if err != nil && tool.IsDebug(cmd) {
		slog.Error("Error binding flag", "error", err)
	} else {
		values = v
	}
	if tool.IsDebug(cmd) {
		slog.Debug("variable:", "ValuesConfig", fmt.Sprintf("%+v", values))
	}
	once.Do(func() {
		cfg = &ValuesConfig{values: values}
	})
	return cfg
}

func GetConfig() *ValuesConfig {
	return cfg
}

// Ssl returns the value of the internal ssl field.
//
// This indicates whether SSL/TLS is enabled for the configuration.
// It is a read-only accessor.
func (c Values) Ssl() bool {
	return c.ssl
}
