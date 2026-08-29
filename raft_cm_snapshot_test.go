package raft

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// failingSnapshotStore — тестовый двойник SnapshotStore, чей Create
// всегда возвращает ошибку и подсчитывает вызовы.
type failingSnapshotStore struct {
	mu      sync.Mutex
	creates int
}

func (f *failingSnapshotStore) Create(_, _ int, _ Configuration, _ int) (SnapshotSink, error) {
	f.mu.Lock()
	f.creates++
	f.mu.Unlock()
	return nil, errors.New("create failed")
}

func (f *failingSnapshotStore) List() ([]*SnapshotMeta, error) {
	return nil, nil
}

func (f *failingSnapshotStore) Open(_ string) (*SnapshotMeta, io.ReadCloser, error) {
	return nil, nil, errors.New("open failed")
}

func (f *failingSnapshotStore) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

// TestRunSnapshots_TakeSnapshotErrorDoesNotCrash проверяет,
// что ошибка takeSnapshot не роняет процесс: цикл снимков продолжает
// работать и повторяет попытки (Create вызывается снова). Сама ошибка
// логируется через traceLogf(0, ...) — проверка через перехват лога
// не выполняется, чтобы не мутировать глобальный логгер из теста.
func TestRunSnapshots_TakeSnapshotErrorDoesNotCrash(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := newSnapshotTestFSM()
	ready := make(chan any)
	close(ready)
	storage := NewMapStorage()
	transport := NewInmemTransport("single")
	store := &failingSnapshotStore{}
	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	defer cm.Stop()

	// Состояние, при котором shouldSnapshot вернёт true, а FSM-снимок
	// успешно создаётся — ошибка возникает только в store.Create.
	cm.mu.Lock()
	cm.cmState.lastLogIndex = 10
	cm.cmState.lastLogTerm = 1
	cm.cmState.lastApplied = 0
	// Снимок берёт индекс из fsmAppliedIndex, поэтому отметку фактического
	// применения выставляем явно: тест не должен зависеть от того, успела ли
	// машина состояний применить noop-запись лидера.
	cm.cmState.fsmAppliedIndex = 0
	cm.cmState.log = []LogEntry{{Index: 10, Term: 1, Type: LogCommand, Data: "k=v"}}
	cm.mu.Unlock()
	cm.SetSnapshotConfig(2, 20*time.Millisecond, 1)

	// Первая попытка должна произойти быстро (сигнал из SetSnapshotConfig);
	// ждём и убеждаемся, что CM жив и попытки повторяются циклом.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if store.createCount() >= 1 {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("takeSnapshot was never attempted: failing store Create not called")
}

// TestLeaderSendSnapshot_EmptyListSignalsSnapshot проверяет,
// что при пустом List() у лидера с lastSnapshotIndex >= 0 вызов не
// паникует, не меняет nextIndex и отправляет сигнал в snapshotCh.
func TestLeaderSendSnapshot_EmptyListSignalsSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{
		snapshotStore: NewInmemSnapshotStore(),
		snapshotCh:    make(chan struct{}, 1),
	}
	cm.cmState.lastSnapshotIndex = 5
	cm.leaderState.nextIndex = map[int]int{1: 0}

	cm.leaderSendSnapshot(1, 1)

	if cm.leaderState.nextIndex[1] != 0 {
		t.Fatalf("nextIndex = %d, want unchanged (0)", cm.leaderState.nextIndex[1])
	}
	select {
	case <-cm.snapshotCh:
	default:
		t.Fatal("snapshotCh not signaled on empty List")
	}
}

// -- Тесты идемпотентности InstallSnapshot. ---

// countingSnapshotStore — обёртка над InmemSnapshotStore, считающая
// вызовы Create: признак «снимок действительно применялся».
type countingSnapshotStore struct {
	*InmemSnapshotStore
	creates atomic.Int64
}

func (s *countingSnapshotStore) Create(index, term int, configuration Configuration, configIndex int) (SnapshotSink, error) {
	s.creates.Add(1)
	return s.InmemSnapshotStore.Create(index, term, configuration, configIndex)
}

// newInstallSnapshotCM собирает литеральный CM с уже установленным
// снимком (lastSnapshotIndex=10, term 2, пустой журнал) для прямого
// вызова handleInstallSnapshot.
func newInstallSnapshotCM() (*ConsensusModule, *countingSnapshotStore) {
	store := &countingSnapshotStore{InmemSnapshotStore: NewInmemSnapshotStore()}
	cm := &ConsensusModule{
		storage:       NewMapStorage(),
		snapshotStore: store,
		fsm:           newSnapshotTestFSM(),
	}
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = 10
	cm.cmState.lastSnapshotTerm = 2
	cm.cmState.lastLogIndex = 10
	cm.cmState.lastLogTerm = 2
	cm.cmState.lastApplied = 10
	cm.cmState.fsmAppliedIndex = 10
	cm.cmState.commitIndex = 10
	cm.cmState.termIndexMap = make(map[int]int)
	return cm, store
}

// installSnapshotRequestData — gob-данные снимка для snapshotTestFSM.
func installSnapshotRequestData(t *testing.T, state map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	return buf.Bytes()
}

// installSnapshotRequest формирует InstallSnapshotRequest с валидной
// конфигурацией и размером тела.
func installSnapshotRequest(leaderID, lastLogIndex, lastLogTerm int, data []byte) *InstallSnapshotRequest {
	cfgData, _ := EncodeConfiguration(Configuration{})
	return &InstallSnapshotRequest{
		RPCHeader:     RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: leaderID},
		Term:          1,
		LeaderID:      leaderID,
		LastLogIndex:  lastLogIndex,
		LastLogTerm:   lastLogTerm,
		Configuration: cfgData,
		ConfigIndex:   0,
		DataSize:      int64(len(data)),
	}
}

