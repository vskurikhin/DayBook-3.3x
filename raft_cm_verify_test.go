package raft

import (
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// Тесты немедленной перерассылки AppendEntries при неудовлетворённом
// verify-запросе (ReadIndex).
//
// Механизм: verify-запрос, поставленный в момент, когда соседу уже летит
// AppendEntries, не получает голоса от этого AppendEntries (ответ имеет старую
// эпоху) и без перерассылки ждал бы ближайшего пульса — до
// HeartbeatTimeoutMs = 33 мс. Перерассылка в defer горутины репликации
// отправляет свежий AppendEntries с новой эпохой сразу после снятия флага,
// не меняя ни одного правила кворума.

// verifyRedispatchTransport — транспорт для таймингового теста AC-1: считает
// вызовы AppendEntries и придерживает каждый из них в полёте до закрытия
// канала blockCh (после закрытия вызовы проходят немедленно). Поля lastArgs
// не ведёт, поэтому нет гонки записи между параллельными горутинами
// репликации (в отличие от mockTransportAE). blockCh унаследован от
// встроенного mockTransportAE.
type verifyRedispatchTransport struct {
	*mockTransportAE
	calls atomic.Int32
}

func (t *verifyRedispatchTransport) AppendEntries(_ ServerID, _ AppendEntriesArgs) (AppendEntriesReply, error) {
	t.calls.Add(1)
	if t.blockCh != nil {
		<-t.blockCh
	}
	return AppendEntriesReply{Term: 1, Success: true}, nil
}

// newRedispatchLeaderCM собирает литеральный CM в роли лидера с соседом 1,
// готовый к запуску цикла лидера (runLeaderLoop). Цикл обрабатывает verifyCh
// (VerifyLeader), heartbeat-тик и выход по shutdownCh. Поля, нужные только
// веткам applyCh/commitCh, заданы консервативно (commitIndex = -1, поэтому
// processLogs по страховочному таймеру не вызывается).
func newRedispatchLeaderCM(transport Transport) *ConsensusModule {
	cm := &ConsensusModule{
		id:                 0,
		transport:          transport,
		shutdownCh:         make(chan struct{}),
		applyCh:            make(chan *logFuture),
		commitCh:           make(chan int, 1),
		stepDown:           make(chan struct{}, 1),
		confChangeCh:       make(chan *configurationChangeFuture, 1),
		verifyCh:           make(chan *verifyFuture, 64),
		checkQuorumTimeout: time.Hour,
		storage:            NewMapStorage(),
		fsm:                NewCommitChannelFSM(make(chan CommitEntry)),
		leaderState: leaderState{
			nextIndex:              map[int]int{1: 1},
			matchIndex:             map[int]int{1: -1},
			inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
			inflight:               map[int]*logFuture{},
			lastContact:            map[int]time.Time{1: time.Now()},
			nextVerifyRedispatchAt: make(map[int]time.Time),
			leadershipTransferCh:   make(chan *leadershipTransferFuture, 1),
			leaderStartIndex:       0,
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			lastApplied:       -1,
			fsmAppliedIndex:   -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
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
			electionTimerDone: make(chan struct{}),
		},
	}
	cm.leaderState.inflightAE[1].Store(false)
	cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
	cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
	return cm
}

// newRedispatchReplicationCM собирает литеральный CM в роли лидера с соседом 1
// без цикла лидера: рассылка запускается напрямую через
// leaderSendAEsToPeerIfIdle, а verify-запросы задаются вручную в
// pendingVerify. Для AC-4 (нет самовоспроизводящейся рассылки).
func newRedispatchReplicationCM(transport *mockTransportAE) *ConsensusModule {
	cm := &ConsensusModule{
		id:         0,
		transport:  transport,
		shutdownCh: make(chan struct{}),
		leaderState: leaderState{
			nextIndex:              map[int]int{1: 1},
			matchIndex:             map[int]int{1: -1},
			inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
			lastAttempt:            map[int]time.Time{},
			replFailures:           map[int]int{},
			verifyEpoch:            0,
			nextVerifyRedispatchAt: make(map[int]time.Time),
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
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
	cm.leaderState.inflightAE[1].Store(false)
	return cm
}

// newPendingVerify собирает verify-запрос с уже учтённым голосом лидера
// (votes = 1) и эпохой epoch: для кворума из 2 голосующих нужен ещё один голос
// соседа, и голос засчитывается только от AppendEntries, отправленного не
// раньше постановки запроса (vf.epoch <= dispatchEpoch).
func newPendingVerify(epoch uint64) *verifyFuture {
	vf := &verifyFuture{
		voted:      map[int]struct{}{0: {}},
		votes:      1,
		quorumSize: 2,
		epoch:      epoch,
	}
	vf.init(make(chan struct{}))
	return vf
}

// inflightAELoaded сообщает значение флага inflightAE[peerID] под cm.mu.
func (cm *ConsensusModule) inflightAELoaded(peerID int) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.leaderState.inflightAE != nil &&
		cm.leaderState.inflightAE[peerID] != nil &&
		cm.leaderState.inflightAE[peerID].Load()
}

// waitFor опрашивает условие cond с бюджетом budget (poll-интервал condition,
// а не фиксированная пауза). Единственная допустимая форма ожидания условия
// в новых тестах.
func waitFor(t *testing.T, desc string, budget time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(_pollInterval)
	}
	t.Fatalf("не дождались условия: %s (за %v)", desc, budget)
}

// TestVerifyRedispatch_AC1_CompletesBeforeHeartbeat — главный тайминговый
// критерий AC-1: VerifyLeader, поставленный в момент активной горутины
// репликации соседа, завершается быстрее HeartbeatTimeoutMs благодаря
// немедленной перерассылке.
//
// Сценарий:
//  1. Запущен цикл лидера (обрабатывает verifyCh и heartbeat).
//  2. Активная горутина репликации соседа 1: флаг inflightAE[1] занят,
//     транспорт придерживает первый AppendEntries в полёте (dispatchEpoch = 0).
//  3. VerifyLeader поставлен в этот момент: цикл лидера инкрементирует эпоху
//     до 1 и ставит запрос в pendingVerify; голос от летящего AppendEntries
//     (старой эпохи 0) не засчитывается, а новый AppendEntries не отправляется
//     (флаг занят).
//  4. Транспорт освобождается: завершившаяся горутина перерассылает свежий
//     AppendEntries с новой эпохой 1, голос засчитывается, запрос завершается.
//     Время от освобождения до завершения — микросекунды, много меньше пульса.
//
// До изменения (без перерассылки) запрос завершился бы только ближайшим тиком
// пульса — до 33 мс, порог проверки 20 мс (30 мс под -race) нарушается.
func TestVerifyRedispatch_AC1_CompletesBeforeHeartbeat(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	transport := &verifyRedispatchTransport{
		mockTransportAE: &mockTransportAE{blockCh: make(chan struct{})},
	}
	cm := newRedispatchLeaderCM(transport)
	defer cm.Stop()
	cm.goSpawn(cm.runLeaderLoop)

	// Активная горутина репликации на соседа 1: флаг занят, первый
	// AppendEntries удерживается транспортом в полёте.
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "первый AppendEntries в полёте (флаг занят)", _inmemRPCTimeout, func() bool {
		return transport.calls.Load() >= 1 && cm.inflightAELoaded(1)
	})

	// VerifyLeader поставлен в момент активной горутины репликации.
	future := cm.VerifyLeader()
	waitFor(t, "verify-запрос в очереди pendingVerify", _inmemRPCTimeout, func() bool {
		cm.mu.Lock()
		n := len(cm.leaderState.pendingVerify)
		cm.mu.Unlock()
		return n == 1
	})
	// Детерминизм против пульса: фиксируем счётчик тиков в момент постановки
	// запроса и дожидаемся, чтобы после неё прошёл полный тик пульса.
	// Ближайший пульс после него будет на расстоянии ~HeartbeatTimeoutMs,
	// поэтому без перерассылки запрос завершился бы за ~33 мс (порог 20 мс
	// нарушен), а перерассылка завершает его за микросекунды ещё до
	// следующего пульса.
	cm.mu.Lock()
	hbAtEnqueue := cm.leaderState.heartbeatTicks
	cm.mu.Unlock()
	waitFor(t, "тик пульса после постановки verify-запроса", _inmemRPCTimeout, func() bool {
		cm.mu.Lock()
		ticks := cm.leaderState.heartbeatTicks
		cm.mu.Unlock()
		return ticks > hbAtEnqueue
	})

	start := time.Now()
	close(transport.blockCh)

	select {
	case err := <-future.ErrorCh():
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("VerifyLeader завершился с ошибкой: %v", err)
		}
		threshold := 20 * time.Millisecond
		if os.Getenv("GORACE") != "" {
			threshold = 30 * time.Millisecond
		}
		if elapsed >= threshold {
			t.Fatalf("VerifyLeader завершился за %v, want < %v (сработала перерассылка, а не пульс)",
				elapsed, threshold)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("VerifyLeader не завершился за 2s (перерассылка не сработала)")
	}
}

