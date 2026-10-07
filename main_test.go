package raft

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// Переменные окружения измерительного режима трассировки. Режим
// включается только парой переменных: без обеих тестовый процесс
// остаётся в обычном режиме с уровнем 0.
const (
	// traceBenchEnvFile — путь к реальному файлу-приёмнику трассировки.
	traceBenchEnvFile = "RAFT_TRACE_BENCH_FILE"

	// traceBenchEnvLevel — порог детализации измерительного режима.
	traceBenchEnvLevel = "RAFT_TRACE_BENCH_LEVEL"

	// traceBenchLevel — обязательное значение порога: сообщения ступени
	// _traceLevelProgress (2) печатаются, только если порог больше 2.
	traceBenchLevel = 3
)

// traceBenchConfig собирает конфигурацию трассировки из пары переменных
// окружения измерительного режима. Без обеих переменных возвращается
// нулевая конфигурация — обычный уровень 0 и стандартный логгер, то есть
// поведение всех обычных тестов не меняется. Неполная пара или
// некорректное значение — ошибка конфигурации: измерительный запуск не
// должен стартовать с неясным порогом или без приёмника.
func traceBenchConfig() (TraceConfig, error) {
	file, fileSet := os.LookupEnv(traceBenchEnvFile)
	levelText, levelSet := os.LookupEnv(traceBenchEnvLevel)
	if !fileSet && !levelSet {
		return TraceConfig{}, nil
	}
	if !fileSet || !levelSet {
		return TraceConfig{}, fmt.Errorf(
			"%s and %s must be set together", traceBenchEnvFile, traceBenchEnvLevel)
	}
	if file == "" {
		return TraceConfig{}, fmt.Errorf("%s must not be empty", traceBenchEnvFile)
	}
	level, err := strconv.Atoi(levelText)
	if err != nil {
		return TraceConfig{}, fmt.Errorf("%s must be an integer: %w", traceBenchEnvLevel, err)
	}
	if level != traceBenchLevel {
		return TraceConfig{}, fmt.Errorf(
			"%s must be %d in the measurement mode, got %d",
			traceBenchEnvLevel, traceBenchLevel, level)
	}
	return TraceConfig{Level: level, LogFile: file}, nil
}

// traceShutdownTimeout — предельное время остановки писателя при
// завершении тестового процесса.
const traceShutdownTimeout = 2 * time.Second

// TestMain выполняет единственную статическую конфигурацию трассировки.
// Обычный режим тестов — уровень 0 и стандартный логгер. Измерительный
// режим включается парой переменных окружения (файл-приёмник и порог 3)
// и предназначен для прогонов с -run '^$', в которых работают только
// бенчмарки включённого пути трассировки. После прогона писатель
// останавливается: завершившийся процесс не оставляет горутину.
func TestMain(m *testing.M) {
	cfg, err := traceBenchConfig()
	if err != nil {
		_, _ = os.Stderr.WriteString("raft: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := SetTrace(cfg); err != nil {
		_, _ = os.Stderr.WriteString("raft: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	ctx, cancel := context.WithTimeout(context.Background(), traceShutdownTimeout)
	if err := ShutdownTrace(ctx); err != nil {
		_, _ = os.Stderr.WriteString("raft: trace shutdown failed: " + err.Error() + "\n")
		code = 1
	}
	cancel()
	os.Exit(code)
}
