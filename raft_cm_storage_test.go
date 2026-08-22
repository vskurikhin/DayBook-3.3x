package raft

import (
	"bytes"
	"encoding/gob"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// writeSnapshotData записывает снимок с gob-кодированным map в
// FileSnapshotStore и возвращает его метаданные.
func writeSnapshotData(t *testing.T, store *FileSnapshotStore, index, term int, state map[string]string) *SnapshotMeta {
	t.Helper()
	sink, err := store.Create(index, term, Configuration{}, index)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := gob.NewEncoder(sink).Encode(state); err != nil {
		t.Fatalf("gob encode state failed: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, meta := range metas {
		if meta.ID == sink.ID() {
			return meta
		}
	}
	t.Fatalf("snapshot %s not listed", sink.ID())
	return nil
}

// waitForSnapshotAndCompaction ждёт, пока узел создаст снимок и
// фактически сжимает журнал.
func waitForSnapshotAndCompaction(t *testing.T, cm *ConsensusModule, originalLogLen int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cm.mu.Lock()
		snapIdx := cm.cmState.lastSnapshotIndex
		logLen := len(cm.cmState.log)
		cm.mu.Unlock()
		if snapIdx > 0 && logLen < originalLogLen {
			return snapIdx
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for snapshot and log compaction")
	return -1
}

// waitForSnapshotQuiescence ждёт, пока цикл снимков узла гарантированно
// завершит все записи: persisted lastSnapshotIndex совпадает с in-memory
// значением и порог нового снимка не достигнут. Необходимо перед
// созданием второго FileStorage на той же директории при in-process
// рестарте — иначе поздний persistToStorage из runSnapshots смог бы
// пересечься с записью нового экземпляра (общий <key>.dat.tmp).
func waitForSnapshotQuiescence(t *testing.T, cm *ConsensusModule, storage Storage, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cm.mu.Lock()
		snapIdx := cm.cmState.lastSnapshotIndex
		needMore := cm.cmState.lastLogIndex-snapIdx >= cm.snapshotThreshold
		cm.mu.Unlock()

		persistedIdx := -1
		if data, found := storage.Get("lastSnapshotIndex"); found {
			_ = gob.NewDecoder(bytes.NewBuffer(data)).Decode(&persistedIdx)
		}
		if !needMore && persistedIdx == snapIdx {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for snapshot quiescence")
}

// TestRestoreFromSnapshotStore_RestoresFSMAndIndex — рестарт узла с
// FileStorage + FileSnapshotStore: FSM восстанавливается из снимка,
// lastApplied/commitIndex соответствуют индексу снимка, суффикс лога
// реплицируется после выборов.
func TestRestoreFromSnapshotStore_RestoresFSMAndIndex(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage1 := NewFileStorage(dir)
	store1, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	fsm1 := newSnapshotTestFSM()
	ready1 := make(chan any)
	close(ready1)
	transport1 := NewInmemTransport("single")

	cm1 := NewConsensusModule(0, []int{}, transport1, storage1, fsm1, ready1, store1)
	waitForLeader(t, cm1, 5*time.Second)

	cm1.SetSnapshotConfig(4, 50*time.Millisecond, 2)

	const numCommands = 10
	for i := 0; i < numCommands; i++ {
		cmd := "k" + itoa(i) + "=v" + itoa(i)
		future := cm1.Apply(cmd, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %q failed: %v", cmd, err)
		}
	}

	waitForSnapshotAndCompaction(t, cm1, numCommands)
	waitForSnapshotQuiescence(t, cm1, storage1, 10*time.Second)

	// После полного успокоения цикла снимков фиксируем финальный индекс:
	// при рестарте restore берёт НОВЕЙШИЙ снимок из стора.
	cm1.mu.Lock()
	snapIdx := cm1.cmState.lastSnapshotIndex
	cm1.mu.Unlock()

	cm1.Stop()
	transport1.Close()

	// Пересоздаём узел в той же директории (полный рестарт процесса).
	storage2 := NewFileStorage(dir)
	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart) failed: %v", err)
	}
	fsm2 := newSnapshotTestFSM()
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("single-2")

	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, fsm2, ready2, store2)
	defer cm2.Stop()

	// Проверяем сразу после конструктора, до выборов.
	cm2.mu.Lock()
	lastApplied := cm2.cmState.lastApplied
	fsmAppliedIndex := cm2.cmState.fsmAppliedIndex
	commitIndex := cm2.cmState.commitIndex
	lastSnapshotIndex := cm2.cmState.lastSnapshotIndex
	cm2.mu.Unlock()

	if lastSnapshotIndex != snapIdx {
		t.Fatalf("lastSnapshotIndex = %d, want %d", lastSnapshotIndex, snapIdx)
	}
	if lastApplied != snapIdx {
		t.Fatalf("lastApplied = %d, want %d", lastApplied, snapIdx)
	}
	if fsmAppliedIndex != snapIdx {
		t.Fatalf("fsmAppliedIndex = %d, want %d", fsmAppliedIndex, snapIdx)
	}
	if commitIndex != snapIdx {
		t.Fatalf("commitIndex = %d, want %d", commitIndex, snapIdx)
	}
	if v := fsm2.getState("k0"); v != "v0" {
		t.Fatalf("k0 = %q after restart, want v0 (key below compaction line)", v)
	}

	// После выборов суффикс лога доигрывается существующим механизмом
	// (processLogs) при первом продвижении commitIndex. В одноузловом
	// кластере продвижение происходит при фиксации новой команды —
	// применяем k10 и проверяем, что replay применил суффикс.
	waitForLeader(t, cm2, 5*time.Second)
	future := cm2.Apply("k10=v10", 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply k10 after restart failed: %v", err)
	}
	// Проверяются ВСЕ ключи, а не только последний: дефектный снимок
	// теряет произвольную подтверждённую запись, и проверка одного ключа
	// пропускает большую часть потерь.
	waitForAllKeys(t, fsm2, numCommands+1, 10*time.Second)
}

// TestRestoreFromSnapshotStore_EmptyStoreNoSnapshots — старт без снимков
// не падает (свежий узел).
func TestRestoreFromSnapshotStore_EmptyStoreNoSnapshots(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{
		fsm:           newSnapshotTestFSM(),
		snapshotStore: NewInmemSnapshotStore(),
	}
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.lastLogIndex = -1
	cm.cmState.lastLogTerm = -1
	cm.cmState.lastApplied = -1
	cm.cmState.commitIndex = -1

	if err := cm.restoreFromSnapshotStore(); err != nil {
		t.Fatalf("restoreFromSnapshotStore on fresh node: %v", err)
	}
	if cm.cmState.lastApplied != -1 {
		t.Fatalf("lastApplied = %d, want -1", cm.cmState.lastApplied)
	}
}

// TestRestoreFromSnapshotStore_FailFastGap проверяет детектирование
// нарушений инвариантов: пустой store при усечённом логе, снимок старее
// lastSnapshotIndex, дыра между снимком и логом, безусловный инвариант
// при пустом журнале.
func TestRestoreFromSnapshotStore_FailFastGap(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// 1. Пустой store + lastSnapshotIndex >= 0 → ошибка.
	cm := &ConsensusModule{
		fsm:           newSnapshotTestFSM(),
		snapshotStore: NewInmemSnapshotStore(),
	}
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastSnapshotTerm = 1
	cm.cmState.lastLogIndex = 9
	cm.cmState.lastLogTerm = 1
	if err := cm.restoreFromSnapshotStore(); err == nil {
		t.Fatal("empty store with compacted log: want error, got nil")
	}

	// 2. Снимок в сторе старее lastSnapshotIndex из storage → ошибка.
	store := NewInmemSnapshotStore()
	sink, err := store.Create(3, 1, Configuration{}, 3)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	_ = sink.Close()
	cm = &ConsensusModule{
		fsm:           newSnapshotTestFSM(),
		snapshotStore: store,
	}
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastSnapshotTerm = 1
	cm.cmState.lastLogIndex = 9
	cm.cmState.lastLogTerm = 1
	if err := cm.restoreFromSnapshotStore(); err == nil {
		t.Fatal("snapshot older than lastSnapshotIndex: want error, got nil")
	}

	// 3. Дыра между снимком и логом → ошибка.
	cm = &ConsensusModule{}
	cm.cmState.log = []LogEntry{{Index: 100, Term: 1}}
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastLogIndex = 100
	cm.cmState.lastLogTerm = 1
	if err := cm.checkSnapshotLogContinuity(); err == nil {
		t.Fatal("gap between snapshot and log: want error, got nil")
	}

	// 4. Безусловный инвариант при пустом журнале (ревью HIGH-1):
	// len(log) == 0, lastLogIndex = -1, lastSnapshotIndex = 5 → ошибка.
	cm = &ConsensusModule{}
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastLogIndex = -1
	cm.cmState.lastLogTerm = -1
	if err := cm.checkSnapshotLogContinuity(); err == nil {
		t.Fatal("empty log behind snapshot: want error, got nil")
	}
}

// TestRestoreFromSnapshotStore_SelfHealsStalePersistKey — ветка
// самовосстановления (ревью HIGH-2): снимок в постоянном хранилище новее
// lastSnapshotIndex на диске (окно крэша) — конструктор не
// падает, метаданные берутся из стора и исправляются на диске.
func TestRestoreFromSnapshotStore_SelfHealsStalePersistKey(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage1 := NewFileStorage(dir)
	store1, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	fsm1 := newSnapshotTestFSM()
	ready1 := make(chan any)
	close(ready1)
	transport1 := NewInmemTransport("single")

	cm1 := NewConsensusModule(0, []int{}, transport1, storage1, fsm1, ready1, store1)
	waitForLeader(t, cm1, 5*time.Second)

	cm1.SetSnapshotConfig(4, 50*time.Millisecond, 2)

	for i := 0; i < 10; i++ {
		cmd := "k" + itoa(i) + "=v" + itoa(i)
		future := cm1.Apply(cmd, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %q failed: %v", cmd, err)
		}
	}

	waitForSnapshotAndCompaction(t, cm1, 10)
	waitForSnapshotQuiescence(t, cm1, storage1, 10*time.Second)

	// Финальный индекс снимка после успокоения цикла снимков.
	cm1.mu.Lock()
	snapIdx := cm1.cmState.lastSnapshotIndex
	cm1.mu.Unlock()

	cm1.Stop()
	transport1.Close()

	// Имитация крэша между Close снимка и persistToStorage:
	// состариваем ключ lastSnapshotIndex на диске.
	storage2 := NewFileStorage(dir)
	staleIdx := -1
	if snapIdx > 1 {
		staleIdx = snapIdx - 1
	}
	storage2.Set("lastSnapshotIndex", gobEncode(t, staleIdx))

	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart) failed: %v", err)
	}
	fsm2 := newSnapshotTestFSM()
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("single-2")

	// Конструктор не должен завершить процесс.
	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, fsm2, ready2, store2)
	defer cm2.Stop()

	cm2.mu.Lock()
	restoredSnapIdx := cm2.cmState.lastSnapshotIndex
	restoredLastApplied := cm2.cmState.lastApplied
	cm2.mu.Unlock()

	if restoredSnapIdx != snapIdx {
		t.Fatalf("lastSnapshotIndex = %d, want %d (from store, not stale storage key)", restoredSnapIdx, snapIdx)
	}
	if restoredLastApplied != snapIdx {
		t.Fatalf("lastApplied = %d, want %d", restoredLastApplied, snapIdx)
	}
	if v := fsm2.getState("k0"); v != "v0" {
		t.Fatalf("k0 = %q, want v0 (FSM restored from snapshot)", v)
	}

	// Self-heal перезаписал ключ на диске.
	persisted, found := storage2.Get("lastSnapshotIndex")
	if !found {
		t.Fatal("lastSnapshotIndex missing on disk after self-heal")
	}
	var persistedIdx int
	if err := gob.NewDecoder(bytes.NewBuffer(persisted)).Decode(&persistedIdx); err != nil {
		t.Fatalf("cannot decode persisted lastSnapshotIndex: %v", err)
	}
	if persistedIdx != snapIdx {
		t.Fatalf("persisted lastSnapshotIndex = %d, want %d", persistedIdx, snapIdx)
	}
}

