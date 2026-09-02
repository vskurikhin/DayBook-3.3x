// Package reset_test — test контракта трассировки pkg/kvservice:
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
	"github.com/vskurikhin/raft/pkg/kvservice"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// buf — синхронизированный приёмник стандартного логгера: запись
// ведётся из HTTP-горутины через log.Default(), чтение — из теста.
var buf syncBuffer

// traceLevel — порог трассировки бинарника. Задаётся явно: эмиттер
// сценария (kvs.ServeHTTP) печатается под гейтом _traceKV > 0,
// а TraceConfig{} означает выключенную трассировку.
const traceLevel = 1

// TestMain выполняет log.SetOutput ДО конфигурации (безопасно в
// выделенном бинарнике) и единственную успешную конфигурацию
// трассировки процесса: SetTrace с пустым LogFile.
func TestMain(m *testing.M) {
	log.SetOutput(&buf)
	if err := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel}); err != nil {
		_, _ = os.Stderr.WriteString("kvservice: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestTraceReset(t *testing.T) {
	// Порядок cleanup (LIFO): остановка сервиса → проверка leaktest.
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	// Строгий set-once: повторный вызов — ошибка контракта.
	if err := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel}); err == nil {
		t.Fatal("SetTrace: want contract error, got nil")
	}

	// Создаём сервис; эмиттер — kvs.ServeHTTP; запись идёт в
	// стандартный логгер (пустой LogFile), файл трассировки
	// не создаётся.
	kvs := newTestService(t)
	if err := kvs.ServeHTTP(":0"); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "serving HTTP on") {
		if time.Now().After(deadline) {
			t.Fatalf("trace buffer does not contain the ServeHTTP entry: %q", buf.String())
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(5 * time.Millisecond)
	}
}

// newTestService создаёт KVService с хранилищем во временном каталоге
// и останавливает его в t.Cleanup.
func newTestService(t *testing.T) *kvservice.KVService {
	t.Helper()
	ready := make(chan any)
	transport, err := raft.NewTCPTransport(":0", 0, 0)
	if err != nil {
		t.Fatalf("raft.NewTCPTransport: %v", err)
	}
	cfg := &kvservice.Config{
		HTTPAddress: ":0",
		Config: raft.Config{
			PeerIds:   []int{},
			ServerID:  7,
			Storage:   store.NewMapStorage(),
			Transport: transport,
		},
	}
	kvs := kvservice.New(cfg, ready)
	close(ready)
	t.Cleanup(func() {
		if err := kvs.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return kvs
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