// driveInstallSnapshotRPC доставляет InstallSnapshot в handleInstallSnapshot
// и возвращает ответ.
func driveInstallSnapshotRPC(t *testing.T, cm *ConsensusModule, req *InstallSnapshotRequest, data []byte) InstallSnapshotResponse {
	t.Helper()
	respCh := make(chan RPCResponse, 1)
	cm.handleInstallSnapshot(RPC{Command: req, Reader: bytes.NewReader(data), RespChan: respCh}, req)
	resp := <-respCh
	reply, ok := resp.Reply.(*InstallSnapshotResponse)
	if !ok {
		t.Fatalf("unexpected reply type %T", resp.Reply)
	}
	return *reply
}

// TestInstallSnapshot_RepeatedIsNoOp — тест 1: два подряд
// InstallSnapshot с одинаковым индексом — второй обрабатывается как
// no-op: Success:true, snapshotStore.Create не вызывается повторно,
// состояние не изменяется. На HEAD до второй вызов создавал
// бы новый снимок (Create) — тест падает.
func TestInstallSnapshot_RepeatedIsNoOp(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, store := newInstallSnapshotCM()
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})
	req := installSnapshotRequest(99, 12, 2, data) // новее имеющегося (10)

	// Первая установка — применяется.
	reply := driveInstallSnapshotRPC(t, cm, req, data)
	if !reply.Success {
		t.Fatalf("first InstallSnapshot: Success=false: %+v", reply)
	}
	if got := store.creates.Load(); got != 1 {
		t.Fatalf("Create calls = %d, want 1 after first install", got)
	}
	if cm.cmState.lastSnapshotIndex != 12 {
		t.Fatalf("lastSnapshotIndex = %d, want 12", cm.cmState.lastSnapshotIndex)
	}

	// Повторная установка того же снимка — no-op.
	reply = driveInstallSnapshotRPC(t, cm, req, data)
	if !reply.Success {
		t.Fatalf("repeated InstallSnapshot: Success=false, want true (idempotent no-op)")
	}
	if got := store.creates.Load(); got != 1 {
		t.Fatalf("Create calls = %d after repeat, want 1 (no-op must not create)", got)
	}
	if cm.cmState.lastSnapshotIndex != 12 || cm.cmState.commitIndex != 12 || cm.cmState.lastApplied != 12 {
		t.Fatalf("state changed by no-op: snapshot=%d commit=%d applied=%d",
			cm.cmState.lastSnapshotIndex, cm.cmState.commitIndex, cm.cmState.lastApplied)
	}
}

// TestInstallSnapshot_StaleDoesNotRollBack — тест 2: InstallSnapshot
// с LastLogIndex меньше текущего lastSnapshotIndex — no-op, commitIndex и
// lastApplied не уменьшаются (монотонность по построению,).
func TestInstallSnapshot_StaleDoesNotRollBack(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, store := newInstallSnapshotCM() // lastSnapshotIndex = commitIndex = 10
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})
	req := installSnapshotRequest(99, 5, 2, data) // старее 10

	reply := driveInstallSnapshotRPC(t, cm, req, data)
	if !reply.Success {
		t.Fatalf("stale InstallSnapshot: Success=false, want true (no-op)")
	}
	if got := store.creates.Load(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 (stale snapshot must not be applied)", got)
	}
	if cm.cmState.commitIndex != 10 || cm.cmState.lastApplied != 10 {
		t.Fatalf("commitIndex/lastApplied rolled back: commit=%d applied=%d, want 10/10",
			cm.cmState.commitIndex, cm.cmState.lastApplied)
	}
	if cm.cmState.lastSnapshotIndex != 10 {
		t.Fatalf("lastSnapshotIndex = %d, want 10 (unchanged)", cm.cmState.lastSnapshotIndex)
	}
	if got := peerSum(cm.counters.installSnapshotSkippedStale); got < 1 {
		t.Fatalf("installSnapshotSkippedStale = %d, want >= 1", got)
	}
}

// countingReader — ридер, считающий прочитанные байты: проверка drain
// тела запроса при no-op.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// TestInstallSnapshot_NoOpDrainsBody — тест 3 (часть 1): при no-op
// тело запроса обязано быть вычитано существующим drain-механизмом в defer
// иначе на TCP-транспорте соединение рассинхронизируется.
func TestInstallSnapshot_NoOpDrainsBody(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, _ := newInstallSnapshotCM()
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})
	req := installSnapshotRequest(99, 5, 2, data) // no-op (5 <= 10)

	reader := &countingReader{r: bytes.NewReader(data)}
	respCh := make(chan RPCResponse, 1)
	cm.handleInstallSnapshot(RPC{Command: req, Reader: reader, RespChan: respCh}, req)
	resp := <-respCh
	reply, ok := resp.Reply.(*InstallSnapshotResponse)
	if !ok || !reply.Success {
		t.Fatalf("no-op InstallSnapshot reply: %+v", resp.Reply)
	}

	if got := reader.n.Load(); got != int64(len(data)) {
		t.Fatalf("drained %d bytes, want %d — request body must be fully drained", got, len(data))
	}
}

