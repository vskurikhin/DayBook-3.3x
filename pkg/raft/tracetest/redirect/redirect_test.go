// Package redirect_test — test контракта трассировки pkg/raft:
// сценарий перенаправления в файл;
// ровно один успешный вызов SetTrace на процесс;
// любой повторный вызов — ошибка контракта.
package redirect_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
)

// traceLevel — порог трассировки бинарника. Задаётся явно: эмиттер
// сценария (cm.Stop) печатает на уровне 0 и виден только при Level > 0,
// а TraceConfig{} означает выключенную трассировку.
const traceLevel = 1

func TestTraceRedirect(t *testing.T) {
	// Порядок cleanup (LIFO): остановка CM → проверка leaktest.
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	// err-path: вызов с недоступным путём ДО успешной конфигурации —
	// ошибка I/O; окно конфигурации не расходуется.
	// При -count>1 строгое set-once окно уже израсходовано первым
	// прогоном процесса (процессный и необратимый контракт) — полный
	// сценарий выполняется один раз на процесс.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "trace.log")
	err := raft.SetTrace(raft.TraceConfig{Level: traceLevel, LogFile: bad})
	if err == nil {
		t.Fatalf("SetTrace(%q): want error, got nil", bad)
	}
	if strings.Contains(err.Error(), "exactly once") {
		t.Skip("trace configuration window consumed by a previous run")
	}

	// Ошибка I/O не израсходовала окно: немедленный повтор с корректным
	// путём обязан быть успешным и является ЕДИНСТВЕННОЙ успешной
	// конфигурацией процесса.
	path := filepath.Join(t.TempDir(), "raft-trace.log")
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel, LogFile: path}); err != nil {
		t.Fatalf("SetTrace: %v", err)
	}

	// Строгий set-once: повторный вызов ДО создания CM — ошибка контракта.
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel, LogFile: path}); err == nil {
		t.Fatal("SetTrace: want contract error before CM creation, got nil")
	}

	// Создаём CM (InmemTransport + MapStorage, без кластера).
	// CommitChannelFSM требует читателя канала при живом узле.
	commitCh := make(chan raft.CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitCh {
		}
	}()
	ready := make(chan any)
	cm := raft.NewConsensusModule(
		0, []int{}, raft.NewInmemTransport("trace-redirect"),
		raft.NewMapStorage(), raft.NewCommitChannelFSM(commitCh), ready,
	)
	close(ready)
	t.Cleanup(func() {
		cm.Stop()
		close(commitCh)
		<-readerDone
	})

	// Эмиттер трассировки — cm.Stop() (traceLogf(0, "CM.Stop called /
	// becomes Dead"), печатается при traceCM=1 > 0): после Stop файл
	// не пуст.
	//
	// Эта же проверка закрывает требование «ошибка I/O не изменила
	// состояние трассировки»: неуспешный вызов выше не израсходовал окно
	// (иначе успешная конфигурация вернула бы ошибку контракта) и не
	// оставил порог непригодным для печати уровня 0 (иначе файл был бы
	// пуст).
	cm.Stop()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "CM.Stop called / becomes Dead") {
		t.Errorf("trace file %q does not contain the CM.Stop entry: %q", path, data)
	}

	// fail-fast: повторный вызов ПОСЛЕ создания CM — ошибка контракта.
	if err := raft.SetTrace(raft.TraceConfig{Level: traceLevel, LogFile: path}); err == nil {
		t.Fatal("SetTrace: want contract error after CM creation, got nil")
	}
}