// TestRestoreFromSnapshotStore_EmptyLogWithSnapshot — пустой журнал +
// валидный снимок (ревью HIGH-1): lastLogIndex/lastLogTerm синхронизируются
// с meta, инвариант lastApplied <= lastLogIndex держится.
func TestRestoreFromSnapshotStore_EmptyLogWithSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()

	// Вручную заполняем storage: метаданные + пустой журнал.
	storage := NewFileStorage(dir)
	storage.Set("currentTerm", gobEncode(t, 1))
	storage.Set("votedFor", gobEncode(t, 0))
	storage.Set("log", gobEncode(t, []LogEntry{}))
	storage.Set("lastSnapshotIndex", gobEncode(t, 5))
	storage.Set("lastSnapshotTerm", gobEncode(t, 1))

	// Снимок с индексом 5 в постоянном хранилище.
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	writeSnapshotData(t, store, 5, 1, map[string]string{"k0": "v0"})

	fsm := newSnapshotTestFSM()
	ready := make(chan any)
	close(ready)
	transport := NewInmemTransport("single")

	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	defer cm.Stop()

	cm.mu.Lock()
	lastLogIndex := cm.cmState.lastLogIndex
	lastLogTerm := cm.cmState.lastLogTerm
	lastApplied := cm.cmState.lastApplied
	commitIndex := cm.cmState.commitIndex
	cm.mu.Unlock()

	if lastLogIndex != 5 || lastLogTerm != 1 {
		t.Fatalf("lastLog = (%d, %d), want (5, 1) — empty log must be synced to snapshot", lastLogIndex, lastLogTerm)
	}
	if lastApplied != 5 || commitIndex != 5 {
		t.Fatalf("lastApplied/commitIndex = (%d, %d), want (5, 5)", lastApplied, commitIndex)
	}
	if v := fsm.getState("k0"); v != "v0" {
		t.Fatalf("k0 = %q, want v0", v)
	}
}

