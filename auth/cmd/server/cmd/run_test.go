package cmd

import (
	"context"
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
	newConfig = func(cmd *cobra.Command) config.Config {
		return mockConfig
	}
	newAuthServer = func(cfg config.Config) server.Server {
		return mockServer
	}

	// act
	runCmd.Run(cmd, []string{})
}
