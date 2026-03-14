package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/wire"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

//go:generate mockgen -destination=run_mock_config_test.go -package=cmd github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/cmd Config
type Config interface {
	Values() config.Values
}

//go:generate mockgen -destination=run_mock_environments_test.go -package=cmd github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/cmd Environments
type Environments interface {
	Values() env.Values
}

//go:generate mockgen -destination=run_mock_server_test.go -package=cmd github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server Server
type Server interface {
	Run(ctx context.Context) error
}

var (
	newAuthServer = func(cfg config.Config, env env.Environments, dbp db.DB) (server.Server, error) {
		return wire.InitializeServer(cfg, env, dbp)
	}
)

// runCmd represents the base command when called without any subcommands
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Starts the authentication server",
	Long: `The "run" command launches the authentication server of the application.
It initializes the configuration by reading values from CLI flags, a configuration file,
or environment variables, sets the logging level, and starts the main server
using the obtained configuration.`,
	// The following line your bare application
	// has an action associated with it:
	RunE: func(cmd *cobra.Command, args []string) error {
		setSlogDebug(cmd)
		mergeCobraAndViper(cmd)
		slogInfoVerbose(cmd)

		cfg, errConfig := newConfig(cmd)
		if errConfig != nil {
			return errConfig
		}

		env, errEnvLoad := envLoad()
		if errEnvLoad != nil {
			return errEnvLoad
		}

		dbp, err := newDB(cmd.Context(), cfg, env)
		if err != nil {
			return err
		}

		srv, err := newAuthServer(cfg, env, dbp)
		if err != nil {
			return err
		}
		return srv.Run(cmd.Context())
	},
}
