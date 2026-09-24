package raft

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vskurikhin/raft/pkg/raft/store"
	"github.com/vskurikhin/raft/pkg/raft/transp"
)

const _submitTimeout = 5 * time.Second

// Test-only константы бюджетов (единственный владелец).
// Разделение Protocol timeout / Test deadline / Poll interval.

const (
	// _maxElectionTimeout — максимальный election timeout:
	// 2*ReelectionTimeoutMs = 762ms.
	_maxElectionTimeout = 2 * ReelectionTimeoutMs * time.Millisecond

	// _preVoteRound — worst-case раунда Pre-Vote (≈ _maxElectionTimeout).
	_preVoteRound = _maxElectionTimeout

	// _inmemRPCTimeout — тайм-аут одного RPC внутрипроцессного
	// транспорта; ссылается на transp.InmemTransportTimeout, рассинхрон
	// с транспортом исключён компилятором.
	_inmemRPCTimeout = transp.InmemTransportTimeout

	// _commitBudgetSteady — бюджет ожидания фиксации при устоявшемся лидере:
	// 4*(_applyBatchInterval + _inmemRPCTimeout) = 4*(50ms+500ms) = 2.2s.
	_commitBudgetSteady = 4 * (_applyBatchInterval + _inmemRPCTimeout)

	// _failoverBudgetMargin — запас бюджета after-failover для поглощения
	// межфазных задержек (worst-case 2*762 + 762 + 50 + 200 ≈ 2.5s).
	_failoverBudgetMargin = 200 * time.Millisecond

	// _commitBudgetAfterFailover — бюджет ожидания фиксации для сценариев
	// с возможными перевыборами (disconnect/restart/leadership transfer):
	// 2*_maxElectionTimeout + _preVoteRound + _applyBatchInterval + запас ≈ 2.5–3s.
	_commitBudgetAfterFailover = 2*_maxElectionTimeout + _preVoteRound + _applyBatchInterval + _failoverBudgetMargin

	// _leaderElectionBudget — бюджет ожидания выборов лидера
	// (§9 architecture.md): 2*_maxElectionTimeout + _preVoteRound ≈ 2.3s.
	// Применяется CheckSingleLeader вместо прежнего необоснованного
	// окна 8×150ms = 1.2s.
	_leaderElectionBudget = 2*_maxElectionTimeout + _preVoteRound

	// _singleLeaderBudget — бюджет схождения кластера к единственному лидеру.
	// Два независимых worst-case: выборы (_leaderElectionBudget) и задержка
	// step-down призрачного лидера прежнего терма, ограниченная потолком
	// задержки повторов репликации (_maxReplicationBackoff):
	// 2286ms + 1000ms = 3286ms.
	_singleLeaderBudget = _leaderElectionBudget + _maxReplicationBackoff

	// _snapshotConvergenceBudget — бюджет схождения снимка к вершине журнала.
	// Вывод при интервале снимков, заданном тестом (например, 50 мс):
	// после возврата последнего Apply машина состояний догоняет журнал
	// не позднее чем за _applyBatchInterval (50 мс), а ближайший тик цикла
	// снимков создаёт снимок с индексом машины состояний; худший случай —
	// _applyBatchInterval + 2*snapshotInterval = 150 мс при интервале 50 мс.
	// Значение берётся равным _commitBudgetSteady = 2.2 с: тот же способ
	// вывода из констант, запас более чем 14-кратный, отдельная константа
	// с литералом не вводится.
	_snapshotConvergenceBudget = _commitBudgetSteady

	// _pollInterval — интервал опроса condition-wait примитивов
	// (Poll interval модели §9 architecture.md: 5–10ms). Не зависит
	// от бюджета ожидания.
	_pollInterval = 10 * time.Millisecond
)

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
}

// Harness — тестовый стенд для unit-тестирования Raft.
// Использует InmemTransport вместо TCP, не открывает порты.
//
// Владение состоянием: поля commits, connected и
// alive защищены h.mu — и читатели, и писатели работают короткими
// критическими секциями. Инвариант границ: h.mu НИКОГДА не
// удерживается через блокирующие операции жизненного цикла — cm.Stop(),
// transport.Close(), close(commitChans[i]), join коллекторов, а также
// через вызовы cm.Report() (значения connected/alive снимаются под
// h.mu, опрос CM выполняется вне её).
//
// Конвенция wait/assert (§8 architecture.md,): предикаты wait
// (committedOn, cond и diag в waitFor) исполняются с УЖЕ удерживаемой
// h.mu; assert-функции CheckCommitted/CheckCommittedN/CheckNotCommitted
// берут h.mu сами — их вызов под удерживаемой h.mu запрещён
// (h.mu нереентерабельна).
type Harness struct {
	mu sync.Mutex

	// cluster — список ConsensusModule, участвующих в кластере.
	cluster []*ConsensusModule

	// transports — in-memory транспорты для каждого узла.
	transports []*transp.InmemTransport

	storage []*store.MapStorage

	// commitChans содержит по одному каналу фиксации для каждого сервера.
	commitChans []chan CommitEntry

	// commits с индексом i содержит последовательность записей,
	// зафиксированных сервером i на текущий момент.
	commits [][]CommitEntry

	// connected содержит по одному логическому значению для каждого сервера
	// кластера и определяет, подключён ли данный сервер в настоящий момент
	// к соседям.
	connected []bool

	// alive содержит по одному логическому значению для каждого сервера
	// кластера и определяет, работает ли данный сервер в настоящий момент.
	alive []bool

	// shutdownOnce защищает Shutdown() от повторного вызова.
	shutdownOnce sync.Once

	// wg — регистрация коллекторов collectCommits; Shutdown() закрывает
	// каналы и присоединяет коллекторы через wg.Wait() (закрытие
	// каналов — только после гарантированного завершения producers).
	wg sync.WaitGroup

	// collectorDone содержит по одному каналу завершения коллектора
	// текущей incarnation каждого узла. Используется quiesce-барьером
	// CrashPeer: коллектор старой incarnation
	// присоединяется до очистки h.commits[id].
	collectorDone []chan struct{}

	n int
	t *testing.T
}

