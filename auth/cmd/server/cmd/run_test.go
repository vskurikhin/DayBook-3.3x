package cmd

import (
	"context"
	"errors"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
	"testing"

	"github.com/spf13/cobra"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"go.uber.org/mock/gomock"
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
	mockServer.EXPECT().Run(context.Background()).Return().Times(1)

	// arrange
	cmd := newTestCommand()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return mockConfig, nil
	}
	newAuthServer = func(_ config.Config, _ env.Environments) server.Server {
		return mockServer
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
	mockServer.EXPECT().Run(context.Background()).Return().Times(0)

	// arrange
	cmd := newTestCommandDebug()
	newConfig = func(cmd *cobra.Command) (config.Config, error) {
		return nil, testError
	}
	newAuthServer = func(_ config.Config, _ env.Environments) server.Server {
		return mockServer
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
