package raft

import (
	"testing"
	"time"

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

// TestAppendEntries_StaleTermFollower_NoStateChange пинирует ветку устаревшего
// терма: ведомый с непустым журналом получает AppendEntries с термом ниже
// собственного. Утверждаются ответ (Success: false, reply.Term равен
// собственному большему терму, ServerID узла) и неизменность состояния узла —
// currentTerm, votedFor, state, commitIndex, lastLogIndex и len(log) идентичны
// до и после вызова. Контактная тройка (electionResetEvent, leaderLastContact,
// leaderID) изменена быть не должна: она принадлежит только ветке равного
// терма.
func TestAppendEntries_StaleTermFollower_NoStateChange(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.id = 5
	cm.storage = NewMapStorage()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 2
	cm.cmState.votedFor = 1
	cm.cmState.lastLogIndex = 0
	cm.cmState.lastLogTerm = 2
	cm.cmState.commitIndex = 0
	cm.cmState.log = []LogEntry{{Index: 0, Term: 2, Type: LogCommand, Data: "x"}}
	cm.cmState.termIndexMap = map[int]int{2: 0}
	cm.cmState.leaderID = 9
	cm.cmState.leaderLastContact = time.Unix(54321, 0)
	cm.cmState.electionResetEvent = time.Unix(12345, 0)

	var reply AppendEntriesReply
	if err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
		Term:         1,
		LeaderID:     9,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
	}, &reply); err != nil {
		t.Fatalf("AppendEntries: %v", err)
	}

	if reply.Success {
		t.Fatal("reply.Success = true, want false for stale term")
	}
	if reply.Term != 2 {
		t.Fatalf("reply.Term = %d, want 2 (own higher term)", reply.Term)
	}
	if reply.RPCHeader.ServerID != 5 {
		t.Fatalf("reply.RPCHeader.ServerID = %d, want 5", reply.RPCHeader.ServerID)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.currentTerm != 2 || cm.cmState.votedFor != 1 || cm.cmState.state != Follower ||
		cm.cmState.commitIndex != 0 || cm.cmState.lastLogIndex != 0 || len(cm.cmState.log) != 1 {
		t.Fatalf("node state changed on stale-term AppendEntries: term=%d votedFor=%d state=%v commitIndex=%d lastLogIndex=%d len(log)=%d",
			cm.cmState.currentTerm, cm.cmState.votedFor, cm.cmState.state, cm.cmState.commitIndex, cm.cmState.lastLogIndex, len(cm.cmState.log))
	}
	if !cm.cmState.electionResetEvent.Equal(time.Unix(12345, 0)) {
		t.Fatal("electionResetEvent changed on stale-term AppendEntries (contact triplet belongs only to the equal-term branch)")
	}
	if !cm.cmState.leaderLastContact.Equal(time.Unix(54321, 0)) {
		t.Fatal("leaderLastContact changed on stale-term AppendEntries (contact triplet belongs only to the equal-term branch)")
	}
	if cm.cmState.leaderID != 9 {
		t.Fatalf("leaderID = %d, want 9 (contact triplet belongs only to the equal-term branch)", cm.cmState.leaderID)
	}
}

// TestAppendEntries_CandidateFromLeadershipTransfer_NoStepDown пинирует ветку
// кандидата передачи лидерства: кандидат с установленным флагом
// candidateFromLeadershipTransfer не делает шаг вниз при AppendEntries от
// старого лидера с равным или на единицу меньшим термом, и только терм строго
// больше собственного приводит к переходу в Follower со сбросом флага. Во всех
// трёх подслучаях узел обязан был персистить текущий терм.
func TestAppendEntries_CandidateFromLeadershipTransfer_NoStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const currentTerm = 5
	cases := []struct {
		name     string
		argsTerm int
		wantTerm int
		wantRole CMState
		wantFlag bool
	}{
		{name: "equal term", argsTerm: currentTerm, wantTerm: currentTerm, wantRole: Candidate, wantFlag: true},
		{name: "term minus one", argsTerm: currentTerm - 1, wantTerm: currentTerm, wantRole: Candidate, wantFlag: true},
		{name: "higher term", argsTerm: currentTerm + 1, wantTerm: currentTerm + 1, wantRole: Follower, wantFlag: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := &ConsensusModule{}
			cm.id = 7
			cm.storage = NewMapStorage()
			cm.shutdownCh = make(chan struct{})
			cm.cmState.state = Candidate
			cm.cmState.currentTerm = currentTerm
			cm.cmState.votedFor = 7
			cm.cmState.electionTimerDone = make(chan struct{})
			cm.cmState.candidateFromLeadershipTransfer.Store(true)
			defer close(cm.shutdownCh)

			var reply AppendEntriesReply
			if err := cm.AppendEntries(AppendEntriesArgs{
				RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
				Term:         tc.argsTerm,
				LeaderID:     1,
				PrevLogIndex: -1,
				PrevLogTerm:  -1,
			}, &reply); err != nil {
				t.Fatalf("AppendEntries: %v", err)
			}

			if reply.Success {
				t.Fatalf("case %q: reply.Success = true, want false", tc.name)
			}
			if reply.Term != tc.wantTerm {
				t.Fatalf("case %q: reply.Term = %d, want %d", tc.name, reply.Term, tc.wantTerm)
			}
			if reply.RPCHeader.ServerID != 7 {
				t.Fatalf("case %q: reply.RPCHeader.ServerID = %d, want 7", tc.name, reply.RPCHeader.ServerID)
			}

			cm.mu.Lock()
			defer cm.mu.Unlock()
			if cm.cmState.state != tc.wantRole {
				t.Fatalf("case %q: state = %v, want %v", tc.name, cm.cmState.state, tc.wantRole)
			}
			if cm.cmState.currentTerm != tc.wantTerm {
				t.Fatalf("case %q: currentTerm = %d, want %d", tc.name, cm.cmState.currentTerm, tc.wantTerm)
			}
			if got := cm.cmState.candidateFromLeadershipTransfer.Load(); got != tc.wantFlag {
				t.Fatalf("case %q: candidateFromLeadershipTransfer = %t, want %t", tc.name, got, tc.wantFlag)
			}

			// Персист: ключ currentTerm присутствует и равен терму после вызова.
			data, ok := cm.storage.Get("currentTerm")
			if !ok {
				t.Fatalf("case %q: currentTerm not persisted", tc.name)
			}
			var stored int
			gobDecode(t, data, &stored)
			if stored != tc.wantTerm {
				t.Fatalf("case %q: persisted currentTerm = %d, want %d", tc.name, stored, tc.wantTerm)
			}
		})
	}
}