// HarnessOption — опция конфигурации узлов Harness. Применяется к
// каждому созданному ConsensusModule ДО close(ready), т.е. до старта
// фоновых горутин (конфигурация — до старта, без post-start мутаций).
// Поддерживает кластерное применение.
type HarnessOption func(cm *ConsensusModule)

// DisablePreVote — опция Harness: отключение Pre-Vote на узле
// (preVoteDisabled=true до старта горутин). При передаче в
// NewHarnessWithOptions применяется ко всем узлам кластера.
func DisablePreVote() HarnessOption {
	return func(cm *ConsensusModule) { cm.preVoteDisabled = true }
}

// NewHarness создаёт новую тестовую запряжку,
// инициализированную n серверами, соединёнными друг с другом
// через InmemTransport.
func NewHarness(t *testing.T, n int) *Harness {
	return NewHarnessWithOptions(t, n)
}

// NewHarnessWithOptions создаёт Harness с опциями opts, применяемыми
// к каждому узлу до close(ready) (до старта фоновых горутин).
func NewHarnessWithOptions(t *testing.T, n int, opts ...HarnessOption) *Harness {
	cluster := make([]*ConsensusModule, n)
	transports := make([]*transp.InmemTransport, n)
	connected := make([]bool, n)
	alive := make([]bool, n)
	commitChans := make([]chan CommitEntry, n)
	commits := make([][]CommitEntry, n)
	ready := make(chan any)
	storage := make([]*store.MapStorage, n)

	// Создаём транспорты и соединяем их все друг с другом.
	for i := 0; i < n; i++ {
		addr := ServerAddress("raft-" + itoa(i))
		transports[i] = transp.NewInmemTransport(addr)
	}

	// Соединяем все транспорты друг с другом.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				transports[i].Connect(ServerID(j), transports[j])
			}
		}
	}

	// Создаём ConsensusModule для каждого узла.
	for i := 0; i < n; i++ {
		peerIds := make([]int, 0)
		for p := 0; p < n; p++ {
			if p != i {
				peerIds = append(peerIds, p)
			}
		}

		storage[i] = store.NewMapStorage()
		commitChans[i] = make(chan CommitEntry)
		cluster[i] = NewConsensusModule(
			i, peerIds, transports[i],
			storage[i], NewCommitChannelFSM(commitChans[i]), ready,
		)
		// Опции применяются до close(ready) — до старта фоновых
		// горутин узла; никакой post-start мутации.
		for _, opt := range opts {
			opt(cluster[i])
		}
		alive[i] = true
		connected[i] = true
	}
	close(ready)

	h := &Harness{
		cluster:       cluster,
		transports:    transports,
		storage:       storage,
		commitChans:   commitChans,
		commits:       commits,
		connected:     connected,
		alive:         alive,
		collectorDone: make([]chan struct{}, n),
		n:             n,
		t:             t,
	}
	for i := 0; i < n; i++ {
		h.collectorDone[i] = h.startCollector(i, commitChans[i])
	}
	return h
}

// Shutdown останавливает все серверы в тестовом окружении.
// Идемпотентен — повторные вызовы безопасны.
//
// Последовательность: cm.Stop() (join) →
// transports[i].Close() → close(commitChans[i]) → join коллекторов.
// Контракт порядка: transport.Close() выполняется до или одновременно
// с join горутин CM — фактически сразу после Stop(), поэтому
// отправители RPC не могут блокироваться навсегда.
// Инвариант границ критических секций h.mu: h.mu удерживается
// только короткими секциями над полями Harness и НИКОГДА не удерживается
// через cm.Stop(), transport.Close(), close(commitChans[i]) и join
// коллекторов (цикл h.mu → join runFSM → Apply в канал → collectCommits
// ждёт h.mu = вечный deadlock).
func (h *Harness) Shutdown() {
	h.shutdownOnce.Do(func() {
		for i := 0; i < h.n; i++ {
			// [h.mu: чтение и сброс alive[i]] release — короткая секция;
			// Stop/Close/close выполняются вне h.mu.
			h.mu.Lock()
			wasAlive := h.alive[i]
			h.alive[i] = false
			h.mu.Unlock()

			if wasAlive {
				h.cluster[i].Stop()
				h.transports[i].Close()
			}
			close(h.commitChans[i])
		}
		// Join коллекторов — завершающий шаг последовательности
		// после возврата Shutdown ни одна горутина
		// не пишет в h.commits, поэтому чтение commits в assert'ах
		// после Shutdown детерминировано.
		h.wg.Wait()
	})
}

// DisconnectPeer отключает сервер от всех остальных серверов кластера
// через разрыв логического соединения в InmemTransport.
//
// Мутация connected — короткой секцией h.mu: операции над
// транспортами выполняются вне блокировки.
func (h *Harness) DisconnectPeer(id int) {
	tlog("Disconnect %d", id)
	h.transports[id].DisconnectAll()
	for j := 0; j < h.n; j++ {
		if j != id {
			h.transports[j].Disconnect(ServerID(id))
		}
	}
	h.mu.Lock()
	h.connected[id] = false
	h.mu.Unlock()
}

// ReconnectPeer повторно подключает сервер к остальным живым серверам
// кластера, логически подключённым в этот момент. Сервер, отключённый
// отдельным вызовом DisconnectPeer, остаётся изолированным: восстановление
// связи с ним нарушило бы учёт connected и делало бы «отключённый» узел
// участником кворума.
//
// Значения alive и connected снимаются под h.mu до работы с транспортами;
// мутация connected — отдельной короткой секцией.
func (h *Harness) ReconnectPeer(id int) {
	tlog("Reconnect %d", id)
	alive := h.aliveSnapshot()
	connected := h.connectedSnapshot()
	for j := 0; j < h.n; j++ {
		if j != id && alive[j] && connected[j] {
			h.transports[id].Connect(ServerID(j), h.transports[j])
			h.transports[j].Connect(ServerID(id), h.transports[id])
		}
	}
	h.mu.Lock()
	h.connected[id] = true
	h.mu.Unlock()
}

