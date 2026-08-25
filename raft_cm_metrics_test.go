package raft

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// -- Тесты подсистемы latency-метрик. ---
//
// Правило детерминизма: NewConsensusModule запускает
// горутину stats, которая раз в секунду снимает и обнуляет агрегаты, поэтому
// утверждения о ЗНАЧЕНИЯХ агрегатов (count/sumUs/среднее) делаются только на
// CM без запущенной stats — собранном литералом (&ConsensusModule{} /
// new(ConsensusModule)). Утверждения о поведении допустимы на любом CM.
// Ни один тест не изменяет traceCM и не вызывает SetTrace
// (.doc/flaky-test.md).

// TestLatencyObserveNonBlockingExactMean — (литеральный CM).
// Серия > 8192 последовательных observe в один агрегат завершается;
// snapshotAndReset возвращает точное среднее заданной выборки; повторное
// снятие сразу после сброса даёт 0. Stats не запущена ⇒ обнулению нечему
// конкурировать со снятием.
func TestLatencyObserveNonBlockingExactMean(t *testing.T) {
	cm := new(ConsensusModule)

	const n = 9000
	var wantUs int64
	for i := 0; i < n; i++ {
		d := time.Duration(i%997+1) * time.Microsecond
		cm.latency.appendEntries.observe(d)
		wantUs += d.Microseconds()
	}

	rep := cm.latency.snapshotAndReset()
	// Арифметика идентична выражению в meanMsAndReset:
	// float64(s)/float64(c)/1000.0 — сравнение точное.
	if want := float64(wantUs) / float64(n) / 1000.0; rep.appendEntries != want {
		t.Fatalf("среднее = %v, want %v", rep.appendEntries, want)
	}

	// Повторное снятие сразу после сброса: окно пустое ⇒ 0 (как Mean
	// пустой выборки), деления на ноль нет.
	rep2 := cm.latency.snapshotAndReset()
	if rep2.appendEntries != 0 {
		t.Fatalf("повторное снятие = %v, want 0", rep2.appendEntries)
	}
}

// TestLatencyCarryBackPreservesSum — литеральный CM;
// Состояние агрегата задаётся напрямую (sumUs.Store/count.Store),
// интерливинг не эмулируется: при count == 0 и sumUs != 0 снятие обязано
// вернуть 0, НО сохранить сумму (carry-back) — следующее снятие с count == 1
// даёт исходный замер. Без carry-back тест падает.
func TestLatencyCarryBackPreservesSum(t *testing.T) {
	cm := new(ConsensusModule)

	const dUs = 12_345
	cm.latency.election.sumUs.Store(dUs)
	cm.latency.election.count.Store(0)

	rep := cm.latency.snapshotAndReset()
	if rep.election != 0 {
		t.Fatalf("снятие при count == 0 вернуло %v, want 0", rep.election)
	}
	// Ключевое утверждение: сумма не выброшена, а возвращена в агрегат.
	if got := cm.latency.election.sumUs.Load(); got != dUs {
		t.Fatalf("sumUs после снятия = %d, want %d (carry-back отсутствует)", got, dUs)
	}

	// «Отставший» count.Add(1) того же observe неизбежно придёт в следующее
	// окно — пара воссоединяется, замер не теряется.
	cm.latency.election.count.Store(1)
	rep2 := cm.latency.snapshotAndReset()
	if want := float64(dUs) / 1000.0; rep2.election != want {
		t.Fatalf("следующее снятие = %v, want %v", rep2.election, want)
	}
}

// TestLatencyObserveUnderMutexDoesNotBlock — (прямой регресс на deadlock).
// Наблюдение при удержанном cm.mu завершается и из горутины теста,
// и из отдельной горутины, пока мьютекс удерживает тест.
// Утверждение — о поведении, не о значениях;
// при возврате реализации к каналам/блокировке тест падает по таймауту.
func TestLatencyObserveUnderMutexDoesNotBlock(t *testing.T) {
	cm := new(ConsensusModule)
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Наблюдение из отдельной горутины при удержанном тестом мьютексе.
	observeDone := make(chan struct{})
	go func() {
		defer close(observeDone)
		cm.latency.appendEntries.observe(time.Millisecond)
	}()

	select {
	case <-observeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("observe из горутины не завершился при удержанном cm.mu")
	}

	// Наблюдения из самого теста при удержанном мьютексе.
	for i := 0; i < 10_000; i++ {
		cm.latency.appendEntries.observe(time.Microsecond)
	}
}