// TestVerifyRedispatch_AC2_NonvoterDoesNotVote — критерий AC-2: голос не
// засчитывается от узла вне голосующих актуальной конфигурации
// (hasVote(latest, peerID) == false). Сосед 1 демотирован в неголосующего:
// его подтверждение пульса голосом не является.
func TestVerifyRedispatch_AC2_NonvoterDoesNotVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	vf := newPendingVerify(0)
	cm := &ConsensusModule{
		id: 0,
		leaderState: leaderState{
			pendingVerify: []*verifyFuture{vf},
		},
		cmState: cmState{
			state:       Leader,
			currentTerm: 1,
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
				}},
				latest: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Nonvoter},
				}},
			},
		},
	}

	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 0)
	votes := vf.votes
	cm.mu.Unlock()

	// Учёт ведётся от self-голоса (votes=1): голос неголосующего узла не
	// добавляется — значение не изменилось.
	if votes != 1 {
		t.Fatalf("votes = %d, want 1 (голос от неголосующего узла не засчитывается)", votes)
	}
	if len(cm.leaderState.pendingVerify) != 1 {
		t.Fatalf("pendingVerify = %v, want запрос в очереди (кворум не собран)", cm.leaderState.pendingVerify)
	}
}

// TestVerifyRedispatch_AC3_StaleEpochDoesNotVote — критерий AC-3: голос не
// засчитывается от ответа на AppendEntries, отправленный до постановки
// verify-запроса (vf.epoch > dispatchEpoch). Эпоха-фильтр не ослаблен.
func TestVerifyRedispatch_AC3_StaleEpochDoesNotVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	newCM := func() (*ConsensusModule, *verifyFuture) {
		vf := newPendingVerify(2) // запрос поставлен в раунде эпохи 2
		cm := &ConsensusModule{
			id: 0,
			leaderState: leaderState{
				pendingVerify: []*verifyFuture{vf},
			},
			cmState: cmState{
				state:       Leader,
				currentTerm: 1,
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
		return cm, vf
	}

	// Ответ AppendEntries, отправленного ДО постановки запроса (dispatchEpoch=1
	// < epoch=2): голос не засчитывается — учёт остаётся на self-голосе (1).
	cm, vf := newCM()
	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 1)
	votesBefore := vf.votes
	cm.mu.Unlock()
	if votesBefore != 1 {
		t.Fatalf("votes = %d, want 1 (ответ AE старой эпохи голосом не является)", votesBefore)
	}

	// Ответ AppendEntries, отправленного ПОСЛЕ постановки (dispatchEpoch=2 ==
	// epoch=2): голос засчитывается — добавляется голос соседа.
	cm.mu.Lock()
	cm.countVerifyVotesLocked(1, 2)
	votesAfter := vf.votes
	cm.mu.Unlock()
	if votesAfter != 2 {
		t.Fatalf("votes = %d, want 2 (ответ AE свежей эпохи голосует)", votesAfter)
	}
}

// TestVerifyRedispatch_AC4_NoSelfReproducingDispatch — критерий AC-4: нет
// самовоспроизводящейся рассылки. Проверяется числом вызовов AppendEntries
// на транспорте:
//   - при пустом pendingVerify завершение горутины репликации не порождает
//     новой рассылки;
//   - при одном verify — порождает не более одной дополнительной рассылки на
//     соседа (после завершения запроса очередь пуста, повторной перерассылки
//     нет);
//   - при завершении по _replicationSkip — не порождает вовсе.
func TestVerifyRedispatch_AC4_NoSelfReproducingDispatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	t.Run("пустой pendingVerify не порождает рассылку", func(t *testing.T) {
		mock := &mockTransportAE{blockCh: make(chan struct{}), replyTerm: 1}
		cm := newRedispatchReplicationCM(mock)
		cm.leaderState.pendingVerify = nil

		cm.leaderSendAEsToPeerIfIdle(1, 1)
		waitFor(t, "первый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
			return mock.callCount.Load() >= 1
		})
		close(mock.blockCh)
		waitFor(t, "горутина репликации завершилась", _inmemRPCTimeout, func() bool {
			return !cm.inflightAELoaded(1)
		})

		if got := mock.callCount.Load(); got != 1 {
			t.Fatalf("AppendEntries calls = %d, want 1 (без verify перерассылки нет)", got)
		}
	})

	t.Run("один verify — не более одной дополнительной рассылки", func(t *testing.T) {
		mock := &mockTransportAE{blockCh: make(chan struct{}), replyTerm: 1}
		cm := newRedispatchReplicationCM(mock)

		// Запуск летящего AE с dispatchEpoch = 0 (verifyEpoch на момент
		// запуска): ответ этого AE голосом запросу эпохи 1 не является.
		cm.leaderSendAEsToPeerIfIdle(1, 1)
		waitFor(t, "первый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
			return mock.callCount.Load() >= 1
		})
		// Постановка verify-запроса после запуска AE: эпоха инкрементируется
		// до 1 (как это делает цикл лидера), запрос в pendingVerify.
		vf := newPendingVerify(1)
		cm.mu.Lock()
		cm.leaderState.verifyEpoch = 1
		cm.leaderState.pendingVerify = []*verifyFuture{vf}
		cm.mu.Unlock()

		close(mock.blockCh)
		// Перерассылка отправляет второй AppendEntries, его ответ голосует и
		// завершает запрос; после этого очередь пуста и третьей рассылки нет.
		waitFor(t, "перерассылка и завершение запроса", _inmemRPCTimeout, func() bool {
			if mock.callCount.Load() < 2 {
				return false
			}
			cm.mu.Lock()
			flagFree := cm.leaderState.inflightAE[1] != nil && !cm.leaderState.inflightAE[1].Load()
			done := len(cm.leaderState.pendingVerify) == 0 && flagFree
			cm.mu.Unlock()
			return done
		})

		if got := mock.callCount.Load(); got != 2 {
			t.Fatalf("AppendEntries calls = %d, want 2 (ровно одна дополнительная рассылка)", got)
		}
	})

	t.Run("завершение по _replicationSkip не перерассылает", func(t *testing.T) {
		mock := &mockTransportAE{}
		cm := newRedispatchReplicationCM(mock)
		// Активная задержка повторов: одна транспортная ошибка и свежая
		// попытка — planReplication вернёт _replicationSkip.
		cm.leaderState.replFailures[1] = 1
		cm.leaderState.lastAttempt[1] = time.Now()
		vf := newPendingVerify(1)
		cm.leaderState.pendingVerify = []*verifyFuture{vf}

		cm.leaderSendAEsToPeerIfIdle(1, 1)
		waitFor(t, "горутина завершилась по _replicationSkip", _inmemRPCTimeout, func() bool {
			return !cm.inflightAELoaded(1)
		})

		if got := mock.callCount.Load(); got != 0 {
			t.Fatalf("AppendEntries calls = %d, want 0 (_replicationSkip не перерассылает)", got)
		}
		cm.mu.Lock()
		stillPending := len(cm.leaderState.pendingVerify) == 1 && vf.votes == 1
		cm.mu.Unlock()
		if !stillPending {
			t.Fatalf("verify-запрос должен остаться в очереди без голоса соседа (votes=%d)",
				vf.votes)
		}
	})
}

