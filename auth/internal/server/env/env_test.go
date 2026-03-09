package env

import (
	"testing"
	"time"
)

func TestEnvironmentsLoad_DefaultValue(t *testing.T) {
	t.Setenv("SERVER_TIMEOUT", "")

	environments := EnvironmentsLoad()
	values := environments.Values()

	expected := 10 * time.Second
	if values.Timeout != expected {
		t.Errorf("expected timeout %v, got %v", expected, values.Timeout)
	}
}

func TestEnvironmentsLoad_FromEnv(t *testing.T) {
	t.Setenv("SERVER_TIMEOUT", "30s")

	environments := EnvironmentsLoad()
	values := environments.Values()

	expected := 30 * time.Second
	if values.Timeout != expected {
		t.Errorf("expected timeout %v, got %v", expected, values.Timeout)
	}
}

func TestValues_Method(t *testing.T) {
	cfg := Config{
		values: Values{
			Timeout: 15 * time.Second,
		},
	}

	values := cfg.Values()

	if values.Timeout != 15*time.Second {
		t.Errorf("expected timeout %v, got %v", 15*time.Second, values.Timeout)
	}
}

func TestEnvironmentsLoad_InvalidDuration(t *testing.T) {
	t.Setenv("SERVER_TIMEOUT", "invalid")
	EnvironmentsLoad()
}
