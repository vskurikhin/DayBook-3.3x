package raft

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// checkQuorumBudget — запас времени на обнаружение потери кворума:
// два срока checkQuorumTimeout плюс период тика пульса.
const checkQuorumBudget = 2*defaultCheckQuorumTimeout + 4*HeartbeatTimeoutMs*time.Millisecond

// withCheckQuorumTimeout — опция Harness: срок проверки кворума контактов.
// Значение «заведомо большее» отключает шаг вниз по потере контакта,
// оставляя действующим только страховочный лимит журнала.
func withCheckQuorumTimeout(timeout time.Duration) HarnessOption {
	return func(cm *ConsensusModule) { cm.checkQuorumTimeout = timeout }
}

// slowStorage — постоянное хранилище с искусственной задержкой записи:
// имитация медленного диска для проверки того, что занятость лидера
// вводом-выводом не порождает ложных шагов вниз.
type slowStorage struct {
	inner Storage
	delay time.Duration
}

func (s *slowStorage) Set(key string, value []byte) {
	time.Sleep(s.delay)
	s.inner.Set(key, value)
}

func (s *slowStorage) Get(key string) ([]byte, bool) { return s.inner.Get(key) }

func (s *slowStorage) HasData() bool { return s.inner.HasData() }

// withSlowStorage — опция Harness: обёртка хранилища узла задержкой записи.
// Применяется после восстановления состояния в конструкторе, поэтому
// задерживает только последующие записи.
func withSlowStorage(delay time.Duration) HarnessOption {
	return func(cm *ConsensusModule) { cm.storage = &slowStorage{inner: cm.storage, delay: delay} }
}

// nodeState возвращает роль узла, снятую под cm.mu.
func nodeState(cm *ConsensusModule) CMState {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.cmState.state
}

// uncommittedLogLen возвращает размер незафиксированного хвоста журнала узла.
func uncommittedLogLen(cm *ConsensusModule) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.uncommittedLogLenLocked()
}

// checkQuorumStepDowns возвращает число шагов вниз по потере контакта
// с кворумом на всех узлах кластера.
func checkQuorumStepDowns(h *Harness) int64 {
	var total int64
	for _, cm := range h.cluster {
		total += cm.counters.stepDowns.checkQuorum.Load()
	}
	return total
}

// waitForState ждёт, пока узел не окажется в одном из ожидаемых состояний.
func waitForState(t *testing.T, cm *ConsensusModule, budget time.Duration, want ...CMState) {
	t.Helper()
	err := waitCond(
		"node reaches expected state",
		budget,
		func() bool {
			state := nodeState(cm)
			for _, w := range want {
				if state == w {
					return true
				}
			}
			return false
		},
		func() string { return "state = " + nodeState(cm).String() },
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestCheckQuorum_IsolatedLeaderStepsDown — AC-1: лидер, потерявший связь
// с обоими голосующими, самостоятельно перестаёт быть лидером в пределах
// двух сроков проверки кворума.
func TestCheckQuorum_IsolatedLeaderStepsDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	h.DisconnectPeer(leaderID)

	waitForState(t, leader, checkQuorumBudget, Follower, PreCandidate, Candidate)

	if got := leader.counters.stepDowns.checkQuorum.Load(); got != 1 {
		t.Errorf("stepDowns.checkQuorum = %d, want 1", got)
	}
}

// TestCheckQuorum_ApplyErrorsAfterStepDown — AC-2: ожидающая запись
// получает ErrLeadershipLost, а новая команда — ErrNotLeader.
func TestCheckQuorum_ApplyErrorsAfterStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	h.DisconnectPeer(leaderID)
	pending := leader.Apply(42, time.Second)

	select {
	case err := <-pending.ErrorCh():
		if !errors.Is(err, ErrLeadershipLost) {
			t.Fatalf("pending Apply error = %v, want %v", err, ErrLeadershipLost)
		}
	case <-time.After(checkQuorumBudget):
		t.Fatal("pending Apply future was not resolved after check-quorum step down")
	}

	waitForState(t, leader, checkQuorumBudget, Follower, PreCandidate, Candidate)

	if err := leader.Apply(43, time.Second).Error(); !errors.Is(err, ErrNotLeader) {
		t.Errorf("Apply after step down = %v, want %v", err, ErrNotLeader)
	}
}