// TestVerifyRedispatch_AC5_FlagsResetOnBecomeFollower — критерий AC-5 (часть 1):
// после смены роли (becomeFollowerLocked) все inflightAE сбрасываются в false —
// флаг не залипает.
func TestVerifyRedispatch_AC5_FlagsResetOnBecomeFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	defer cm.Stop()

	// Имитация «зависшей» горутины репликации: флаг установлен вручную.
	cm.mu.Lock()
	cm.leaderState.inflightAE[1].Store(true)
	cm.becomeFollowerLocked(2)
	stuck := cm.leaderState.inflightAE[1].Load()
	cm.mu.Unlock()

	if stuck {
		t.Fatal("inflightAE[1] = true после becomeFollowerLocked, want false (флаг не залипает)")
	}
}

// TestVerifyRedispatch_AC5_FlagsResetOnLeaderLoopExit — критерий AC-5 (часть 2):
// после выхода из цикла лидера все inflightAE сбрасываются в false. Выход по
// остановке узла (shutdownCh) не проходит через becomeFollowerLocked — сброс
// выполняет defer цикла лидера.
func TestVerifyRedispatch_AC5_FlagsResetOnLeaderLoopExit(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	cm.goSpawn(cm.runLeaderLoop)

	// Имитация «зависшей» горутины репликации: флаг установлен вручную (как
	// при CAS-успехе до старта горутины).
	cm.mu.Lock()
	cm.leaderState.inflightAE[1].Store(true)
	cm.mu.Unlock()

	// Остановка узла: цикл лидера завершается, его defer сбрасывает флаги.
	// Stop() ждёт завершения всех горутин (wg.Wait), поэтому после возврата
	// цикл лидера гарантированно вышел.
	cm.Stop()

	cm.mu.Lock()
	stuck := cm.leaderState.inflightAE[1].Load()
	cm.mu.Unlock()
	if stuck {
		t.Fatal("inflightAE[1] = true после выхода из цикла лидера, want false (флаг не залипает)")
	}
}

// TestVerifyRedispatch_AC6_LeadershipTransfer — критерий AC-6: передача
// лидерства (handleLeadershipTransfer) успешна, без утечек горутин (leaktest),
// и после передачи на новом лидере ни один флаг inflightAE не залипает
// (инвариант «на одного соседа не более одной живой горутины репликации»).
func TestVerifyRedispatch_AC6_LeadershipTransfer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3
	if targetID == origLeaderID {
		targetID = (origLeaderID + 2) % 3
	}

	// Команды, которые нужно реплицировать целевому узлу.
	h.SubmitToServer(origLeaderID, 5)
	h.SubmitToServer(origLeaderID, 6)
	h.WaitForCommit(6, 3)

	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
	if err := future.Error(); err != nil {
		t.Fatalf("LeadershipTransfer failed: %v", err)
	}
	newLeaderID, _ := h.CheckSingleLeader()
	if newLeaderID != targetID {
		t.Errorf("new leader = %d, want %d", newLeaderID, targetID)
	}

	// На новом лидере все флаги inflightAE сброшены: ни один сосед не имеет
	// активной горутины репликации после завершения передачи.
	cm := h.cluster[newLeaderID]
	cm.mu.Lock()
	for p, f := range cm.leaderState.inflightAE {
		if f != nil && f.Load() {
			t.Errorf("inflightAE[%d] = true на новом лидере после передачи лидерства", p)
		}
	}
	cm.mu.Unlock()
}

// TestVerifyRedispatch_AECounter — контроль счётчика исходящих AppendEntries
// (aeSentPerPeer): каждая фактическая отправка AE соседу инкрементирует
// счётчик по соседу, и счётчик экспонируется в статистике CM.
func TestVerifyRedispatch_AECounter(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportAE{}
	cm := newRedispatchReplicationCM(mock)

	cm.leaderSendAEsToPeer(1, 1, 0, true)

	if got := cm.counters.aeSentPerPeer[1]; got != 1 {
		t.Fatalf("aeSentPerPeer[1] = %d, want 1", got)
	}
	rep := cm.countersReport()
	if !strings.Contains(rep, "AESent=1") {
		t.Fatalf("countersReport не содержит AESent=1: %q", rep)
	}
}

// Тесты троттлинга немедленной перерассылки AppendEntries (окно
// verifyRedispatchMinInterval) — критерии AC-2…AC-5 задачи. Механизм:
// после фактической перерассылки соседу ставится метка окна; повторная
// перерассылка в пределах интервала подавляется (счётчик
// verifyRedispatchSuppressed), после истечения — возобновляется (счётчик
// verifyRedispatched). Окно не переживает смену лидерства, а частота
// перерассылок ограничена по построению.

// throttleTransport — транспорт для тестов троттлинга: считает вызовы
// AppendEntries и придерживает КАЖДЫЙ вызов в полёте до получения токена из
// release (по одному токену на вызов). Даёт детерминированный контроль над
// моментом завершения каждого AppendEntries без фиксированных пауз.
type throttleTransport struct {
	*mockTransportAE
	calls   atomic.Int32
	release chan struct{}
}

func (t *throttleTransport) AppendEntries(_ ServerID, _ AppendEntriesArgs) (AppendEntriesReply, error) {
	t.calls.Add(1)
	<-t.release
	return AppendEntriesReply{Term: 1, Success: true}, nil
}

