package raft

import (
	"errors"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// TestLeaderApplyBatch_GroupCommitDrainsQueuedCommands — выборка group-commit
// кейса команд клиента: команды, накопившиеся в канале до того, как цикл
// лидера взял первую, попадают в журнал одной группой, а не по одной.
//
// Наблюдаемые следствия одной диспетчеризации на всю группу: канал команд
// разобран целиком одним вызовом обработчика; индексы записей идут подряд;
// все записи несут общую отметку диспетчеризации — её проставляет один
// вызов dispatchLogs на весь срез.
//
// Приспособление: лидер с одним соседом и буферизованным каналом команд.
// Буфер позволяет накопить очередь детерминированно; цикл лидера не
// запущен, его кейс вызывается напрямую — ровно так, как это делает select.
func TestLeaderApplyBatch_GroupCommitDrainsQueuedCommands(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// queued — больше одной команды и заведомо меньше верхней границы
	// выборки leaderBatchSize.
	const queued = 3

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	cm.applyCh = make(chan *logFuture, leaderBatchSize)
	defer cm.Stop()

	heartbeatTicker := time.NewTicker(HeartbeatTimeoutMs * time.Millisecond)
	defer heartbeatTicker.Stop()

	futures := make([]*logFuture, 0, queued)
	for i := 0; i < queued; i++ {
		queuedFuture, ok := cm.Apply(i, time.Second).(*logFuture)
		if !ok {
			t.Fatalf("Apply(%d) не поставил команду в очередь", i)
		}
		futures = append(futures, queuedFuture)
	}
	if got := len(cm.applyCh); got != queued {
		t.Fatalf("в канале команд %d команд(ы), want %d", got, queued)
	}

	cm.mu.Lock()
	firstIndex := cm.cmState.lastLogIndex + 1
	cm.mu.Unlock()

	// Первую команду забирает кейс select — здесь её забирает тест.
	first := <-cm.applyCh
	if !cm.handleLeaderApplyBatch(first, heartbeatTicker) {
		t.Fatal("handleLeaderApplyBatch = false, want true (лидерство не терялось)")
	}

	if got := len(cm.applyCh); got != 0 {
		t.Fatalf(
			"в канале команд осталось %d команд(ы), want 0: выборка group-commit не разобрала очередь",
			got,
		)
	}
	for i, f := range futures {
		if want := firstIndex + i; f.Index() != want {
			t.Fatalf(
				"команда %d получила индекс %d, want %d: индексы группы не идут подряд",
				i, f.Index(), want,
			)
		}
		if !f.dispatch.Equal(futures[0].dispatch) {
			t.Fatalf(
				"команда %d диспетчеризована отдельно (%v != %v): группа записана не одной записью",
				i, f.dispatch, futures[0].dispatch,
			)
		}
	}

	cm.mu.Lock()
	lastLogIndex := cm.cmState.lastLogIndex
	inflightCount := len(cm.leaderState.inflight)
	cm.mu.Unlock()
	if want := firstIndex + queued - 1; lastLogIndex != want {
		t.Errorf("lastLogIndex = %d, want %d", lastLogIndex, want)
	}
	if inflightCount != queued {
		t.Errorf("ожидающих записей журнала %d, want %d", inflightCount, queued)
	}
}

// TestLeaderApplyBatch_NotLeaderStopsLoop — карта выходов кейса команд
// клиента: обработчик, обнаруживший, что узел больше не лидер, отвечает
// клиенту ErrNotLeader и требует завершения цикла лидера.
//
// Приспособление воспроизводит достижимое окно: постановка команды в канал
// и её разбор циклом лидера — разные критические секции, поэтому шаг вниз
// (потеря контакта с кворумом или более высокий терм из ответа) успевает
// произойти между ними, и обработчик получает команду, будучи ведомым.
func TestLeaderApplyBatch_NotLeaderStopsLoop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	cm.applyCh = make(chan *logFuture, 1)
	defer cm.Stop()

	heartbeatTicker := time.NewTicker(HeartbeatTimeoutMs * time.Millisecond)
	defer heartbeatTicker.Stop()

	queuedFuture, ok := cm.Apply(1, time.Second).(*logFuture)
	if !ok {
		t.Fatal("Apply не поставил команду в очередь")
	}

	// Шаг вниз уже после постановки команды в очередь.
	cm.mu.Lock()
	cm.becomeFollowerLocked(cm.cmState.currentTerm)
	logLenBefore := len(cm.cmState.log)
	cm.mu.Unlock()

	first := <-cm.applyCh
	if cm.handleLeaderApplyBatch(first, heartbeatTicker) {
		t.Fatal("handleLeaderApplyBatch = true, want false (лидерство потеряно — цикл завершается)")
	}
	if err := waitFuture(t, queuedFuture, time.Second); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ошибка команды = %v, want %v", err, ErrNotLeader)
	}

	cm.mu.Lock()
	logLenAfter := len(cm.cmState.log)
	cm.mu.Unlock()
	if logLenAfter != logLenBefore {
		t.Errorf(
			"длина журнала = %d, want %d: команда не-лидера в журнал не попадает",
			logLenAfter, logLenBefore,
		)
	}
}

// TestLeaderCommitAdvance_SelfRemovalStopsLoop — карта выходов кейса
// фиксации: лидер, чьё собственное удаление из состава голосующих оказалось
// зафиксированным, шагает вниз, учитывает это в счётчике шагов вниз и
// требует завершения цикла лидера.
//
// Приспособление воспроизводит достижимое состояние: запись об удалении
// самого лидера принята в журнал (актуальная конфигурация без него,
// её индекс — latestIndex), а по каналу фиксации приходит индекс этой
// записи.
func TestLeaderCommitAdvance_SelfRemovalStopsLoop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := newRedispatchLeaderCM(&mockTransportAE{})
	defer cm.Stop()

	// removalIndex — индекс записи о собственном удалении лидера.
	const removalIndex = 1

	cm.mu.Lock()
	cm.cmState.configurations.committedIndex = 0
	cm.cmState.configurations.latest = Configuration{
		ConfigServers: []ConfigServer{{ID: 1, Suffrage: Voter}},
	}
	cm.cmState.configurations.latestIndex = removalIndex
	cm.mu.Unlock()

	if cm.handleLeaderCommitAdvance(removalIndex) {
		t.Fatal(
			"handleLeaderCommitAdvance = true, want false " +
				"(лидер вне зафиксированной конфигурации — цикл завершается)",
		)
	}

	cm.mu.Lock()
	state := cm.cmState.state
	commitIndex := cm.cmState.commitIndex
	stillVoter := hasVote(cm.cmState.configurations.committed, cm.id)
	cm.mu.Unlock()

	if state != Follower {
		t.Errorf("роль = %v, want Follower", state)
	}
	if commitIndex != removalIndex {
		t.Errorf("индекс фиксации = %d, want %d", commitIndex, removalIndex)
	}
	if stillVoter {
		t.Error("лидер остался голосующим зафиксированной конфигурации, want удалён")
	}
	if got := cm.counters.stepDowns.configExit.Load(); got != 1 {
		t.Errorf("счётчик шагов вниз по выходу из конфигурации = %d, want 1", got)
	}
}