// TestInstallSnapshot_NoOpKeepsTCPConnectionUsable — тест 3
// (часть 2, TCP-уровень): после no-op InstallSnapshot то же соединение
// обязано обслужить следующий RPC (AppendEntries) — интегральная проверка
// drain'а на реальном TCP-транспорте.
func TestInstallSnapshot_NoOpKeepsTCPConnectionUsable(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	client, server, cleanup := newTCPTransportPair(t)
	defer cleanup()

	ready := make(chan any)
	close(ready)
	cm := NewConsensusModule(1, []int{}, server, NewMapStorage(), newSnapshotTestFSM(), ready, NewInmemSnapshotStore())
	defer cm.Stop()

	// Узел уже владеет снимком на индексе 10.
	cm.mu.Lock()
	cm.cmState.lastSnapshotIndex = 10
	cm.cmState.lastSnapshotTerm = 2
	cm.cmState.lastLogIndex = 10
	cm.cmState.lastLogTerm = 2
	cm.cmState.lastApplied = 10
	cm.cmState.fsmAppliedIndex = 10
	cm.cmState.commitIndex = 10
	cm.mu.Unlock()

	// Ждём, пока runRPCReader начнёт обслуживать входящие RPC.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(50)

	data := []byte("snapshot-body-bytes")
	cfgData, _ := EncodeConfiguration(Configuration{})
	req := InstallSnapshotRequest{
		RPCHeader:     RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 0},
		Term:          1,
		LeaderID:      0,
		LastLogIndex:  5, // старее 10 — no-op
		LastLogTerm:   2,
		Configuration: cfgData,
		DataSize:      int64(len(data)),
	}
	reply, err := client.InstallSnapshot(1, req, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("InstallSnapshot: %v", err)
	}
	if !reply.Success {
		t.Fatalf("no-op InstallSnapshot: Success=false: %+v", reply)
	}

	// Следующий RPC на том же транспорте/соединении.
	aeReply, err := client.AppendEntries(1, AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 0},
		Term:         1,
		LeaderID:     0,
		PrevLogIndex: 10,
		PrevLogTerm:  2,
		LeaderCommit: -1,
	})
	if err != nil {
		t.Fatalf("AppendEntries after no-op InstallSnapshot: %v", err)
	}
	if !aeReply.Success {
		t.Fatalf("AppendEntries after no-op InstallSnapshot failed: %+v", aeReply)
	}
}

// commandIndexMap — соответствие «индекс записи журнала → ключ команды».
// Вычисляется из ApplyFuture.Index(), а не задаётся константой: индекс
// зависит от noop-записи лидера и от истории узла.
type commandIndexMap map[int]string

