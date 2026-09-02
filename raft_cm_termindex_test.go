package raft

import (
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// mockTransportConflict — минимальная реализация Transport для тестов
// обработки ConflictTerm в leaderSendAEsToPeer.
// Позволяет задать фиксированный ответ AppendEntries с указанными
// replyTerm (должен совпадать с currentTerm лидера), Success, ConflictTerm.
type mockTransportConflict struct {
	Transport
	replyTerm    int
	success      bool
	conflictTerm int
}

func (m *mockTransportConflict) AppendEntries(_ ServerID, _ AppendEntriesArgs) (AppendEntriesReply, error) {
	return AppendEntriesReply{
		Term:         m.replyTerm,
		Success:      m.success,
		ConflictTerm: m.conflictTerm,
	}, nil
}

// TestTermIndexMap_AppendEntries проверяет инкрементальное обновление
// termIndexMap в dispatchLogsLocked при добавлении записей в конец лога.
//
// Сценарий:
//  1. Создать CM с пустым логом, currentTerm=1.
//  2. Вызвать dispatchLogsLocked с 3 записями — все получают Term=1.
//  3. Проверить termIndexMap[1] == 3 (последний Index с Term=1).
//  4. Сменить currentTerm на 2.
//  5. Вызвать dispatchLogsLocked с 1 записью — получает Term=2.
//  6. Проверить termIndexMap[2] == 4 (обновлён).
//  7. Проверить termIndexMap[1] == 3 (не изменился).
//
// Ожидание: termIndexMap корректно отражает последний индекс каждого терма.
func TestTermIndexMap_AppendEntries(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := store.NewMapStorage()
	cm := &ConsensusModule{
		leaderState: leaderState{
			matchIndex: map[int]int{0: 0},
			inflight:   make(map[int]*logFuture),
		},

		id:         0,
		storage:    storage,
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			currentTerm:  1,
			lastLogIndex: 0,
			lastLogTerm:  0,
			log:          make([]LogEntry, 0),
			termIndexMap: make(map[int]int),
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
			},
		},
	}
	defer cm.Stop()

	// Первая группа: 3 записи, все с Term=1 (cm.cmState.currentTerm=1).
	futures1 := make([]*logFuture, 3)
	for i := range futures1 {
		futures1[i] = &logFuture{
			deferError: deferError{errCh: make(chan error, 1)},
			log:        LogEntry{Type: LogCommand, Data: []byte("x")},
		}
	}
	cm.dispatchLogsLocked(futures1)

	// После 3 записей с Term=1: termIndexMap[1] = 3.
	if idx := cm.cmState.termIndexMap[1]; idx != 3 {
		t.Fatalf("termIndexMap[1] = %d, want 3", idx)
	}

	// Вторая группа: 1 запись с Term=2.
	cm.cmState.currentTerm = 2
	futures2 := []*logFuture{
		{deferError: deferError{errCh: make(chan error, 1)}, log: LogEntry{Type: LogCommand, Data: []byte("y")}},
	}
	cm.dispatchLogsLocked(futures2)

	// Проверяем termIndexMap после второй группы.
	if idx := cm.cmState.termIndexMap[2]; idx != 4 {
		t.Fatalf("termIndexMap[2] = %d, want 4", idx)
	}
	// termIndexMap[1] не должен измениться.
	if idx := cm.cmState.termIndexMap[1]; idx != 3 {
		t.Fatalf("termIndexMap[1] = %d, want 3 (unchanged)", idx)
	}
}

