package raft

import (
	"errors"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// waitFuture ожидает завершения future через канал ErrorCh с дедлайном.
// Единственная допустимая форма ожидания future в новых тестах:
// у future нет собственного таймаута (future.go), поэтому f.Error()
// при потере кворума завис бы навсегда вместо assert'а.
func waitFuture(t *testing.T, f Future, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-f.ErrorCh():
		return err
	case <-time.After(timeout):
		t.Fatalf("future not resolved in %v", timeout)
		return nil
	}
}

// TestCommitmentE2E_FourNodes_TwoDownNoCommit — n=4 negative:
// при двух отключённых ведомых (живы 2/4, кворум 3/4 недостижим)
// команда лидера НЕ фиксируется и FSM её не применяет; после
// восстановления связи фиксация проходит на реальном кворуме.
func TestCommitmentE2E_FourNodes_TwoDownNoCommit(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключить двух ведомых — остаются лидер + 1 follower = 2/4.
	f1 := (lid + 1) % 4
	f2 := (lid + 2) % 4
	h.DisconnectPeer(f1)
	h.DisconnectPeer(f2)

	cmd := 1001
	future := h.cluster[lid].Apply(cmd, 0)

	// Команда не фиксируется при 2/4: кворум 3/4 недостижим. Лидер,
	// потерявший контакт с кворумом, шагает вниз и отклоняет ожидающую
	// запись — ошибкой, а не фиксацией.
	if err := waitFuture(t, future, checkQuorumBudget); !errors.Is(err, ErrLeadershipLost) {
		t.Fatalf("pending Apply without quorum = %v, want %v", err, ErrLeadershipLost)
	}
	h.CheckNotCommitted(cmd)

	// Восстановить связь — кворум 4/4 достижим, кластер снова фиксирует записи.
	h.ReconnectPeer(f1)
	h.ReconnectPeer(f2)

	newLid, _ := h.WaitForSingleLeader(leaderElectionBudget)
	restored := 1011
	if err := waitFuture(t, h.cluster[newLid].Apply(restored, 0), 3*time.Second); err != nil {
		t.Fatalf("Apply failed after reconnect: %v", err)
	}
	// После реконнекта подключены все 4 узла: assert CheckCommittedN
	// (выполняется WaitForCommit после ожидания сходимости) требует
	// точного числа подключённых узлов.
	h.WaitForCommit(restored, 4)
}

// TestCommitmentE2E_FourNodes_OneDownStillCommits — n=4 позитивный сценарий:
// при одном отключённом ведомом (живы 3/4 = кворум) фиксация проходит.
func TestCommitmentE2E_FourNodes_OneDownStillCommits(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	down := (lid + 1) % 4
	h.DisconnectPeer(down)

	cmd := 1002
	future := h.cluster[lid].Apply(cmd, 0)
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("Apply failed with 3/4 alive: %v", err)
	}
	h.WaitForCommit(cmd, 3)
}

// TestCommitmentE2E_TwoNodes_FollowerDownNoCommit — n=2 регрессия:
// при отключённом ведомом (жив 1 из 2) команда НЕ фиксируется.
func TestCommitmentE2E_TwoNodes_FollowerDownNoCommit(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 2)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	follower := (lid + 1) % 2
	h.DisconnectPeer(follower)

	cmd := 1003
	future := h.cluster[lid].Apply(cmd, 0)

	// При 1/2 фиксация невозможна (кворум 2/2): лидер шагает вниз по потере
	// контакта и отклоняет ожидающую запись.
	if err := waitFuture(t, future, checkQuorumBudget); !errors.Is(err, ErrLeadershipLost) {
		t.Fatalf("pending Apply without quorum = %v, want %v", err, ErrLeadershipLost)
	}
	h.CheckNotCommitted(cmd)

	h.ReconnectPeer(follower)
	newLid, _ := h.WaitForSingleLeader(leaderElectionBudget)
	restored := 1013
	if err := waitFuture(t, h.cluster[newLid].Apply(restored, 0), 3*time.Second); err != nil {
		t.Fatalf("Apply failed after reconnect: %v", err)
	}
	h.WaitForCommit(restored, 2)
}