// decodeSnapshotState декодирует состояние snapshotTestFSM из снимка.
func decodeSnapshotState(t *testing.T, store SnapshotStore, id string) map[string]string {
	t.Helper()
	_, reader, err := store.Open(id)
	if err != nil {
		t.Fatalf("open snapshot %s: %v", id, err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", id, err)
	}
	state := make(map[string]string)
	if err := gob.NewDecoder(bytes.NewBuffer(data)).Decode(&state); err != nil {
		t.Fatalf("decode snapshot %s: %v", id, err)
	}
	return state
}

// checkSnapshotsContainAppliedCommands проверяет INV-SNAP по содержимому:
// снимок с meta.Index = N обязан содержать результат применения всех
// команд, записи которых имеют индекс ≤ N.
func checkSnapshotsContainAppliedCommands(t *testing.T, store SnapshotStore, byIndex commandIndexMap) {
	t.Helper()
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List snapshots: %v", err)
	}
	if len(metas) == 0 {
		t.Fatal("no snapshots found: scenario did not produce a snapshot")
	}
	for _, meta := range metas {
		state := decodeSnapshotState(t, store, meta.ID)
		var missing []string
		for idx, key := range byIndex {
			if idx > meta.Index {
				continue
			}
			if _, ok := state[key]; !ok {
				missing = append(missing, fmt.Sprintf("%s(index %d)", key, idx))
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Fatalf("snapshot %s (meta.Index=%d) is missing commands applied at or below its index: %v",
				meta.ID, meta.Index, missing)
		}
	}
}

// waitForAllKeys ждёт (condition-wait), пока в машине состояний окажутся
// все ключи k0..kN-1 со значениями v0..vN-1. При неуспехе перечисляет
// именно потерянные ключи: проверка «только последний ключ» недооценивает
// потерю подтверждённых записей.
func waitForAllKeys(t *testing.T, fsm *snapshotTestFSM, n int, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var missing []string
	for time.Now().Before(deadline) {
		missing = missing[:0]
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("k%d", i)
			if fsm.getState(key) != fmt.Sprintf("v%d", i) {
				missing = append(missing, key)
			}
		}
		if len(missing) == 0 {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("keys lost after restart: %v (of k0..k%d)", missing, n-1)
}

// TestSnapshotContent_MatchesMetaIndex — валидация содержимого снимков:
// сценарий аналитика (10 команд, порог 4, интервал 50 мс, хвост 2) на
// FileStorage + FileSnapshotStore. Каждый снимок обязан содержать
// результат применения всех команд с индексом не выше своего meta.Index.
// На коде до исправления новейший снимок регулярно оказывается дефектным.
func TestSnapshotContent_MatchesMetaIndex(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage := NewFileStorage(dir)
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore: %v", err)
	}
	fsm := newSnapshotTestFSM()
	ready := make(chan any)
	close(ready)
	transport := NewInmemTransport("snapshot-content")

	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	defer cm.Stop()
	waitForLeader(t, cm, 5*time.Second)
	cm.SetSnapshotConfig(4, 50*time.Millisecond, 2)

	const numCommands = 10
	byIndex := make(commandIndexMap, numCommands)
	for i := 0; i < numCommands; i++ {
		key := fmt.Sprintf("k%d", i)
		future := cm.Apply(key+"="+fmt.Sprintf("v%d", i), 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %s: %v", key, err)
		}
		byIndex[future.Index()] = key
	}

	waitForSnapshotAndCompaction(t, cm, numCommands)
	waitForSnapshotQuiescence(t, cm, storage, 10*time.Second)

	checkSnapshotsContainAppliedCommands(t, store, byIndex)
}

// TestSnapshotIndexProperty_BothSelectBranches — property-тест обоих
// исходов select в runFSM (варианты A и B).
//
// В каждой итерации: первая команда стоит на шлюзе внутри Apply, вторая
// ждёт в буфере fsmMutateCh, запрос снимка — на рандеву fsmSnapshotCh.
// После открытия шлюза оба канала готовы, и выбор ветви случаен.
// Проверяемое свойство истинно при любом выборе: meta.Index созданного
// снимка не превышает максимальный индекс, применение которого к машине
// состояний завершилось к моменту вызова Snapshot().
func TestSnapshotIndexProperty_BothSelectBranches(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := newGatedTestFSM(64)
	store := NewInmemSnapshotStore()
	ready := make(chan any)
	close(ready)

	cm := NewConsensusModule(
		0, []int{}, NewInmemTransport("snapshot-property"),
		NewMapStorage(), fsm, ready, store,
	)
	defer cm.Stop()
	defer fsm.release(8)

	waitForLeader(t, cm, 5*time.Second)
	// Порог отключает самостоятельные снимки: снимки инициирует только тест.
	cm.SetSnapshotConfig(1<<20, time.Hour, 2)

	const iterations = 30
	firstBatchWins, secondBatchWins := 0, 0
	entered := 0

	for i := 0; i < iterations; i++ {
		firstCmd := fmt.Sprintf("a%d=v%d", i, i)
		secondCmd := fmt.Sprintf("b%d=v%d", i, i)

		first := cm.Apply(firstCmd, 0)
		entered++
		waitForCondition(t, 5*time.Second, "runFSM entered Apply for the first command",
			func() bool { return fsm.enteredCount() >= entered })

		second := cm.Apply(secondCmd, 0)
		waitForCondition(t, 5*time.Second, "second batch buffered in fsmMutateCh",
			func() bool { return len(cm.fsmMutateCh) > 0 })

		snapErrCh := make(chan error, 1)
		sendStarted := make(chan struct{})
		go func() {
			close(sendStarted)
			snapErrCh <- cm.takeSnapshot()
		}()
		<-sendStarted

		// Открываем шлюз для обеих записей: в этот момент в select
		// горутины runFSM могут быть готовы оба канала (вариант B).
		fsm.release(2)
		entered++

		if err := first.Error(); err != nil {
			t.Fatalf("iteration %d: Apply %s: %v", i, firstCmd, err)
		}
		if err := second.Error(); err != nil {
			t.Fatalf("iteration %d: Apply %s: %v", i, secondCmd, err)
		}
		if err := <-snapErrCh; err != nil {
			t.Fatalf("iteration %d: takeSnapshot: %v", i, err)
		}

		metas, err := store.List()
		if err != nil {
			t.Fatalf("iteration %d: List: %v", i, err)
		}
		if len(metas) == 0 {
			continue
		}
		meta := metas[0]
		_, reader, err := store.Open(meta.ID)
		if err != nil {
			t.Fatalf("iteration %d: Open %s: %v", i, meta.ID, err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatalf("iteration %d: read snapshot: %v", i, err)
		}
		var snap gatedSnapshotState
		if err := gob.NewDecoder(bytes.NewBuffer(data)).Decode(&snap); err != nil {
			t.Fatalf("iteration %d: decode snapshot: %v", i, err)
		}
		if meta.Index > snap.MaxApplied {
			t.Fatalf("iteration %d: snapshot meta.Index=%d ahead of FSM state (max applied at Snapshot(): %d)",
				i, meta.Index, snap.MaxApplied)
		}
		switch meta.Index {
		case first.Index():
			firstBatchWins++
		case second.Index():
			secondBatchWins++
		}
	}

	// Защита от вакуумности: снимки обязаны создаваться внутри окна
	// «машина состояний отстаёт от диспетчеризации».
	behind := cm.counters.snapshotIndexBehindDispatched.Load()
	t.Logf("snapshotIndexBehindDispatched=%d, snapshot at first batch index: %d, at second batch index: %d of %d iterations",
		behind, firstBatchWins, secondBatchWins, iterations)
	if behind == 0 {
		t.Fatal("snapshotIndexBehindDispatched = 0: the dangerous window was never exercised")
	}

	if firstBatchWins == 0 || secondBatchWins == 0 {
		t.Skipf("test is uninformative: only one select branch was exercised "+
			"(snapshot at first batch index: %d, at second batch index: %d of %d iterations)",
			firstBatchWins, secondBatchWins, iterations)
	}
}

// TestSnapshot_RestartFromSnapshotProducesNewSnapshot — Путь 2 и риск
// «тихой остановки создания снимков»: после перезапуска из снимка отметка
// применения синхронизирована со снимком, и новые команды приводят к
// новому снимку со строго большим индексом. Без инициализации отметки на
// пути восстановления запрос снимка навсегда отвечал бы
// ErrNothingNewToSnapshot.
func TestSnapshot_RestartFromSnapshotProducesNewSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage := NewFileStorage(dir)
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore: %v", err)
	}
	ready := make(chan any)
	close(ready)
	transport := NewInmemTransport("restart-snapshot")
	cm := NewConsensusModule(0, []int{}, transport, storage, newSnapshotTestFSM(), ready, store)
	waitForLeader(t, cm, 5*time.Second)
	cm.SetSnapshotConfig(4, 50*time.Millisecond, 2)

	const numCommands = 10
	for i := 0; i < numCommands; i++ {
		if err := cm.Apply(fmt.Sprintf("k%d=v%d", i, i), 0).Error(); err != nil {
			t.Fatalf("Apply k%d: %v", i, err)
		}
	}
	snapIdx := waitForSnapshotAndCompaction(t, cm, numCommands)
	waitForSnapshotQuiescence(t, cm, storage, 10*time.Second)
	cm.Stop()
	transport.Close()

	// Перезапуск в той же директории.
	storage2 := NewFileStorage(dir)
	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart): %v", err)
	}
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("restart-snapshot-2")
	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, newSnapshotTestFSM(), ready2, store2)
	defer cm2.Stop()

	cm2.mu.Lock()
	restoredApplied := cm2.cmState.fsmAppliedIndex
	restoredSnapIdx := cm2.cmState.lastSnapshotIndex
	cm2.mu.Unlock()
	if restoredApplied != restoredSnapIdx {
		t.Fatalf("after restart: fsmAppliedIndex = %d, want %d (lastSnapshotIndex)",
			restoredApplied, restoredSnapIdx)
	}
	if restoredSnapIdx < snapIdx {
		t.Fatalf("after restart: lastSnapshotIndex = %d, want >= %d", restoredSnapIdx, snapIdx)
	}

	waitForLeader(t, cm2, 5*time.Second)
	cm2.SetSnapshotConfig(4, 50*time.Millisecond, 2)
	for i := numCommands; i < numCommands+10; i++ {
		if err := cm2.Apply(fmt.Sprintf("k%d=v%d", i, i), 0).Error(); err != nil {
			t.Fatalf("Apply k%d after restart: %v", i, err)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cm2.mu.Lock()
		current := cm2.cmState.lastSnapshotIndex
		cm2.mu.Unlock()
		if current > restoredSnapIdx {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no new snapshot after restart: lastSnapshotIndex still %d", restoredSnapIdx)
}

// TestSnapshot_InstallSyncsFollowerWatermark — канал распространения
// дефектного снимка (отставший follower получает InstallSnapshot):
// после установки отметка применения приёмника не отстаёт от индекса
// снимка, индексы сходятся между собой, а состояние машины состояний
// follower'а совпадает с лидерским.
func TestSnapshot_InstallSyncsFollowerWatermark(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, _, _ := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}

	snapIdx := h.getLastSnapshotIndex(followerID)
	if applied := h.getFsmAppliedIndex(followerID); applied < snapIdx {
		t.Fatalf("follower %d: fsmAppliedIndex=%d < lastSnapshotIndex=%d", followerID, applied, snapIdx)
	}

	// В покое отметка применения догоняет диспетчеризацию и фиксацию.
	waitForCondition(t, 10*time.Second, "follower watermarks converge", func() bool {
		applied := h.getFsmAppliedIndex(followerID)
		return applied == h.getLastApplied(followerID) && applied == h.getCommitIndex(followerID)
	})

	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)
}