// aliveSnapshot возвращает копию среза alive, снятую под h.mu.
// Контракт: вызывается БЕЗ удерживаемой h.mu.
func (h *Harness) aliveSnapshot() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]bool, h.n)
	copy(out, h.alive)
	return out
}

// connectedSnapshot возвращает копию среза connected, снятую под h.mu.
// Контракт: вызывается БЕЗ удерживаемой h.mu.
func (h *Harness) connectedSnapshot() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]bool, h.n)
	copy(out, h.connected)
	return out
}

// CrashPeer «аварийно завершает работу» сервера, отключая его
// и останавливая CM. Хранилище сохраняется.
//
// Quiesce-барьер: канал commitChans[id] пересоздаётся
// per-incarnation под владением Harness — закрытие старого канала → join
// коллектора старой incarnation → новый канал → новый коллектор → очистка
// h.commits[id] под h.mu после join. Инвариант: в момент возврата ни одна
// горутина не держит ссылку на старый канал, h.commits[id] пуст и остаётся
// пустым. Инвариант границ h.mu: блокирующие операции жизненного цикла
// выполняются ВНЕ h.mu.
func (h *Harness) CrashPeer(id int) {
	tlog("Crash %d", id)
	h.DisconnectPeer(id)

	// [h.mu: alive[id]=false] release — короткая секция.
	h.mu.Lock()
	h.alive[id] = false
	h.mu.Unlock()

	h.cluster[id].Stop()
	h.transports[id].Close()

	// Quiesce-барьер: join коллектора старой incarnation до очистки.
	oldCh := h.commitChans[id]
	close(oldCh)
	<-h.collectorDone[id]

	h.commitChans[id] = make(chan CommitEntry)
	h.collectorDone[id] = h.startCollector(id, h.commitChans[id])

	// [h.mu: очистка commits[id]] release — после join старого коллектора.
	h.mu.Lock()
	h.commits[id] = h.commits[id][:0]
	h.mu.Unlock()
}

// RestartPeer «перезапускает» сервер, создавая новый CM и InmemTransport.
//
// Инвариант связности: рестарт НЕ восстанавливает логически разорванные
// связи — h.connected является единственным источником истины о связности;
// новый транспорт соединяется только с узлами alive[j] && connected[j].
// Сам перезапускаемый узел возвращается в кластер подключённым
// (connected[id] = true): он был аварийно остановлен (CrashPeer
// отключает его), а не разделён перегородкой.
//
// Значения alive и connected снимаются под h.mu; создание CM, подключение
// транспортов и ready-wait выполняются вне блокировки.
func (h *Harness) RestartPeer(id int) {
	h.mu.Lock()
	alive := make([]bool, h.n)
	connected := make([]bool, h.n)
	copy(alive, h.alive)
	copy(connected, h.connected)
	h.mu.Unlock()

	if alive[id] {
		log.Fatalf("id=%d is alive in RestartPeer", id)
	}
	tlog("Restart %d", id)

	peerIds := make([]int, 0)
	for p := 0; p < h.n; p++ {
		if p != id {
			peerIds = append(peerIds, p)
		}
	}

	ready := make(chan any)
	addr := ServerAddress("raft-" + itoa(h.n) + "-" + itoa(id))
	h.transports[id] = transp.NewInmemTransport(addr)

	// Подключаем новый транспорт только к живым и связным соседям:
	// молчаливое восстановление разорванной связи запрещено инвариантом
	// связности харнесса.
	for j := 0; j < h.n; j++ {
		if j != id && alive[j] && connected[j] {
			h.transports[id].Connect(ServerID(j), h.transports[j])
			h.transports[j].Connect(ServerID(id), h.transports[id])
		}
	}

	h.cluster[id] = NewConsensusModule(
		id, peerIds, h.transports[id],
		h.storage[id], NewCommitChannelFSM(h.commitChans[id]), ready,
	)
	close(ready)
	h.mu.Lock()
	h.alive[id] = true
	h.connected[id] = true
	h.mu.Unlock()
	h.waitForPeerReady(id)
}

