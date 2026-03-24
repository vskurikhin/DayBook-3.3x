// Package cmd provides the CLI entry point and command configuration
// for the application.
//
// The package is built on top of Cobra and Viper and is responsible for:
//
//   - Defining root and run - subcommands
//   - Registering CLI flags
//   - Binding flags to configuration values
//   - Loading configuration from file and environment variables
//
// # Configuration Resolution Order
//
// The application resolves configuration in the following order:
//
//  1. Explicit config file passed via --config flag
//  2. Default config file in $HOME directory named ".auth-server.yaml"
//  3. Environment variables (via Viper AutomaticEnv)
//  4. Command-line flags
//
// # Persistent Flags
//
// The root command defines the following persistent flags:
//
//	--debug, -d
//	    Enables debug mode.
//
//	--verbose, -v
//	    Enables verbose output.
//
//	--config
//	    Path to a custom configuration file.
//
// Local Flags (run command)
//
// The run command defines:
//
//	--address
//	    Server address in host:port format.
//
//	--insecure-skip-verify
//	    Disables TLS certificate verification.
//
// # Configuration Loading
//
// During initialization, initConfig is automatically executed using
// cobra.OnInitialize. It:
//
//   - Loads the configuration file if present
//   - Enables environment variable binding
//   - Prints the active config file path to stderr
//
// This package relies on global Cobra and Viper instances and performs
// initialization in init(), which means command wiring happens at import time.
//
// For advanced use cases (testing or embedding), consider refactoring
// into a constructor-based command builder instead of using global state.
package cmd

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

const (
	FlagAddress                 = "address"
	FlagAuthCost                = "auth-cost"
	FlagDBHost                  = "dbhost"
	FlagDBName                  = "dbname"
	FlagDBOptions               = "dboptions"
	FlagDBPassword              = "dbpassword"
	FlagDBPoolHealthCheckPeriod = "db-pool-health-check-period"
	FlagDBPoolMaxConnIdleTime   = "db-pool-max-conn-idle-time"
	FlagDBPoolMaxConnLifeTime   = "db-pool-max-conn-lifetime"
	FlagDBPoolMaxConns          = "db-pool-max-conns"
	FlagDBPoolMinConns          = "db-pool-min-conns"
	FlagDBPort                  = "dbport"
	FlagDBUser                  = "dbuser"
	FlagDebug                   = "debug"
	FlagHostname                = "hostname"
	FlagInsecureSkipVerify      = "insecure-skip-verify"
	FlagIsHTTPS                 = "https"
	FlagJWThs256SignKey         = "jwt-hs256-sign-key"
	FlagRequestMaxBytes         = "request-max-bytes"
	FlagServerCertFile          = "server-cert-file"
	FlagServerKeyFile           = "server-key-file"
	FlagValidPeriodAccessToken  = "valid-period-access-token"
	FlagValidPeriodRefreshToken = "valid-period-refresh-token"
	FlagVerbose                 = "verbose"
)

var (
	envLoad   = func() (env.Environments, error) { return env.EnvironmentsLoad() }
	newConfig = func(cmd *cobra.Command) (config.Config, error) { return config.NewConfig(cmd) }
	newDB     = func(ctx context.Context, cfg config.Config, env env.Environments) (db.DB, error) {
		return db.NewDB(ctx, cfg, env)
	}
)

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default is $HOME/.auth-server.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.PersistentFlags().BoolP(FlagDebug, "d", false, "Help message for debug.")
	rootCmd.PersistentFlags().BoolP(FlagVerbose, "v", false, "Verbose.")

	runCmd.Flags().Bool(FlagInsecureSkipVerify, false, "Controls whether a client verifies the server's certificate chain and host name.")
	runCmd.Flags().Bool(FlagIsHTTPS, false, "Controls whether a server's HTTPS bindings.")
	runCmd.Flags().Duration(FlagDBPoolHealthCheckPeriod, time.Minute, "Pgx DB pool health check period.")
	runCmd.Flags().Duration(FlagDBPoolMaxConnIdleTime, 30*time.Minute, "Pgx DB pool max connection idle time.")
	runCmd.Flags().Duration(FlagDBPoolMaxConnLifeTime, time.Hour, "Pgx DB pool max connection lifetime.")
	runCmd.Flags().Duration(FlagValidPeriodAccessToken, 45*time.Second, "Valid period access token.")
	runCmd.Flags().Duration(FlagValidPeriodRefreshToken, 15*time.Hour, "Valid period refresh token.")
	runCmd.Flags().String(FlagAddress, "127.0.0.1:8089", "Address as host:port")
	runCmd.Flags().String(FlagDBHost, "localhost", "Pgx pool DB host.")
	runCmd.Flags().String(FlagDBName, "db", "Pgx pool DB name.")
	runCmd.Flags().String(FlagDBOptions, "application_name=auth&search_path=auth", "Pgx pool DB options.")
	runCmd.Flags().String(FlagDBPassword, "password", "Pgx pool DB user password.")
	runCmd.Flags().String(FlagDBUser, "dbuser", "Pgx pool DB username.")
	runCmd.Flags().String(FlagHostname, "localhost", "Hostname.")
	runCmd.Flags().String(FlagJWThs256SignKey, "", "JWT HS256 signing key.")
	runCmd.Flags().String(FlagServerCertFile, "cert.pem", "Server certificate file.")
	runCmd.Flags().String(FlagServerKeyFile, "key.pem", "Server certificate key file.")
	runCmd.Flags().Uint16(FlagDBPort, 5432, "Pgx pool DB port.")
	runCmd.Flags().Uint64(FlagRequestMaxBytes, 1<<20, "Request max bytes.")
	runCmd.Flags().Uint8(FlagAuthCost, 14, "The minimum allowable cost as passed in")
	runCmd.Flags().Uint8(FlagDBPoolMaxConns, 4, "Pgx DB pool max connections.")
	runCmd.Flags().Uint8(FlagDBPoolMinConns, 0, "Pgx DB pool min connections.")

	migrateCmd.Flags().String(FlagDBHost, "localhost", "Pgx pool DB host.")
	migrateCmd.Flags().String(FlagDBName, "db", "Pgx pool DB name.")
	migrateCmd.Flags().String(FlagDBOptions, "application_name=auth&search_path=auth", "Pgx pool DB options.")
	migrateCmd.Flags().String(FlagDBPassword, "password", "Pgx pool DB user password.")
	migrateCmd.Flags().Uint16(FlagDBPort, 5432, "Pgx pool DB port.")
	migrateCmd.Flags().String(FlagDBUser, "dbuser", "Pgx pool DB username.")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(migrateCmd)
}

// initConfig reads in Config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use Config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search Config in home directory with name ".auth-server" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".auth-server")
	}
	viper.AutomaticEnv() // read in environment variables that match

	// If a Config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		slog.Info("Using Config", slog.String("file", viper.ConfigFileUsed()))
	}
}