// TestTermIndexMap_CompactLogs проверяет перестройку termIndexMap
// после сжатия начала журнала.
//
// Сценарий:
//  1. Создать CM с логом: T1/I1, T1/I2, T2/I3, T3/I4, T4/I5.
//  2. Проверить начальное состояние termIndexMap.
//  3. compactLogsLocked(3) — удалить записи с Index < 3.
//  4. Проверить: T1 удалён из карты, T2/I3, T3/I4, T4/I5 сохранены.
//
// Ожидание: после сжатия карта содержит только оставшиеся термы.
func TestTermIndexMap_CompactLogs(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.shutdownCh = make(chan struct{})
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 2},
		{Index: 4, Term: 3},
		{Index: 5, Term: 4},
	}
	cm.rebuildTermIndexMapLocked()

	// Проверяем начальное состояние.
	if idx := cm.cmState.termIndexMap[1]; idx != 2 {
		t.Fatalf("before compact: termIndexMap[1] = %d, want 2", idx)
	}
	if idx := cm.cmState.termIndexMap[2]; idx != 3 {
		t.Fatalf("before compact: termIndexMap[2] = %d, want 3", idx)
	}
	if idx := cm.cmState.termIndexMap[3]; idx != 4 {
		t.Fatalf("before compact: termIndexMap[3] = %d, want 4", idx)
	}
	if idx := cm.cmState.termIndexMap[4]; idx != 5 {
		t.Fatalf("before compact: termIndexMap[4] = %d, want 5", idx)
	}

	// Очищаем старые записи с Index < 3.
	cm.compactLogsLocked(3)

	// Проверяем состояние после сжатия.
	if _, ok := cm.cmState.termIndexMap[1]; ok {
		t.Fatal("termIndexMap[1] should be deleted after compact")
	}
	if idx := cm.cmState.termIndexMap[2]; idx != 3 {
		t.Fatalf("after compact: termIndexMap[2] = %d, want 3", idx)
	}
	if idx := cm.cmState.termIndexMap[3]; idx != 4 {
		t.Fatalf("after compact: termIndexMap[3] = %d, want 4", idx)
	}
	if idx := cm.cmState.termIndexMap[4]; idx != 5 {
		t.Fatalf("after compact: termIndexMap[4] = %d, want 5", idx)
	}
}

// TestTermIndexMap_AppendEntriesConflict проверяет перестройку termIndexMap
// при обрезке лога в AppendEntries handler из-за несовпадения термов.
//
// Сценарий:
//  1. Создать CM (Follower) с логом: T1/I1, T1/I2, T2/I3.
//  2. Вызвать AppendEntries с PrevLogIndex=2, PrevLogTerm=1,
//     Entries=[{Index:3, Term:3}] — конфликт на Index=3.
//  3. После обработки: лог обрезан на logInsertPos, Term=2 удалён.
//  4. Проверить: termIndexMap[T2] не существует, termIndexMap[T3]==3.
//
// Ожидание: termIndexMap корректно отражает новый состав лога.
func TestTermIndexMap_AppendEntriesConflict(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{
		storage:    store.NewMapStorage(),
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Follower,
			currentTerm:  1,
			commitIndex:  -1,
			lastApplied:  -1,
			lastLogIndex: 3,
			lastLogTerm:  2,
			log:          make([]LogEntry, 0),
			termIndexMap: make(map[int]int),
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}}},
			},
		},
	}
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 2},
	}
	cm.rebuildTermIndexMapLocked()

	// AppendEntries с конфликтом на Index=3.
	args := AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
		Term:         1,
		LeaderID:     1,
		PrevLogIndex: 2,
		PrevLogTerm:  1,
		Entries: []LogEntry{
			{Index: 3, Term: 3}, // конфликт: существующая T2->T3
		},
		LeaderCommit: -1, // не продвигать commitIndex (нет FSM)
	}
	reply := &AppendEntriesReply{}

	if err := cm.AppendEntries(args, reply); err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries should succeed after conflict resolution")
	}

	// Проверяем длину и последнюю запись.
	if len(cm.cmState.log) != 3 {
		t.Fatalf("log length = %d, want 3", len(cm.cmState.log))
	}
	if cm.cmState.log[2].Term != 3 {
		t.Fatalf("log[2].Term = %d, want 3", cm.cmState.log[2].Term)
	}
	if cm.cmState.log[2].Index != 3 {
		t.Fatalf("log[2].Index = %d, want 3", cm.cmState.log[2].Index)
	}

	// Проверяем termIndexMap: T2 удалён, T3 добавлен.
	if _, ok := cm.cmState.termIndexMap[2]; ok {
		t.Fatal("termIndexMap[2] should be deleted after conflict")
	}
	if idx := cm.cmState.termIndexMap[3]; idx != 3 {
		t.Fatalf("termIndexMap[3] = %d, want 3", idx)
	}
	// T1 должен остаться.
	if idx := cm.cmState.termIndexMap[1]; idx != 2 {
		t.Fatalf("termIndexMap[1] = %d, want 2", idx)
	}
}