// waitForPeerReady ожидает готовность перезапущенного узла: CM отвечает
// на Report() (CM создан и restore завершён синхронно в конструкторе)
// и транспорт узла подключён ко всем соседям, с которыми связь ожидается
// (alive && connected — разрыв с логически отключённым соседом не является
// неготовностью, иначе ожидание тянулось бы до исчерпания бюджета).
// Бюджет — _commitBudgetSteady (условия обычно истинны сразу — опрос
// немедленный).
func (h *Harness) waitForPeerReady(id int) {
	h.t.Helper()
	deadline := time.Now().Add(_commitBudgetSteady)
	for time.Now().Before(deadline) {
		// Значения alive/connected снимаются под h.mu, Report()
		// вызывается вне её (инвариант границ).
		h.mu.Lock()
		alive := make([]bool, h.n)
		connected := make([]bool, h.n)
		copy(alive, h.alive)
		copy(connected, h.connected)
		h.mu.Unlock()
		reportID, _, _ := h.cluster[id].Report()
		ready := reportID == id
		if ready {
			for j := 0; j < h.n; j++ {
				if j != id && alive[j] && connected[j] && h.transports[id].IsDisconnected(ServerID(j)) {
					ready = false
					break
				}
			}
		}
		if ready {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("restarted peer %d did not become ready within %v", id, _commitBudgetSteady)
}

// PeerDropCallsAfterN — больше не поддерживается (RPCProxy удалён).
// Метод сохранён для совместимости, но не выполняет никаких действий.
func (h *Harness) PeerDropCallsAfterN(id int, n int) {
	tlog("peer %d drop calls after %d (no-op)", id, n)
}

// PeerDontDropCalls — больше не поддерживается (RPCProxy удалён).
func (h *Harness) PeerDontDropCalls(id int) {
	tlog("peer %d don't drop calls (no-op)", id)
}

// CheckSingleLeader проверяет инвариант Election Safety: не более одного
// лидера в пределах одного терма. Два подключённых узла в состоянии Leader
// с одинаковым термом — немедленный фатальный отказ (нарушение инварианта
// Raft при любом порядке снятия состояний: каждое наблюдение означает
// состоявшуюся победу в этом терме). Два лидера в разных термах —
// допустимое переходное состояние: изолированный лидер прежнего терма не
// имеет кворума, ничего не фиксирует и уходит в step-down только при первом
// контакте с большим термом, который откладывается задержкой повторов
// репликации до _maxReplicationBackoff; опрос продолжается до схождения.
//
// Бюджет — _singleLeaderBudget = _leaderElectionBudget + _maxReplicationBackoff
// (два независимых worst-case: выборы и step-down призрачного лидера).
// Значения connected снимаются под h.mu, Report() опрашивается вне
// блокировки (инвариант границ). По исчерпании бюджета (ноль лидеров либо
// сохраняющаяся множественность) — фатальный отказ со структурированной
// диагностикой nodeStates() (id, роль, терм, connected, alive).
func (h *Harness) CheckSingleLeader() (int, int) {
	h.t.Helper()
	return h.waitSingleLeader(_singleLeaderBudget)
}

// waitSingleLeader — общая логика опроса для CheckSingleLeader и
// WaitForSingleLeader: схождение к ровно одному лидеру в пределах бюджета.
// Семантика множественности — term-aware (см. CheckSingleLeader).
func (h *Harness) waitSingleLeader(budget time.Duration) (int, int) {
	h.t.Helper()
	id, term, err := h.pollSingleLeader(budget)
	if err != nil {
		h.t.Fatalf("%v\n  %s", err, h.nodeStates())
	}
	return id, term
}

// pollSingleLeader — non-fatal ядро waitSingleLeader: возвращает лидера
// и его терм либо ошибку (нарушение Election Safety либо исчерпание
// бюджета). Используется напрямую в юнит-тестах харнесса.
func (h *Harness) pollSingleLeader(budget time.Duration) (int, int, error) {
	deadline := time.Now().Add(budget)
	for {
		connected := h.connectedSnapshot()
		leaderId := -1
		leaderTerm := -1
		multiple := false
		for i := 0; i < h.n; i++ {
			if !connected[i] {
				continue
			}
			_, term, isLeader := h.cluster[i].Report()
			if !isLeader {
				continue
			}
			if leaderId < 0 {
				leaderId, leaderTerm = i, term
				continue
			}
			if term == leaderTerm {
				return -1, -1, fmt.Errorf(
					"servers %d and %d both think they're leaders in term %d (election safety violation)",
					leaderId, i, term,
				)
			}
			// Лидеры в разных термах: изолированный лидер прежнего терма
			// уходит в step-down с задержкой повторов репликации.
			multiple = true
		}
		if leaderId >= 0 && !multiple {
			return leaderId, leaderTerm, nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(_pollInterval)
	}

	return -1, -1, fmt.Errorf(
		"no single leader within budget %v (polled %v):\n"+
			"  expected: exactly one leader among connected peers (election safety)",
		budget, _pollInterval,
	)
}

// nodeStates возвращает строку состояния всех узлов (id, state, term,
// connected/alive) для диагностики при исчерпании бюджета ожидания.
// Контракт: вызывается БЕЗ удерживаемой h.mu (внутри снимает её на
// время чтения connected/alive и опрашивает CM вне блокировки).
func (h *Harness) nodeStates() string {
	connected := h.connectedSnapshot()
	alive := h.aliveSnapshot()

	var sb strings.Builder
	sb.WriteString("state:")
	for i := 0; i < h.n; i++ {
		if h.cluster[i] == nil {
			_, _ = fmt.Fprintf(&sb, " [%d: <no CM>]", i)
			continue
		}
		_, term, isLeader := h.cluster[i].Report()
		role := "follower/other"
		if isLeader {
			role = "leader"
		}
		_, _ = fmt.Fprintf(
			&sb, " [%d: %s term=%d connected=%t alive=%t]",
			i, role, term, connected[i], alive[i],
		)
	}
	return sb.String()
}

// WaitForSingleLeader ждёт, пока среди подключённых узлов останется ровно
// один лидер, и возвращает его ID и term. Отличие от CheckSingleLeader —
// только явный бюджет ожидания: обе функции пользуются одной term-aware
// логикой опроса (Election Safety: два лидера в одном терме — немедленный
// фатальный отказ; в разных термах — переходное состояние, шаг-down
// изолированного лидера откладывается задержкой повторов репликации
// до _maxReplicationBackoff). По исчерпании бюджета — фатальный отказ
// с диагностикой nodeStates(), без повторного прохода по бюджету.
func (h *Harness) WaitForSingleLeader(timeout time.Duration) (int, int) {
	h.t.Helper()
	return h.waitSingleLeader(timeout)
}

// GetTerm возвращает текущий term указанного сервера.
// Используется в тестах для проверки стабильности term при Pre-Vote.
func (h *Harness) GetTerm(serverID int) int {
	id, term, _ := h.cluster[serverID].Report()
	if id != serverID {
		return -1
	}
	return term
}

// CheckNoLeader проверяет, что ни один из подключённых серверов
// не считает себя лидером. Значения connected снимаются под h.mu,
// Report() опрашивается вне блокировки.
func (h *Harness) CheckNoLeader() {
	h.t.Helper()
	connected := h.connectedSnapshot()
	for i := 0; i < h.n; i++ {
		if connected[i] {
			_, _, isLeader := h.cluster[i].Report()
			if isLeader {
				h.t.Fatalf("server %d leader; want none", i)
			}
		}
	}
}

// waitFor опрашивает предикат cond с интервалом _pollInterval, пока он
// не станет истинным либо не истечёт budget.
//
// Контракт блокировки (§8 architecture.md,,): cond и diag
// вызываются с УЖЕ удерживаемой h.mu — waitFor захватывает h.mu перед
// каждым вычислением и освобождает сразу после. Поэтому cond/diag не
// должны вызывать функции, самостоятельно берущие h.mu (CheckCommitted,
// CheckCommittedN, CheckNotCommitted, *Snapshot) — h.mu нереентерабельна,
// нарушение даёт немедленное зависание. Вызывать waitFor следует
// БЕЗ удерживаемой h.mu.
//
// desc описывает ожидаемое состояние; diag (может быть nil) строит
// расширенную диагностику и вызывается только при timeout — happy path
// не несёт издержек. Возвращает nil при успехе, иначе ошибку с
// диагностикой (§21 architecture.md); решение о t.Errorf/t.Fatalf
// принимает вызывающий.
func (h *Harness) waitFor(desc string, budget time.Duration, cond func() bool, diag func() string) error {
	var diagCond func() string
	if diag != nil {
		// Диагностика исполняется под h.mu — та же конвенция, что у cond.
		diagCond = func() string {
			h.mu.Lock()
			defer h.mu.Unlock()
			return diag()
		}
	}
	err := waitCond(desc, budget, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return cond()
	}, diagCond)
	if err == nil {
		return nil
	}
	// Хвост nodeStates() — часть контракта диагностики waitFor (волна 1);
	// вызывается без удерживаемой h.mu.
	return fmt.Errorf("%s\n  %s", err, h.nodeStates())
}

// waitCond опрашивает предикат cond с интервалом _pollInterval, пока он
// не станет истинным либо не истечёт budget; возвращает nil при успехе,
// иначе ошибку с бюджетом, описанием и деталями diag.
//
// Тестовая инфраструктура по назначению. Файл harness_test.go размещён вне
// _test-сборки по историческим причинам (внешние потребители харнесса
// живут в других пакетах) и компилируется в обычную сборку пакета raft,
// поэтому waitCond попадает в production-бинарники как неиспользуемый код;
// применять её в production-коде не предполагается.
//
// waitCond не берёт никаких блокировок: захват мьютексов, необходимых
// cond и diag (например cm.mu), — ответственность вызывающего. Проверка
// cond выполняется до первой паузы: условие, истинное сразу, возвращает
// nil немедленно. diag может быть nil и вызывается только при исчерпании
// бюджета (happy path не несёт издержек).
func waitCond(desc string, budget time.Duration, cond func() bool, diag func() string) error {
	deadline := time.Now().Add(budget)
	for {
		if cond() {
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(_pollInterval)
	}

	details := ""
	if diag != nil {
		details = diag()
	}
	return fmt.Errorf(
		"wait timeout (budget %v, polled %v):\n  expected: %s\n  %s",
		budget, _pollInterval, desc, details,
	)
}

// committedOn возвращает (nc, converged):
//   - nc — число подключённых узлов, у которых команда cmd присутствует
//     в h.commits (в любой позиции);
//   - converged == true, когда выполнены предусловия CheckCommitted:
//     у всех подключённых узлов равные длины commits И запись cmd
//     находится у них на одной и той же позиции с одинаковым Index.
//
// Предикат non-fatal (не вызывает t.Error*/t.Fatal*) и по построению
// НЕ слабее assert'а CheckCommitted: узел, «ушедший вперёд» по commits,
// удерживает converged == false, поэтому ожидание не может завершиться раньше,
// чем assert станет выполнимым.
//
// Контракт: h.mu must be held.
func (h *Harness) committedOn(cmd int) (nc int, converged bool) {
	commitsLen := -1
	lenEqual := true
	for i := 0; i < h.n; i++ {
		if !h.connected[i] {
			continue
		}
		if commitsLen < 0 {
			commitsLen = len(h.commits[i])
		} else if len(h.commits[i]) != commitsLen {
			lenEqual = false
		}
		for c := range h.commits[i] {
			if v, ok := h.commits[i][c].Data.(int); ok && v == cmd {
				nc++
				break
			}
		}
	}
	if commitsLen < 0 || !lenEqual {
		// Подключённых узлов нет либо журналы фиксаций разошлись —
		// предусловие CheckCommitted не выполнено.
		return nc, false
	}

	for c := 0; c < commitsLen; c++ {
		index := -1
		same := true
		for i := 0; i < h.n; i++ {
			if !h.connected[i] {
				continue
			}
			v, ok := h.commits[i][c].Data.(int)
			if !ok || v != cmd {
				same = false
				break
			}
			if index < 0 {
				index = h.commits[i][c].Index
			} else if h.commits[i][c].Index != index {
				same = false
				break
			}
		}
		if same {
			return nc, true
		}
	}
	return nc, false
}

// commitValuesOn возвращает (nc, lengthsConverged):
//   - nc — число подключённых узлов, у которых команда cmd присутствует
//     в h.commits (в любой позиции);
//   - lengthsConverged == true, когда у всех подключённых узлов равные
//     длины списков фиксации.
//
// В отличие от committedOn, не требует одинаковой позиции/Index записи:
// после перевыборов предыстории журналов могут расходиться (перезапись
// незафиксированного хвоста), и для проверки восстановления достаточно
// наличия команды по значению. Предикат non-fatal (не вызывает
// t.Error*/t.Fatal*).
//
// Контракт: h.mu must be held.
func (h *Harness) commitValuesOn(cmd int) (nc int, lengthsConverged bool) {
	commitsLen := -1
	lengthsEqual := true
	for i := 0; i < h.n; i++ {
		if !h.connected[i] {
			continue
		}
		if commitsLen < 0 {
			commitsLen = len(h.commits[i])
		} else if len(h.commits[i]) != commitsLen {
			lengthsEqual = false
		}
		for c := range h.commits[i] {
			if v, ok := h.commits[i][c].Data.(int); ok && v == cmd {
				nc++
				break
			}
		}
	}
	if commitsLen < 0 {
		return nc, false
	}
	return nc, lengthsEqual
}

// checkCommittedValues утверждает наличие команды cmd по значению на
// каждом подключённом узле, не требуя одинаковой позиции/Index записи
// (см. commitValuesOn). Чистый assert БЕЗ ожидания: вызывается после
// wait по commitValuesOn, когда cmd уже присутствует на всех
// подключённых узлах; наличие монотонно, поэтому assert не гоняется с
// коллектором.
func (h *Harness) checkCommittedValues(cmd int) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	nc := 0
	for i := 0; i < h.n; i++ {
		if !h.connected[i] {
			continue
		}
		nc++
		found := false
		for c := range h.commits[i] {
			if v, ok := h.commits[i][c].Data.(int); ok && v == cmd {
				found = true
				break
			}
		}
		if !found {
			h.t.Fatalf("cmd=%d not committed on connected peer %d: commits[%d] = %v", cmd, i, i, h.commits[i])
		}
	}
	if nc == 0 {
		h.t.Fatalf("cmd=%d: no connected peers", cmd)
	}
}

// commitsDiag возвращает строку состояния h.commits для диагностики.
// Контракт: h.mu must be held.
func (h *Harness) commitsDiag() string {
	var sb strings.Builder
	sb.WriteString("commits:")
	for i := 0; i < h.n; i++ {
		_, _ = fmt.Fprintf(&sb, " len[%d]=%d", i, len(h.commits[i]))
		if n := len(h.commits[i]); n > 0 {
			_, _ = fmt.Fprintf(&sb, " last[%d]=%v", i, h.commits[i][n-1].Data)
		}
	}
	return sb.String()
}

// CheckCommitted проверяет, что команда cmd зафиксирована на всех
// подключённых серверах с одинаковым индексом.
//
// Контракт: это чистый assert БЕЗ ожидания. Future команды разрешается
// в applySingle сразу после того, как коллектор принял значение из
// небуферизованного канала фиксации, но ДО захвата h.mu и записи
// в h.commits — поэтому возврат SubmitToServer/future.Error() ещё не
// гарантирует видимости записи. Применять CheckCommitted к условию,
// удовлетворяемому асинхронно относительно возврата SubmitToServer,
// запрещено; корректная схема — wait (WaitForCommit /
// WaitForCommitBudget / WaitForCommitAll) затем assert.
func (h *Harness) CheckCommitted(cmd int) (nc int, index int) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()

	commitsLen := -1
	for i := 0; i < h.n; i++ {
		if h.connected[i] {
			if commitsLen >= 0 {
				if len(h.commits[i]) != commitsLen {
					h.t.Fatalf("commits[%d] = %d, commitsLen = %d", i, h.commits[i], commitsLen)
				}
			} else {
				commitsLen = len(h.commits[i])
			}
		}
	}

	for c := 0; c < commitsLen; c++ {
		cmdAtC := -1
		for i := 0; i < h.n; i++ {
			if h.connected[i] {
				cmdOfN, ok := h.commits[i][c].Data.(int)
				if !ok {
					h.t.Fatalf("commits[%d][%d]: data %T is not int", i, c, h.commits[i][c].Data)
				}
				if cmdAtC >= 0 {
					if cmdOfN != cmdAtC {
						h.t.Errorf("got %d, want %d at h.commits[%d][%d]", cmdOfN, cmdAtC, i, c)
					}
				} else {
					cmdAtC = cmdOfN
				}
			}
		}
		if cmdAtC == cmd {
			index := -1
			nc := 0
			for i := 0; i < h.n; i++ {
				if h.connected[i] {
					if index >= 0 && h.commits[i][c].Index != index {
						h.t.Errorf("got Index=%d, want %d at h.commits[%d][%d]", h.commits[i][c].Index, index, i, c)
					} else {
						index = h.commits[i][c].Index
					}
					nc++
				}
			}
			return nc, index
		}
	}

	h.t.Errorf("cmd=%d not found in commits", cmd)
	return -1, -1
}

// CheckCommittedN проверяет, что команда cmd зафиксирована на n серверах.
//
// Контракт: чистый assert БЕЗ ожидания (см. CheckCommitted) — требуется
// предшествующий wait по той же команде.
func (h *Harness) CheckCommittedN(cmd int, n int) {
	h.t.Helper()
	nc, _ := h.CheckCommitted(cmd)
	if nc != n {
		h.t.Errorf("CheckCommittedN got nc=%d, want %d", nc, n)
	}
}

// WaitForCommit ждёт сходимости фиксации команды cmd минимум на n
// подключённых серверах и затем проверяет фиксацию assert'ом
// CheckCommittedN.
//
// Бюджет по умолчанию — _commitBudgetAfterFailover: сценарий вызова
// может содержать перевыборы. Для сценариев с заведомо устоявшимся
// лидером используйте WaitForCommitBudget(cmd, n, _commitBudgetSteady).
func (h *Harness) WaitForCommit(cmd int, n int) {
	h.t.Helper()
	h.WaitForCommitBudget(cmd, n, _commitBudgetAfterFailover)
}

// WaitForCommitBudget — WaitForCommit с явным бюджетом ожидания
// (_commitBudgetSteady / _commitBudgetAfterFailover —).
//
// Схема (§8 architecture.md): wait по предикату сходимости committedOn
// (под h.mu) → assert CheckCommittedN (без h.mu). Раннего return без
// assert'а нет — assert выполняется и при успешном ожидании, и при
// исчерпании бюджета (ожидание не слабее assert'а).
func (h *Harness) WaitForCommitBudget(cmd int, n int, budget time.Duration) {
	h.t.Helper()
	desc := fmt.Sprintf("cmd=%d committed on >=%d connected peers (converged)", cmd, n)
	err := h.waitFor(
		desc, budget,
		func() bool {
			nc, converged := h.committedOn(cmd)
			return nc >= n && converged
		},
		h.commitsDiag,
	)
	if err != nil {
		h.t.Errorf("WaitForCommit %v", err)
	}
	// Assert выполняется ВНЕ h.mu (CheckCommittedN берёт её сам).
	h.CheckCommittedN(cmd, n)
}

// WaitForCommitAll ждёт сходимости фиксации команды cmd на всех
// подключённых серверах и проверяет фиксацию assert'ом CheckCommitted.
// Предикат сходимости — тот же committedOn (равные длины commits,
// одинаковая позиция и Index записи).
func (h *Harness) WaitForCommitAll(cmd int, timeout time.Duration) {
	h.t.Helper()
	desc := fmt.Sprintf("cmd=%d committed on all connected peers (converged)", cmd)
	err := h.waitFor(
		desc, timeout,
		func() bool {
			nc, converged := h.committedOn(cmd)
			connected := 0
			for i := 0; i < h.n; i++ {
				if h.connected[i] {
					connected++
				}
			}
			return converged && nc == connected
		},
		h.commitsDiag,
	)
	if err != nil {
		h.t.Errorf("WaitForCommitAll %v", err)
	}
	h.CheckCommitted(cmd)
}

// WaitForCommitConvergedValues ждёт, пока списки фиксации подключённых
// узлов не сойдутся по длине и команда cmd не появится минимум на n
// подключённых узлах, затем утверждает наличие cmd по значению на всех
// подключённых узлах.
//
// В отличие от WaitForCommit, не требует одинаковой позиции/Index записи:
// после перевыборов предыстории журналов могут расходиться (перезапись
// незафиксированного хвоста), и это требование невыполнимо; для проверки
// восстановления достаточно наличия команды по значению.
//
// Бюджет по умолчанию — _commitBudgetAfterFailover.
func (h *Harness) WaitForCommitConvergedValues(cmd int, n int) {
	h.t.Helper()
	h.WaitForCommitConvergedValuesBudget(cmd, n, _commitBudgetAfterFailover)
}

// WaitForCommitConvergedValuesBudget — WaitForCommitConvergedValues
// с явным бюджетом ожидания.
//
// Схема: wait по предикату commitValuesOn (равные длины и наличие cmd
// на >= n узлах) под h.mu → assert checkCommittedValues (без h.mu).
// Наличие cmd монотонно, поэтому assert выполняется и при успешном
// ожидании, и при исчерпании бюджета (ожидание не слабее assert'а).
func (h *Harness) WaitForCommitConvergedValuesBudget(cmd int, n int, budget time.Duration) {
	h.t.Helper()
	desc := fmt.Sprintf("cmd=%d present on >=%d connected peers (converged lengths)", cmd, n)
	err := h.waitFor(
		desc, budget,
		func() bool {
			nc, lengthsConverged := h.commitValuesOn(cmd)
			return nc >= n && lengthsConverged
		},
		h.commitsDiag,
	)
	if err != nil {
		h.t.Errorf("WaitForCommitConvergedValues %v", err)
	}
	h.checkCommittedValues(cmd)
}

// waitForSuffrage ждёт, пока узел observerID не увидит в своей последней
// конфигурации сервер target с правом голоса suffrage — наблюдаемый признак
// того, что изменение членства дошло до этого узла и применено.
//
// Примитив не-commit ожидания: ConfigurationFuture, возвращаемый
// GetConfiguration, доступен на любом узле (снимок configurations.latest
// под cm.mu), поэтому h.mu не участвует.
func (h *Harness) waitForSuffrage(observerID int, target ServerID, suffrage ServerSuffrage, budget time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(budget)
	for {
		cfg := h.cluster[observerID].GetConfiguration().Configuration()
		for _, srv := range cfg.ConfigServers {
			if srv.ID == target && srv.Suffrage == suffrage {
				return
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(_pollInterval)
	}
	h.t.Fatalf(
		"waitForSuffrage timeout (budget %v, polled %v):\n"+
			"  expected: server %d sees server %d with suffrage %v\n  %s",
		budget, _pollInterval, observerID, target, suffrage, h.nodeStates(),
	)
}

// waitForIsolated ждёт, пока изоляция узла id фактически вступит в силу:
// транспорт id не видит ни одного другого живого узла и ни один живой
// узел не видит id.
//
// Примитив не-commit ожидания (§8 architecture.md, группа B):
// заменяет фиксированный sleep после DisconnectPeer. Условие наблюдаемо
// напрямую (состояние InmemTransport), поэтому ожидание, как правило,
// завершается на первом опросе.
func (h *Harness) waitForIsolated(id int, budget time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(budget)
	for {
		alive := h.aliveSnapshot()
		isolated := true
		for j := 0; j < h.n; j++ {
			if j == id || !alive[j] {
				continue
			}
			if !h.transports[id].IsDisconnected(ServerID(j)) ||
				!h.transports[j].IsDisconnected(ServerID(id)) {
				isolated = false
				break
			}
		}
		if isolated {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(_pollInterval)
	}
	h.t.Fatalf(
		"waitForIsolated timeout (budget %v, polled %v):\n"+
			"  expected: server %d is disconnected from every live peer\n  %s",
		budget, _pollInterval, id, h.nodeStates(),
	)
}

// applyInflightCount возвращает число future в leaderState.inflight
// узла id — записей, отправленных в журнал лидера и ещё не
// применённых к FSM. Читается под cm.mu; h.mu не участвует.
//
// Используется как база отсчёта для waitForApplyInflight: у только что
// избранного лидера в inflight может оставаться собственная noop-запись,
// поэтому «inflight непусто» само по себе не означает, что до лидерского
// цикла дошла именно команда теста.
func (h *Harness) applyInflightCount(id int) int {
	cm := h.cluster[id]
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.leaderState.inflight)
}

// waitForApplyInflight ждёт, пока число future в leaderState.inflight
// узла id не достигнет want — т.е. отправленный Apply дошёл до
// лидерского цикла и ожидает фиксации.
//
// Примитив не-commit ожидания (§8 architecture.md,): состояние
// читается под cm.mu, h.mu не участвует — вложенных блокировок нет.
// Порог want задаётся вызывающим как applyInflightCount(id) + N,
// снятый ДО вызова Apply.
func (h *Harness) waitForApplyInflight(id int, want int, budget time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if h.applyInflightCount(id) >= want {
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(_pollInterval)
	}
	h.t.Fatalf(
		"waitForApplyInflight timeout (budget %v, polled %v):\n"+
			"  expected: server %d has >=%d inflight Apply futures (got %d)\n  %s",
		budget, _pollInterval, id, want, h.applyInflightCount(id), h.nodeStates(),
	)
}

// lastLogIndex возвращает индекс последней записи журнала узла id.
// Читается под cm.mu; h.mu не участвует.
func (h *Harness) lastLogIndex(id int) int {
	cm := h.cluster[id]
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.cmState.lastLogIndex
}

// waitForLastLogIndex ждёт, пока журнал узла id не дорастёт до индекса want:
// команда, отправленная клиентом, дошла до лидерского цикла и записана
// в журнал. Барьер не зависит от скорости применения к машине состояний,
// в отличие от ожидания непустой очереди inflight.
func (h *Harness) waitForLastLogIndex(id, want int, budget time.Duration) {
	h.t.Helper()
	if err := waitCond(
		"log of the node reaches the expected index",
		budget,
		func() bool { return h.lastLogIndex(id) >= want },
		func() string { return "lastLogIndex = " + itoa(h.lastLogIndex(id)) },
	); err != nil {
		h.t.Fatalf("waitForLastLogIndex(server %d, want %d): %v\n  %s", id, want, err, h.nodeStates())
	}
}

// CheckNotCommitted проверяет, что команда cmd ещё не зафиксирована.
//
// Контракт: чистый assert БЕЗ ожидания (см. CheckCommitted). Для негативных
// проверок «не зафиксирована» корректен только после барьера, гарантирующего
// завершение асинхронных фиксаций (например, после WaitForCommit другой
// команды, упорядоченной после проверяемой).
func (h *Harness) CheckNotCommitted(cmd int) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := 0; i < h.n; i++ {
		if h.connected[i] {
			for c := 0; c < len(h.commits[i]); c++ {
				gotCmd, ok := h.commits[i][c].Data.(int)
				if !ok {
					h.t.Fatalf("commits[%d][%d]: data %T is not int", i, c, h.commits[i][c].Data)
				}
				if gotCmd == cmd {
					h.t.Errorf("found %d at commits[%d][%d], expected none", cmd, i, c)
				}
			}
		}
	}
}

// SubmitToServer отправляет команду серверу и дожидается фиксации.
func (h *Harness) SubmitToServer(serverId int, cmd any) int {
	future := h.cluster[serverId].Apply(cmd, 0)

	errCh := make(chan error, 1)
	go func() {
		errCh <- future.Error()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return -1
		}
		return future.Index()
	case <-time.After(_submitTimeout):
		return -1
	}
}

func tlog(format string, a ...any) {
	format = "[TEST] " + format
	log.Printf(format, a...)
}

func sleepMs(n int) {
	time.Sleep(time.Duration(n) * time.Millisecond)
}

// startCollector запускает коллектор узла i для канала ch и возвращает
// канал завершения коллектора. Коллектор регистрируется в h.wg (join в
// Shutdown) и закрывает done по завершении (quiesce-барьер CrashPeer).
func (h *Harness) startCollector(i int, ch chan CommitEntry) chan struct{} {
	done := make(chan struct{})
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer close(done)
		h.collectCommits(i, ch)
	}()
	return done
}