// recordingStorage — обёртка над Storage, фиксирующая последовательность Set.
type recordingStorage struct {
	Storage
	mu   sync.Mutex
	keys []string
}

func (r *recordingStorage) Set(key string, value []byte) {
	r.mu.Lock()
	r.keys = append(r.keys, key)
	r.mu.Unlock()
	r.Storage.Set(key, value)
}

func (r *recordingStorage) keysSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.keys...)
}

// persistKeys — ключи, которые вправе записывать persistToStorage.
var persistKeys = []string{"currentTerm", "votedFor", "lastSnapshotIndex", "lastSnapshotTerm", "log"}

// assertPersistKeyOrder проверяет инвариант крэш-безопасности по наблюдённой
// последовательности записанных ключей: снимок-ключи предшествуют журналу,
// журнал записан последним, каждый ключ известен и записан не более раза.
//
// Число записанных ключей инвариантом не является: слой хранения вправе
// пропустить запись значения, которое уже лежит на диске, поэтому любой ключ
// может отсутствовать в последовательности — его значение уже долговечно.
func assertPersistKeyOrder(t *testing.T, keys []string) {
	t.Helper()

	known := make(map[string]bool, len(persistKeys))
	for _, key := range persistKeys {
		known[key] = true
	}

	position := make(map[string]int, len(keys))
	for i, key := range keys {
		if !known[key] {
			t.Fatalf("persist wrote unknown key %q (keys = %v)", key, keys)
		}
		if _, repeated := position[key]; repeated {
			t.Fatalf("persist wrote key %q more than once (keys = %v)", key, keys)
		}
		position[key] = i
	}

	logPos, logWritten := position["log"]
	if !logWritten {
		return
	}
	if logPos != len(keys)-1 {
		t.Fatalf("persist key order = %v: log must be written last", keys)
	}
	for _, key := range []string{"lastSnapshotIndex", "lastSnapshotTerm"} {
		if pos, written := position[key]; written && pos > logPos {
			t.Fatalf("persist key order = %v: %s must be written before log", keys, key)
		}
	}
}

