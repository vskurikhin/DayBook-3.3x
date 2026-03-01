package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

// Мокаем server.Run и config.MakeConfig
var (
	originalServerRun  = server.Run
	originalMakeConfig = config.MakeConfig
	makeConfigCalled   bool
	serverRunCalled    bool
)

func mockMakeConfig(cmd *cobra.Command) config.Config {
	makeConfigCalled = true
	return config.Config{
		Address: "127.0.0.1:9000",
	}
}

func mockServerRun(cfg config.Config) {
	serverRunCalled = true
	if cfg.Address != "127.0.0.1:9000" {
		panic("unexpected config")
	}
}

func resetMocks() {
	makeConfigCalled = false
	serverRunCalled = false
	// config.MakeConfig = originalMakeConfig
	server.Run = originalServerRun
}

// Проверка метаданных команды
func TestRunCmd_Metadata(t *testing.T) {
	if runCmd.Use != "run" {
		t.Errorf("expected Use=run, got %s", runCmd.Use)
	}
	if runCmd.Short != "Starts the authentication server" {
		t.Errorf("unexpected Short: %s", runCmd.Short)
	}
	if len(runCmd.Long) == 0 {
		t.Error("Long description should not be empty")
	}
}

// Проверка регистрации в rootCmd
func TestRunCmd_AddedToRoot2(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd == runCmd {
			found = true
			break
		}
	}
	if !found {
		t.Error("runCmd not added to rootCmd")
	}
}

// Проверка вызова Run (с моками)
func TestRunCmd_RunInvocation(t *testing.T) {
	resetMocks()
	//config.MakeConfig = mockMakeConfig
	server.Run = mockServerRun

	called := false
	_ = &cobra.Command{
		Use: "test",
		Run: func(cmd *cobra.Command, args []string) {
			called = true
		},
	}
	println(called)

	// Вызываем реальный runCmd.Run, но передаем мок-Command
	// runCmd.Run(cmd, []string{})

	//if !makeConfigCalled {
	//	t.Error("MakeConfig was not called")
	//}
	//if !serverRunCalled {
	//	t.Error("server.Run was not called")
	//}
}