// newThrottleCM собирает литеральный CM в роли лидера с соседом 1 и заданным
// интервалом троттлинга verifyRedispatchMinInterval. По образцу
// newRedispatchReplicationCM; окно троттлинга инициализируется там же, где
// inflightAE (лидер работающей системы всегда имеет свежую карту окна).
func newThrottleCM(transport Transport, interval time.Duration) *ConsensusModule {
	cm := &ConsensusModule{
		id:         0,
		transport:  transport,
		shutdownCh: make(chan struct{}),
		leaderState: leaderState{
			nextIndex:              map[int]int{1: 1},
			matchIndex:             map[int]int{1: -1},
			inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
			lastAttempt:            map[int]time.Time{},
			replFailures:           map[int]int{},
			verifyEpoch:            0,
			nextVerifyRedispatchAt: make(map[int]time.Time),
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
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
		verifyRedispatchMinInterval: interval,
	}
	cm.leaderState.inflightAE[1].Store(false)
	return cm
}

// redispatchCounters возвращает счётчики перерассылок по соседу 1 под cm.mu.
func (cm *ConsensusModule) redispatchCounters() (redispatched, suppressed int64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.counters.verifyRedispatched[1], cm.counters.verifyRedispatchSuppressed[1]
}

// TestVerifyRedispatchThrottle_AC2_WindowSuppressesRedispatch — критерий AC-2
// (детерминированный): окно троттлинга блокирует повторную перерассылку.
// Сценарий: выполненная перерассылка ставит метку окна (интервал задан
// заведомо большим, поэтому окно не истекает); новый verify-запрос с эпохой
// позже отправки в пределах окна НЕ порождает отправку: счётчик
// verifyRedispatched не растёт, verifyRedispatchSuppressed растёт, вызовов
// AppendEntries больше нет. Детерминизм — без sleep: окно активно по
// построению (большой интервал), управление завершением AppendEntries — через
// транспорт.
func TestVerifyRedispatchThrottle_AC2_WindowSuppressesRedispatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tr := &throttleTransport{release: make(chan struct{}, 1)}
	cm := newThrottleCM(tr, time.Hour)

	// Первый AppendEntries в полёте с dispatchEpoch = 0.
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "первый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 1
	})

	// verify-запрос с эпохой позже отправки — условие перерассылки выполнено.
	vf1 := newPendingVerify(1)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 1
	cm.leaderState.pendingVerify = []*verifyFuture{vf1}
	cm.mu.Unlock()

	// Освобождаем AppendEntries: defer перерассылает, метка окна ставится кодом
	// (окно = now + большой интервал). Перерассланный AE придерживается в полёте.
	tr.release <- struct{}{}
	waitFor(t, "перерассылка в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 2
	})
	rd, _ := cm.redispatchCounters()
	if rd != 1 {
		t.Fatalf("verifyRedispatched[1] = %d, want 1 (первая перерассылка выполнена)", rd)
	}

	// Новый verify-запрос с эпохой позже отправки перерассланного AE — второе
	// условие перерассылки. Освобождаем перерассланный AE: его defer обязан
	// подавить повторную перерассылку окном.
	vf2 := newPendingVerify(2)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 2
	cm.leaderState.pendingVerify = []*verifyFuture{vf1, vf2}
	cm.mu.Unlock()
	tr.release <- struct{}{}
	waitFor(t, "горутина репликации завершилась", _inmemRPCTimeout, func() bool {
		return !cm.inflightAELoaded(1)
	})

	rd, sup := cm.redispatchCounters()
	if rd != 1 {
		t.Fatalf("verifyRedispatched[1] = %d, want 1 (окно подавило повторную перерассылку)", rd)
	}
	if sup != 1 {
		t.Fatalf("verifyRedispatchSuppressed[1] = %d, want 1 (подавление зафиксировано)", sup)
	}
	if got := tr.calls.Load(); got != 2 {
		t.Fatalf("AppendEntries calls = %d, want 2 (после подавления новых вызовов нет)", got)
	}
}

// TestVerifyRedispatchThrottle_AC3_PastWindowAllowsRedispatch — критерий AC-3
// (детерминированная часть): при метке окна в прошлом (окно свободно)
// перерассылка выполняется — счётчик verifyRedispatched растёт.
func TestVerifyRedispatchThrottle_AC3_PastWindowAllowsRedispatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tr := &throttleTransport{release: make(chan struct{}, 1)}
	cm := newThrottleCM(tr, time.Hour)

	// Окно свободно: метка в прошлом.
	cm.mu.Lock()
	cm.leaderState.nextVerifyRedispatchAt[1] = time.Now().Add(-time.Hour)
	cm.mu.Unlock()

	// Первый AppendEntries в полёте с dispatchEpoch = 0.
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "первый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 1
	})

	// verify-запрос с эпохой позже отправки.
	vf := newPendingVerify(1)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 1
	cm.leaderState.pendingVerify = []*verifyFuture{vf}
	cm.mu.Unlock()

	// Освобождаем AppendEntries: defer перерассылает (окно свободно).
	tr.release <- struct{}{}
	waitFor(t, "перерассылка в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 2
	})

	rd, sup := cm.redispatchCounters()
	if rd != 1 {
		t.Fatalf("verifyRedispatched[1] = %d, want 1 (перерассылка при свободном окне)", rd)
	}
	if sup != 0 {
		t.Fatalf("verifyRedispatchSuppressed[1] = %d, want 0", sup)
	}

	// Дренируем перерассланный AppendEntries: его defer не перерассылает
	// (verify эпохи 1 покрыт эпохой отправки 1).
	tr.release <- struct{}{}
	waitFor(t, "горутина перерассылки завершилась", _inmemRPCTimeout, func() bool {
		return !cm.inflightAELoaded(1)
	})
	if got := tr.calls.Load(); got != 2 {
		t.Fatalf("AppendEntries calls = %d, want 2 (ровно одна перерассылка)", got)
	}
}

// TestVerifyRedispatchThrottle_AC3_IntervalExpiryAllowsRedispatch — критерий
// AC-3 (тайминговая форма): при укороченном интервале окно подавляет
// перерассылку до истечения интервала и возобновляет её после. Проверка
// «не ранее истечения интервала» — по счётчику. Допуск на CI — серия
// -race -count=10 (не более 1 отказа из 10).
func TestVerifyRedispatchThrottle_AC3_IntervalExpiryAllowsRedispatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const interval = 30 * time.Millisecond
	tr := &throttleTransport{release: make(chan struct{}, 1)}
	cm := newThrottleCM(tr, interval)

	// Первый AppendEntries в полёте (dispatchEpoch = 0).
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "первый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 1
	})

	// verify эпохи 1; освобождаем первый AE → первая перерассылка, окно = now+interval.
	vf1 := newPendingVerify(1)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 1
	cm.leaderState.pendingVerify = []*verifyFuture{vf1}
	cm.mu.Unlock()
	tr.release <- struct{}{}
	waitFor(t, "первая перерассылка в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 2
	})
	rd, _ := cm.redispatchCounters()
	if rd != 1 {
		t.Fatalf("verifyRedispatched[1] = %d, want 1 (первая перерассылка)", rd)
	}
	// Момент установки окна фиксируем сразу после перерассылки (чуть позже
	// фактической метки — безопасно для проверки «до истечения»).
	windowStart := time.Now()

	// До истечения интервала: новый verify эпохи 2; освобождаем перерассланный
	// AE — окно должно подавить повторную перерассылку.
	vf2 := newPendingVerify(2)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 2
	cm.leaderState.pendingVerify = []*verifyFuture{vf1, vf2}
	cm.mu.Unlock()
	tr.release <- struct{}{}
	waitFor(t, "подавление перерассылки окном", _inmemRPCTimeout, func() bool {
		_, sup := cm.redispatchCounters()
		return sup >= 1 && !cm.inflightAELoaded(1)
	})
	rd, sup := cm.redispatchCounters()
	if rd != 1 {
		t.Fatalf("verifyRedispatched[1] = %d, want 1 (до истечения интервала перерассылки нет)", rd)
	}
	if sup < 1 {
		t.Fatalf("verifyRedispatchSuppressed[1] = %d, want >= 1 (подавление зафиксировано)", sup)
	}
	if got := tr.calls.Load(); got != 2 {
		t.Fatalf("AppendEntries calls = %d, want 2 (до истечения новых вызовов нет)", got)
	}

	// Ждём истечения интервала.
	waitFor(t, "истечение интервала троттлинга", _inmemRPCTimeout, func() bool {
		return time.Since(windowStart) >= interval
	})

	// После истечения: свежий AE с dispatchEpoch = 2 (покрывает vf2), затем
	// verify эпохи 3, поставленный после отправки — завершение AE обязано
	// перерасслать (окно свободно).
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "свежий AppendEntries в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 3
	})
	vf3 := newPendingVerify(3)
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 3
	cm.leaderState.pendingVerify = []*verifyFuture{vf3}
	cm.mu.Unlock()
	tr.release <- struct{}{}
	waitFor(t, "перерассылка после истечения интервала", _inmemRPCTimeout, func() bool {
		rd, _ := cm.redispatchCounters()
		return rd >= 2
	})
	rd, _ = cm.redispatchCounters()
	if rd != 2 {
		t.Fatalf("verifyRedispatched[1] = %d, want 2 (перерассылка после истечения)", rd)
	}

	// Дренируем последнюю перерассылку в полёте.
	tr.release <- struct{}{}
	waitFor(t, "горутина перерассылки завершилась", _inmemRPCTimeout, func() bool {
		return !cm.inflightAELoaded(1)
	})
}

