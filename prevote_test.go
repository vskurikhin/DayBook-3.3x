package raft

import (
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// --- Pre-Vote: защита от лишних выборов при сетевых сбоях (§4) ---
//
// Pre-Vote предотвращает нестабильность кластера, вызванную отключёнными
// узлами, которые инициируют выборы с более высоким term после
// переподключения. Перед увеличением currentTerm кандидат отправляет
// RequestPreVote всем голосующим и ждёт кворум. Только после этого
// запускаются настоящие выборы.

// TestPreVote_DisconnectedFollower_NoElection проверяет, что отключённый
// follower НЕ инициирует выборы и term кластера остаётся стабильным.
//
// Сценарий:
//  1. Кластер из 3 узлов, лидер выбран
//  2. Отключаем один follower от всех
//  3. Ждём несколько election timeout — отключённый узел пытается PreVote,
//     но получает отказ (лидер известен, log-safety не проходит)
//  4. Проверяем: лидер остался тем же, term не изменился
//
// Это ключевое преимущество Pre-Vote перед классическим Raft: в классическом
// Raft отключённый follower увеличил бы term при каждой попытке выборов.
func TestPreVote_DisconnectedFollower_NoElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, origTerm := h.CheckSingleLeader()

	// Отключаем один follower — он перестаёт получать heartbeat от лидера.
	otherID := (lid + 1) % 3
	h.DisconnectPeer(otherID)

	// keep: budgeted negative window — проверяется, что за целый
	// worst-case election timeout (maxElectionTimeout = 2*ReelectionTimeoutMs)
	// отключённый узел НЕ увеличил term и не сменил лидера. Опрос
	// не доказывает отсутствия события — окно осознанно временное;
	// его уменьшение ослабило бы assert.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(maxElectionTimeout)
	newLid, newTerm := h.CheckSingleLeader()
	if newLid != lid {
		t.Errorf("leader changed from %d to %d, want same", lid, newLid)
	}
	if newTerm != origTerm {
		t.Errorf("term changed from %d to %d, want same", origTerm, newTerm)
	}
}

// TestPreVote_CrashedLeader_NewElection проверяет, что при краше лидера
// PreVote НЕ блокирует выборы нового лидера.
//
// Сценарий:
//  1. Кластер из 3 узлов, лидер выбран
//  2. Крашим лидера (CrashPeer) — он полностью недоступен
//  3. Два оставшихся узла не получают heartbeat
//  4. Один из них проходит PreVote (leaderLastContact устарел, log-safety OK)
//     и запускает настоящие выборы
//  5. Проверяем: выбран новый лидер (не crashed)
func TestPreVote_CrashedLeader_NewElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Крашим лидера — теперь два follower'а должны выбрать нового.
	h.CrashPeer(lid)
	h.CheckNoLeader()

	// replace: позитивное ожидание — CheckSingleLeader сам опрашивает
	// состояние до leaderElectionBudget (worst-case выборов), поэтому
	// отдельная фиксированная пауза не нужна.
	newLid, _ := h.CheckSingleLeader()
	if newLid == lid {
		t.Errorf("same leader after crash")
	}
}

// TestPreVote_MajorityPartition проверяет, что при разделении сети
// (majority partition) лидер остается в большинстве, а minority не
// может инициировать выборы.
//
// Сценарий:
//  1. Кластер из 3 узлов, лидер выбран
//  2. Отключаем один follower (minority partition — {лидер, follower} | {follower})
//  3. Majority ({лидер, follower}) продолжает работу — лидер остаётся
//  4. Minority (отключённый узел) пытается PreVote, но получает отказ
//     от majority (leader known)
//  5. Проверяем: лидер из majority, term не изменился
func TestPreVote_MajorityPartition(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, origTerm := h.CheckSingleLeader()

	// Отключаем один follower — он оказывается в minority partition.
	otherID := (lid + 1) % 3
	h.DisconnectPeer(otherID)

	// keep: budgeted negative window — за worst-case election timeout
	// (maxElectionTimeout) minority не должен ни выиграть выборы,
	// ни увеличить term. Окно осознанно временное (см. выше).
	time.Sleep(maxElectionTimeout)
	newLid, newTerm := h.CheckSingleLeader()
	if newLid == otherID {
		t.Errorf("minority node became leader, want majority")
	}
	majorityHasVote := (newLid == lid || newLid == (lid+2)%3)
	if !majorityHasVote {
		t.Errorf("leader %d not in majority", newLid)
	}
	// Term не должен вырасти — PreVote предотвращает выборы minority.
	if newTerm != origTerm {
		t.Errorf("term changed from %d to %d, want same", origTerm, newTerm)
	}
}