// TestLatencyZeroValueCMMetricPaths — (литеральный CM по образцу newAliasTestCM).
// CM без конструктора и без инициализации метрик проходит метрические пути
// AppendEntries → processLogs → sendBatch и наблюдение election-агрегата без паники
// и блокировки. Положительные утверждения о счётчиках допустимы:
// stats на таком CM не запущена.
func TestLatencyZeroValueCMMetricPaths(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.storage = NewMapStorage()
	cm.fsmMutateCh = make(chan []*commitTuple, batchApplyBuffer)
	cm.shutdownCh = make(chan struct{})
	cm.leaderState.inflight = make(map[int]*logFuture)
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.log = []LogEntry{
		{Index: 0, Term: 1, Type: LogCommand, Data: "v0"},
		{Index: 1, Term: 1, Type: LogCommand, Data: "v1"},
		{Index: 2, Term: 1, Type: LogCommand, Data: "v2"},
	}
	cm.cmState.lastLogIndex = 2
	cm.cmState.lastLogTerm = 1
	cm.cmState.commitIndex = -1
	cm.cmState.lastApplied = -1

	// Метрический путь RPC-хендлера: AppendEntries продвигает commitIndex и
	// через processLogs → sendBatch отправляет батч в fsmMutateCh.
	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         1,
		LeaderID:     99,
		PrevLogIndex: -1,
		PrevLogTerm:  0,
		LeaderCommit: 2,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries на литеральном CM: %v", err)
	}

	// Явный вызов processLogs: последнийApplied уже 2 ⇒ ранний возврат,
	// но метрический defer обязан отработать.
	cm.processLogs(2)

	// Наблюдение election-агрегата (путь startElectionLocked здесь не запускается —
	// проверяется именно нулевая инициализация агрегата).
	cm.latency.election.observe(time.Microsecond)

	// Положительные парные утверждения: метрические пути действительно
	// отработали, а не «прошли мимо» (P06R2-02).
	if got := cm.latency.appendEntries.count.Load(); got < 1 {
		t.Errorf("appendEntries.count = %d, want >= 1", got)
	}
	if got := cm.latency.processLogs.count.Load(); got < 1 {
		t.Errorf("processLogs.count = %d, want >= 1", got)
	}
	if got := cm.latency.sendBatch.count.Load(); got < 1 {
		t.Errorf("sendBatch.count = %d, want >= 1", got)
	}
	if got := cm.latency.election.count.Load(); got < 1 {
		t.Errorf("election.count = %d, want >= 1", got)
	}
}

// TestLatencyAttributionPerCM — (два литеральных CM). После observe
// на CM1 обязаны выполняться одновременно CM1.count > 0 И CM2.count == 0:
// парное положительное утверждение исключает вакуумный проход (P06R2-02).
// Проверка по значениям агрегатов, без печатного вывода и без мутации traceCM.
func TestLatencyAttributionPerCM(t *testing.T) {
	cm1 := new(ConsensusModule)
	cm2 := new(ConsensusModule)

	cm1.latency.appendEntries.observe(time.Millisecond)

	if got := cm1.latency.appendEntries.count.Load(); got <= 0 {
		t.Fatalf("CM1.count = %d, want > 0", got)
	}
	if got := cm2.latency.appendEntries.count.Load(); got != 0 {
		t.Fatalf("CM2.count = %d, want 0 (замер CM1 не должен попасть в CM2)", got)
	}
}

// TestLatencyReportFormat — (чистая функция форматирования).
// Заданный снимок агрегатов даёт строку с ожидаемыми колонками в ожидаемом
// порядке, без собственного таймстампа и без префикса [state,N:id,T:term] —
// их добавляют логгер трассировки и traceLogf. Получатель —
// *latencyReport.
func TestLatencyReportFormat(t *testing.T) {
	rep := &latencyReport{
		appendEntries:     1.25,
		fsmApply:          2.5,
		election:          3.75,
		handleFsmSnapshot: 4.0,
		installSnapshot:   5.25,
		processLogs:       6.5,
		requestPreVote:    7.75,
		requestVote:       8.0,
		sendBatch:         9.25,
		takeSnapshot:      10.5,
		timeoutNowRequest: 11.75,
	}
	got := rep.format()
	want := "AE= 1.25ms, BatchingFSM= 2.50ms, Election= 3.75ms, FSMSnapSh= 4.00ms," +
		" InstSnapShot= 5.25ms, ProcessLog= 6.50ms, RqPVt= 7.75ms, RqVote= 8.00ms," +
		" SendBatch= 9.25ms, TakeSnapshot=10.50ms, TmOutNowRq=11.75ms"
	if got != want {
		t.Fatalf("format() = %q\nwant      = %q", got, want)
	}
}

