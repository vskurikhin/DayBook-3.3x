package raft

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"

	"github.com/vskurikhin/raft/pkg/raft/store"
)

// TestFileStorage_RestartRestoresState проверяет, что после рестарта
// ConsensusModule с FileStorage состояние (currentTerm, votedFor, log,
// lastLogIndex) восстанавливается с диска и узел не стартует с пустым
// журналом (lastLogIndex == -1), что является условием цикла
// PreCandidate -> Follower -> PreCandidate.
func TestFileStorage_RestartRestoresState(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage := store.NewFileStorage(dir)
	fsm1 := newSnapshotTestFSM()
	ready1 := make(chan any)
	close(ready1)
	transport1 := NewInmemTransport("single")

	cm1 := NewConsensusModule(0, []int{}, transport1, storage, fsm1, ready1)
	defer cm1.Stop()

	// Ждём, пока одиночный узел станет лидером.
	waitForLeader(t, cm1, 2*time.Second)

	// Записываем несколько команд (int — не требует gob.Register(Command{})).
	const numCommands = 10
	for i := 0; i < numCommands; i++ {
		future := cm1.Apply(i, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply(%d) failed: %v", i, err)
		}
	}

	// Запоминаем состояние до рестарта.
	cm1.mu.Lock()
	prevTerm := cm1.cmState.currentTerm
	prevVotedFor := cm1.cmState.votedFor
	prevLogLen := len(cm1.cmState.log)
	prevLastLogIndex := cm1.cmState.lastLogIndex
	cm1.mu.Unlock()

	if prevLastLogIndex < 0 {
		t.Fatal("leader did not write any log entries before restart")
	}

	// Симулируем рестарт: останавливаем CM и транспорт, создаём новый
	// FileStorage в той же директории и новый CM.
	cm1.Stop()
	transport1.Close()

	fsm2 := newSnapshotTestFSM()
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("single-2")
	storage2 := store.NewFileStorage(dir)

	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, fsm2, ready2)
	defer cm2.Stop()

	// Проверяем восстановление состояния с диска.
	cm2.mu.Lock()
	restoredTerm := cm2.cmState.currentTerm
	restoredVotedFor := cm2.cmState.votedFor
	restoredLogLen := len(cm2.cmState.log)
	restoredLastLogIndex := cm2.cmState.lastLogIndex
	cm2.mu.Unlock()

	if !cm2.storage.HasData() {
		t.Fatal("HasData() == false after restart, want true")
	}
	if restoredTerm != prevTerm {
		t.Fatalf("currentTerm = %d after restart, want %d", restoredTerm, prevTerm)
	}
	if restoredVotedFor != prevVotedFor {
		t.Fatalf("votedFor = %d after restart, want %d", restoredVotedFor, prevVotedFor)
	}
	if restoredLogLen != prevLogLen {
		t.Fatalf("log length = %d after restart, want %d", restoredLogLen, prevLogLen)
	}
	if restoredLastLogIndex != prevLastLogIndex {
		t.Fatalf("lastLogIndex = %d after restart, want %d", restoredLastLogIndex, prevLastLogIndex)
	}
	if restoredLastLogIndex < 0 {
		t.Fatal("lastLogIndex == -1 after restart: pre-vote loop condition not eliminated")
	}

	// Проверяем, что восстановленный узел работоспособен: одиночный узел
	// снова становится лидером (lastLogIndex != -1 не мешает выиграть выборы).
	waitForLeader(t, cm2, 2*time.Second)
}

// gobEncode кодирует value в gob-формат, как это делает raft_cm_storage.go.
func gobEncode(t *testing.T, value any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	return buf.Bytes()
}

// gobDecode декодирует data в value, как это делает raft_cm_storage.go.
func gobDecode(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := gob.NewDecoder(bytes.NewBuffer(data)).Decode(value); err != nil {
		t.Fatalf("gob decode: %v", err)
	}
}
