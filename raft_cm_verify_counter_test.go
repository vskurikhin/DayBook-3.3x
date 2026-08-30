package raft

import (
	"testing"

	"github.com/fortytw2/leaktest"
)

// Тесты счётчиков verify-запросов: verifyCompleted (знаменатель доли
// «дождавшихся пульса») и verifyWaitedHeartbeat (числитель).
//
// Операционное определение: verify «дождался пульса», если между постановкой
// в pendingVerify и успешным завершением (vf.respond(nil) при
// votes >= quorumSize) успел сработать тик пульса — счётчик
// leaderState.heartbeatTicks вырос относительно значения при постановке
// (vf.enqueuedAtHeartbeat). Ошибки verify в долю не входят ни числителем,
// ни знаменателем.
//
// Правило детерминизма: счётчики читаются только под cm.mu на литеральном
// CM без запущенной stats (как в raft_cm_metrics_test.go). Ни один тест не
// изменяет _traceCM.

// newVerifyCounterCM собирает литеральный CM в роли лидера с двумя
// голосующими (0 — сам узел, 1 — сосед) и заданной очередью pendingVerify.
// vf.init выполняется вызывающим — счётчики завершаются через канал future.
func newVerifyCounterCM(heartbeatTicks uint64, pending ...*verifyFuture) *ConsensusModule {
	return &ConsensusModule{
		id:         0,
		shutdownCh: make(chan struct{}),
		leaderState: leaderState{
			heartbeatTicks: heartbeatTicks,
			pendingVerify:  pending,
		},
		cmState: cmState{
			state:       Leader,
			currentTerm: 1,
			commitIndex: -1,
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
				}},
				latest: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
				}},
			},
		},
	}
}

// completedVerify собирает verify-запрос с уже собранным кворумом голосов:
// votes == quorumSize, поэтому при засчитывании голосов соседа он завершается
// успешно на первом же вызове countVerifyVotesLocked.
func completedVerify(enqueuedAtHeartbeat uint64) *verifyFuture {
	vf := &verifyFuture{
		quorumSize:          2,
		votes:               2,
		voted:               map[int]struct{}{0: {}, 1: {}},
		enqueuedAtHeartbeat: enqueuedAtHeartbeat,
	}
	vf.init(make(chan struct{}))
	return vf
}

// TestVerifyCounters_CompletedBeforeHeartbeatTick — verify, завершённый без
// единого тика пульса после постановки, попадает в знаменатель, но не в
// числитель: verifyCompleted=1, verifyWaitedHeartbeat=0.
func TestVerifyCounters_CompletedBeforeHeartbeatTick(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newVerifyCounterCM(5, completedVerify(5))
	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 1)
	done, waited := cm.counters.verifyCompleted, cm.counters.verifyWaitedHeartbeat
	cm.mu.Unlock()

	if done != 1 || waited != 0 {
		t.Fatalf("verifyCompleted=%d verifyWaitedHeartbeat=%d, want 1 и 0", done, waited)
	}
}

// TestVerifyCounters_CompletedAfterHeartbeatTick — verify, завершённый после
// тика пульса (счётчик тиков вырос относительно постановки), попадает и в
// числитель, и в знаменатель: verifyCompleted=1, verifyWaitedHeartbeat=1.
func TestVerifyCounters_CompletedAfterHeartbeatTick(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newVerifyCounterCM(6, completedVerify(5))
	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 1)
	done, waited := cm.counters.verifyCompleted, cm.counters.verifyWaitedHeartbeat
	cm.mu.Unlock()

	if done != 1 || waited != 1 {
		t.Fatalf("verifyCompleted=%d verifyWaitedHeartbeat=%d, want 1 и 1", done, waited)
	}
}

// TestVerifyCounters_ErrorCompletionDoesNotCount — verify, завершённый с
// ошибкой (потеря лидерства, выход из цикла лидера), не изменяет ни
// числитель, ни знаменатель. Проверяются два пути:
//   - завершение ошибкой напрямую (respond(ErrLeadershipLost), как при выходе
//     из цикла лидера);
//   - ранний возврат countVerifyVotesLocked, когда узел уже не лидер.
func TestVerifyCounters_ErrorCompletionDoesNotCount(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Путь 1: завершение ошибкой не трогает счётчики, хотя запрос готов
	// был завершиться успехом (votes == quorumSize).
	cm := newVerifyCounterCM(6, completedVerify(5))
	cm.mu.Lock()
	for _, vf := range cm.leaderState.pendingVerify {
		vf.respond(ErrLeadershipLost)
	}
	cm.leaderState.pendingVerify = nil
	done, waited := cm.counters.verifyCompleted, cm.counters.verifyWaitedHeartbeat
	cm.mu.Unlock()
	if done != 0 || waited != 0 {
		t.Fatalf("после ошибки verifyCompleted=%d verifyWaitedHeartbeat=%d, want 0 и 0", done, waited)
	}

	// Путь 2: countVerifyVotesLocked при state != Leader завершается раньше
	// засчитывания — счётчики не изменяются.
	cm2 := newVerifyCounterCM(6, completedVerify(5))
	cm2.cmState.state = Follower
	cm2.mu.Lock()
	cm2.countVerifyVotesLocked(1, 1)
	done, waited = cm2.counters.verifyCompleted, cm2.counters.verifyWaitedHeartbeat
	cm2.mu.Unlock()
	if done != 0 || waited != 0 {
		t.Fatalf("при не-лидере verifyCompleted=%d verifyWaitedHeartbeat=%d, want 0 и 0", done, waited)
	}
}

// TestVerifyCounters_MultiplePending — несколько завершающихся запросов
// учитываются независимо: один дождался тика пульса, другой нет.
func TestVerifyCounters_MultiplePending(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newVerifyCounterCM(6, completedVerify(5), completedVerify(6))
	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 1)
	done, waited := cm.counters.verifyCompleted, cm.counters.verifyWaitedHeartbeat
	cm.mu.Unlock()

	if done != 2 || waited != 1 {
		t.Fatalf("verifyCompleted=%d verifyWaitedHeartbeat=%d, want 2 и 1", done, waited)
	}
}
