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
//  2. Default config file in $HOME directory named ".auth.yaml"
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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagAddress            = "address"
	FlagDebug              = "debug"
	FlagInsecureSkipVerify = "insecure-skip-verify"
	FlagVerbose            = "verbose"
)

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default is $HOME/.auth-server.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.PersistentFlags().BoolP(FlagDebug, "d", false, "Help message for debug")
	rootCmd.PersistentFlags().BoolP(FlagVerbose, "v", false, "Verbose")

	runCmd.Flags().String(FlagAddress, "", "Address as host:port")
	runCmd.Flags().Bool(FlagInsecureSkipVerify, false, "Controls whether a client verifies the server's certificate chain and host name.")

	rootCmd.AddCommand(runCmd)
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

		// Search Config in home directory with name ".auth" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".auth")
	}
	viper.AutomaticEnv() // read in environment variables that match

	// If a Config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		_, _ = fmt.Fprintln(os.Stderr, "Using Config file:", viper.ConfigFileUsed())
	}
}