// TestPersistToStorage_LogWrittenLast проверяет относительный порядок записи:
// снимок-ключи записываются ДО усечённого журнала, чтобы крэш между записями
// не оставлял «журнал усечён, а lastSnapshotIndex старый».
func TestPersistToStorage_LogWrittenLast(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	rec := &recordingStorage{Storage: NewMapStorage()}
	cm := &ConsensusModule{storage: rec}
	cm.cmState.log = []LogEntry{{Index: 5, Term: 1}}
	cm.cmState.logNeedsPersist = true

	cm.persistToStorage()

	keys := rec.keysSnapshot()
	if len(keys) == 0 {
		t.Fatal("persist wrote no keys")
	}
	assertPersistKeyOrder(t, keys)
}

// skippingStorage — хранилище, пропускающее запись части ключей: имитирует
// слой, который не переписывает значения, уже лежащие на диске.
type skippingStorage struct {
	Storage
	allowed map[string]bool
}

func (s *skippingStorage) Set(key string, value []byte) {
	if !s.allowed[key] {
		return
	}
	s.Storage.Set(key, value)
}

// TestPersistToStorage_KeyOrderWithSkippedWrites проверяет, что пропуск записи
// неизменившихся ключей инвариант порядка не нарушает: наблюдаются только
// currentTerm и журнал.
func TestPersistToStorage_KeyOrderWithSkippedWrites(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	rec := &recordingStorage{Storage: NewMapStorage()}
	skipping := &skippingStorage{
		Storage: rec,
		allowed: map[string]bool{"currentTerm": true, "log": true},
	}
	cm := &ConsensusModule{storage: skipping}
	cm.cmState.log = []LogEntry{{Index: 5, Term: 1}}
	cm.cmState.logNeedsPersist = true

	cm.persistToStorage()

	keys := rec.keysSnapshot()
	if len(keys) != 2 || keys[0] != "currentTerm" || keys[1] != "log" {
		t.Fatalf("observed keys = %v, want [currentTerm log]", keys)
	}
	assertPersistKeyOrder(t, keys)
}