// TestVerifyRedispatchThrottle_AC4_WindowDoesNotSurviveLeadership — критерий
// AC-4: окно троттлинга не переживает смену лидерства. После becomeFollowerLocked
// и нового вступления в лидерство карта окна свежая (нулевые метки), поэтому
// первая перерассылка нового лидерства немедленна.
func TestVerifyRedispatchThrottle_AC4_WindowDoesNotSurviveLeadership(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	defer cm.Stop()

	// Неистёкшее окно на соседе 1 (имитация активного троттлинга).
	cm.mu.Lock()
	cm.leaderState.nextVerifyRedispatchAt[1] = time.Now().Add(time.Hour)
	cm.mu.Unlock()

	// Смена роли: лидер → ведомый.
	cm.mu.Lock()
	cm.becomeFollowerLocked(cm.cmState.currentTerm + 1)
	cm.mu.Unlock()

	// Новое вступление в лидерство: создаётся свежая карта окна.
	cm.mu.Lock()
	cm.startLeaderLocked()
	cm.mu.Unlock()

	// Проверка чтением карты под cm.mu: метка будущего не пережила лидерство.
	// startLeaderLocked создаёт свежую пустую карту окна — сосед 1 не имеет
	// метки (окно свободно, первая перерассылка немедленна).
	cm.mu.Lock()
	n := len(cm.leaderState.nextVerifyRedispatchAt)
	mark, present := cm.leaderState.nextVerifyRedispatchAt[1]
	cm.mu.Unlock()
	if n != 0 || present || !mark.IsZero() {
		t.Fatalf("nextVerifyRedispatchAt после нового лидерства: len=%d, [1]=%v (present=%v), want свежую пустую карту (окно свободно)",
			n, mark, present)
	}
}

// TestVerifyRedispatchThrottle_AC5_FrequencyBound — критерий AC-5: граница
// частоты перерассылок по построению. При укороченном интервале за окно
// времени T (≥ 5 интервалов) число фактических перерассылок соседу не
// превышает ceil(T/интервал) + 1 независимо от числа запросов.
func TestVerifyRedispatchThrottle_AC5_FrequencyBound(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const interval = 10 * time.Millisecond
	const window = 5 * interval
	tr := &throttleTransport{release: make(chan struct{}, 1)}
	cm := newThrottleCM(tr, interval)

	// Стартовый AppendEntries в полёте (dispatchEpoch = 0).
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	waitFor(t, "стартовый AppendEntries в полёте", _inmemRPCTimeout, func() bool {
		return tr.calls.Load() >= 1
	})

	start := time.Now()
	var epoch uint64 = 1
	for time.Since(start) < window {
		// Новый verify с эпохой позже dispatchEpoch завершающегося AE.
		cm.mu.Lock()
		cm.leaderState.verifyEpoch = epoch
		cm.leaderState.pendingVerify = []*verifyFuture{newPendingVerify(epoch)}
		cm.mu.Unlock()
		// Освобождаем завершающийся AE: defer либо перерассылает (окно истекло),
		// либо подавляет (окно занято).
		tr.release <- struct{}{}
		waitFor(t, "перерассылка либо завершение горутины", _inmemRPCTimeout, func() bool {
			return tr.calls.Load() >= int32(epoch+1) || !cm.inflightAELoaded(1)
		})
		// Если горутина завершилась без перерассылки (подавление окном) —
		// запускаем свежий AppendEntries, чтобы продолжать нагружать окно.
		if !cm.inflightAELoaded(1) {
			cm.leaderSendAEsToPeerIfIdle(1, 1)
		}
		epoch++
	}
	elapsed := time.Since(start)

	// Дренируем оставшийся AppendEntries в полёте: очередь verify пуста,
	// поэтому завершение не породит новую перерассылку.
	cm.mu.Lock()
	cm.leaderState.pendingVerify = nil
	cm.mu.Unlock()
	if cm.inflightAELoaded(1) {
		tr.release <- struct{}{}
		waitFor(t, "дренаж горутины репликации", _inmemRPCTimeout, func() bool {
			return !cm.inflightAELoaded(1)
		})
	}

	rd, _ := cm.redispatchCounters()
	bound := int(math.Ceil(float64(elapsed)/float64(interval))) + 1
	if rd > int64(bound) {
		t.Fatalf("verifyRedispatched[1] = %d, want <= %d (ceil(T/интервал)+1, T=%v) — граница частоты нарушена",
			rd, bound, elapsed)
	}
}

// TestVerifyLeader_TwoNodes_FollowerDownNoQuorum — пороговый тест n=2:
// при отключённом follower'е подтверждение лидерства НЕ наступает
// (порог quorumSize(2)=2, есть только self-голос); после восстановления
// связи — наступает после первого ack'а follower'а.
//
// Старый код (порог len(peerIds)/2+1 = 1) подтверждал бы лидерство
// в одиночку — тест дискриминирует.
func TestVerifyLeader_TwoNodes_FollowerDownNoQuorum(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 2)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	follower := (lid + 1) % 2
	h.DisconnectPeer(follower)

	future := h.cluster[lid].VerifyLeader()

	// Контрольное окно с запасом на несколько heartbeat-интервалов:
	// подтверждение не должно наступить без ack'а follower'а.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed without follower ack (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}

	h.ReconnectPeer(follower)
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed after reconnect: %v", err)
	}
}