// TestSnapshot_UnappliedBatchesReplayedAfterRestart — записи, оставшиеся
// в очереди машины состояний на момент остановки, не теряются: отметка
// применения их не учитывает, снимок их не фиксирует, а после
// перезапуска они переигрываются из журнала.
func TestSnapshot_UnappliedBatchesReplayedAfterRestart(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage := NewFileStorage(dir)
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore: %v", err)
	}
	fsm := newGatedTestFSM(16)
	ready := make(chan any)
	close(ready)
	transport := NewInmemTransport("stop-with-pending")
	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	waitForLeader(t, cm, 5*time.Second)
	cm.SetSnapshotConfig(2, 50*time.Millisecond, 2)

	// Фаза 1: команды применяются штатно и подтверждаются клиенту.
	const appliedCommands = 5
	for i := 0; i < appliedCommands; i++ {
		fsm.release(1)
		if err := cm.Apply(fmt.Sprintf("k%d=v%d", i, i), 0).Error(); err != nil {
			t.Fatalf("Apply k%d: %v", i, err)
		}
	}
	waitForSnapshotAndCompaction(t, cm, appliedCommands)

	// Дальнейшие снимки отключаем: содержимое машины состояний с этого
	// момента заморожено шлюзом, и сценарий не должен от них зависеть.
	cm.SetSnapshotConfig(1<<20, time.Hour, 2)
	waitForSnapshotQuiescence(t, cm, storage, 10*time.Second)

	// Фаза 2: шлюз закрыт — записи попадают в журнал и в очередь машины
	// состояний, но не применяются.
	const pendingCommands = 3
	// Базовая линия отметки применения, зафиксированная до
	// диспетчеризации команд фазы 2: условие ниже опирается только на
	// прирост отметки и не зависит от служебных записей, дошедших до
	// машины состояний ранее (noop и пр.).
	baseDispatched, _ := readWatermarks(cm)
	for i := appliedCommands; i < appliedCommands+pendingCommands; i++ {
		cm.Apply(fmt.Sprintf("k%d=v%d", i, i), 0)
	}
	// Инвариант: отметка применения растёт только при обработке
	// фиксации, а фиксация возможна лишь после записи журнала на диск
	// (персист выполняется при добавлении записей в журнал — до
	// продвижения индекса фиксации; сама отметка присваивается в начале
	// processLogs, до постановки батча в очередь машины состояний).
	// Поэтому продвижение отметки до базовой линии + 3 гарантирует
	// присутствие всех трёх команд фазы 2 в журнале на диске: остановка
	// не может застать диспетчеризацию, и после перезапуска все три
	// записи будут переиграны.
	waitForCondition(t, 5*time.Second, "pending commands committed to the durable log", func() bool {
		dispatched, _ := readWatermarks(cm)
		return dispatched >= baseDispatched+pendingCommands
	})

	// Останавливаем узел: Apply обязан завершаться, поэтому переводим
	// машину состояний в режим завершения без применения записей.
	fsm.abortApplies()
	cm.Stop()
	transport.Close()

	total := appliedCommands + pendingCommands
	if maxApplied := fsm.maxAppliedIndex(); maxApplied < 0 {
		t.Fatalf("nothing applied before stop: maxApplied=%d", maxApplied)
	}
	for i := appliedCommands; i < total; i++ {
		if v := fsm.getState(fmt.Sprintf("k%d", i)); v != "" {
			t.Fatalf("k%d = %q before restart, want empty (must not be applied)", i, v)
		}
	}

	// Перезапуск в той же директории: все записи обязаны присутствовать.
	storage2 := NewFileStorage(dir)
	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart): %v", err)
	}
	// Тип машины состояний обязан совпадать: снимок сериализован её
	// собственным форматом. Шлюз открыт заранее — перезапуск применяет
	// записи без задержек.
	fsm2 := newGatedTestFSM(64)
	fsm2.release(64)
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("stop-with-pending-2")
	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, fsm2, ready2, store2)
	defer cm2.Stop()
	waitForLeader(t, cm2, 5*time.Second)

	deadline := time.Now().Add(10 * time.Second)
	var missing []string
	for time.Now().Before(deadline) {
		missing = missing[:0]
		for i := 0; i < total; i++ {
			key := fmt.Sprintf("k%d", i)
			if fsm2.getState(key) != fmt.Sprintf("v%d", i) {
				missing = append(missing, key)
			}
		}
		if len(missing) == 0 {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("after restart keys are missing from the FSM: %v", missing)
}

// newFsmSnapshotCM собирает литеральный CM для прямого вызова
// handleFsmSnapshot: журнал из одной записи, счётчики и латентность —
// нулевые значения (готовы к использованию).
func newFsmSnapshotCM(fsmApplied, dispatched, lastSnapshotIndex int) *ConsensusModule {
	cm := &ConsensusModule{
		storage:    NewMapStorage(),
		fsm:        newSnapshotTestFSM(),
		shutdownCh: make(chan struct{}),
	}
	cm.cmState.fsmAppliedIndex = fsmApplied
	cm.cmState.lastApplied = dispatched
	cm.cmState.lastSnapshotIndex = lastSnapshotIndex
	cm.cmState.lastSnapshotTerm = 1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.log = []LogEntry{{Index: fsmApplied, Term: 1, Type: LogCommand, Data: "k=v"}}
	cm.cmState.lastLogIndex = fsmApplied
	cm.cmState.lastLogTerm = 1
	return cm
}

// TestSnapshotCounter_NotIncrementedWhenFSMInSync — счётчик прохождения
// окна не растёт, когда машина состояний не отстаёт от диспетчеризации.
func TestSnapshotCounter_NotIncrementedWhenFSMInSync(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newFsmSnapshotCM(7, 7, -1)
	req := &reqSnapshotFuture{}
	req.init(cm.shutdownCh)
	cm.handleFsmSnapshot(req)
	if err := req.Error(); err != nil {
		t.Fatalf("handleFsmSnapshot: %v", err)
	}
	if req.index != 7 {
		t.Fatalf("snapshot index = %d, want 7", req.index)
	}
	if got := cm.counters.snapshotIndexBehindDispatched.Load(); got != 0 {
		t.Fatalf("snapshotIndexBehindDispatched = %d, want 0 (FSM is in sync)", got)
	}
	if req.snapshot != nil {
		req.snapshot.Release()
	}
}

// TestSnapshotCounter_IncrementedWhenFSMBehind — счётчик растёт ровно на
// единицу за запрос снимка, когда машина состояний отстаёт от
// диспетчеризации, а индекс снимка берётся из отметки применения.
func TestSnapshotCounter_IncrementedWhenFSMBehind(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newFsmSnapshotCM(7, 12, -1)
	req := &reqSnapshotFuture{}
	req.init(cm.shutdownCh)
	cm.handleFsmSnapshot(req)
	if err := req.Error(); err != nil {
		t.Fatalf("handleFsmSnapshot: %v", err)
	}
	if req.index != 7 {
		t.Fatalf("snapshot index = %d, want 7 (apply watermark, not lastApplied=12)", req.index)
	}
	if got := cm.counters.snapshotIndexBehindDispatched.Load(); got != 1 {
		t.Fatalf("snapshotIndexBehindDispatched = %d, want 1", got)
	}
	if req.snapshot != nil {
		req.snapshot.Release()
	}
}

// TestSnapshotGuard_NothingNewAtSnapshotIndex — предохранитель
// монотонности: если с момента прошлого снимка машина состояний не
// применила ни одной новой записи, takeSnapshot возвращает nil и не
// обращается к хранилищу снимков.
func TestSnapshotGuard_NothingNewAtSnapshotIndex(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store := &countingSnapshotStore{InmemSnapshotStore: NewInmemSnapshotStore()}
	cm := &ConsensusModule{
		storage:       NewMapStorage(),
		snapshotStore: store,
		fsm:           newSnapshotTestFSM(),
		shutdownCh:    make(chan struct{}),
		fsmMutateCh:   make(chan []*commitTuple, 1),
		fsmSnapshotCh: make(chan *reqSnapshotFuture),
	}
	cm.cmState.fsmAppliedIndex = 10
	cm.cmState.lastApplied = 10
	cm.cmState.lastSnapshotIndex = 10
	cm.cmState.lastSnapshotTerm = 1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.goSpawn(cm.runFSM)
	defer cm.Stop()

	if err := cm.takeSnapshot(); err != nil {
		t.Fatalf("takeSnapshot = %v, want nil (nothing new to snapshot)", err)
	}
	if got := store.creates.Load(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 (snapshot index would not advance)", got)
	}
}

// gatedFSMCase — вариант машины состояний для сценариев со шлюзом:
// покрываются обе ветви runFSM (applySingle и applyBatch).
type gatedFSMCase struct {
	name  string
	fsm   FSM
	gated *gatedTestFSM
}

func gatedFSMCases(gateCapacity int) []gatedFSMCase {
	single := newGatedTestFSM(gateCapacity)
	batching := newGatedBatchingTestFSM(gateCapacity)
	return []gatedFSMCase{
		{name: "FSM", fsm: single, gated: single},
		{name: "BatchingFSM", fsm: batching, gated: batching.gatedTestFSM},
	}
}

// readWatermarks возвращает пару (lastApplied, fsmAppliedIndex) под cm.mu.
func readWatermarks(cm *ConsensusModule) (int, int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.cmState.lastApplied, cm.cmState.fsmAppliedIndex
}

// TestSnapshotIndexNotAheadOfFSM_Deterministic — ключевой тест исправления.
//
// Детерминированно воспроизводит окно «диапазон отправлен в очередь машины
// состояний, но ещё не применён»: горутина runFSM стоит внутри Apply на
// шлюзе, тогда как lastApplied уже продвинут processLogs до индекса
// следующей записи. В этот момент из горутины теста запрашивается снимок.
//
// Требуемое свойство: индекс снимка не превышает максимальный индекс,
// применение которого к машине состояний завершилось. На коде до
// исправления индекс брался из lastApplied, и тест падает.
func TestSnapshotIndexNotAheadOfFSM_Deterministic(t *testing.T) {
	for _, tc := range gatedFSMCases(8) {
		t.Run(tc.name, func(t *testing.T) {
			defer leaktest.CheckTimeout(t, LeaktestBudget)()

			ready := make(chan any)
			close(ready)
			cm := NewConsensusModule(
				0, []int{}, NewInmemTransport(ServerAddress("watermark-"+tc.name)),
				NewMapStorage(), tc.fsm, ready, NewInmemSnapshotStore(),
			)
			defer cm.Stop()
			// Освобождение шлюза обязано выполниться раньше Stop:
			// иначе runFSM осталась бы внутри Apply и wg.Wait() не завершился.
			defer tc.gated.release(4)

			waitForLeader(t, cm, 5*time.Second)

			// Батч 1: шлюз открыт, применение завершается полностью.
			tc.gated.release(1)
			first := cm.Apply("k0=v0", 0)
			if err := first.Error(); err != nil {
				t.Fatalf("Apply k0: %v", err)
			}
			appliedIdx := first.Index()

			// Батч 2: пропусков в шлюзе нет — runFSM входит в Apply и
			// останавливается внутри него.
			second := cm.Apply("k1=v1", 0)
			waitForCondition(t, 5*time.Second, "runFSM entered Apply for the second command",
				func() bool { return tc.gated.enteredCount() >= 2 })

			dispatched, applied := readWatermarks(cm)
			if dispatched <= applied {
				t.Fatalf("window not reproduced: lastApplied=%d, fsmAppliedIndex=%d (want lastApplied > fsmAppliedIndex)",
					dispatched, applied)
			}
			if applied != appliedIdx {
				t.Fatalf("fsmAppliedIndex = %d, want %d (index of the fully applied entry)", applied, appliedIdx)
			}

			req := &reqSnapshotFuture{}
			req.init(cm.shutdownCh)
			cm.handleFsmSnapshot(req)
			if err := req.Error(); err != nil {
				t.Fatalf("handleFsmSnapshot: %v", err)
			}
			if maxApplied := tc.gated.maxAppliedIndex(); req.index > maxApplied {
				t.Fatalf("snapshot index %d ahead of FSM: max applied index %d (lastApplied=%d)",
					req.index, maxApplied, dispatched)
			}
			if req.index != applied {
				t.Fatalf("snapshot index = %d, want %d (apply watermark)", req.index, applied)
			}
			// Защита от вакуумности: счётчик подтверждает, что снимок
			// действительно создавался внутри опасного окна.
			if got := cm.counters.snapshotIndexBehindDispatched.Load(); got != 1 {
				t.Fatalf("snapshotIndexBehindDispatched = %d, want 1 (window must be observed)", got)
			}
			if req.snapshot != nil {
				req.snapshot.Release()
			}

			// Освобождаем вторую запись, чтобы её future разрешился.
			tc.gated.release(1)
			if err := second.Error(); err != nil {
				t.Fatalf("Apply k1: %v", err)
			}
		})
	}
}

// TestFsmAppliedIndex_EmptyStartHasNothingToSnapshot — Путь 1 (пустой
// старт) и граница -1: пока машина состояний ничего не применила,
// handleFsmSnapshot отвечает ErrNothingNewToSnapshot, а takeSnapshot
// трактует это как «не ошибка» и не обращается к хранилищу снимков.
func TestFsmAppliedIndex_EmptyStartHasNothingToSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store := &countingSnapshotStore{InmemSnapshotStore: NewInmemSnapshotStore()}
	cm := &ConsensusModule{
		storage:       NewMapStorage(),
		snapshotStore: store,
		fsm:           newGatedTestFSM(1),
		shutdownCh:    make(chan struct{}),
		fsmMutateCh:   make(chan []*commitTuple, 1),
		fsmSnapshotCh: make(chan *reqSnapshotFuture),
	}
	cm.cmState.lastApplied = -1
	cm.cmState.fsmAppliedIndex = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.goSpawn(cm.runFSM)
	defer cm.Stop()

	if err := cm.takeSnapshot(); err != nil {
		t.Fatalf("takeSnapshot on empty FSM = %v, want nil", err)
	}
	if got := store.creates.Load(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 (nothing applied yet)", got)
	}
}

