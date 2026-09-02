package raft

import (
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// -- Тесты rebuildLastLogLocked. ---
//
// Инвариант: при пустом суффиксе журнала lastLogIndex/lastLogTerm
// берутся из lastSnapshotIndex/lastSnapshotTerm. Для свежего узла без
// снимка (lastSnapshotIndex == -1) поведение прежнее: (-1, -1).
// Для непустого среза поведение не меняется: последняя запись среза.

// TestRebuildLastLog_EmptyLogWithoutSnapshot проверяет, что поведение
// свежего узла не изменилось: пустой журнал и lastSnapshotIndex == -1
// дают (-1, -1).
func TestRebuildLastLog_EmptyLogWithoutSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.cmState.log = nil
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1

	cm.rebuildLastLogLocked()

	if cm.cmState.lastLogIndex != -1 || cm.cmState.lastLogTerm != -1 {
		t.Fatalf("lastLog = (%d, %d), want (-1, -1) — fresh node behavior must not change",
			cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}

// TestRebuildLastLog_EmptyLogWithSnapshot — ядро:
// пустой журнал при установленном снимке даёт (N, T) от снимка.
// Именно эта ветка ломала догон в инциденте: rebuildLastLogLocked затирал
// корректные lastLogIndex/lastLogTerm в -1/-1.
func TestRebuildLastLog_EmptyLogWithSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.cmState.log = nil
	cm.cmState.lastSnapshotIndex = 10
	cm.cmState.lastSnapshotTerm = 2

	cm.rebuildLastLogLocked()

	if cm.cmState.lastLogIndex != 10 || cm.cmState.lastLogTerm != 2 {
		t.Fatalf("lastLog = (%d, %d), want (10, 2) — INV-S2 violated",
			cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}

// TestRebuildLastLog_NonEmptyLog проверяет, что при непустом срезе
// поведение не меняется: берётся последняя запись среза, граница
// снимка не участвует.
func TestRebuildLastLog_NonEmptyLog(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.cmState.log = []LogEntry{
		{Index: 5, Term: 1},
		{Index: 7, Term: 3},
	}
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastSnapshotTerm = 1

	cm.rebuildLastLogLocked()

	if cm.cmState.lastLogIndex != 7 || cm.cmState.lastLogTerm != 3 {
		t.Fatalf("lastLog = (%d, %d), want (7, 3)", cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}

// TestRebuildLastLog_AppendEntriesCallSite проверяет неизменность поведения
// точки вызова (call site) в raft_cm_rpc.go при обработке AppendEntries
// со вставкой записей.
//
// Ключевой сценарий: после вставки записей срез гарантированно непустой,
// поэтому определённая ветка логики остаётся недостижимой. В результате
// метод корректно возвращает последнюю вставленную запись.
// Тест фиксирует это поведение и защищает от регрессий, которые могут
// незаметно изменить логику определения «последней записи».
func TestRebuildLastLog_AppendEntriesCallSite(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.storage = store.NewMapStorage()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.commitIndex = -1

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         1,
		LeaderID:     99,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries: []LogEntry{
			{Index: 0, Term: 1},
			{Index: 1, Term: 1},
		},
		LeaderCommit: -1,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if !reply.Success {
		t.Fatalf("AppendEntries reply: %+v, want Success", reply)
	}
	if cm.cmState.lastLogIndex != 1 || cm.cmState.lastLogTerm != 1 {
		t.Fatalf("lastLog = (%d, %d), want (1, 1) — call site behavior must not change",
			cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}

// TestRebuildLastLog_RestoreFromStorageCallSite проверяет, что поведение
// точки вызова (call site) в raft_cm_storage.go (метод restoreFromStorage)
// остаётся неизменным.
//
// Ключевой момент: rebuildLastLogLocked вызывается до того, как из хранилища
// загружается lastSnapshotIndex (поле ещё имеет начальное значение -1,
// как в конструкторе). Поэтому при пустом журнале результат равен (-1, -1).
// Тест фиксирует этот сценарий и защищает от регрессий в порядке инициализации.
func TestRebuildLastLog_RestoreFromStorageCallSite(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := store.NewMapStorage()
	storage.Set("currentTerm", gobEncode(t, 1))
	storage.Set("votedFor", gobEncode(t, -1))
	storage.Set("log", gobEncode(t, []LogEntry{}))

	cm := &ConsensusModule{storage: storage}
	// Конструктор устанавливает lastSnapshotIndex = -1 до restoreFromStorage.
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1

	cm.restoreFromStorage()

	if cm.cmState.lastLogIndex != -1 || cm.cmState.lastLogTerm != -1 {
		t.Fatalf("lastLog = (%d, %d), want (-1, -1) — restore call site behavior must not change",
			cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}

// TestRebuildLastLog_RestoreFromStorageNonEmptyLog проверяет восстановление
// из хранилища при наличии непустого журнала. Тест использует тот же вызов
// (call site), что и в базовом сценарии, но с данными в storage.
// Проверяется, что поведение для последней записи остаётся прежним: логика
// обработки «последнего лога» не зависит от того, был ли журнал пустым
// или уже содержал записи.
func TestRebuildLastLog_RestoreFromStorageNonEmptyLog(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := store.NewMapStorage()
	storage.Set("currentTerm", gobEncode(t, 1))
	storage.Set("votedFor", gobEncode(t, -1))
	storage.Set("log", gobEncode(t, []LogEntry{
		{Index: 0, Term: 1},
		{Index: 1, Term: 1},
	}))

	cm := &ConsensusModule{storage: storage}
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1

	cm.restoreFromStorage()

	if cm.cmState.lastLogIndex != 1 || cm.cmState.lastLogTerm != 1 {
		t.Fatalf("lastLog = (%d, %d), want (1, 1)", cm.cmState.lastLogIndex, cm.cmState.lastLogTerm)
	}
}
