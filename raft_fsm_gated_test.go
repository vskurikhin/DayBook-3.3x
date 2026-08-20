package raft

import (
	"bytes"
	"encoding/gob"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedTestFSM — тестовая машина состояний с управляемым шлюзом.
//
// Каждый вызов Apply для записи типа LogCommand забирает один пропуск из
// шлюза; пока пропусков нет, вызов стоит внутри Apply. Это позволяет
// детерминированно, без зависимости от планировщика, воспроизвести
// состояние «диапазон отправлен в очередь машины состояний, но ещё не
// применён», в котором раньше создавался снимок с завышенным индексом.
//
// Всё изменяемое состояние защищено mu: запись выполняется из горутины
// runFSM, чтение — из горутины теста и из Snapshot. Ожидание пропуска
// выполняется без удержания mu, иначе Snapshot встал бы вместе с Apply.
type gatedTestFSM struct {
	mu         sync.Mutex
	state      map[string]string
	maxApplied int
	entered    int
	gate       chan struct{}
	abort      chan struct{}
}

// gatedSnapshotState — сериализуемое содержимое снимка gatedTestFSM.
// MaxApplied позволяет тесту сверить содержимое снимка с его meta.Index.
type gatedSnapshotState struct {
	State      map[string]string
	MaxApplied int
}

// newGatedTestFSM создаёт машину состояний с ёмкостью шлюза gateCapacity.
// Ёмкость должна покрывать максимальное число пропусков, выданных до того,
// как их заберёт runFSM: release не должен блокировать горутину теста.
func newGatedTestFSM(gateCapacity int) *gatedTestFSM {
	return &gatedTestFSM{
		state:      make(map[string]string),
		maxApplied: -1,
		gate:       make(chan struct{}, gateCapacity),
		abort:      make(chan struct{}),
	}
}

// abortApplies переводит машину состояний в режим завершения: все вызовы
// Apply, стоящие на шлюзе и приходящие далее, возвращаются немедленно и
// состояние не меняют. Нужен для сценария остановки узла с записями,
// отправленными в очередь, но не применёнными: контракт FSM требует, чтобы
// Apply завершался, иначе Stop() не дождался бы горутины runFSM.
func (f *gatedTestFSM) abortApplies() {
	close(f.abort)
}

// release выдаёт n пропусков: столько вызовов Apply смогут завершиться.
func (f *gatedTestFSM) release(n int) {
	for i := 0; i < n; i++ {
		f.gate <- struct{}{}
	}
}

func (f *gatedTestFSM) Apply(log *LogEntry) any {
	f.mu.Lock()
	f.entered++
	f.mu.Unlock()

	select {
	case <-f.gate:
	case <-f.abort:
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if cmd, ok := log.Data.(string); ok {
		if parts := strings.SplitN(cmd, "=", 2); len(parts) == 2 {
			f.state[parts[0]] = parts[1]
		}
	}
	if log.Index > f.maxApplied {
		f.maxApplied = log.Index
	}
	return nil
}

func (f *gatedTestFSM) Snapshot() (FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := gatedSnapshotState{
		State:      make(map[string]string, len(f.state)),
		MaxApplied: f.maxApplied,
	}
	for k, v := range f.state {
		snap.State[k] = v
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(snap); err != nil {
		return nil, err
	}
	return &testSnapshot{data: buf.Bytes()}, nil
}

func (f *gatedTestFSM) Restore(r io.ReadCloser) error {
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	var snap gatedSnapshotState
	if err := gob.NewDecoder(bytes.NewBuffer(data)).Decode(&snap); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = snap.State
	if f.state == nil {
		f.state = make(map[string]string)
	}
	f.maxApplied = snap.MaxApplied
	return nil
}

// maxAppliedIndex возвращает максимальный индекс записи, применение
// которой к машине состояний завершилось.
func (f *gatedTestFSM) maxAppliedIndex() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxApplied
}

// enteredCount возвращает число вызовов Apply, дошедших до шлюза.
// Наблюдаемый признак для condition-wait вместо фиксированной паузы.
func (f *gatedTestFSM) enteredCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entered
}

func (f *gatedTestFSM) getState(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state[key]
}

// gatedBatchingTestFSM — вариант gatedTestFSM, реализующий BatchingFSM,
// чтобы обе ветви runFSM (applyBatch и applySingle) проверялись одними и
// теми же сценариями. Каждая запись батча забирает свой пропуск, поэтому
// шлюз управляет батчем так же по-записно.
type gatedBatchingTestFSM struct {
	*gatedTestFSM
}

func newGatedBatchingTestFSM(gateCapacity int) *gatedBatchingTestFSM {
	return &gatedBatchingTestFSM{gatedTestFSM: newGatedTestFSM(gateCapacity)}
}

func (f *gatedBatchingTestFSM) ApplyBatch(logs []*LogEntry) []any {
	responses := make([]any, len(logs))
	for i, log := range logs {
		responses[i] = f.Apply(log)
	}
	return responses
}

// waitForCondition — condition-wait с poll-интервалом (не фиксированная
// пауза): ждёт выполнения cond до истечения budget, иначе валит тест с
// сообщением msg.
func waitForCondition(t *testing.T, budget time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition: %s", msg)
}