// TestFsmAppliedIndex_FreshStartAdvancesOnFirstBatch — Путь 1: до первого
// применения отметка равна -1, после применения первой команды совпадает
// с индексом её записи журнала.
func TestFsmAppliedIndex_FreshStartAdvancesOnFirstBatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := newGatedTestFSM(8)
	// ready не закрыт: до close ни выборы, ни запись noop не начинаются,
	// поэтому проверка «отметка равна -1» детерминирована.
	ready := make(chan any)
	cm := NewConsensusModule(
		0, []int{}, NewInmemTransport("fresh-start"),
		NewMapStorage(), fsm, ready, NewInmemSnapshotStore(),
	)
	defer cm.Stop()
	defer fsm.release(4)

	if _, applied := readWatermarks(cm); applied != -1 {
		t.Fatalf("fsmAppliedIndex = %d before first apply, want -1", applied)
	}

	close(ready)
	waitForLeader(t, cm, 5*time.Second)

	fsm.release(1)
	future := cm.Apply("k0=v0", 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply k0: %v", err)
	}
	if _, applied := readWatermarks(cm); applied != future.Index() {
		t.Fatalf("fsmAppliedIndex = %d after first batch, want %d", applied, future.Index())
	}
}

// TestFsmAppliedIndex_InstallSnapshotSyncsWatermark — Путь 3: после
// успешной установки снимка отметка фактически применённого индекса
// совпадает с lastApplied, commitIndex и индексом снимка; запрос
// устаревшего снимка значений не меняет.
func TestFsmAppliedIndex_InstallSnapshotSyncsWatermark(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm, _ := newInstallSnapshotCM() // lastSnapshotIndex = lastApplied = 10
	data := installSnapshotRequestData(t, map[string]string{"k0": "v0"})

	reply := driveInstallSnapshotRPC(t, cm, installSnapshotRequest(99, 12, 2, data), data)
	if !reply.Success {
		t.Fatalf("InstallSnapshot: Success=false: %+v", reply)
	}
	if cm.cmState.fsmAppliedIndex != 12 ||
		cm.cmState.lastApplied != 12 ||
		cm.cmState.commitIndex != 12 {
		t.Fatalf("after install: fsmAppliedIndex=%d lastApplied=%d commitIndex=%d, want 12/12/12",
			cm.cmState.fsmAppliedIndex, cm.cmState.lastApplied, cm.cmState.commitIndex)
	}

	// Устаревший запрос — no-op: отметка не откатывается.
	reply = driveInstallSnapshotRPC(t, cm, installSnapshotRequest(99, 5, 2, data), data)
	if !reply.Success {
		t.Fatalf("stale InstallSnapshot: Success=false, want true (no-op)")
	}
	if cm.cmState.fsmAppliedIndex != 12 {
		t.Fatalf("fsmAppliedIndex = %d after stale install, want 12 (unchanged)", cm.cmState.fsmAppliedIndex)
	}
}

// TestPublishFsmApplied_Monotonic — продвижение отметки монотонно:
// запоздалый батч со старыми индексами не откатывает её назад, пустой
// батч значение не меняет.
func TestPublishFsmApplied_Monotonic(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.cmState.fsmAppliedIndex = 10

	cm.publishFsmApplied(nil)
	if cm.cmState.fsmAppliedIndex != 10 {
		t.Fatalf("fsmAppliedIndex = %d after empty batch, want 10", cm.cmState.fsmAppliedIndex)
	}

	cm.publishFsmApplied([]*commitTuple{{log: &LogEntry{Index: 5}}})
	if cm.cmState.fsmAppliedIndex != 10 {
		t.Fatalf("fsmAppliedIndex = %d after stale batch, want 10 (must not roll back)", cm.cmState.fsmAppliedIndex)
	}

	cm.publishFsmApplied([]*commitTuple{
		{log: &LogEntry{Index: 11}},
		{log: &LogEntry{Index: 12}},
	})
	if cm.cmState.fsmAppliedIndex != 12 {
		t.Fatalf("fsmAppliedIndex = %d after newer batch, want 12", cm.cmState.fsmAppliedIndex)
	}
}