// TestVerifyLeader_FourNodes_OneFollowerNotEnough — порог 3/4:
// self + один подключённый follower (2/4) НЕ подтверждают лидерство,
// сколько бы повторных подтверждений пульса ни прислал тот же follower
// (дедупликация голосов по peer). После подключения второго follower'а
// (3/4 = кворум) подтверждение наступает.
//
// Старый код (порог len(peerIds)/2+1 = 2) подтверждал бы при self + 1
// follower — тест дискриминирует.
func TestVerifyLeader_FourNodes_OneFollowerNotEnough(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключить двух follower'ов; один остаётся подключённым.
	down1 := (lid + 1) % 4
	down2 := (lid + 2) % 4
	h.DisconnectPeer(down1)
	h.DisconnectPeer(down2)

	future := h.cluster[lid].VerifyLeader()

	// Негативное окно с запасом на несколько heartbeat-тиков:
	// повторные ack'и одного follower'а не накапливаются.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed with 2/4 votes (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}

	// Подключить второго follower'а — 3/4 = кворум.
	h.ReconnectPeer(down1)
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed with 3/4 alive: %v", err)
	}
}

// TestVerifyLeader_FourNodes_OneDownStillVerifies — позитивный контроль:
// при одном отключённом follower (живы 3/4) подтверждение наступает.
// Окно ожидания включает несколько heartbeat-интервалов
// (подтверждение может задержаться до следующего тика heartbeat из-за
// занятого inflightAE).
func TestVerifyLeader_FourNodes_OneDownStillVerifies(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	down := (lid + 1) % 4
	h.DisconnectPeer(down)

	future := h.cluster[lid].VerifyLeader()
	if err := waitFuture(t, future, 3*time.Second); err != nil {
		t.Fatalf("VerifyLeader failed with 3/4 alive: %v", err)
	}
}

