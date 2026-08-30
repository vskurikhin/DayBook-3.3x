package raft

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

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
