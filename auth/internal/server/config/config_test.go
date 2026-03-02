package config

import (
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// 🔧 Вспомогательная функция сброса глобального состояния
func resetConfigState() {
	cfg = nil
	once = new(sync.Once)
	viper.Reset()
}

// 1️⃣ Test: ValuesConfig реализует интерфейс Config
func TestValuesConfig_ImplementsConfig(t *testing.T) {
	var c Config = &ValuesConfig{}
	if c == nil {
		t.Fatal("expected non-nil config")
	}
}

// 2️⃣ Test: Values() возвращает правильные данные
func TestValuesConfig_Values(t *testing.T) {
	values := Values{
		Address: "localhost:8080",
		Debug:   true,
		Verbose: true,
	}

	cfg := ValuesConfig{values: values}

	got := cfg.Values()

	if got.Address != values.Address {
		t.Fatalf("expected address %s, got %s", values.Address, got.Address)
	}
	if got.Debug != values.Debug {
		t.Fatalf("expected debug %v, got %v", values.Debug, got.Debug)
	}
}

// 3️⃣ Test: NewConfig создаёт singleton
func TestNewConfig_Singleton(t *testing.T) {
	resetConfigState()

	viper.Set("address", "localhost:9000")
	viper.Set("debug", true)

	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", false, "")

	c1 := NewConfig(cmd)
	c2 := NewConfig(cmd)

	if c1 != c2 {
		t.Fatal("expected singleton instance")
	}
}

// 4️⃣ Test: NewConfig читает значения из viper
func TestNewConfig_UnmarshalValues(t *testing.T) {
	resetConfigState()

	viper.Set("address", "127.0.0.1:8080")
	viper.Set("debug", true)
	viper.Set("verbose", true)
	viper.Set("insecure_skip_verify", true)

	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", false, "")

	cfg := NewConfig(cmd)
	values := cfg.Values()

	if values.Address != "127.0.0.1:8080" {
		t.Fatalf("expected address 127.0.0.1:8080, got %s", values.Address)
	}
	if !values.Debug {
		t.Fatal("expected debug true")
	}
	if !values.Verbose {
		t.Fatal("expected verbose true")
	}
	if !values.InsecureSkipVerify {
		t.Fatal("expected insecure_skip_verify true")
	}
}

// 5️⃣ Test: NewConfig не перезаписывает значения после первого вызова
func TestNewConfig_OnceBehavior(t *testing.T) {
	resetConfigState()

	viper.Set("address", "first")
	cmd := &cobra.Command{}
	cmd.Flags().Bool("debug", false, "")

	c1 := NewConfig(cmd)

	// меняем viper после первого вызова
	viper.Set("address", "second")

	c2 := NewConfig(cmd)

	if c2.Values().Address != "first" {
		t.Fatalf("expected singleton to keep first value, got %s", c2.Values().Address)
	}

	if c1 != c2 {
		t.Fatal("expected same instance")
	}
}

// 6️⃣ Test: Values.Ssl() возвращает false по умолчанию
func TestValues_Ssl_DefaultFalse(t *testing.T) {
	v := Values{}

	if v.Ssl() {
		t.Fatal("expected ssl to be false by default")
	}
}

// 7️⃣ Test: Проверка unexported ssl поля (через литерал)
func TestValues_Ssl_True(t *testing.T) {
	v := Values{
		ssl: true,
	}

	if !v.Ssl() {
		t.Fatal("expected ssl true")
	}
}

func newTestCommand(debug bool) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("debug", debug, "")
	cmd.Flags().Bool("verbose", false, "")
	_ = cmd.Flags().Set("debug", map[bool]string{true: "true", false: "false"}[debug])
	return cmd
}

func TestNewConfig_ShouldBindValuesFromViper(t *testing.T) {
	resetConfigState()

	viper.Set("address", "localhost:8080")
	viper.Set("debug", true)
	viper.Set("verbose", true)
	viper.Set("insecure_skip_verify", true)

	cmd := newTestCommand(true)

	config := NewConfig(cmd)
	values := config.Values()

	if values.Address != "localhost:8080" {
		t.Errorf("expected address 'localhost:8080', got '%s'", values.Address)
	}

	if !values.Debug {
		t.Error("expected Debug to be true")
	}

	if !values.Verbose {
		t.Error("expected Verbose to be true")
	}

	if !values.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

func TestNewConfig_ShouldBeSingleton(t *testing.T) {
	resetConfigState()

	viper.Set("address", "first")

	cmd := newTestCommand(false)
	config1 := NewConfig(cmd)

	// Меняем значение во viper
	viper.Set("address", "second")

	config2 := NewConfig(cmd)

	if config1 != config2 {
		t.Error("expected same instance due to sync.Once singleton")
	}

	if config2.Values().Address != "first" {
		t.Error("expected address to remain 'first' due to singleton behavior")
	}
}

func TestValuesMethod_ReturnsCorrectValues(t *testing.T) {
	resetConfigState()

	expected := Values{
		Address: "test",
		Debug:   true,
	}

	cfg = &ValuesConfig{values: expected}

	values := cfg.Values()

	if values.Address != expected.Address {
		t.Errorf("expected address %s, got %s", expected.Address, values.Address)
	}

	if values.Debug != expected.Debug {
		t.Errorf("expected debug %v, got %v", expected.Debug, values.Debug)
	}
}

func TestSsl_DefaultValueIsFalse(t *testing.T) {
	values := Values{}

	if values.Ssl() {
		t.Error("expected default ssl to be false")
	}
}