// TestCommitmentE2E_NoopCommitsAfterFailover — проверка фиксации noop после failover:
//
// Сценарий failover подразумевает, что недоступный узел (бывший лидер) должен быть
// признан недоступным ДО начала выборов нового лидера.
//
// По завершении выборов нового лидера его noop‑запись текущего терма должна быть
// зафиксирована без участия клиентских команд, что подтверждается условием:
//
//	cmState.commitIndex >= leaderStartIndex + 1
//
// При этом self‑match должен оставаться синхронизированным с noop‑записью.
//
// ВНИМАНИЕ: формулировка «отключить ведомого после завершения выборов лидера» некорректна:
// она не учитывает требование кворума и тем самым нарушает гарантии консистентности.
// В частности, операция noop должна считаться зафиксированной только при подтверждении
// её репликации кворумом из трёх живых ведомых узлов. Использование логики,
// игнорирующей этот кворум, запрещено.
func TestCommitmentE2E_NoopCommitsAfterFailover(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключить действующего лидера до избрания нового.
	h.DisconnectPeer(lid)

	// Дождаться нового лидера среди трёх оставшихся узлов.
	deadline := time.Now().Add(5 * time.Second)
	newLeader := -1
	for time.Now().Before(deadline) && newLeader < 0 {
		for i := 0; i < h.n; i++ {
			if i == lid || !h.connected[i] {
				continue
			}
			if _, _, isLeader := h.cluster[i].Report(); isLeader {
				newLeader = i
				break
			}
		}
		if newLeader < 0 {
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(10 * time.Millisecond)
		}
	}
	if newLeader < 0 {
		t.Fatalf("no new leader elected after disconnecting leader %d", lid)
	}

	// Поллинг: noop нового терма должен зафиксироваться без клиентских
	// записей. Без §9 п.3 commitIndex остаётся <= leaderStartIndex
	// (дискриминирует код без self-match с noop).
	cm := h.cluster[newLeader]
	deadline = time.Now().Add(5 * time.Second)
	committed := false
	for time.Now().Before(deadline) && !committed {
		cm.mu.Lock()
		committed = cm.cmState.commitIndex >= cm.leaderState.leaderStartIndex+1
		cm.mu.Unlock()
		if !committed {
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !committed {
		cm.mu.Lock()
		ci := cm.cmState.commitIndex
		lsi := cm.leaderState.leaderStartIndex
		cm.mu.Unlock()
		t.Fatalf("noop of new leader not committed: commitIndex=%d leaderStartIndex=%d", ci, lsi)
	}
}

// TestConfiguration_AddVoterFromSingleVoter — проверка добавления узла с правом голоса
// в кластер, изначально состоящий из одного узла с правом голоса (bootstrap AddVoter).
//
// Harness неприменим для данного сценария, поскольку все его узлы обладают правом голоса.
// Вследствие этого одноузловой кластер формируется вручную.
//
// Узел B инициализируется с peerIds=[0] (это обязательное условие).
// Пустой список peerIds=[] недопустим: в таком случае узел B самостоятельно избирается
// лидером в терме 1 и фиксирует собственную noop‑запись на индексе 0. Это приводит
// к возникновению двух независимых «единиц истины» в рамках одного терма, что делает
// результат теста недетерминированным (успех возможен лишь за счёт совпадения содержимого noop).
//
// Перед выполнением операции AddVoter фиксируется исходное состояние узла B:
// он не является лидером, а currentTerm равен 0.
//
// При отсутствии add‑only репликации (начиная с момента append, см. §9 п. 4) тест
// переходит в состояние бесконечного ожидания: конфигурационная запись не реплицируется
// на узел B, но его подтверждение (ack) учитывается в кворуме 2/2. В результате
// ожидание будущего (future) не завершается — поэтому использование дедлайна обязательно.
func TestConfiguration_AddVoterFromSingleVoter(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	transportA := NewInmemTransport(ServerAddress("raft-A"))
	transportB := NewInmemTransport(ServerAddress("raft-B"))
	transportA.Connect(ServerID(1), transportB)
	transportB.Connect(ServerID(0), transportA)

	commitChA := make(chan CommitEntry, 16)
	commitChB := make(chan CommitEntry, 16)
	readyA := make(chan any)
	readyB := make(chan any)

	// Узел A: одноузловой кластер (голосующий только он сам).
	cmA := NewConsensusModule(0, []int{}, transportA, NewMapStorage(),
		NewCommitChannelFSM(commitChA), readyA)
	defer cmA.Stop()
	// Узел B: follower с известным соседом 0 (непустой peerIds).
	// Готовность B закрывается ПОЗЖЕ — после избрания лидера A:
	// иначе B (не получая AE, т.к. ещё не в конфигурации A) успевает
	// самоизбраться в терме 1 и assert на currentTerm == 0 флакает.
	cmB := NewConsensusModule(1, []int{0}, transportB, NewMapStorage(),
		NewCommitChannelFSM(commitChB), readyB)
	defer cmB.Stop()

	// Сначала запускается только A и дожидается лидерства.
	close(readyA)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, isLeader := cmA.Report(); isLeader {
			break
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, isLeader := cmA.Report(); !isLeader {
		t.Fatal("node A did not become leader")
	}

	// Фиксация состояния B ДО запуска его election timer: B не лидер
	// и currentTerm == 0 (узел не успевает самоизбраться).
	if _, term, isLeader := cmB.Report(); isLeader || term != 0 {
		t.Fatalf("B before ready: term=%d isLeader=%v, want term=0 isLeader=false", term, isLeader)
	}

	// Запуск B и немедленный AddVoter: AE с config entry доходят до B
	// раньше, чем истекает его election timeout, поэтому B остаётся
	// follower'ом (терм переходит в 1 от AE лидера — штатно).
	close(readyB)
	if _, term, isLeader := cmB.Report(); isLeader || term != 0 {
		t.Fatalf("B before AddVoter: term=%d isLeader=%v, want term=0 isLeader=false", term, isLeader)
	}

	// Bootstrap AddVoter: config entry реплицируется на B (add-only
	// репликация с момента append) и фиксируется 2/2.
	future := cmA.AddVoter(ServerID(1), ServerAddress("raft-B"))
	if err := waitFuture(t, future, 5*time.Second); err != nil {
		t.Fatalf("AddVoter from single voter failed: %v", err)
	}

	// Конфигурация A содержит B как голосующего.
	if !hasVote(cmA.GetConfiguration().Configuration(), 1) {
		t.Fatal("node B is not a voter after AddVoter")
	}

	// Клиентская запись на A применяется на ОБОИХ узлах (B получает
	// записи после добавления).
	cmd := 2001
	future = cmA.Apply(cmd, 0)
	if err := waitFuture(t, future, 5*time.Second); err != nil {
		t.Fatalf("Apply after AddVoter failed: %v", err)
	}
	for _, ch := range []chan CommitEntry{commitChA, commitChB} {
		select {
		case entry := <-ch:
			if entry.Data.(int) != cmd {
				t.Fatalf("committed %v, want %d", entry.Data, cmd)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for committed command")
		}
	}
}

// TestConfiguration_RemovedServerSeesRemoval — после
// RemoveServer(non-leader) удалённый узел видит себя вне конфигурации
// (GetConfiguration не содержит его как голосующего). Дискриминирует
// симметричную форму §9 п.4: при останове репликации на момент append
// удаляемый сервер не получает запись о собственном удалении и
// остаётся «сиротой» (видит себя голосующим).
func TestConfiguration_RemovedServerSeesRemoval(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	victim := (lid + 1) % 3

	future := h.cluster[lid].RemoveServer(ServerID(victim))
	if err := waitFuture(t, future, 5*time.Second); err != nil {
		t.Fatalf("RemoveServer failed: %v", err)
	}

	// Поллинг на удалённом узле: запись об удалении доставляется ему
	// best-effort репликацией (несколько heartbeat-интервалов).
	deadline := time.Now().Add(3 * time.Second)
	removed := false
	for time.Now().Before(deadline) && !removed {
		cfg := h.cluster[victim].GetConfiguration().Configuration()
		removed = !hasVote(cfg, victim)
		if !removed {
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(10 * time.Millisecond)
		}
	}
	cfg := h.cluster[victim].GetConfiguration().Configuration()
	if hasVote(cfg, victim) {
		t.Fatalf("victim %d still sees itself as voter; cfg=%+v", victim, cfg.ConfigServers)
	}
}