// collectCommits читает сообщения из канала ch и добавляет их в
// h.commits[i] под h.mu. Завершается по закрытию канала (range).
//
// Инвариант монотонности видимости: на каждый узел существует ровно одна
// горутина-коллектор, читающая FIFO-канал фиксации и дописывающая
// h.commits[i] последовательно. Следовательно, видимость записи k влечёт
// видимость всех записей, доставленных ранее, — на этот инвариант
// разрешено опираться: одного ожидания последней команды достаточно
// для корректности последующей серии CheckCommitted*. Его нарушение
// (второй коллектор на узел, буферизация канала с переупорядочением)
// является осознанным изменением контракта харнесса.
func (h *Harness) collectCommits(i int, ch <-chan CommitEntry) {
	for c := range ch {
		h.mu.Lock()
		tlog("collectCommits(%d) got %+v", i, c)
		h.commits[i] = append(h.commits[i], c)
		h.mu.Unlock()
	}
}

// LeadershipTransfer инициирует передачу лидерства через публичный API.
func (h *Harness) LeadershipTransfer(serverID int, targetID ServerID) LeadershipTransferFuture {
	return h.cluster[serverID].LeadershipTransfer(targetID)
}

// SendTimeoutNow отправляет TimeoutNow напрямую через транспорт.
// Используется в тестах для проверки реакции на TimeoutNow без участия
// лидера (например, тест TimeoutNow на Follower).
func (h *Harness) SendTimeoutNow(from, to int) (TimeoutNowResponse, error) {
	return h.transports[from].TimeoutNow(
		ServerID(to),
		TimeoutNowRequest{
			RPCHeader: RPCHeader{
				ProtocolVersion: ProtocolVersion,
				ServerID:        from,
			},
		},
	)
}

// CheckLeaderIs проверяет, что лидером является указанный сервер.
func (h *Harness) CheckLeaderIs(expectedID int) {
	h.t.Helper()
	leaderID, _ := h.CheckSingleLeader()
	if leaderID != expectedID {
		h.t.Fatalf("leader = %d, want %d", leaderID, expectedID)
	}
}

// itoa — простейшее преобразование int в строку (без импорта strconv).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