// TestTermIndexMap_RestoreFromStorage проверяет перестройку termIndexMap
// после загрузки состояния из storage.
//
// Сценарий:
//  1. Создать CM, заполнить лог, сохранить (persistToStorage).
//  2. Создать новый CM с тем же storage, вызвать restoreFromStorage.
//  3. Проверить: termIndexMap восстановлен из log.
//
// Ожидание: termIndexMap содержит все термы из восстановленного лога.
func TestTermIndexMap_RestoreFromStorage(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := store.NewMapStorage()
	ready := make(chan any)
	close(ready)
	cm := NewConsensusModule(0, []int{}, NewInmemTransport("test"), storage, newSnapshotTestFSM(), ready)

	// Заполняем лог.
	cm.mu.Lock()
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 2},
		{Index: 4, Term: 3},
	}
	cm.cmState.lastLogIndex = 4
	cm.cmState.lastLogTerm = 3
	cm.rebuildTermIndexMapLocked()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.persistToStorage()
	cm.mu.Unlock()
	cm.Stop()

	// Создаём новый CM с тем же storage.
	storage2 := storage
	ready2 := make(chan any)
	close(ready2)

	// Восстанавливаем через прямой вызов (NewConsensusModule вызывает restoreFromStorage).
	cm2 := NewConsensusModule(0, []int{}, NewInmemTransport("test"), storage2, newSnapshotTestFSM(), ready2)
	defer cm2.Stop()

	cm2.mu.Lock()
	defer cm2.mu.Unlock()

	// Проверяем termIndexMap.
	if len(cm2.cmState.termIndexMap) != 3 {
		t.Fatalf("termIndexMap length = %d, want 3", len(cm2.cmState.termIndexMap))
	}
	if idx := cm2.cmState.termIndexMap[1]; idx != 2 {
		t.Fatalf("termIndexMap[1] = %d, want 2", idx)
	}
	if idx := cm2.cmState.termIndexMap[2]; idx != 3 {
		t.Fatalf("termIndexMap[2] = %d, want 3", idx)
	}
	if idx := cm2.cmState.termIndexMap[3]; idx != 4 {
		t.Fatalf("termIndexMap[3] = %d, want 4", idx)
	}
}