// TestVerifyLeader_NonvoterAckDoesNotVote — негатив по неголосующему:
// после DemoteVoter живы только лидер и неголосующий с непрерывными
// ack'ами — подтверждение НЕ наступает: ack неголосующего голосом не
// является, порог 2 голосов (3 voters) собирается только из голосующих.
//
// Старый код голосовал на каждый success-ответ без фильтра Suffrage —
// self + неголосующий давали бы 2 голоса и ложное подтверждение.
func TestVerifyLeader_NonvoterAckDoesNotVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Демотировать одного follower'а в неголосующего и дождаться фиксации
	// конфигурации (дедлайн-поллинг GetConfiguration).
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := waitFuture(t, f, 3*time.Second); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	demoted := false
	for time.Now().Before(deadline) && !demoted {
		cfg := h.cluster[lid].GetConfiguration().Configuration()
		for _, s := range cfg.ConfigServers {
			if s.ID == ServerID(demoteID) {
				demoted = s.Suffrage == Nonvoter
			}
		}
		if !demoted {
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !demoted {
		t.Fatalf("server %d not demoted to nonvoter in time", demoteID)
	}

	// Отключить остальных голосующих follower'ов: живы лидер + неголосующий.
	for i := 0; i < h.n; i++ {
		if i == lid || i == demoteID {
			continue
		}
		h.DisconnectPeer(i)
	}

	future := h.cluster[lid].VerifyLeader()

	// Негативное окно с запасом: ack'и неголосующего не голосуют.
	select {
	case err := <-future.ErrorCh():
		t.Fatalf("VerifyLeader confirmed by nonvoter acks (err=%v)", err)
	case <-time.After(HeartbeatTimeoutMs * 5 * time.Millisecond):
	}
}

// TestVerifyFuture_VoteDeduplication — белый короб на verifyFuture.vote:
// повторные голоса одного peerID не накапливаются.
// Прямая проверка счётчика: два голоса одного узла дают votes=1,
// голос другого узла — votes=2.
func TestVerifyFuture_VoteDeduplication(t *testing.T) {
	vf := &verifyFuture{
		voted:      make(map[int]struct{}),
		quorumSize: 2,
	}

	vf.vote(1, true)
	vf.vote(1, true) // повторное подтверждение пульса того же узла — no-op
	if vf.votes != 1 {
		t.Fatalf("votes = %d, want 1 (duplicate vote must be ignored)", vf.votes)
	}

	vf.vote(2, true)
	if vf.votes != 2 {
		t.Fatalf("votes = %d, want 2", vf.votes)
	}
}

// TestVerifyLeader_EpochFilter: голос учитывается только если AE отправлен
// не раньше постановки запроса (vf.epoch <= dispatchEpoch).
//
// Предмет обоих подслучаев — «AE, отправленный до постановки, не голосует» —
// требует vf.epoch > dispatchEpoch, то есть ровно условия перерассылки:
// завершившаяся горутина репликации видит в pendingVerify запрос с эпохой позже
// отправки и немедленно отправляет свежий AE с новой эпохой. Поэтому подслучай
// «до постановки» разбит на две фазы, разделённые гейтом транспорта: фаза 1
// фиксирует, что ответ AE старой эпохи голосом не является (утверждение
// детерминировано — перерассылка придержана гейтом); фаза 2 освобождает гейт и
// проверяет, что перерассланный AE с новой эпохой голос засчитывает. Без гейта
// утверждение фазы 1 было бы гоночным, а несогласованная фикстура (verifyEpoch,
// не покрывающая эпоху запроса) дала бы бесконечную эстафету горутин.
func TestVerifyLeader_EpochFilter(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Терм отправки AppendEntries в вызовах теста — константа; сверяется с
	// currentTerm приспособления ниже (терм отправки должен равняться currentTerm).
	const sendTerm = 1

	newCM := func(transport Transport, epoch uint64) (*ConsensusModule, *verifyFuture) {
		cm := &ConsensusModule{
			id: 0,
			// Терм ответа совпадает с термом лидера: отправка выполняется
			// в текущем терме, иначе успешный ответ к состоянию репликации
			// не применяется и фильтр по раунду верификации не проверялся бы.
			transport: transport,
			commitCh:  make(chan int, 1),
			leaderState: leaderState{
				nextIndex:              map[int]int{1: 0},
				matchIndex:             map[int]int{1: -1},
				inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
				nextVerifyRedispatchAt: map[int]time.Time{},
			},
			cmState: cmState{
				state:        Leader,
				log:          []LogEntry{{Index: 0, Term: 1}},
				lastLogIndex: 0,
				currentTerm:  1,
				commitIndex:  -1,
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
		cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
		cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
		vf := &verifyFuture{
			voted:      make(map[int]struct{}),
			quorumSize: 2,
			epoch:      epoch,
		}
		// Согласование фикстуры с инвариантом «эпоха ожидающего запроса не
		// больше verifyEpoch»: перерассылка отправляет AE с эпохой
		// newDispatchEpoch = verifyEpoch, и при verifyEpoch = 0 ответ такого AE
		// голосом запросу с эпохой > 0 не был бы, а эстафета горутин — бесконечной.
		cm.mu.Lock()
		cm.leaderState.verifyEpoch = epoch
		cm.leaderState.pendingVerify = []*verifyFuture{vf}
		// Защитная проверка согласованности приспособления: сочетание достижимо
		// в работающей системе, только если терм отправки равен currentTerm и
		// эпоха ожидающего запроса не превосходит verifyEpoch.
		if cm.cmState.currentTerm != sendTerm {
			t.Fatalf("sendTerm = %d, currentTerm = %d: приспособление несогласованно (терм отправки != currentTerm)",
				sendTerm, cm.cmState.currentTerm)
		}
		if vf.epoch > cm.leaderState.verifyEpoch {
			t.Fatalf("verifyEpoch = %d < vf.epoch = %d: приспособление несогласованно (эпоха ожидающего запроса больше verifyEpoch)",
				cm.leaderState.verifyEpoch, vf.epoch)
		}
		cm.mu.Unlock()
		return cm, vf
	}

	t.Run("AE отправленный до постановки не голосует", func(t *testing.T) {
		mock := &mockTransportAE{replyTerm: 1, gateBlockAfter: 2, gateCh: make(chan struct{})}
		cm, vf := newCM(mock, 2)

		// Фаза 1: первый синхронный вызов с dispatchEpoch = 1 < epoch = 2.
		// Ответ AE, отправленного до постановки запроса, голосом не является;
		// гейт придерживает второй вызов (перерассылку), поэтому утверждение
		// детерминировано.
		cm.leaderSendAEsToPeer(1, sendTerm, 1, true)
		if vf.votes != 0 {
			t.Fatalf("votes = %d, want 0 (AE dispatched before request must not vote)", vf.votes)
		}

		// Фаза 2: освобождение гейта. Перерассланный AE с новой эпохой
		// (newDispatchEpoch = verifyEpoch = 2) голос засчитывает; кворум 2 не
		// достигнут, запрос остаётся в очереди, третьей отправки нет (эстафета
		// конечна: 2 <= 2).
		close(mock.gateCh)
		waitFor(t, "перерассылка и голос перерассланного AE", _inmemRPCTimeout, func() bool {
			return mock.callCount.Load() >= 2 && !cm.inflightAELoaded(1)
		})
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (redispatched AE must vote)", vf.votes)
		}
		cm.mu.Lock()
		remaining := cm.leaderState.pendingVerify
		cm.mu.Unlock()
		if len(remaining) != 1 || remaining[0] != vf {
			t.Fatalf("pendingVerify = %v, want [vf] still queued", remaining)
		}
		if got := mock.callCount.Load(); got != 2 {
			t.Fatalf("AppendEntries calls = %d, want 2 (no third dispatch)", got)
		}
	})

	t.Run("AE отправленный после постановки голосует", func(t *testing.T) {
		mock := &mockTransportAE{replyTerm: 1}
		cm, vf := newCM(mock, 2)
		cm.leaderSendAEsToPeer(1, sendTerm, 2, true)
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (AE dispatched after request must vote)", vf.votes)
		}
	})
}

// TestVerifyLeader_MultiplePendingDifferentEpochs — фильтр по раунду
// верификации применяется к каждому ожидающему запросу отдельно.
//
// dispatchEpoch — раунд, снятый в момент отправки AppendEntries. Голос
// засчитывается только тем запросам верификации, которые были поставлены не
// позже отправки (epoch запроса не больше dispatchEpoch). Набравшие кворум
// запросы покидают очередь, остальные сохраняются в исходном порядке.
//
// В очереди есть запрос с эпохой 7 при dispatchEpoch = 5 (поставленный позже
// отправки) — ровно условие перерассылки: завершившаяся горутина отправляет
// свежий AE с новой эпохой. Поэтому проверка разбита на две фазы, разделённые
// гейтом транспорта: фаза 1 фиксирует голоса от AE старой эпохи (детерминировано
// — перерассылка придержана гейтом); фаза 2 освобождает гейт и проверяет
// сходимость после перерассланного AE. Согласованная фикстура
// (verifyEpoch = максимум эпох) делает эстафету конечной: 7 <= 7.
//
// Запрос vfSame (эпоха 5, кворум 3) недобирает голос до кворума: в фикстуре
// лишь два источника голоса — лидер (self-голос учтён при создании) и сосед 1
// (единственный получатель AppendEntries). Повторный голос соседа от
// перерассланного AE дедуплицируется (verifyFuture.vote), поэтому vfSame
// остаётся в очереди с 2 голосами из 3 — это и есть честная сходимость фазы 2.
func TestVerifyLeader_MultiplePendingDifferentEpochs(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Терм отправки AppendEntries в вызове теста — константа; сверяется с
	// currentTerm приспособления ниже (терм отправки должен равняться currentTerm).
	const sendTerm = 1

	mock := &mockTransportAE{replyTerm: 1, gateBlockAfter: 2, gateCh: make(chan struct{})}
	cm := &ConsensusModule{
		id:         0,
		transport:  mock,
		commitCh:   make(chan int, 1),
		shutdownCh: make(chan struct{}),
		leaderState: leaderState{
			nextIndex:              map[int]int{1: 0},
			matchIndex:             map[int]int{1: -1},
			inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
			nextVerifyRedispatchAt: map[int]time.Time{},
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      0,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       -1,
			termIndexMap:      map[int]int{1: 0},
			log:               []LogEntry{{Index: 0, Term: 1}},
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
					{ID: 2, Suffrage: Voter},
				}},
				latest: Configuration{ConfigServers: []ConfigServer{
					{ID: 0, Suffrage: Voter},
					{ID: 1, Suffrage: Voter},
					{ID: 2, Suffrage: Voter},
				}},
			},
		},
	}
	cm.leaderState.inflightAE[1].Store(false)
	cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
	cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1, 2}, cm.lookupTermLocked)

	// newVerifyFuture — запрос верификации с уже учтённым голосом лидера.
	newVerifyFuture := func(epoch uint64, quorumSize int) *verifyFuture {
		vf := &verifyFuture{
			voted:      map[int]struct{}{cm.id: {}},
			votes:      1,
			quorumSize: quorumSize,
			epoch:      epoch,
		}
		vf.init(cm.shutdownCh)
		return vf
	}
	vfOld := newVerifyFuture(3, 2)
	vfSame := newVerifyFuture(5, 3)
	vfFuture := newVerifyFuture(7, 2)
	// Согласование фикстуры с инвариантом «эпоха ожидающего запроса не больше
	// verifyEpoch»: verifyEpoch = максимум эпох очереди. Перерассылка отправляет
	// AE с эпохой newDispatchEpoch = verifyEpoch = 7, покрывающей все ожидающие
	// запросы, поэтому эстафета горутин конечна, а голоса засчитываются.
	cm.mu.Lock()
	cm.leaderState.verifyEpoch = 7
	cm.leaderState.pendingVerify = []*verifyFuture{vfOld, vfSame, vfFuture}
	// Защитная проверка согласованности приспособления: сочетание достижимо
	// в работающей системе, только если терм отправки равен currentTerm и эпоха
	// каждого ожидающего запроса не превосходит verifyEpoch.
	if cm.cmState.currentTerm != sendTerm {
		t.Fatalf("sendTerm = %d, currentTerm = %d: приспособление несогласованно (терм отправки != currentTerm)",
			sendTerm, cm.cmState.currentTerm)
	}
	for _, v := range cm.leaderState.pendingVerify {
		if v.epoch > cm.leaderState.verifyEpoch {
			t.Fatalf("verifyEpoch = %d < vf.epoch = %d: приспособление несогласованно (эпоха ожидающего запроса больше verifyEpoch)",
				cm.leaderState.verifyEpoch, v.epoch)
		}
	}
	cm.mu.Unlock()

	// Фаза 1: первый синхронный вызов с dispatchEpoch = 5. Голос засчитывается
	// запросам с эпохой не больше 5 (vfOld, vfSame), запрос с эпохой 7
	// (поставленный позже отправки) — нет. Гейт придерживает второй вызов
	// (перерассылку), поэтому утверждения детерминированы.
	cm.leaderSendAEsToPeer(1, sendTerm, 5, true)

	if err := vfOld.Error(); err != nil {
		t.Fatalf("vfOld.Error() = %v, want nil (quorum reached, epoch 3 <= 5)", err)
	}
	if vfSame.votes != 2 {
		t.Fatalf("vfSame.votes = %d, want 2 (epoch 5 <= 5 votes, quorum 3 not reached)", vfSame.votes)
	}
	if vfFuture.votes != 1 {
		t.Fatalf("vfFuture.votes = %d, want 1 (epoch 7 > 5 must not vote)", vfFuture.votes)
	}
	// Очередь читается под cm.mu: перерассылка после завершения AppendEntries
	// (есть запрос с эпохой 7 > 5) запускает фоновую горутину репликации, которая
	// меняет pendingVerify под блокировкой.
	cm.mu.Lock()
	remaining := cm.leaderState.pendingVerify
	cm.mu.Unlock()
	if len(remaining) != 2 || remaining[0] != vfSame || remaining[1] != vfFuture {
		t.Fatalf("pendingVerify = %v, want [vfSame vfFuture] in the original order", remaining)
	}

	// Фаза 2: освобождение гейта. Перерассланный AE с новой эпохой
	// (newDispatchEpoch = verifyEpoch = 7) голосует оставшимся запросам (эпохи
	// 5 и 7): vfFuture добирает голос и завершается; vfSame остаётся в очереди
	// (кворум 3, повторный голос соседа дедуплицирован). Третьей отправки нет
	// (5 <= 7 — повторная перерассылка не запускается).
	close(mock.gateCh)
	waitFor(t, "перерассылка и сходимость очереди", _inmemRPCTimeout, func() bool {
		if mock.callCount.Load() < 2 {
			return false
		}
		if cm.inflightAELoaded(1) {
			return false
		}
		cm.mu.Lock()
		stable := len(cm.leaderState.pendingVerify) == 1 && cm.leaderState.pendingVerify[0] == vfSame
		cm.mu.Unlock()
		return stable
	})
	// vfSame не завершён (остался в очереди) — Error() блокировался бы, поэтому
	// незавершённость проверяется неблокирующим чтением канала завершения.
	select {
	case <-vfSame.ErrorCh():
		t.Fatal("vfSame resolved, want non-resolved (quorum 3 not reached, remains queued)")
	default:
	}
	if vfSame.votes != 2 {
		t.Fatalf("vfSame.votes = %d, want 2 (deduplicated peer 1 vote, quorum 3 not reached)", vfSame.votes)
	}
	if err := vfFuture.Error(); err != nil {
		t.Fatalf("vfFuture.Error() = %v, want nil (quorum reached, epoch 7 <= 7)", err)
	}
	cm.mu.Lock()
	remaining = cm.leaderState.pendingVerify
	cm.mu.Unlock()
	if len(remaining) != 1 || remaining[0] != vfSame {
		t.Fatalf("pendingVerify = %v, want [vfSame] after redispatch", remaining)
	}
	if got := mock.callCount.Load(); got != 2 {
		t.Fatalf("AppendEntries calls = %d, want 2 (no third dispatch)", got)
	}
}

