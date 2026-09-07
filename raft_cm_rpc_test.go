package raft

import (
	"testing"

	"github.com/fortytw2/leaktest"
)

// -- Тесты генерации ConflictIndex в AppendEntries. ---

// TestAppendEntries_ConflictIndexSnapshotBoundary проверяет корректность
// нижней границы ConflictIndex при работе с границей снимка.
//
// Сценарий: рассматривается ветка кода (raft_cm_rpc.go),
// где PrevLogIndex > lastLogIndex — это случай, когда у последователя
// пустой журнал, но есть снимок на индексе N.
//
// Ключевое поведение: в такой ситуации follower возвращает ConflictIndex = N+1,
// а не 0. Это важно: последователь никогда не запрашивает записи, которые
// уже покрыты его снимком — он сразу указывает на первую запись, которая
// должна идти после снимка.
//
// Тест также подтверждает, что после обработки ответа согласованное состояние
// сохраняется: lastLogIndex остаётся равным N.
func TestAppendEntries_ConflictIndexSnapshotBoundary(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.storage = NewMapStorage()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = 13490
	cm.cmState.lastSnapshotTerm = 2
	cm.cmState.lastLogIndex = 13490
	cm.cmState.lastLogTerm = 2
	cm.cmState.commitIndex = 13490
	cm.cmState.termIndexMap = make(map[int]int)

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         1,
		LeaderID:     99,
		PrevLogIndex: 13491, // > lastLogIndex — ветка генерации конфликта
		PrevLogTerm:  2,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if reply.Success {
		t.Fatal("AppendEntries must fail with PrevLogIndex beyond lastLogIndex")
	}
	if reply.ConflictIndex != 13491 {
		t.Fatalf("ConflictIndex = %d, want %d (lastSnapshotIndex+1)", reply.ConflictIndex, 13491)
	}
}

// TestAppendEntries_ConflictIndexSnapshotBoundaryCorrupted — defense-in-depth
// даже при испорченном lastLogIndex (-1, состояние,
// порождавшее инцидент) граница работает: ConflictIndex = max(-1, N)+1 = N+1.
func TestAppendEntries_ConflictIndexSnapshotBoundaryCorrupted(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.storage = NewMapStorage()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = 5
	cm.cmState.lastSnapshotTerm = 1
	cm.cmState.lastLogIndex = -1 // испорченное состояние
	cm.cmState.lastLogTerm = -1
	cm.cmState.commitIndex = 5
	cm.cmState.termIndexMap = make(map[int]int)

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         1,
		LeaderID:     99,
		PrevLogIndex: 10,
		PrevLogTerm:  1,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}
	if reply.Success {
		t.Fatal("AppendEntries must fail")
	}
	if reply.ConflictIndex != 6 {
		t.Fatalf("ConflictIndex = %d, want 6 (max(lastLogIndex, lastSnapshotIndex)+1)", reply.ConflictIndex)
	}
}