// TestPersistToStorage_NoWritesWhenUnchanged проверяет, что сохранение
// неизменившегося состояния не выполняет записей на диск, а изменение журнала
// выполняет ровно одну запись.
func TestPersistToStorage_NoWritesWhenUnchanged(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := NewFileStorage(t.TempDir())
	cm := &ConsensusModule{storage: storage}
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.log = []LogEntry{{Index: 0, Term: 1}}
	cm.cmState.logNeedsPersist = true

	cm.persistToStorage()
	if got := storage.writeCount(); got != 5 {
		t.Fatalf("writeCount = %d after first persist, want 5", got)
	}

	before := storage.writeCount()
	cm.persistToStorage()
	if got := storage.writeCount() - before; got != 0 {
		t.Fatalf("%d writes for unchanged state, want 0", got)
	}

	cm.cmState.log = append(cm.cmState.log, LogEntry{Index: 1, Term: 1})
	cm.cmState.logNeedsPersist = true
	before = storage.writeCount()
	cm.persistToStorage()
	if got := storage.writeCount() - before; got != 1 {
		t.Fatalf("%d writes for changed log, want 1", got)
	}
}

// TestCheckSnapshotKeysConsistency проверяет fail-fast при отсутствии
// снимок-ключей для сжатого журнала.
func TestCheckSnapshotKeysConsistency(t *testing.T) {
	// Сжатый журнал без снимок-ключей → ошибка.
	storage := NewMapStorage()
	storage.Set("currentTerm", gobEncode(t, 1))
	storage.Set("votedFor", gobEncode(t, 0))
	storage.Set("log", gobEncode(t, []LogEntry{{Index: 5, Term: 1}}))
	cm := &ConsensusModule{storage: storage}
	cm.cmState.log = []LogEntry{{Index: 5, Term: 1}}
	if err := cm.checkSnapshotKeysConsistency(); err == nil {
		t.Fatal("compacted log without snapshot keys: want error, got nil")
	}

	// Полный лог (Index от 0) без снимок-ключей → старт разрешён.
	cm = &ConsensusModule{storage: storage}
	cm.cmState.log = []LogEntry{{Index: 0, Term: 1}}
	if err := cm.checkSnapshotKeysConsistency(); err != nil {
		t.Fatalf("full log without snapshot keys: %v, want nil", err)
	}

	// Пустой лог → старт разрешён.
	cm = &ConsensusModule{storage: storage}
	if err := cm.checkSnapshotKeysConsistency(); err != nil {
		t.Fatalf("empty log: %v, want nil", err)
	}
}

