package _init

import (
	"log"

	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/internal/config"
	"github.com/vskurikhin/raft/pkg/kvservice"
)

var Values config.Values

// configureTraceLogging применяет параметры трассировки командной строки:
// --trace-log-level задаёт пороги обоих пакетов (raft и kvservice),
// --trace-cm-log-file — файл трассировки консенсус-модуля,
// --trace-kv-log-file — файл трассировки KV-сервиса (пустая строка — стандартный логгер).
// Это единственное место, где значения флагов отображаются в публичные типы raft.TraceConfig
// и kvservice.TraceConfig: публичные пакеты не зависят от internal/config.
func configureTraceLogging(values config.Values) error {
	err := raft.SetTrace(raft.TraceConfig{
		Level:   values.TraceLogLevel,
		LogFile: values.TraceCMLogFile,
	})
	if err != nil {
		return err
	}
	return kvservice.SetTrace(kvservice.TraceConfig{
		Level:   values.TraceLogLevel,
		LogFile: values.TraceKVLogFile,
	})
}

//nolint:gochecknoinits
func init() {
	Values = config.ParseFlags()
	if err := configureTraceLogging(Values); err != nil {
		log.Fatal(err)
	}
}
