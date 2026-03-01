package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "auth-server",
	Short: "Authentication server for DayBook application",
	Long: `The "auth-server" command is the entry point for the authentication service
of the DayBook application.

It provides subcommands to run the server, manage configuration, and
perform administrative tasks. Configuration can be supplied via CLI flags,
a configuration file, or environment variables.

Typical usage:

  # Run the server with default configuration
  auth-server run

  # Run the server with custom config file
  auth-server --config /path/to/config.yaml run

All commands support standard flags such as --debug, --verbose, and --config.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {
		setSlogDebug(cmd)
		mergeCobraAndViper(cmd)
		slogInfoVerbose(cmd)
		config.MakeConfig(cmd)
	},
}

func slogInfoVerbose(cmd *cobra.Command) {
	if tool.IsVerbose(cmd) {
		for key, value := range viper.GetViper().AllSettings() {
			slog.Info("variable:", key, value)
		}
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
