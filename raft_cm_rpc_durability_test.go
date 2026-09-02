package raft

import (
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// newAEDurabilityCM собирает ведомого с постоянным хранилищем в каталоге dir
// и сохраняет исходное состояние на диск, чтобы последующие записи можно было
// считать от установившейся точки.
func newAEDurabilityCM(dir string) (*ConsensusModule, *store.FileStorage) {
	storage := store.NewFileStorage(dir)
	cm := &ConsensusModule{storage: storage}
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.lastLogIndex = -1
	cm.cmState.lastLogTerm = -1
	cm.cmState.commitIndex = -1
	cm.cmState.lastApplied = -1
	cm.cmState.fsmAppliedIndex = -1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.logNeedsPersist = true
	cm.persistToStorage()
	return cm, storage
}

// startAEDurabilityFSM подключает к ведомому машину состояний и горутину её
// применения: без неё ветка продвижения индекса фиксации не работает.
// Вызывающий обязан закрыть cm.shutdownCh до проверки утечки горутин.
func startAEDurabilityFSM(t *testing.T, cm *ConsensusModule) *snapshotTestFSM {
	t.Helper()
	fsm := newSnapshotTestFSM()
	cm.fsm = fsm
	cm.fsmMutateCh = make(chan []*commitTuple, 16)
	cm.shutdownCh = make(chan struct{})
	cm.leaderState.inflight = make(map[int]*logFuture)
	go cm.runFSM()
	return fsm
}

// aeArgs формирует запрос AppendEntries от лидера 99 в терме 1.
func aeArgs(prevLogIndex, prevLogTerm, leaderCommit int, entries []LogEntry) AppendEntriesArgs {
	return AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         1,
		LeaderID:     99,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: leaderCommit,
	}
}

// readLogFromDisk открывает каталог новым экземпляром хранилища и возвращает
// журнал, фактически лежащий на диске.
func readLogFromDisk(t *testing.T, dir string) []LogEntry {
	t.Helper()
	data, ok := store.NewFileStorage(dir).Get("log")
	if !ok {
		t.Fatal("key log not found on disk")
	}
	var log []LogEntry
	gobDecode(t, data, &log)
	return log
}

// TestAppendEntries_LogDurableBeforeSuccess проверяет, что ведомый отвечает
// Success только после того, как принятые записи стали долговечными, даже
// когда лидер не продвигает индекс фиксации.
func TestAppendEntries_LogDurableBeforeSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	cm, _ := newAEDurabilityCM(dir)

	entries := []LogEntry{{Index: 0, Term: 1, Type: LogCommand, Data: "k0=v0"}}
	var reply AppendEntriesReply
	if err := cm.AppendEntries(aeArgs(-1, -1, -1, entries), &reply); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if !reply.Success {
		t.Fatal("reply.Success = false, want true")
	}

	cm.mu.Lock()
	commitIndex := cm.cmState.commitIndex
	cm.mu.Unlock()
	if commitIndex != -1 {
		t.Fatalf("commitIndex = %d, want -1: сценарий требует непродвинутого индекса фиксации", commitIndex)
	}

	onDisk := readLogFromDisk(t, dir)
	if len(onDisk) != 1 || onDisk[0].Index != 0 || onDisk[0].Term != 1 {
		t.Fatalf("log on disk = %+v, want one entry (index 0, term 1) before Success", onDisk)
	}
}

// TestAppendEntries_LogDurableWithCommitAdvance проверяет, что поведение при
// продвижении индекса фиксации не изменилось: журнал долговечен, записи
// применены к машине состояний.
func TestAppendEntries_LogDurableWithCommitAdvance(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	cm, _ := newAEDurabilityCM(dir)
	fsm := startAEDurabilityFSM(t, cm)
	defer close(cm.shutdownCh)

	entries := []LogEntry{{Index: 0, Term: 1, Type: LogCommand, Data: "k0=v0"}}
	var reply AppendEntriesReply
	if err := cm.AppendEntries(aeArgs(-1, -1, 0, entries), &reply); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if !reply.Success {
		t.Fatal("reply.Success = false, want true")
	}

	cm.mu.Lock()
	commitIndex := cm.cmState.commitIndex
	cm.mu.Unlock()
	if commitIndex != 0 {
		t.Fatalf("commitIndex = %d, want 0", commitIndex)
	}

	onDisk := readLogFromDisk(t, dir)
	if len(onDisk) != 1 || onDisk[0].Index != 0 || onDisk[0].Term != 1 {
		t.Fatalf("log on disk = %+v, want one entry (index 0, term 1)", onDisk)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if fsm.getState("k0") == "v0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("entry was not applied to FSM")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAppendEntries_HeartbeatNoWrites проверяет, что пульс без записей и без
// продвижения индекса фиксации не выполняет записей на диск.
func TestAppendEntries_HeartbeatNoWrites(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	cm, storage := newAEDurabilityCM(dir)

	before := storage.WriteCount()
	var reply AppendEntriesReply
	if err := cm.AppendEntries(aeArgs(-1, -1, -1, nil), &reply); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if !reply.Success {
		t.Fatal("reply.Success = false, want true")
	}
	if got := storage.WriteCount() - before; got != 0 {
		t.Fatalf("%d writes on heartbeat, want 0", got)
	}
}

// TestAppendEntries_SingleWriteWithCommitAdvance проверяет, что приём записей
// вместе с продвижением индекса фиксации выполняет ровно одну запись на диск,
// несмотря на несколько вызовов сохранения состояния в одном обработчике.
func TestAppendEntries_SingleWriteWithCommitAdvance(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	cm, storage := newAEDurabilityCM(dir)
	startAEDurabilityFSM(t, cm)
	defer close(cm.shutdownCh)

	entries := []LogEntry{{Index: 0, Term: 1, Type: LogCommand, Data: "k0=v0"}}
	before := storage.WriteCount()
	var reply AppendEntriesReply
	if err := cm.AppendEntries(aeArgs(-1, -1, 0, entries), &reply); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if !reply.Success {
		t.Fatal("reply.Success = false, want true")
	}
	if got := storage.WriteCount() - before; got != 1 {
		t.Fatalf("%d writes on AppendEntries with commit advance, want 1", got)
	}
}
