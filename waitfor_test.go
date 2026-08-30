package raft

import (
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// newSyntheticHarness создаёт Harness без кластера: заполнены только
// поля, читаемые предикатами ожидания (commits, connected, n, t).
// Используется для юнит-тестов waitFor/committedOn, где нужен полный
// контроль над содержимым h.commits.
func newSyntheticHarness(t *testing.T, n int) *Harness {
	t.Helper()
	h := &Harness{
		cluster:       make([]*ConsensusModule, n),
		commits:       make([][]CommitEntry, n),
		connected:     make([]bool, n),
		alive:         make([]bool, n),
		collectorDone: make([]chan struct{}, n),
		n:             n,
		t:             t,
	}
	for i := 0; i < n; i++ {
		h.connected[i] = true
		h.alive[i] = true
	}
	return h
}

// TestWaitFor_ConditionAlreadyTrue: условие истинно сразу — waitFor
// возвращается немедленно и без ошибки.
func TestWaitFor_ConditionAlreadyTrue(t *testing.T) {
	h := newSyntheticHarness(t, 3)

	start := time.Now()
	err := h.waitFor("always true", time.Second, func() bool { return true }, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitFor returned %v, want nil", err)
	}
	if elapsed > _pollInterval {
		t.Fatalf("waitFor took %v, want immediate return (<= %v)", elapsed, _pollInterval)
	}
}

// TestWaitFor_ConditionBecomesTrue: условие становится истинным
// позже — waitFor дожидается его в пределах бюджета.
func TestWaitFor_ConditionBecomesTrue(t *testing.T) {
	h := newSyntheticHarness(t, 3)

	calls := 0
	err := h.waitFor("true on 3rd poll", time.Second, func() bool {
		calls++
		return calls >= 3
	}, nil)

	if err != nil {
		t.Fatalf("waitFor returned %v, want nil", err)
	}
	if calls < 3 {
		t.Fatalf("cond evaluated %d times, want >= 3", calls)
	}
}

// TestWaitFor_TimeoutDiagnostics: условие никогда не истинно — waitFor
// исчерпывает бюджет и возвращает структурированную диагностику
// (§21 architecture.md: ожидание, бюджет, достигнутое состояние).
func TestWaitFor_TimeoutDiagnostics(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	h.commits[0] = []CommitEntry{{Data: 41, Index: 1, Term: 1}}

	budget := 60 * time.Millisecond
	start := time.Now()
	err := h.waitFor("cmd=42 committed", budget, func() bool { return false }, h.commitsDiag)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("waitFor returned nil, want timeout error")
	}
	if elapsed < budget {
		t.Fatalf("waitFor returned after %v, want >= budget %v", elapsed, budget)
	}
	msg := err.Error()
	for _, want := range []string{"budget", "expected: cmd=42 committed", "commits:", "len[0]=1", "state:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostics %q does not contain %q", msg, want)
		}
	}
}

// TestCommittedOn_NotWeakerThanAssert — регрессионный тест:
// предикат ожидания не слабее assert'а CheckCommitted.
// Узел, «ушедший вперёд» по commits, обязан удерживать converged == false,
// а wait — не возвращаться преждевременно.
func TestCommittedOn_NotWeakerThanAssert(t *testing.T) {
	h := newSyntheticHarness(t, 3)

	// Узел 0 ушёл вперёд: команда 42 есть у всех, но длины не равны —
	// предусловие CheckCommitted (равные длины) не выполнено.
	h.commits[0] = []CommitEntry{{Data: 42, Index: 1, Term: 1}, {Data: 43, Index: 2, Term: 1}}
	h.commits[1] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}
	h.commits[2] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}

	h.mu.Lock()
	nc, converged := h.committedOn(42)
	h.mu.Unlock()
	if nc != 3 {
		t.Fatalf("committedOn nc = %d, want 3", nc)
	}
	if converged {
		t.Fatal("committedOn converged = true while commits lengths differ; predicate is weaker than CheckCommitted")
	}

	// Прежний WaitForCommit вернулся бы здесь немедленно (nc >= n);
	// новый предикат обязан исчерпать бюджет.
	budget := 60 * time.Millisecond
	start := time.Now()
	err := h.waitFor("cmd=42 converged", budget, func() bool {
		n, ok := h.committedOn(42)
		return n >= 3 && ok
	}, h.commitsDiag)
	if err == nil {
		t.Fatal("waitFor returned early: predicate is weaker than assert")
	}
	if elapsed := time.Since(start); elapsed < budget {
		t.Fatalf("waitFor returned after %v, want >= budget %v", elapsed, budget)
	}

	// После сходимости узлов предикат обязан стать истинным.
	h.mu.Lock()
	h.commits[1] = append(h.commits[1], CommitEntry{Data: 43, Index: 2, Term: 1})
	h.commits[2] = append(h.commits[2], CommitEntry{Data: 43, Index: 2, Term: 1})
	nc, converged = h.committedOn(42)
	h.mu.Unlock()
	if nc != 3 || !converged {
		t.Fatalf("committedOn = (%d, %t), want (3, true) after convergence", nc, converged)
	}
}

