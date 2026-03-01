package cmd

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

func mergeCobraAndViper(cmd *cobra.Command) {
	for key, _ := range viper.GetViper().AllSettings() {
		viperBindPFlag(key, tool.SnakeCaseToKebabCase(key), cmd)
	}
	cmd.Flags().VisitAll(mergeCobraAndViperFunc(cmd))
}

func mergeCobraAndViperFunc(cmd *cobra.Command) func(f *pflag.Flag) {
	return func(f *pflag.Flag) {
		if tool.IsDebug(cmd) {
			slog.Debug(
				"variable:", "flag",
				fmt.Sprintf("FLAG: (Type: %s, Changed: %v) --%s=%q",
					f.Value.Type(), f.Changed, f.Name, f.Value.String(),
				))
		}
		if f.Changed || viper.Get(tool.KebabCaseToSnakeCase(f.Name)) == nil {
			switch f.Value.Type() {
			case "bool":
				viper.Set(tool.KebabCaseToSnakeCase(f.Name), f.Value.String() == "true")
			case "int":
				value, _ := strconv.Atoi(f.Value.String())
				if value > 0 {
					viper.Set(tool.KebabCaseToSnakeCase(f.Name), value)
				} else {
					value, _ = strconv.Atoi(f.DefValue)
					viper.Set(tool.KebabCaseToSnakeCase(f.Name), value)
				}
			case "string":
				viper.Set(tool.KebabCaseToSnakeCase(f.Name), f.Value.String())
			}
		}
	}
}

func viperBindPFlag(key, flag string, cmd *cobra.Command) {
	err := viper.BindPFlag(key, cmd.Flags().Lookup(flag))
	if err != nil && tool.IsDebug(cmd) {
		slog.Error("Error binding flag", "error", err)
	}
}

func setSlogDebug(cmd *cobra.Command) {
	if tool.IsDebug(cmd) {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
}