// TestVerifyLeader_NoVoteOnFailureAndSnapshotPath фиксирует существующее
// консервативное поведение: подтверждением лидерства считается только
// успешный ответ AppendEntries. Отказ ведомого (Success: false) и путь
// отправки снимка голосом не являются, запрос остаётся в очереди.
//
// Вопрос о том, следует ли считать такие контакты подтверждением лидерства,
// за пределами данной проверки и производственным кодом не решается.
func TestVerifyLeader_NoVoteOnFailureAndSnapshotPath(t *testing.T) {
	// newCM собирает литерального лидера с одним ожидающим запросом
	// верификации: голос лидера уже учтён, для кворума нужен ещё один.
	newCM := func(transport Transport, snapshotStore SnapshotStore, lastSnapshotIndex, nextIndex int, log []LogEntry) (*ConsensusModule, *verifyFuture) {
		cm := &ConsensusModule{
			id:         0,
			transport:  transport,
			commitCh:   make(chan int, 1),
			shutdownCh: make(chan struct{}),
			leaderState: leaderState{
				nextIndex:              map[int]int{1: nextIndex},
				matchIndex:             map[int]int{1: -1},
				inflightAE:             map[int]*atomic.Bool{1: new(atomic.Bool)},
				nextVerifyRedispatchAt: map[int]time.Time{},
			},
			cmState: cmState{
				state:             Leader,
				currentTerm:       1,
				lastLogIndex:      5,
				lastLogTerm:       1,
				lastSnapshotIndex: lastSnapshotIndex,
				lastSnapshotTerm:  1,
				commitIndex:       -1,
				termIndexMap:      map[int]int{1: 5},
				log:               log,
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
			snapshotStore: snapshotStore,
		}
		cm.leaderState.inflightAE[1].Store(false)
		cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, cm.commitCh)
		cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
		vf := &verifyFuture{
			voted:      map[int]struct{}{cm.id: {}},
			votes:      1,
			quorumSize: 2,
			epoch:      0,
		}
		vf.init(cm.shutdownCh)
		cm.leaderState.pendingVerify = []*verifyFuture{vf}
		return cm, vf
	}

	t.Run("логический отказ ведомого голосом не является", func(t *testing.T) {
		mock := &mockTransportAE{failReply: true, failConflictIndex: 8, failConflictTerm: -1, replyTerm: 1}
		cm, vf := newCM(mock, nil, -1, 5, []LogEntry{{Index: 4, Term: 1}, {Index: 5, Term: 1}})

		cm.leaderSendAEsToPeer(1, 1, 0, true)

		if got := mock.callCount.Load(); got != 1 {
			t.Fatalf("AppendEntries calls = %d, want 1", got)
		}
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (rejection must not vote)", vf.votes)
		}
		if len(cm.leaderState.pendingVerify) != 1 || cm.leaderState.pendingVerify[0] != vf {
			t.Fatalf("pendingVerify = %v, want the request still queued", cm.leaderState.pendingVerify)
		}
		select {
		case err := <-vf.ErrorCh():
			t.Fatalf("verify request completed with %v, want still pending", err)
		default:
		}
	})

	t.Run("путь снимка голосом не является", func(t *testing.T) {
		// installReplyTerm = 0 не совпадает с термом запроса, поэтому ответ
		// на снимок состояние репликации не изменяет: проверяется только
		// отсутствие голоса.
		mock := &mockTransportAE{replyTerm: 1, installReplyTermSet: true, installReplyTerm: 0}
		store := NewInmemSnapshotStore()
		sink, err := store.Create(3, 1, Configuration{}, 0)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if _, err := sink.Write([]byte("snapshot data")); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if err := sink.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		// prev = nextIndex-1 = 4 попадает в дыру между границей снимка (3)
		// и первой записью журнала (5) — отправляется снимок.
		cm, vf := newCM(mock, store, 3, 5, []LogEntry{{Index: 5, Term: 1}})

		cm.leaderSendAEsToPeer(1, 1, 0, true)

		if got := mock.installCallCount.Load(); got != 1 {
			t.Fatalf("InstallSnapshot calls = %d, want 1", got)
		}
		if got := mock.callCount.Load(); got != 0 {
			t.Fatalf("AppendEntries calls = %d, want 0 (snapshot path)", got)
		}
		if vf.votes != 1 {
			t.Fatalf("votes = %d, want 1 (snapshot path must not vote)", vf.votes)
		}
		if len(cm.leaderState.pendingVerify) != 1 || cm.leaderState.pendingVerify[0] != vf {
			t.Fatalf("pendingVerify = %v, want the request still queued", cm.leaderState.pendingVerify)
		}
	})
}