// TestCheckQuorum_UncommittedLimitBoundsLog — AC-3: при отключённой
// проверке кворума рост незафиксированного хвоста ограничен
// maxUncommittedEntries, а команда сверх предела отклоняется.
func TestCheckQuorum_UncommittedLimitBoundsLog(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarnessWithOptions(t, 3, withCheckQuorumTimeout(time.Hour))
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	h.DisconnectPeer(leaderID)

	// Изолированный лидер фиксацию не продвигает: журнал растёт до предела.
	for i := 0; i < maxUncommittedEntries+leaderBatchSize; i++ {
		leader.Apply(i, time.Second)
		if uncommittedLogLen(leader) >= maxUncommittedEntries {
			break
		}
	}
	if err := waitCond(
		"uncommitted tail reaches the limit",
		2*time.Second,
		func() bool { return uncommittedLogLen(leader) >= maxUncommittedEntries },
		func() string { return "uncommitted = " + itoa(uncommittedLogLen(leader)) },
	); err != nil {
		t.Fatal(err)
	}

	if got := uncommittedLogLen(leader); got > maxUncommittedEntries {
		t.Fatalf("uncommitted tail = %d, want <= %d", got, maxUncommittedEntries)
	}
	if state := nodeState(leader); state != Leader {
		t.Fatalf("state = %v, want Leader (check-quorum must be disabled in this test)", state)
	}

	rejected := leader.Apply(-1, time.Second)
	select {
	case err := <-rejected.ErrorCh():
		if !errors.Is(err, ErrTooManyUncommittedEntries) {
			t.Fatalf("Apply over the limit = %v, want %v", err, ErrTooManyUncommittedEntries)
		}
	case <-time.After(time.Second):
		t.Fatal("Apply over the limit was not rejected")
	}
	if got := uncommittedLogLen(leader); got > maxUncommittedEntries {
		t.Fatalf("uncommitted tail = %d, want <= %d", got, maxUncommittedEntries)
	}
}

// TestCheckQuorum_ServiceEntriesBypassLimit — AC-4a: запись конфигурации
// проходит мимо страховочного лимита — узел с полным незафиксированным
// хвостом сохраняет способность изменить состав кластера.
func TestCheckQuorum_ServiceEntriesBypassLimit(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarnessWithOptions(t, 3, withCheckQuorumTimeout(time.Hour))
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	h.DisconnectPeer(leaderID)

	for i := 0; i < maxUncommittedEntries+leaderBatchSize; i++ {
		leader.Apply(i, time.Second)
		if uncommittedLogLen(leader) >= maxUncommittedEntries {
			break
		}
	}
	if err := waitCond(
		"uncommitted tail reaches the limit",
		2*time.Second,
		func() bool { return uncommittedLogLen(leader) >= maxUncommittedEntries },
		func() string { return "uncommitted = " + itoa(uncommittedLogLen(leader)) },
	); err != nil {
		t.Fatal(err)
	}

	leader.mu.Lock()
	beforeIndex := leader.cmState.lastLogIndex
	leader.mu.Unlock()

	// Запись конфигурации добавляется в журнал, несмотря на полный хвост:
	// служебные записи лимитом не проверяются. Ответ future не придёт —
	// изолированный лидер запись не зафиксирует.
	leader.AddNonvoter(ServerID(99), ServerAddress("raft-99"))

	if err := waitCond(
		"configuration entry is appended past the limit",
		2*time.Second,
		func() bool {
			leader.mu.Lock()
			defer leader.mu.Unlock()
			return leader.cmState.lastLogIndex > beforeIndex &&
				findServer(leader.cmState.configurations.latest.ConfigServers, ServerID(99)) >= 0
		},
		func() string { return "uncommitted = " + itoa(uncommittedLogLen(leader)) },
	); err != nil {
		t.Fatal(err)
	}

	// Носитель того же свойства для noop вступления в лидерство: обе
	// служебные записи идут одним путём — dispatchLogsUnsafe.
	noop := &logFuture{log: LogEntry{Type: LogNoop}}
	noop.init(leader.shutdownCh)
	leader.mu.Lock()
	beforeIndex = leader.cmState.lastLogIndex
	leader.dispatchLogsUnsafe([]*logFuture{noop})
	afterIndex := leader.cmState.lastLogIndex
	leader.mu.Unlock()
	if afterIndex != beforeIndex+1 {
		t.Fatalf("lastLogIndex after noop = %d, want %d", afterIndex, beforeIndex+1)
	}
}