// TestRestoreFromSnapshotStore_RestoresConfiguration проверяет,
// что конфигурация кластера восстанавливается из метаданных снимка,
// если она новее конфигурации из сжатого суффикса лога.
func TestRestoreFromSnapshotStore_RestoresConfiguration(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	snapConfig := Configuration{ConfigServers: []ConfigServer{
		{ID: 0, Address: "0", Suffrage: Voter},
		{ID: 1, Address: "1", Suffrage: Voter},
		{ID: 2, Address: "2", Suffrage: Voter},
	}}
	logConfig := Configuration{ConfigServers: []ConfigServer{
		{ID: 0, Address: "0", Suffrage: Voter},
		{ID: 1, Address: "1", Suffrage: Voter},
	}}

	newRestoredCM := func(t *testing.T, configIndex int) *ConsensusModule {
		t.Helper()
		store := NewInmemSnapshotStore()
		sink, err := store.Create(10, 1, snapConfig, configIndex)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if err := gob.NewEncoder(sink).Encode(map[string]string{"k0": "v0"}); err != nil {
			t.Fatalf("gob encode failed: %v", err)
		}
		if err := sink.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		cm := &ConsensusModule{
			fsm:           newSnapshotTestFSM(),
			snapshotStore: store,
			storage:       NewMapStorage(),
		}
		cm.cmState.lastSnapshotIndex = 10
		cm.cmState.lastSnapshotTerm = 1
		cm.cmState.lastLogIndex = 12
		cm.cmState.lastLogTerm = 1
		cm.cmState.log = []LogEntry{
			{Index: 9, Term: 1},
			{Index: 10, Term: 1},
			{Index: 11, Term: 1},
			{Index: 12, Term: 1},
		}
		cm.cmState.configurations.committed = logConfig
		cm.cmState.configurations.committedIndex = 3
		cm.cmState.configurations.latest = logConfig
		cm.cmState.configurations.latestIndex = 3
		return cm
	}

	// Снимок с ConfigIndex = 7 новее конфигурации лога (latestIndex = 3) —
	// после restore используется конфигурация снимка.
	cm := newRestoredCM(t, 7)
	if err := cm.restoreFromSnapshotStore(); err != nil {
		t.Fatalf("restoreFromSnapshotStore failed: %v", err)
	}
	if cm.cmState.configurations.latestIndex != 7 {
		t.Fatalf("latestIndex = %d, want 7 (from snapshot)", cm.cmState.configurations.latestIndex)
	}
	if len(cm.cmState.configurations.latest.ConfigServers) != 3 {
		t.Fatalf("latest config servers = %d, want 3 (from snapshot)", len(cm.cmState.configurations.latest.ConfigServers))
	}

	// Снимок с ConfigIndex = 2 старее конфигурации лога (latestIndex = 3) —
	// конфигурация из лога не перезаписывается.
	cm = newRestoredCM(t, 2)
	if err := cm.restoreFromSnapshotStore(); err != nil {
		t.Fatalf("restoreFromSnapshotStore failed: %v", err)
	}
	if cm.cmState.configurations.latestIndex != 3 {
		t.Fatalf("latestIndex = %d, want 3 (log is newer)", cm.cmState.configurations.latestIndex)
	}
	if len(cm.cmState.configurations.latest.ConfigServers) != 2 {
		t.Fatalf("latest config servers = %d, want 2 (from log)", len(cm.cmState.configurations.latest.ConfigServers))
	}
}
