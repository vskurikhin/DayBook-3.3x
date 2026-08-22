package raft

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

const (
	// applyLatencySamples — число замеров промежутка «фиксация →
	// применение к машине состояний» в тесте задержки применения.
	applyLatencySamples = 10

	// applyLatencyBudget — предел медианы промежутка «фиксация →
	// применение». Заметно меньше интервала страховочного тика
	// (applyBatchInterval): при применении по тику ожидание составляло бы
	// в среднем около половины интервала.
	applyLatencyBudget = 20 * time.Millisecond

	// commitPollInterval — шаг опроса индекса фиксации ведущего узла
	// при поиске момента фиксации записи.
	commitPollInterval = 20 * time.Microsecond
)

// medianDuration возвращает медиану выборки; выборка сортируется на месте.
func medianDuration(samples []time.Duration) time.Duration {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[len(samples)/2]
}

// TestApplyOnCommit_LatencyBelowTickInterval — AC-1: зафиксированная
// запись применяется к машине состояний сразу, без ожидания страховочного
// тика.
//
// Измеряется именно промежуток «фиксация → применение». Полное время
// вызова Apply для этой проверки не годится: в кластере из нескольких
// узлов оно включает репликацию, задержка которой ограничена периодом
// пульса и к применению отношения не имеет. Одноузловой кластер тоже не
// подходит: там фиксация наступает прямо в dispatchLogs, и ветка канала
// фиксации в цикле лидера не задействуется.
//
// Момент фиксации ловит опрашивающая горутина, следящая за индексом
// фиксации ведущего узла. Опрос может застать индекс уже после того, как
// запись применена: такой замер считается нулевым.
func TestApplyOnCommit_LatencyBelowTickInterval(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	samples := make([]time.Duration, 0, applyLatencySamples)
	for i := range applyLatencySamples {
		samples = append(samples, commitToApplyLatency(t, leader, 7000+i))
	}

	median := medianDuration(samples)
	t.Logf(
		"commit-to-apply latency: median=%v min=%v max=%v",
		median, samples[0], samples[len(samples)-1],
	)
	if median >= applyLatencyBudget {
		t.Errorf(
			"median commit-to-apply latency = %v, want < %v (apply must not wait for the safety tick)",
			median, applyLatencyBudget,
		)
	}
}

// commitToApplyLatency подаёт команду cmd ведущему узлу и возвращает время,
// прошедшее от фиксации записи до ответа обещания, то есть до применения
// записи к машине состояний. Замеры, в которых опрос застал индекс фиксации
// уже после применения, возвращаются нулевыми.
func commitToApplyLatency(t *testing.T, leader *ConsensusModule, cmd int) time.Duration {
	t.Helper()

	leader.mu.Lock()
	target := leader.cmState.lastLogIndex + 1
	leader.mu.Unlock()

	committed := make(chan time.Time, 1)
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			leader.mu.Lock()
			reached := leader.cmState.commitIndex >= target
			leader.mu.Unlock()
			if reached {
				committed <- time.Now()
				return
			}
			time.Sleep(commitPollInterval)
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()

	if err := leader.Apply(cmd, time.Second).Error(); err != nil {
		t.Fatalf("Apply %d: %v", cmd, err)
	}
	appliedAt := time.Now()

	select {
	case commitAt := <-committed:
		if d := appliedAt.Sub(commitAt); d > 0 {
			return d
		}
		return 0
	case <-time.After(leaderElectionBudget):
		t.Fatalf("commit of entry %d was not observed", target)
		return 0
	}
}

// TestApplyOnCommit_BatchingPreserved — AC-3: применение остаётся
// групповым — число вызовов ApplyBatch меньше числа записей.
func TestApplyOnCommit_BatchingPreserved(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const commands = 200
	fsm := NewRecordingBatchingFSM()
	cm := testServerWithFSM(t, fsm)
	defer cm.Stop()

	waitForLeader(t, cm, leaderElectionBudget)

	var wg sync.WaitGroup
	for i := 0; i < commands; i++ {
		wg.Add(1)
		go func(cmd int) {
			defer wg.Done()
			if err := cm.Apply(cmd, time.Second).Error(); err != nil {
				t.Errorf("Apply %d: %v", cmd, err)
			}
		}(i)
	}
	wg.Wait()

	if got := fsm.TotalEntries(); got != commands {
		t.Fatalf("TotalEntries = %d, want %d", got, commands)
	}
	if got := fsm.NumBatches(); got >= commands {
		t.Errorf("NumBatches = %d, want < %d (batching must be preserved)", got, commands)
	}
}

// TestApplyOnCommit_SafetyTickApplies — AC-4: если индекс фиксации
// продвинулся без уведомления в канал фиксации, записи всё равно
// применяются в пределах одного интервала страховочного тика.
//
// Потерянное уведомление воспроизводится отступлением отметки
// диспетчеризации lastApplied на одну запись: ветка канала фиксации при
// этом не срабатывает, и единственный оставшийся путь применения — тик.
func TestApplyOnCommit_SafetyTickApplies(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	leader := h.cluster[leaderID]

	cmd := 8100
	if err := leader.Apply(cmd, time.Second).Error(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	h.WaitForCommit(cmd, 3)

	countApplied := func() int {
		h.mu.Lock()
		defer h.mu.Unlock()
		n := 0
		for _, entry := range h.commits[leaderID] {
			if entry.Data == cmd {
				n++
			}
		}
		return n
	}
	if got := countApplied(); got != 1 {
		t.Fatalf("applied %d times before the rewind, want 1", got)
	}

	leader.mu.Lock()
	leader.cmState.lastApplied--
	leader.mu.Unlock()

	if err := waitCond(
		"entry is re-applied by the safety tick",
		2*applyBatchInterval+leaderElectionBudget,
		func() bool { return countApplied() == 2 },
		func() string { return "applied count = " + itoa(countApplied()) },
	); err != nil {
		t.Fatal(err)
	}
}

// TestApplyOnCommit_ConcurrentApplyWithLeaderChange — AC-2: параллельные
// команды во время смены лидера не приводят к гонкам и зависаниям.
// Проверяется под детектором гонок в общем прогоне пакета.
func TestApplyOnCommit_ConcurrentApplyWithLeaderChange(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderID, _ := h.CheckSingleLeader()
	target := (leaderID + 1) % 3

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(cmd int) {
			defer wg.Done()
			// Ошибки закономерны: во время передачи лидерства команды
			// отклоняются. Предмет проверки — отсутствие гонок и зависаний.
			future := h.cluster[leaderID].Apply(cmd, time.Second)
			select {
			case <-future.ErrorCh():
			case <-time.After(leaderElectionBudget):
			}
		}(9000 + i)
	}

	future := h.LeadershipTransfer(leaderID, ServerID(target))
	select {
	case <-future.ErrorCh():
	case <-time.After(leaderElectionBudget):
		t.Error("LeadershipTransfer did not complete")
	}
	wg.Wait()

	h.WaitForSingleLeader(leaderElectionBudget)
}
