// Package env provides loading and access to application configuration
// defined via environment variables.
//
// The package is responsible for reading environment variables at startup
// and converting them into a strongly typed configuration structure.
//
// Configuration values are parsed using the
// github.com/caarlos0/env/v11 library, which maps environment variables
// to struct fields using struct tags.
//
// The package exposes the Environments interface that allows other
// components of the application to access configuration values without
// depending on the concrete implementation. This makes the configuration
// layer easy to mock in unit tests.
//
// Typical usage:
//
//	cfg := env.EnvironmentsLoad()
//	timeout := cfg.Values().Timeout
//
// Environment variables:
//
//	SERVER_TIMEOUT   HTTP server request timeout (default: 10s)
//
// If parsing of environment variables fails, the error is logged using
// slog and an empty configuration is returned.
//
// The package also supports mock generation via go:generate for testing
// components that depend on the Environments interface.
package env

import (
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

//go:generate mockgen -destination=env_mock_test.go -package=env github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env Environments
type Environments interface {
	Values() Values
}

var _ Environments = (*Config)(nil)

type Config struct {
	values Values
}

// Values returns the environment configuration values stored in Config.
func (c Config) Values() Values {
	return c.values
}

// Values статичная конфигурация из переменных окружения.
type Values struct {
	Timeout time.Duration `env:"SERVER_TIMEOUT" envDefault:"10s"`
}

// EnvironmentsLoad parses environment variables into a Config structure
// and returns it as an Environments implementation. If parsing fails,
// the error is logged and an empty configuration is returned.
func EnvironmentsLoad() *Config {
	values := Values{}

	if err := env.Parse(&values); err != nil {
		slog.Error("Error parsing env", "error", err)
		return &Config{}
	}
	return &Config{values: values}
}