// TestLatencyTraceEnabledPredicate — (read-only проверка гейта).
// Значение traceCM не изменяется: ценность теста — фиксация
// направления сравнения (<, а не <=) относительно текущего порога.
func TestLatencyTraceEnabledPredicate(t *testing.T) {
	if !traceEnabled(traceCM - 1) {
		t.Errorf("traceEnabled(traceCM-1) = false, want true")
	}
	if traceEnabled(traceCM) {
		t.Errorf("traceEnabled(traceCM) = true, want false")
	}
}

// TestFailedAETraceReportsActualNextIndex — тест 4
// текст трассировки неудачного AE печатает фактически
// присвоенное значение nextIndex (не ni-1) и поля matchIndex/ConflictIndex/
// ConflictTerm — ровно тот набор, которого не хватило при разборе обоих
// инцидентов. Формат проверяется через чистую функцию: мутация traceCM и
// _traceLogger в тестах запрещена (P06R2-02 T5c).
func TestFailedAETraceReportsActualNextIndex(t *testing.T) {
	// Значения инцидента: фактически присвоенный nextIndex = 0 при
	// matchIndex = 13490; старая трасса печатала бы «13490» (ni-1).
	got := failedAETrace(3, 0, 13490, 0, -1)
	want := "AppendEntries reply from 3 !success: nextIndex := 0 (ConflictIndex=0, ConflictTerm=-1, matchIndex=13490)"
	if got != want {
		t.Fatalf("failedAETrace = %q\nwant          = %q", got, want)
	}
}

// TestRaftCountersReportFormat — тест 1 (новая строка отчёта):
// счётчики и показатели по каждому соседу nextIndex/matchIndex форматируются
// детерминированно (пиры — по возрастанию ID). Существующая строка
// латентности защищена отдельно (TestLatencyReportFormat, /005).
func TestRaftCountersReportFormat(t *testing.T) {
	c := &raftCounters{
		installSnapshotSent:         map[int]int64{2: 3, 1: 4},
		installSnapshotSkippedStale: map[int]int64{1: 2},
		appendEntriesRejected:       map[int]int64{1: 7},
		nextIndexRejectionIgnored:   map[int]int64{1: 1},
	}
	c.installSnapshotReceived.Add(5)
	c.snapshotLogBoundaryViolation.Add(0)
	c.snapshotIndexBehindDispatched.Add(3)
	c.sendBatchEntrySkipped.Add(2)
	ls := &leaderState{
		nextIndex:  map[int]int{2: 30, 1: 25},
		matchIndex: map[int]int{2: 29, 1: 24},
	}
	got := c.report(ls)
	want := "ISsent=7 ISrecv=5 ISstale=2 AErej=7 NIrejIgn=1 BndViol=0 SnapLag=3 BatchSkip=2 VerifyDone=0 VerifyWaited=0 p1:ni=25/mi=24 p2:ni=30/mi=29"
	if got != want {
		t.Fatalf("report = %q\nwant   = %q", got, want)
	}
}