// TestCheckQuorum_LeadershipRegainedAfterStepDown — AC-5: после шага вниз
// по потере кворума узел снова вступает в лидерство, его цикл лидера
// работает (новая запись фиксируется), канал шага вниз пуст, и в любой
// момент на узле живёт не более одной горутины цикла лидера.
func TestCheckQuorum_LeadershipRegainedAfterStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	orig := h.cluster[origLeaderID]

	// Наблюдатель инварианта «не более одной живой горутины цикла лидера».
	var (
		monitorWG   sync.WaitGroup
		monitorStop = make(chan struct{})
		maxAlive    int
		maxAliveMu  sync.Mutex
	)
	monitorWG.Add(1)
	go func() {
		defer monitorWG.Done()
		for {
			select {
			case <-monitorStop:
				return
			default:
			}
			for _, cm := range h.cluster {
				cm.mu.Lock()
				alive := cm.leaderLoopsAlive
				cm.mu.Unlock()
				maxAliveMu.Lock()
				if alive > maxAlive {
					maxAlive = alive
				}
				maxAliveMu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer func() {
		close(monitorStop)
		monitorWG.Wait()
		maxAliveMu.Lock()
		defer maxAliveMu.Unlock()
		if maxAlive > 1 {
			t.Errorf("max live leader loops on a node = %d, want <= 1", maxAlive)
		}
	}()

	h.DisconnectPeer(origLeaderID)
	waitForState(t, orig, checkQuorumBudget, Follower, PreCandidate, Candidate)
	h.ReconnectPeer(origLeaderID)

	// Возврат лидерства прежнему узлу: передача от текущего лидера.
	newLeaderID, _ := h.CheckSingleLeader()
	if newLeaderID != origLeaderID {
		future := h.LeadershipTransfer(newLeaderID, ServerID(origLeaderID))
		select {
		case err := <-future.ErrorCh():
			if err != nil {
				t.Fatalf("LeadershipTransfer to %d: %v", origLeaderID, err)
			}
		case <-time.After(leaderElectionBudget):
			t.Fatalf("LeadershipTransfer to %d did not complete", origLeaderID)
		}
	}
	if err := waitCond(
		"original node is leader again",
		leaderElectionBudget,
		func() bool { return nodeState(orig) == Leader },
		func() string { return "state = " + nodeState(orig).String() },
	); err != nil {
		t.Fatal(err)
	}

	// Цикл лидера жив: новая запись доходит до фиксации. Оставшийся в канале
	// токен шага вниз завершил бы цикл сразу после вступления в лидерство,
	// и фиксация стала бы невозможной.
	committed := orig.Apply(4242, time.Second)
	select {
	case err := <-committed.ErrorCh():
		if err != nil {
			t.Fatalf("Apply on the node that regained leadership: %v", err)
		}
	case <-time.After(leaderElectionBudget):
		t.Fatal("command submitted to the node that regained leadership was not committed")
	}

	if got := len(orig.stepDown); got != 0 {
		t.Errorf("len(stepDown) = %d, want 0", got)
	}
}

// TestCheckQuorum_LeadershipTransferNoFalseStepDown — AC-6: передача
// лидерства с догоняющей репликацией завершается успешно и не порождает
// шагов вниз по потере контакта.
func TestCheckQuorum_LeadershipTransferNoFalseStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()

	target := (leaderID + 1) % 3
	h.SubmitToServer(leaderID, 100)
	h.WaitForCommit(100, 3)

	// Цель отстаёт по журналу: записи, добавленные при отключённой цели,
	// требуют догоняющей репликации в момент передачи лидерства.
	h.DisconnectPeer(target)
	for i := 0; i < 5; i++ {
		h.SubmitToServer(leaderID, 200+i)
	}
	h.WaitForCommit(204, 2)
	h.ReconnectPeer(target)

	future := h.LeadershipTransfer(leaderID, ServerID(target))
	if err := future.Error(); err != nil {
		t.Fatalf("LeadershipTransfer to %d: %v", target, err)
	}
	h.CheckLeaderIs(target)

	if got := checkQuorumStepDowns(h); got != 0 {
		t.Errorf("stepDowns.checkQuorum across cluster = %d, want 0", got)
	}
}

// TestCheckQuorum_BusyLeaderNoFalseStepDown — AC-4: занятый лидер
// (медленный диск под потоком записей) не шагает вниз по потере контакта.
// Задержка каждой записи в хранилище — slowStorageDelay.
func TestCheckQuorum_BusyLeaderNoFalseStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	// Величина задержки подобрана так, чтобы один обмен AppendEntries
	// оставался заметно короче срока проверки кворума: в тестовом
	// транспорте дедлайн RPC (500 мс) больше этого срока, поэтому
	// более медленное хранилище порождало бы ложные шаги вниз
	// свойством стенда, а не проверяемым кодом.
	const slowStorageDelay = 5 * time.Millisecond

	h := NewHarnessWithOptions(t, 3, withSlowStorage(slowStorageDelay))
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()

	deadline := time.Now().Add(3 * time.Second)
	last := 0
	for i := 0; time.Now().Before(deadline); i++ {
		if idx := h.SubmitToServer(leaderID, 1000+i); idx >= 0 {
			last = 1000 + i
		}
		if nodeState(h.cluster[leaderID]) != Leader {
			break
		}
	}

	if state := nodeState(h.cluster[leaderID]); state != Leader {
		t.Errorf("leader state under slow storage = %v, want Leader", state)
	}
	if last != 0 {
		h.WaitForCommit(last, 3)
	}
	if got := checkQuorumStepDowns(h); got != 0 {
		t.Errorf("stepDowns.checkQuorum across cluster = %d, want 0", got)
	}
}

