package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

func TestRunCmd_ShouldCallServerRun(t *testing.T) {
	// backup originals
	origNewConfig := newConfig
	origNewServer := newAuthServer
	defer func() {
		newConfig = origNewConfig
		newAuthServer = origNewServer
	}()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConfig := NewMockConfig(ctrl)
	mockServer := NewMockServer(ctrl)

	mockConfig.EXPECT().Values().Times(0)
	mockServer.EXPECT().Run(context.Background()).Return(nil).Times(1)

	// arrange
	cmd := newTestCommand()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return mockConfig, nil
	}
	newAuthServer = func(_ config.Config, _ env.Environments) (server.Server, error) {
		return mockServer, nil
	}

	// act
	err := runCmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatal(err)
	}
}

var testError = errors.New("test error")

func TestRunCmd_ShouldReturnTestError(t *testing.T) {
	// backup originals
	origNewConfig := newConfig
	origNewServer := newAuthServer
	defer func() {
		newConfig = origNewConfig
		newAuthServer = origNewServer
	}()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConfig := NewMockConfig(ctrl)
	mockServer := NewMockServer(ctrl)

	mockConfig.EXPECT().Values().Times(0)
	mockServer.EXPECT().Run(context.Background()).Return(nil).Times(0)

	// arrange
	cmd := newTestCommandDebug()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return nil, testError
	}
	newAuthServer = func(_ config.Config, _ env.Environments) (server.Server, error) {
		return mockServer, nil
	}

	// act
	err := runCmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatalf("expected error: '%s', got none", testError)
	}
	if err.Error() != testError.Error() {
		t.Fatalf("expected error: '%s', got '%s'", testError.Error(), err.Error())
	}
}

func TestRunCmd_EnvLoad_ShouldReturnTestError(t *testing.T) {
	// backup originals
	origNewConfig := newConfig
	origEnvLoad := envLoad
	origNewServer := newAuthServer
	defer func() {
		newConfig = origNewConfig
		envLoad = origEnvLoad
		newAuthServer = origNewServer
	}()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConfig := NewMockConfig(ctrl)
	mockServer := NewMockServer(ctrl)

	mockConfig.EXPECT().Values().Times(0)
	mockServer.EXPECT().Run(context.Background()).Return(nil).Times(0)

	// arrange
	cmd := newTestCommandDebug()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return mockConfig, nil
	}
	envLoad = func() (env.Environments, error) { return nil, testError }
	newAuthServer = func(_ config.Config, _ env.Environments) (server.Server, error) {
		return mockServer, nil
	}

	// act
	err := runCmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatalf("expected error: '%s', got none", testError)
	}
	if err.Error() != testError.Error() {
		t.Fatalf("expected error: '%s', got '%s'", testError.Error(), err.Error())
	}
}

func TestRunCmd_newAuthServer_ShouldReturnTestError(t *testing.T) {
	// backup originals
	origNewConfig := newConfig
	origEnvLoad := envLoad
	origNewServer := newAuthServer
	defer func() {
		newConfig = origNewConfig
		envLoad = origEnvLoad
		newAuthServer = origNewServer
	}()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConfig := NewMockConfig(ctrl)
	mockServer := NewMockServer(ctrl)
	mockEnvironments := NewMockEnvironments(ctrl)

	mockConfig.EXPECT().Values().Times(0)
	mockServer.EXPECT().Run(context.Background()).Return(nil).Times(0)

	// arrange
	cmd := newTestCommandDebug()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return mockConfig, nil
	}
	envLoad = func() (env.Environments, error) { return mockEnvironments, nil }
	newAuthServer = func(_ config.Config, _ env.Environments) (server.Server, error) {
		return nil, testError
	}

	// act
	err := runCmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatalf("expected error: '%s', got none", testError)
	}
	if err.Error() != testError.Error() {
		t.Fatalf("expected error: '%s', got '%s'", testError.Error(), err.Error())
	}
}