// TestCommittedOn_IndexMismatch: равные длины, но разный Index записи —
// предусловие CheckCommitted (равный Index) не выполнено.
func TestCommittedOn_IndexMismatch(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	h.commits[0] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}
	h.commits[1] = []CommitEntry{{Data: 42, Index: 2, Term: 1}}
	h.commits[2] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}

	h.mu.Lock()
	nc, converged := h.committedOn(42)
	h.mu.Unlock()

	if nc != 3 {
		t.Fatalf("committedOn nc = %d, want 3", nc)
	}
	if converged {
		t.Fatal("committedOn converged = true while Index differs")
	}
}

// TestCommittedOn_IgnoresDisconnected: отключённые узлы не участвуют
// ни в подсчёте nc, ни в проверке сходимости.
func TestCommittedOn_IgnoresDisconnected(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	h.commits[0] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}
	h.commits[1] = []CommitEntry{{Data: 42, Index: 1, Term: 1}}
	// Узел 2 отстал и отключён — на сходимость влиять не должен.
	h.commits[2] = nil
	h.connected[2] = false

	h.mu.Lock()
	nc, converged := h.committedOn(42)
	h.mu.Unlock()

	if nc != 2 || !converged {
		t.Fatalf("committedOn = (%d, %t), want (2, true)", nc, converged)
	}
}

// TestWaitCond_Immediate: условие, истинное с самого начала, — возврат nil
// без единой паузы опроса (элапс не превышает _pollInterval).
func TestWaitCond_Immediate(t *testing.T) {
	start := time.Now()
	err := waitCond("always true", time.Second, func() bool { return true }, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitCond returned %v, want nil", err)
	}
	if elapsed > _pollInterval {
		t.Fatalf("waitCond took %v, want immediate return (<= %v)", elapsed, _pollInterval)
	}
}

// TestWaitCond_TimeoutWithDiag: условие никогда не истинно — ошибка
// по исчерпании бюджета; текст содержит desc и результат diag.
func TestWaitCond_TimeoutWithDiag(t *testing.T) {
	budget := 3 * _pollInterval
	start := time.Now()
	err := waitCond("counter reaches 5", budget, func() bool { return false }, func() string {
		return "counter=3"
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("waitCond returned nil, want timeout error")
	}
	if elapsed < budget {
		t.Fatalf("waitCond returned after %v, want >= budget %v", elapsed, budget)
	}
	msg := err.Error()
	for _, want := range []string{"budget", "expected: counter reaches 5", "counter=3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostics %q does not contain %q", msg, want)
		}
	}
}

// TestWaitCond_NilDiag: diag == nil не приводит к панике при таймауте.
func TestWaitCond_NilDiag(t *testing.T) {
	err := waitCond("never true", 2*_pollInterval, func() bool { return false }, nil)
	if err == nil {
		t.Fatal("waitCond returned nil, want timeout error")
	}
	if msg := err.Error(); !strings.Contains(msg, "expected: never true") {
		t.Errorf("diagnostics %q does not contain desc", msg)
	}
}

// newLeaderDouble создаёт минимальный ConsensusModule, у которого Report()
// возвращает заданные терм и роль Leader (управляемый тестовый двойник
// для проверки логики CheckSingleLeader без реального кластера).
func newLeaderDouble(id, term int) *ConsensusModule {
	cm := new(ConsensusModule)
	cm.id = id
	cm.cmState.currentTerm = term
	cm.cmState.state = Leader
	return cm
}

// newFollowerDouble — двойник-ведомый с заданным термом.
func newFollowerDouble(id, term int) *ConsensusModule {
	cm := new(ConsensusModule)
	cm.id = id
	cm.cmState.currentTerm = term
	return cm
}