// TestCheckQuorum_ContactRecordedOnFailedAEReply — отказ AppendEntries
// остаётся доказательством того, что сосед жив: отметка контакта
// обновляется при любом ответе без транспортной ошибки.
func TestCheckQuorum_ContactRecordedOnFailedAEReply(t *testing.T) {
	tracker := newCommitmentTracker(0, 1, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: 5},
			matchIndex:        map[int]int{1: -1},
			commitmentTracker: tracker,
			lastContact:       make(map[int]time.Time),
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			lastLogIndex:      4,
			termIndexMap:      make(map[int]int),
		},
		id: 0,
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	reply := AppendEntriesReply{Term: 1, Success: false, ConflictIndex: 2, ConflictTerm: -1}
	cm.handleAEReply(1, 1, 5, 0, reply, 0)

	if _, ok := cm.leaderState.lastContact[1]; !ok {
		t.Error("lastContact[1] is not set after a rejected AppendEntries reply")
	}
}

// TestCheckQuorum_ContactRecordedOnSnapshotReply — ответ InstallSnapshot
// обрабатывается вне пути ответа AppendEntries, поэтому отметка контакта
// обновляется и там: сосед, догоняемый снимком, отвечает дольше одного
// пульса и не должен считаться потерянным.
func TestCheckQuorum_ContactRecordedOnSnapshotReply(t *testing.T) {
	transport := &mockTransportAE{replyTerm: 1}
	tracker := newCommitmentTracker(0, 1, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: -1},
			matchIndex:        map[int]int{1: -1},
			commitmentTracker: tracker,
			lastContact:       make(map[int]time.Time),
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastSnapshotIndex: 5,
			lastSnapshotTerm:  2,
		},
		id:        0,
		transport: transport,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: "snap-5", Index: 5, Term: 2, Size: 100}},
			openMeta:   &SnapshotMeta{Index: 5, Term: 2, Size: 100},
		},
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	cm.leaderSendSnapshot(1, 1)

	if _, ok := cm.leaderState.lastContact[1]; !ok {
		t.Error("lastContact[1] is not set after an InstallSnapshot reply")
	}
}
