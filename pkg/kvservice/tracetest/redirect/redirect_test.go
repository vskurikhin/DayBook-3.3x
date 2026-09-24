// Package redirect_test — test контракта трассировки pkg/kvservice:
// сценарий перенаправления в файл.
package redirect_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/kvservice"
	"github.com/vskurikhin/raft/pkg/raft/store"
	"github.com/vskurikhin/raft/pkg/raft/transp"
)

const traceLevel = 1

func TestTraceRedirect(t *testing.T) {
	// Порядок cleanup (LIFO): остановка сервиса → проверка leaktest.
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	// err-path: вызов с недоступным путём ДО успешной конфигурации —
	// ошибка I/O; окно конфигурации не расходуется.
	// При -count>1 строгое set-once окно уже израсходовано первым
	// прогоном процесса (процессный и необратимый контракт) — полный
	// сценарий выполняется один раз на процесс.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "trace.log")
	err := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel, LogFile: bad})
	if err == nil {
		t.Fatalf("SetTrace(%q): want error, got nil", bad)
	}
	if strings.Contains(err.Error(), "exactly once") {
		t.Skip("trace configuration window consumed by a previous run")
	}

	// Ошибка I/O не израсходовала окно: немедленный повтор с корректным
	// путём обязан быть успешным и является ЕДИНСТВЕННОЙ успешной
	// конфигурацией процесса.
	path := filepath.Join(t.TempDir(), "kv-trace.log")
	if err := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel, LogFile: path}); err != nil {
		t.Fatalf("SetTrace: %v", err)
	}

	// Строгий set-once: повторный вызов ДО создания сервиса — ошибка контракта.
	contractErr := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel, LogFile: path})
	if contractErr == nil {
		t.Fatal("SetTrace: want contract error before service creation, got nil")
	}

	// Ошибка контракта формулируется в терминологии СВОЕГО пакета:
	// kvservice/KVService, а не ConsensusModule (регрессия на копипасту
	// текста ошибки из pkg/raft).
	if !strings.Contains(contractErr.Error(), "kvservice") {
		t.Errorf("contract error %q does not mention kvservice", contractErr)
	}
	if strings.Contains(contractErr.Error(), "ConsensusModule") {
		t.Errorf("contract error %q mentions ConsensusModule of another package", contractErr)
	}

	// Создаём KVService; сторожевой флаг _traceCMCreated поднимается
	// конструктором.
	kvs := newTestService(t)

	// Эмиттер трассировки — kvs.ServeHTTP (traceLogf("serving HTTP on %s"),
	// печатается при _traceKV=1 > 0): после запуска файл не пуст.
	if err := kvs.ServeHTTP(":0"); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "serving HTTP on") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace file %q does not contain the ServeHTTP entry: %v", path, err)
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(5 * time.Millisecond)
	}

	// fail-fast: повторный вызов ПОСЛЕ создания сервиса — ошибка контракта.
	if err := kvservice.SetTrace(kvservice.TraceConfig{Level: traceLevel, LogFile: path}); err == nil {
		t.Fatal("SetTrace: want contract error after service creation, got nil")
	}
}

// newTestService создаёт KVService с хранилищем во временном каталоге
// и останавливает его в t.Cleanup.
func newTestService(t *testing.T) *kvservice.KVService {
	t.Helper()
	ready := make(chan any)
	transport, err := transp.NewTCPTransport(":0", 0, 0)
	if err != nil {
		t.Fatalf("transp.NewTCPTransport: %v", err)
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