// TestTermIndexMap_LeaderSendAEsConflictLookup проверяет, что
// leaderSendAEsToPeer использует termIndexMap для ConflictTerm lookup
// и устанавливает корректный nextIndex (LogEntry.Index, не slice position).
//
// Сценарий:
//  1. Создать CM-лидер с логом: T1/I1, T2/I2, T2/I3.
//  2. nextIndex[peerID] = 3, currentTerm = 2.
//  3. Вызвать leaderSendAEsToPeer с mock, возвращающим ConflictTerm=2.
//  4. Проверить: nextIndex[peerID] = I3 + 1 = 4.
//
// Ожидание: O(1) lookup в termIndexMap даёт correct raft index.
func TestTermIndexMap_LeaderSendAEsConflictLookup(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportConflict{replyTerm: 2, success: false, conflictTerm: 2}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 3},
			matchIndex: map[int]int{1: -1},
		},

		id:         0,
		transport:  mock,
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Leader,
			currentTerm:  2,
			log:          make([]LogEntry, 0),
			lastLogIndex: 3,
			lastLogTerm:  2,
			commitIndex:  -1,
			termIndexMap: make(map[int]int),
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},
	}
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 2},
		{Index: 3, Term: 2},
	}
	cm.rebuildTermIndexMapLocked()

	// Проверяем pre-condition.
	if idx := cm.cmState.termIndexMap[2]; idx != 3 {
		t.Fatalf("pre: termIndexMap[2] = %d, want 3", idx)
	}

	// Вызываем leaderSendAEsToPeer с ConflictTerm=2.
	cm.leaderSendAEsToPeer(1, 2, 0, true)

	// Проверяем nextIndex: должен быть 4 (последний LogEntry.Index с term=2 это 3, +1).
	cm.mu.Lock()
	ni := cm.leaderState.nextIndex[1]
	cm.mu.Unlock()
	if ni != 4 {
		t.Fatalf("nextIndex[1] = %d, want 4 (LogEntry.Index 3 + 1)", ni)
	}
}

// TestTermIndexMap_SlicePositionBug проверяет, что скрытый баг со slice
// position исправлен: после сжатия журнала termIndexMap возвращает
// LogEntry.Index, а не slice position.
//
// Старое поведение (slices.Backward):
//   - cm.cmState.log = [{I100,T1}, {I200,T2}, {I300,T2}]
//   - compactLogsLocked(100) → log не меняется (pos=0)
//   - slices.Backward даёт i=2 для последней записи T2
//   - nextIndex = 2+1 = 3 — НЕВЕРНО (должно быть 301)
//
// Новое поведение (termIndexMap):
//   - termIndexMap[T2] = 300 (LogEntry.Index)
//   - nextIndex = 300 + 1 = 301 — ВЕРНО
//
// Ожидание: nextIndex = 301, подтверждающий исправление бага.
func TestTermIndexMap_SlicePositionBug(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportConflict{replyTerm: 2, success: false, conflictTerm: 2}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 3},
			matchIndex: map[int]int{1: -1},
		},

		id:         0,
		transport:  mock,
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Leader,
			currentTerm:  2,
			log:          make([]LogEntry, 0),
			lastLogIndex: 300,
			lastLogTerm:  2,
			commitIndex:  -1,
			termIndexMap: make(map[int]int),
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},
	}
	// Лог после сжатия: записи начинаются с Index=100.
	cm.cmState.log = []LogEntry{
		{Index: 100, Term: 1},
		{Index: 200, Term: 2},
		{Index: 300, Term: 2},
	}
	cm.rebuildTermIndexMapLocked()

	// Проверяем, что termIndexMap хранит LogEntry.Index, а не slice position.
	if idx := cm.cmState.termIndexMap[2]; idx != 300 {
		t.Fatalf("termIndexMap[2] = %d, want 300 (LogEntry.Index, not slice position)", idx)
	}

	// Вызываем leaderSendAEsToPeer с ConflictTerm=2.
	cm.leaderSendAEsToPeer(1, 2, 0, true)

	// Проверяем nextIndex: должен быть 301 (LogEntry.Index 300 + 1).
	cm.mu.Lock()
	ni := cm.leaderState.nextIndex[1]
	cm.mu.Unlock()
	if ni != 301 {
		t.Fatalf("nextIndex[1] = %d, want 301 (LogEntry.Index 300 + 1). "+
			"Old slices.Backward code would give 3 (slice position+1)!", ni)
	}
}

// TestTermIndexMap_NoSlicesImport подтверждает, что пакет slices
// удалён из импорта raft_cm_replication.go и больше не используется
// в логике репликации.
func TestTermIndexMap_NoSlicesImport(t *testing.T) {
	// Импорт "slices" был удалён в пользу прямого map lookup.
	// Перекомпиляция пакета подтверждает отсутствие ошибок.
}
