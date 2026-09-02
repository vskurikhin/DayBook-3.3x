// Package reset_test — test контракта трассировки pkg/raft:
// сценарий вывода по умолчанию (stderr-путь).
package reset_test

import (
	"bytes"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// buf — синхронизированный приёмник стандартного логгера: запись
// ведётся из горутин CM через log.Default(), чтение — из теста.
var buf syncBuffer

// traceLevel — порог трассировки бинарника. Задаётся явно: эмиттер
// сценария (cm.Stop) печатает на уровне 0 и виден только при Level > 0,
// а TraceConfig{} означает выключенную трассировку.
const traceLevel = 1

// TestMain выполняет log.SetOutput ДО конфигурации (безопасно в
// выделенном бинарнике) и единственную успешную конфигурацию
// трассировки процесса: SetTrace с пустым LogFile.
func TestMain(m *testing.M) {
	log.SetOutput(&buf)
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel}); err != nil {
		_, _ = os.Stderr.WriteString("raft: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
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
		0, []int{}, raft.NewInmemTransport("trace-reset"),
		store.NewMapStorage(), raft.NewCommitChannelFSM(commitCh), ready,
	)
	close(ready)
	t.Cleanup(func() {
		cm.Stop()
		close(commitCh)
		<-readerDone
	})

	// Эмиттер трассировки — cm.Stop(); запись идёт в стандартный
	// логгер (пустой LogFile), файл трассировки не создаётся.
	cm.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "CM.Stop called / becomes Dead") {
		if time.Now().After(deadline) {
			t.Fatalf("trace buffer does not contain the CM.Stop entry: %q", buf.String())
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(5 * time.Millisecond)
	}
}

// syncBuffer — потокобезопасный буфер для перехвата вывода
// стандартного логгера.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
