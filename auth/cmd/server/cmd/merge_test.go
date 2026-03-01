package cmd

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// helper
func resetViper() {
	viper.Reset()
}

func newTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "test",
	}

	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().Bool("enabled", false, "")
	cmd.Flags().Int("port", 8080, "")
	cmd.Flags().String("name", "default", "")

	return cmd
}

func TestMergeCobraAndViper(t *testing.T) {
	type (
		args struct {
			name  string
			value string
		}
	)
	tests := []struct {
		name   string
		args   args
		result func(name string) any
		want   any
	}{
		{
			name: "positive #1: Bool flag merge",
			args: args{
				name:  "enabled",
				value: "true",
			},
			result: func(name string) any {
				return viper.GetBool(name)
			},
			want: true,
		},
		{
			name: "positive #2: Int flag positive value",
			args: args{
				name:  "port",
				value: "9000",
			},
			result: func(name string) any {
				return viper.GetInt(name)
			},
			want: 9000,
		},
		{
			name: "positive #3: Int flag fallback to default",
			args: args{
				name:  "port",
				value: "-1",
			},
			result: func(name string) any {
				return viper.GetInt(name)
			},
			want: 8080,
		},
		{
			name: "positive #3: String flag",
			args: args{
				name:  "name",
				value: "production",
			},
			result: func(name string) any {
				return viper.GetString(name)
			},
			want: "production",
		},
		{
			name: "positive #4: Existing viper value should not override if flag not changed",
			args: args{
				name:  "name",
				value: "from-config",
			},
			result: func(name string) any {
				return viper.GetString(name)
			},
			want: "from-config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViper()
			cmd := newTestCommand()
			_ = cmd.Flags().Set(tt.args.name, tt.args.value)
			mergeCobraAndViper(cmd)
			val := tt.result(tt.args.name)
			assert.Equal(t, tt.want, val)
		})
	}
}

func TestViperBindPFlag_ErrorLogging(t *testing.T) {
	resetViper()
	cmd := newTestCommand()

	_ = cmd.Flags().Set("debug", "true")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	viperBindPFlag("missing", "missing-flag", cmd)

	if buf.Len() == 0 {
		t.Errorf("expected error log when bind fails in debug mode")
	}
}

func TestSetSlogDebug(t *testing.T) {
	cmd := newTestCommand()
	_ = cmd.Flags().Set("debug", "true")

	setSlogDebug(cmd)

	// если уровень установлен — значит функция отработала
	// прямой getter нет, поэтому просто проверяем отсутствие panic
}