// TestCheckSingleLeader_SameTermFails: два узла в состоянии Leader
// с одинаковым термом — ошибка Election Safety возвращается немедленно,
// без расходования бюджета.
func TestCheckSingleLeader_SameTermFails(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	h.cluster[0] = newLeaderDouble(0, 5)
	h.cluster[1] = newLeaderDouble(1, 5)
	h.cluster[2] = newFollowerDouble(2, 5)

	start := time.Now()
	_, _, err := h.pollSingleLeader(_singleLeaderBudget)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("pollSingleLeader returned nil error with two leaders in the same term; want election safety violation")
	}
	if want := "servers 0 and 1 both think they're leaders in term 5"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
	if elapsed > _pollInterval {
		t.Fatalf("pollSingleLeader took %v, want immediate return (<= %v)", elapsed, _pollInterval)
	}
}

// TestCheckSingleLeader_DifferentTermsConverge: два лидера в разных
// термах — переходное состояние; опрос продолжается, и при схождении
// к одному лидеру возвращается именно он.
func TestCheckSingleLeader_DifferentTermsConverge(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	// Призрачный лидер прежнего терма и действующий лидер нового терма.
	h.cluster[0] = newLeaderDouble(0, 4)
	h.cluster[1] = newLeaderDouble(1, 5)
	h.cluster[2] = newFollowerDouble(2, 5)

	// После нескольких опросов призрачный лидер уходит в step-down.
	go func() {
		time.Sleep(2 * _pollInterval)
		h.cluster[0].mu.Lock()
		h.cluster[0].cmState.state = Follower
		h.cluster[0].mu.Unlock()
	}()

	id, term, err := h.pollSingleLeader(_singleLeaderBudget)
	if err != nil {
		t.Fatalf("pollSingleLeader returned %v, want nil", err)
	}
	if id != 1 || term != 5 {
		t.Fatalf("pollSingleLeader = (%d, %d), want (1, 5)", id, term)
	}
}

// TestCheckSingleLeader_MultiplePersistsFails: множественность лидеров
// в разных термах сохраняется до конца бюджета — ошибка с указанием
// бюджета и периода опроса.
func TestCheckSingleLeader_MultiplePersistsFails(t *testing.T) {
	h := newSyntheticHarness(t, 2)
	h.cluster[0] = newLeaderDouble(0, 4)
	h.cluster[1] = newLeaderDouble(1, 5)

	_, _, err := h.pollSingleLeader(3 * _pollInterval)
	if err == nil {
		t.Fatal("pollSingleLeader returned nil error with persistent multiple leaders; want budget exhaustion")
	}
	if want := "no single leader within budget"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

// TestCheckSingleLeader_NoLeaderFails: ноль лидеров по исчерпании бюджета —
// ошибка ожидания единственного лидера.
func TestCheckSingleLeader_NoLeaderFails(t *testing.T) {
	h := newSyntheticHarness(t, 3)
	for i := range h.cluster {
		h.cluster[i] = newFollowerDouble(i, 1)
	}

	_, _, err := h.pollSingleLeader(3 * _pollInterval)
	if err == nil {
		t.Fatal("pollSingleLeader returned nil error with no leaders; want budget exhaustion")
	}
}

// TestRestartPeer_DoesNotReconnectDisconnected: инвариант связности
// харнесса — рестарт не восстанавливает логически разорванную связь.
// После DisconnectPeer(X) + CrashPeer(Y) + RestartPeer(Y) транспорт Y
// остаётся отключённым от X.
func TestRestartPeer_DoesNotReconnectDisconnected(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	const x, y = 0, 1

	h.DisconnectPeer(x)
	h.CrashPeer(y)
	h.RestartPeer(y)

	if !h.transports[y].IsDisconnected(ServerID(x)) {
		t.Fatalf("transport %d reconnected to disconnected peer %d after restart", y, x)
	}

	// Соединение с третьим узлом (живым и связным) восстановлено.
	if h.transports[y].IsDisconnected(ServerID(2)) {
		t.Fatalf("transport %d is disconnected from live connected peer 2 after restart", y)
	}
}

// TestWaitForApplyInflight: примитив не-commit ожидания дождается
// появления inflight-future на лидере после Apply.
func TestWaitForApplyInflight(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Изолируем лидера — Apply останется в inflight (зафиксировать
	// его без кворума невозможно).
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)

	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Возвращаем связность, чтобы future разрешился и тест не оставил
	// висящих ожиданий.
	h.ReconnectPeer((lid + 1) % 3)
	h.ReconnectPeer((lid + 2) % 3)
	_ = future.Error()
}