// TestPreVote_ReconnectStable проверяет, что при циклическом
// отключении/подключении follower'а лидер и term остаются стабильными.
//
// Сценарий:
//  1. Кластер из 3 узлов, лидер выбран
//  2. Циклически (5 раз): отключаем follower, ждём, подключаем обратно
//  3. После каждого цикла PreVote предотвращает выборы отключённым узлом
//  4. После всех циклов проверяем: term не изменился
//
// Это стресс-тест стабильности Pre-Vote при повторяющихся сетевых сбоях.
func TestPreVote_ReconnectStable(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, origTerm := h.CheckSingleLeader()

	otherID := (lid + 1) % 3
	for range 5 {
		h.DisconnectPeer(otherID)

		h.CheckSingleLeader()

		h.ReconnectPeer(otherID)

		h.CheckSingleLeader()
	}

	_, newTerm := h.CheckSingleLeader()
	if newTerm != origTerm {
		t.Errorf("term changed from %d to %d after reconnect cycles", origTerm, newTerm)
	}
}

// TestPreVote_Disabled проверяет, что при preVoteDisabled = true поведение
// соответствует классическому Raft: отключённый узел увеличивает term.
//
// Сценарий:
//  1. Кластер из 3 узлов с отключённым PreVote, лидер выбран
//  2. Отключаем один follower
//  3. Отключённый узел переходит в Candidate (без PreVote) и увеличивает term
//  4. После переподключения follower'а обнаруживается его более высокий term,
//     что приводит к stepDown текущего лидера и новым выборам
//  5. Проверяем: term вырос (выборы состоялись)
func TestPreVote_Disabled(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Pre-Vote отключается на всех узлах кластера через опцию Harness
	// ДО close(ready) — до старта фоновых горутин (никакой
	// post-start мутации конфигурации). TickerTimeoutMs = 7*Quantum =
	// 21ms — первый tick election timer не успевает сработать раньше.
	h := NewHarnessWithOptions(t, 3, DisablePreVote())
	defer h.Shutdown()

	lid, origTerm := h.CheckSingleLeader()

	// Отключаем один follower — в классическом Raft он начнёт выборы
	// и увеличит term при каждом election timeout.
	otherID := (lid + 1) % 3
	h.DisconnectPeer(otherID)

	// replace: позитивное ожидание — poll GetTerm с дедлайном вместо
	// фиксированной паузы. В классическом Raft отключённый узел
	// увеличивает term на каждом election timeout.
	deadline := time.Now().Add(commitBudgetAfterFailover)
	disconnectedTerm := h.GetTerm(otherID)
	for disconnectedTerm <= origTerm {
		if !time.Now().Before(deadline) {
			t.Fatalf("disconnected node term = %d, want > %d within %v",
				disconnectedTerm, origTerm, commitBudgetAfterFailover)
		}
		time.Sleep(pollInterval)
		disconnectedTerm = h.GetTerm(otherID)
	}
}

// mockPreVoteGrant — минимальный транспорт для unit-теста retry-пути
// кандидата. RequestPreVote всегда предоставляет голос с термом соседа,
// равным текущему терму кандидата (сценарий split vote), а RequestVote
// возвращает пустой ответ, чтобы узел не выигрывал реальные выборы.
type mockPreVoteGrant struct {
	Transport
	peerTerm int
}

func (m *mockPreVoteGrant) RequestPreVote(_ ServerID, _ RequestPreVoteArgs) (RequestPreVoteReply, error) {
	return RequestPreVoteReply{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        1,
		},
		Term:        m.peerTerm,
		VoteGranted: true,
	}, nil
}

func (m *mockPreVoteGrant) RequestVote(_ ServerID, _ RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

// TestPreVote_CandidateRetry проверяет, что кандидат, не набравший кворум
// на реальных выборах (split vote), не «застревает» в состоянии Candidate,
// а повторяет выборы через PreVote.
//
// Раньше runPreCandidate выходил сразу, если узел находился в состоянии
// Candidate. Таймер выборов (runElectionTimer) передавал управление в
// runPreCandidate и завершался, а тот возвращался, ничего не сделав — после
// проигранных выборов кандидат навсегда оставался в Candidate, и кластер
// мог навсегда остаться без лидера (livelock). На практике это проявлялось
// как флаки-тесты вида «leader not found».
//
// Тест напрямую вызывает runPreCandidate на узле в состоянии Candidate с
// транспортной заглушкой, предоставляющей PreVote. Если retry работает, узел
// выигрывает PreVote, запускает настоящие выборы и увеличивает терм; если
// runPreCandidate выходит сразу (старое поведение) — терм остаётся прежним.
func TestPreVote_CandidateRetry(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{
		id:         0,
		transport:  &mockPreVoteGrant{peerTerm: 2},
		storage:    NewMapStorage(),
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:              Candidate,
			currentTerm:        2,
			votedFor:           0,
			electionResetEvent: time.Now(),
			electionTimerDone:  make(chan struct{}),
			configurations: configurations{
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},
	}
	cm.cmState.log = make([]LogEntry, 0)
	defer close(cm.shutdownCh)

	done := make(chan struct{})
	go func() {
		cm.runPreCandidate()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runPreCandidate did not return")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.currentTerm != 3 {
		t.Fatalf("candidate retry did not start new election: term=%d, want 3", cm.cmState.currentTerm)
	}
	if cm.cmState.state != Candidate {
		t.Fatalf("state=%v after retry, want Candidate at new term", cm.cmState.state)
	}
}
