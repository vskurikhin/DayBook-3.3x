package raft

import (
	"errors"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/contract"
	"github.com/vskurikhin/raft/pkg/raft/store"
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
	// worst-case election timeout (_maxElectionTimeout = 2*ReelectionTimeoutMs)
	// отключённый узел НЕ увеличил term и не сменил лидера. Опрос
	// не доказывает отсутствия события — окно осознанно временное;
	// его уменьшение ослабило бы assert.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(_maxElectionTimeout)
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
	// состояние до _leaderElectionBudget (worst-case выборов), поэтому
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
	// (_maxElectionTimeout) minority не должен ни выиграть выборы,
	// ни увеличить term. Окно осознанно временное (см. выше).
	time.Sleep(_maxElectionTimeout)
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
	deadline := time.Now().Add(_commitBudgetAfterFailover)
	disconnectedTerm := h.GetTerm(otherID)
	for disconnectedTerm <= origTerm {
		if !time.Now().Before(deadline) {
			t.Fatalf("disconnected node term = %d, want > %d within %v",
				disconnectedTerm, origTerm, _commitBudgetAfterFailover)
		}
		time.Sleep(_pollInterval)
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
		storage:    store.NewMapStorage(),
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

// newPreVoteTestCM собирает узел для прямого вызова runPreCandidate: сам узел
// и один сосед с правом голоса, инициализированные каналы завершения таймера
// выборов и остановки. becomeFollowerLocked запускает runElectionTimer, поэтому
// вызывающий обязан закрыть cm.shutdownCh до проверки утечки горутин.
func newPreVoteTestCM(transport Transport, term int) *ConsensusModule {
	cm := &ConsensusModule{
		id:         0,
		transport:  transport,
		storage:    store.NewMapStorage(),
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:              Follower,
			currentTerm:        term,
			votedFor:           -1,
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
	return cm
}

// runPreCandidateOnce запускает runPreCandidate в отдельной горутине и ждёт
// её завершения. Возвращает длительность перехода.
func runPreCandidateOnce(t *testing.T, cm *ConsensusModule) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		cm.runPreCandidate()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runPreCandidate did not return")
	}
	return time.Since(start)
}

// blockingPreVoteTransport — транспорт, чей RequestPreVote блокируется до
// закрытия канала release. Ответы в канал сбора не приходят вовсе, поэтому
// select выходит по таймауту, а пост-select проверка потери недостижима.
type blockingPreVoteTransport struct {
	Transport
	release chan struct{}
}

func (m *blockingPreVoteTransport) RequestPreVote(_ ServerID, _ RequestPreVoteArgs) (RequestPreVoteReply, error) {
	<-m.release
	return RequestPreVoteReply{}, errors.New("blocked transport released")
}

func (m *blockingPreVoteTransport) RequestVote(_ ServerID, _ RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

// refusingPreVoteTransport — транспорт, отвечающий отказом (VoteGranted: false)
// на каждый запрос pre-vote с термом, равным текущему. Все ответы приходят,
// кворум не собирается, и шаг вниз выполняет пост-select проверка потери.
type refusingPreVoteTransport struct {
	Transport
	term int
}

func (m *refusingPreVoteTransport) RequestPreVote(_ ServerID, _ RequestPreVoteArgs) (RequestPreVoteReply, error) {
	return RequestPreVoteReply{
		RPCHeader:   RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
		Term:        m.term,
		VoteGranted: false,
	}, nil
}

func (m *refusingPreVoteTransport) RequestVote(_ ServerID, _ RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

// notImplementedPreVoteTransport — транспорт, чей RequestPreVote возвращает
// contract.ErrNotImplemented: ошибка засчитывается как выданный голос.
type notImplementedPreVoteTransport struct {
	Transport
}

func (m *notImplementedPreVoteTransport) RequestPreVote(_ ServerID, _ RequestPreVoteArgs) (RequestPreVoteReply, error) {
	return RequestPreVoteReply{}, contract.ErrNotImplemented
}

func (m *notImplementedPreVoteTransport) RequestVote(_ ServerID, _ RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

// TestPreVote_CollectTimeout_StepsDownToFollower пинирует ветку таймаута сбора
// pre-vote: транспорт блокирует RequestPreVote, ответы не приходят, и select
// выходит по кейсу таймаута. Узел завершает в Follower с неизменённым
// currentTerm (pre-vote терм не инкрементирует), а переход занимает не менее
// ReelectionTimeoutMs — это отличает ветку таймаута от ветки потери.
func TestPreVote_CollectTimeout_StepsDownToFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	release := make(chan struct{})
	cm := newPreVoteTestCM(&blockingPreVoteTransport{release: release}, 2)
	defer close(release)
	defer close(cm.shutdownCh)

	elapsed := runPreCandidateOnce(t, cm)

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state != Follower {
		t.Fatalf("state = %v after pre-vote timeout, want Follower", cm.cmState.state)
	}
	if cm.cmState.currentTerm != 2 {
		t.Fatalf("currentTerm = %d after pre-vote timeout, want 2 (pre-vote must not increment term)", cm.cmState.currentTerm)
	}
	if elapsed < time.Duration(ReelectionTimeoutMs)*time.Millisecond {
		t.Fatalf("step-down via pre-vote timeout took %v, want >= %v", elapsed, time.Duration(ReelectionTimeoutMs)*time.Millisecond)
	}
}

// TestPreVote_QuorumLost_StepsDownToFollower пинирует пост-select проверку
// потери: транспорт отвечает отказом всем соседям, все ответы приходят,
// votersResponded достигает totalVoters, и шаг вниз выполняет проверка потери,
// а не кейс таймаута. Переход происходит строго быстрее ReelectionTimeoutMs —
// без этой границы тест не отличает ветку потери от ветки таймаута.
func TestPreVote_QuorumLost_StepsDownToFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newPreVoteTestCM(&refusingPreVoteTransport{term: 2}, 2)
	defer close(cm.shutdownCh)

	elapsed := runPreCandidateOnce(t, cm)

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state != Follower {
		t.Fatalf("state = %v after pre-vote loss, want Follower", cm.cmState.state)
	}
	if cm.cmState.currentTerm != 2 {
		t.Fatalf("currentTerm = %d after pre-vote loss, want 2 (pre-vote must not increment term)", cm.cmState.currentTerm)
	}
	if elapsed >= time.Duration(ReelectionTimeoutMs)*time.Millisecond {
		t.Fatalf("step-down via quorum loss took %v, want strictly faster than %v", elapsed, time.Duration(ReelectionTimeoutMs)*time.Millisecond)
	}
}

// TestPreVote_ErrNotImplemented_Granted пинирует грант pre-vote при
// contract.ErrNotImplemented: транспорт, не поддерживающий PreVote, засчитывает голос
// предоставленным. Узел переходит к выборам — состояние становится Candidate
// (или Leader), currentTerm инкрементирован на 1.
func TestPreVote_ErrNotImplemented_Granted(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newPreVoteTestCM(&notImplementedPreVoteTransport{}, 2)
	defer close(cm.shutdownCh)

	runPreCandidateOnce(t, cm)

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state != Candidate && cm.cmState.state != Leader {
		t.Fatalf("state = %v after ErrNotImplemented pre-vote, want Candidate or Leader", cm.cmState.state)
	}
	if cm.cmState.currentTerm != 3 {
		t.Fatalf("currentTerm = %d, want 3 (ErrNotImplemented counts as granted vote, election started)", cm.cmState.currentTerm)
	}
}
