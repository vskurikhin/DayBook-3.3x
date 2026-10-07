// Package reset_test — test контракта трассировки pkg/raft:
// сценарий вывода по умолчанию (stderr-путь).
package reset_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/raft/store"
	"github.com/vskurikhin/raft/pkg/raft/transp"
)

// traceLinePattern — полная форма строки трассировки сцены: локальная
// метка с шестью микросекундами и обязательный префикс состояния.
var traceLinePattern = regexp.MustCompile(
	`(?m)^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6} \[[FLC?],N:\d+,T:\d{3,}\] `)

// logBuffer — потокобезопасный приёмник стандартного логгера: запись
// ведётся через log.Printf, чтение — из теста.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write принимает порцию вывода логгера.
func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

// String возвращает накопленный вывод.
func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// traceLevel — порог трассировки бинарника. Задаётся явно: эмиттер
// сценария (cm.Stop) печатает на уровне 0 и виден только при Level > 0,
// а TraceConfig{} означает выключенную трассировку.
const traceLevel = 1

// traceTimeout — предельное время ожидания Flush/Shutdown в сценарии.
const traceTimeout = 2 * time.Second

// stderrPath — файл, которым на время процесса подменяется стандартный
// поток ошибок. Писатель захватывает os.Stderr в момент SetTrace и пишет
// в него напрямую, поэтому перехват выполняется до конфигурации.
var stderrPath string

// TestMain подменяет os.Stderr временным файлом до единственной успешной
// конфигурации трассировки процесса (SetTrace с пустым LogFile), а после
// прогона останавливает писателя и восстанавливает поток.
func TestMain(m *testing.M) {
	originalStderr := os.Stderr
	f, err := os.CreateTemp("", "raft-trace-reset-*.log")
	if err != nil {
		_, _ = originalStderr.WriteString("raft: temp stderr: " + err.Error() + "\n")
		os.Exit(1)
	}
	stderrPath = f.Name()
	os.Stderr = f
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel}); err != nil {
		_, _ = originalStderr.WriteString("raft: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()

	ctx, cancel := context.WithTimeout(context.Background(), traceTimeout)
	if err := raft.ShutdownTrace(ctx); err != nil {
		_, _ = originalStderr.WriteString("raft: trace shutdown failed: " + err.Error() + "\n")
		code = 1
	}
	cancel()
	if err := f.Close(); err != nil {
		_, _ = originalStderr.WriteString("raft: trace file close: " + err.Error() + "\n")
		code = 1
	}
	os.Stderr = originalStderr
	_ = os.Remove(stderrPath)
	os.Exit(code)
}

func TestTraceReset(t *testing.T) {
	// Порядок cleanup (LIFO): остановка CM → проверка leaktest.
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	// Строгий set-once: повторный вызов — ошибка контракта.
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel}); err == nil {
		t.Fatal("SetTrace: want contract error, got nil")
	}

	// Создаём CM (InmemTransport + MapStorage, без кластера).
	commitCh := make(chan raft.CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitCh {
		}
	}()
	ready := make(chan any)
	cm := raft.NewConsensusModule(
		0, []int{}, transp.NewInmemTransport("trace-reset"),
		store.NewMapStorage(), raft.NewCommitChannelFSM(commitCh), ready,
	)
	close(ready)
	t.Cleanup(func() {
		cm.Stop()
		close(commitCh)
		<-readerDone
	})

	// Эмиттер трассировки — cm.Stop(); запись идёт напрямую в захваченный
	// os.Stderr, поэтому log.SetOutput на неё не влияет. Перенаправление
	// стандартного логгера в буфер обязано остаться без следа трассировки.
	var logBuf logBuffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	cm.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), traceTimeout)
	defer cancel()
	if err := raft.FlushTrace(ctx); err != nil {
		t.Fatalf("FlushTrace: %v", err)
	}
	data, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "CM.Stop called / becomes Dead") {
		t.Fatalf("stderr trace %q does not contain the CM.Stop entry: %q", stderrPath, data)
	}
	// Метка события содержит микросекунды, префикс — состояние и терм.
	if !traceLinePattern.MatchString(string(data)) {
		t.Fatalf("строка трассировки не содержит микросекунд или обязательного префикса: %q", data)
	}
	if strings.Contains(logBuf.String(), "CM.Stop") {
		t.Fatalf("log.SetOutput перенаправил трассировку: %q", logBuf.String())
	}
}
