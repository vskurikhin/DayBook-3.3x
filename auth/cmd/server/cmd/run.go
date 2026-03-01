package cmd

import (
	"github.com/spf13/cobra"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
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
	Run: func(cmd *cobra.Command, args []string) {
		setSlogDebug(cmd)
		mergeCobraAndViper(cmd)
		slogInfoVerbose(cmd)
		cfg := config.MakeConfig(cmd)
		server.Run(cfg)
	},
}