// TestCounters_DistinguishSnapshotLoop — тест 2
// (Validation): искусственно смоделированный цикл
// «InstallSnapshot → AppendEntries-failure» делает наблюдаемым рост
// installSnapshotSent и appendEntriesRejected — однозначно отличимый
// от нормального догона (ISsent=1, AErej=0). Одновременно проверяется
// предохранитель: повторная отсылка снимка блокируется,
// nextIndex остаётся согласованным (snapshotIndex+1, а не 0).
func TestCounters_DistinguishSnapshotLoop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportAE{
		failReply: true, failConflictIndex: 0, failConflictTerm: -1,
		replyTerm: 1,
	}
	tracker := newCommitmentTracker(0, 1, -1, make(chan int, 1))
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:         map[int]int{1: 1},
			matchIndex:        map[int]int{1: -1},
			inflightAE:        map[int]*atomic.Bool{1: new(atomic.Bool)},
			commitmentTracker: tracker,
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			commitIndex:       -1,
			lastLogIndex:      5,
			lastLogTerm:       2,
			lastSnapshotIndex: 5,
			lastSnapshotTerm:  2,
			log:               []LogEntry{},
			termIndexMap:      make(map[int]int),
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id:        0,
		transport: mock,
		snapshotStore: &mockSnapshotStoreCfg{
			listResult: []*SnapshotMeta{{ID: "snap", Index: 5, Term: 2, Size: 0}},
			openMeta:   &SnapshotMeta{ID: "snap", Index: 5, Term: 2, Size: 0},
		},
	}
	tracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)
	cm.leaderState.inflightAE[1].Store(false)

	// Три итерации искусственного цикла: каждая итерация сбрасывает
	// состояние репликации так, как его портил бы повреждённый ведомый
	// (nextIndex/matchIndex в начало),
	// затем — пара «IS-успех → AE-отказ».
	const iterations = 3
	for i := 0; i < iterations; i++ {
		cm.mu.Lock()
		cm.leaderState.nextIndex[1] = 1
		cm.leaderState.matchIndex[1] = -1
		cm.mu.Unlock()

		cm.leaderSendAEsToPeer(1, 1, 0, true) // ветка снимка → успех IS
		cm.leaderSendAEsToPeer(1, 1, 0, true) // ветка AE → отказ CI=0
	}

	cm.mu.Lock()
	isSent := cm.counters.installSnapshotSent[1]
	aeRej := cm.counters.appendEntriesRejected[1]
	niRejIgn := cm.counters.nextIndexRejectionIgnored[1]
	nextIndex := cm.leaderState.nextIndex[1]
	cm.mu.Unlock()

	if isSent != iterations {
		t.Fatalf("installSnapshotSent = %d, want %d", isSent, iterations)
	}
	if aeRej != iterations {
		t.Fatalf("appendEntriesRejected = %d, want %d", aeRej, iterations)
	}
	if niRejIgn != iterations {
		t.Fatalf("nextIndexRejectionIgnored = %d, want %d (impossible rejections blocked)", niRejIgn, iterations)
	}
	if nextIndex != 6 {
		t.Fatalf("nextIndex = %d, want 6 (snapshotIndex+1) — loop must not corrupt replication state", nextIndex)
	}
}

// TestLatencyStopShutdownLifecycle — (жизненный цикл; любой CM).
// Stop() завершает stats (leaktest в defer гарантирует отсутствие утечек),
// повторный Stop() безопасен, observe после Stop() не паникует и не
// блокируется.
func TestLatencyStopShutdownLifecycle(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := new(ConsensusModule)
	cm.shutdownCh = make(chan struct{})
	go cm.stats(cm.shutdownCh)

	// Двойной Stop: идемпотентен (raft_cm.go:221-232).
	cm.Stop()
	cm.Stop()

	// Наблюдение после Stop: атомарный инкремент по живой памяти CM.
	cm.latency.appendEntries.observe(time.Millisecond)
	// Горутина stats вышла по shutdownCh — проверяется leaktest в defer.
}

// TestLatencyRPCStormSmoke — (нагрузочный smoke на Harness).
// Поток входящих AppendEntries (heartbeat-терма лидера) на follower-ов в
// течение короткого интервала: все хендлеры завершаются, кластер сохраняет
// лидера. Искусственная остановка потребителя метрик не эмулируется
// (буфера больше нет).
func TestLatencyRPCStormSmoke(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, leaderTerm := h.CheckSingleLeader()

	// Лидера не трогаем: AppendEntries со своим термом заставил бы его
	// step-down (корректное поведение Raft).
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		for i := 0; i < h.n; i++ {
			if i == leaderID {
				continue
			}
			var reply AppendEntriesReply
			err := h.cluster[i].AppendEntries(AppendEntriesArgs{
				RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: leaderID},
				Term:         leaderTerm,
				LeaderID:     leaderID,
				PrevLogIndex: -1,
				PrevLogTerm:  0,
				LeaderCommit: -1,
			}, &reply)
			if err != nil {
				t.Fatalf("AppendEntries на узле %d завершился с ошибкой: %v", i, err)
			}
		}
	}

	// Кластер сохранил лидера.
	h.CheckLeaderIs(leaderID)
}

