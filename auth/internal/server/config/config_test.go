package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// helper: reset viper between tests
func resetViper() {
	viper.Reset()
}

func TestMakeConfig_Success(t *testing.T) {
	resetViper()

	viper.Set("address", "localhost:8080")
	viper.Set("debug", true)
	viper.Set("insecure_skip_verify", true)
	viper.Set("verbose", true)

	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", true, "")
	_ = cmd.Flags().Set("debug", "true")

	cfg := MakeConfig(cmd)

	if cfg.Address != "localhost:8080" {
		t.Errorf("expected address localhost:8080, got %s", cfg.Address)
	}

	if !cfg.Debug {
		t.Errorf("expected debug true")
	}

	if !cfg.InsecureSkipVerify {
		t.Errorf("expected insecure_skip_verify true")
	}

	if !cfg.Verbose {
		t.Errorf("expected verbose true")
	}
}

func TestMakeConfig_UnmarshalError_DebugMode(t *testing.T) {
	resetViper()

	// intentionally set incompatible type
	viper.Set("debug", "not_bool")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", true, "")
	_ = cmd.Flags().Set("debug", "true")

	_ = MakeConfig(cmd)

	if buf.Len() == 0 {
		t.Errorf("expected log output in debug mode")
	}
}

func TestMakeConfig_UnmarshalError_NoDebug(t *testing.T) {
	resetViper()

	viper.Set("debug", "not_bool")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", false, "")
	_ = cmd.Flags().Set("debug", "false")

	_ = MakeConfig(cmd)

	if buf.Len() != 0 {
		t.Errorf("expected no log output when debug is false")
	}
}

func TestSsl_DefaultFalse(t *testing.T) {
	cfg := Config{}
	if cfg.Ssl() {
		t.Errorf("expected ssl to be false by default")
	}
}