// TestLatencyFsmApplyCountsBatches: проверка метрики fsmApply.
// При >= 2 применённых батчах получаем >= 2 замера: метрика считает применения,
// а не время жизни горутины. После Stop() новые замеры не появляются.
// Тест использует ручной запуск runFSM (паттерн raft_cm_apply_test.go),
// статистика не запущена — агрегат не обнуляется.
func TestLatencyFsmApplyCountsBatches(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := &countBatchesFSM{}
	cm := new(ConsensusModule)
	cm.fsm = fsm
	cm.fsmMutateCh = make(chan []*commitTuple, batchApplyBuffer)
	cm.shutdownCh = make(chan struct{})
	// runFSM регистрируется в cm.wg: Stop() становится барьером
	// (возврат Stop() гарантирует завершение runFSM).
	cm.goSpawn(cm.runFSM)
	// Идемпотентная очистка при любом исходе: t.Fatalf до явного Stop() не
	// должен оставлять горутину runFSM в живых (leaktest в defer).
	defer cm.Stop()

	// Два батча (N >= 2) — по одному замеру на каждый.
	for b := 0; b < 2; b++ {
		cm.fsmMutateCh <- []*commitTuple{{
			log:    &LogEntry{Index: b, Term: 1, Type: LogCommand, Data: b},
			future: nil,
		}}
	}

	// Ожидание выполняется по той же величине, о которой утверждение:
	// метрика observe выполняется в runFSM ПОСЛЕ возврата ApplyBatch,
	// поэтому «FSM применила 2 батча» ещё не влечёт «метрика насчитала 2».
	// Бюджет 2 с — запас к трем интервалам ApplyBatch по 1 мс.
	const metricBudget = 2 * time.Second
	err := waitCond(
		"fsmApply.count >= 2",
		metricBudget,
		func() bool { return cm.latency.fsmApply.count.Load() >= 2 },
		func() string {
			return fmt.Sprintf(
				"fsmApply.count=%d fsmApply.sumUs=%d fsm.appliedCount=%d",
				cm.latency.fsmApply.count.Load(), cm.latency.fsmApply.sumUs.Load(), fsm.appliedCount(),
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// T9: count >= 2 и sumUs > 0 — на литеральном CM (без stats).
	if got := cm.latency.fsmApply.count.Load(); got < 2 {
		t.Fatalf("fsmApply.count = %d, want >= 2 (замер на каждый батч)", got)
	}
	if got := cm.latency.fsmApply.sumUs.Load(); got <= 0 {
		t.Fatalf("fsmApply.sumUs = %d, want > 0", got)
	}
	// FSM действительно применила оба батча (отдельное утверждение после
	// ожидания метрики).
	if got := fsm.appliedCount(); got < 2 {
		t.Fatalf("fsm.appliedCount = %d, want >= 2", got)
	}

	// T9b: после Stop() новых замеров не появляется. runFSM зарегистрирована
	// в cm.wg, поэтому возврат Stop() — барьер: wg.Wait() завершён, runFSM
	// вышла, значение счётчика зафиксировано. Одного сравнения достаточно,
	// окно стабильности не нужно.
	countBefore := cm.latency.fsmApply.count.Load()
	appliedBefore := fsm.appliedCount()
	cm.Stop()
	if got := fsm.appliedCount(); got != appliedBefore {
		t.Fatalf("батч применён после Stop(): fsm.appliedCount %d -> %d", appliedBefore, got)
	}
	if got := cm.latency.fsmApply.count.Load(); got != countBefore {
		t.Fatalf("fsmApply.count изменился после Stop(): %d -> %d", countBefore, got)
	}
}

// countBatchesFSM — тестовая BatchingFSM, считающая применённые батчи.
// Используется для проверки, что fsmApply измеряет применение батча,
// а не время жизни горутины runFSM.
type countBatchesFSM struct {
	mu      sync.Mutex
	applied int
}

// ApplyBatch увеличивает счётчик применённых батчей. Задержка 1 мс делает
// замер измеримым: без неё применение тривиального батча быстрее 1 мкс,
// и оба замера усекаются до 0 мкс, что не позволяет проверить sumUs > 0.
// Потокобезопасно: вызывается из горутины runFSM, читается из горутины
// теста.
func (f *countBatchesFSM) ApplyBatch(logs []*LogEntry) []any {
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(time.Millisecond)
	f.mu.Lock()
	f.applied++
	f.mu.Unlock()
	return make([]any, len(logs))
}

// appliedCount возвращает число применённых батчей.
func (f *countBatchesFSM) appliedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied
}

// Apply не используется: countBatchesFSM реализует BatchingFSM, поэтому
// runFSM вызывает только ApplyBatch.
func (f *countBatchesFSM) Apply(*LogEntry) any { return nil }

// Snapshot — заглушка; в T9 снимки не используются.
func (f *countBatchesFSM) Snapshot() (FSMSnapshot, error) { return nil, ErrNotImplemented }

// Restore — заглушка; в T9 восстановление не используется.
func (f *countBatchesFSM) Restore(io.ReadCloser) error { return ErrNotImplemented }
