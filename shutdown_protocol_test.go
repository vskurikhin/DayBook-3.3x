package raft

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// stressIterationBudget — граница одной итерации стресс-тестов:
// нормальная итерация укладывается в секунды; превышение означает
// зависание (deadlock), которое по AC обязано быть FAIL
// (deadlock проявляется зависанием, а не паникой).
const stressIterationBudget = 30 * time.Second

// TestStopRepeatedAndParallel — постусловие идемпотентного Stop()
// любой возврат из Stop(), включая повторный и параллельный,
// гарантирует завершение всех горутин CM.
func TestStopRepeatedAndParallel(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Параллельный Stop() одного узла: sync.Once.Do блокирует
	// остальных вызывающих до завершения первого — каждый возврат
	// следует за полным join.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.cluster[0].Stop()
		}()
	}
	wg.Wait()
	// Повторный Stop() после join — идемпотентный вызов без зависания.
	h.cluster[0].Stop()
}

// TestStopDuringReplication — стресс: Stop() одновременно с активной
// репликацией/выборами. Прогоняется через -count=100; нет паники
// WaitGroup и зависаний (критерий завершения — R-N11).
func TestStopDuringReplication(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Активная репликация: непрерывная подача команд лидеру.
	stopSubmitting := make(chan struct{})
	var submitWG sync.WaitGroup
	submitWG.Add(1)
	go func() {
		defer submitWG.Done()
		for i := 0; ; i++ {
			select {
			case <-stopSubmitting:
				return
			default:
			}
			h.SubmitToServer(lid, i)
		}
	}()

	// Дать репликации разойтись, затем остановить ВСЕ узлы параллельно —
	// Join на фоне активных выборов/репликации не должен ни паниковать,
	// ни зависать.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(50)
	var wg sync.WaitGroup
	for i := 0; i < h.n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			h.cluster[id].Stop()
		}(i)
	}
	wg.Wait()
	close(stopSubmitting)
	submitWG.Wait()
}

// TestHarnessShutdownCrashRestartStress — стресс 100×
// NewHarness→Shutdown и CrashPeer→RestartPeer→Shutdown. Прогон обязан
// завершаться в пределах таймаута теста; зависание = FAIL (R-N11 —
// deadlock по / проявляется зависанием, а не паникой).
func TestHarnessShutdownCrashRestartStress(t *testing.T) {
	const iterations = 100
	for i := 0; i < iterations; i++ {
		done := make(chan struct{})
		go func(i int) {
			defer close(done)
			h := NewHarness(t, 3)
			lid := h.waitLeader()
			if lid < 0 {
				return
			}
			h.SubmitToServer(lid, i)
			h.CrashPeer((lid + 1) % 3)
			h.RestartPeer((lid + 1) % 3)
			h.Shutdown()
		}(i)

		select {
		case <-done:
		case <-time.After(stressIterationBudget):
			t.Fatalf("stress iteration %d did not complete within %v: hang = FAIL", i, stressIterationBudget)
		}
	}
}

// TestJoinFiniteWhenReceiverStopped — порядок Stop/Close:
// CM-получатель остановлен, его транспорт открыт; отправитель
// шлёт AE. Enqueue-timeout делает блокировку отправителя конечной
// (≤ t.timeout), поэтому join в Stop() конечен при любом порядке вызовов.
func TestJoinFiniteWhenReceiverStopped(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 2)
	defer h.Shutdown()

	// Получатель остановлен (join завершён — runRPCReader вышел),
	// транспорт при этом открыт.
	h.cluster[1].Stop()

	start := time.Now()
	_, err := h.transports[0].AppendEntries(ServerID(1), AppendEntriesArgs{})
	if !errors.Is(err, ErrEnqueueTimeout) {
		t.Fatalf("AppendEntries: got %v, want ErrEnqueueTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*_inmemRPCTimeout {
		t.Fatalf("enqueue blocked for %v, want <= %v", elapsed, 2*_inmemRPCTimeout)
	}

	// Join отправителя конечен.
	h.cluster[0].Stop()
}

// TestCrashPeerQuiesce — CrashPeer + in-flight commit
// после CrashPeer h.commits[id] пуст и остаётся пустым —
// коллектор старой incarnation присоединён до очистки, поэтому
// «висячая» запись в полёте не может попасть в срез после очистки.
func TestCrashPeerQuiesce(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.waitLeader()
	if lid < 0 {
		t.Fatal("leader not found")
	}

	// Подать команды и немедленно уронить follower — фиксация может быть
	// в полёте к нему в момент CrashPeer.
	victim := (lid + 1) % 3
	for i := 0; i < 10; i++ {
		h.SubmitToServer(lid, i)
	}
	h.CrashPeer(victim)

	checkEmpty := func(phase string) {
		t.Helper()
		h.mu.Lock()
		n := len(h.commits[victim])
		h.mu.Unlock()
		if n != 0 {
			t.Fatalf("%s: commits[%d] has %d entries, want 0", phase, victim, n)
		}
	}
	checkEmpty("immediately after CrashPeer")

	// Повторная проверка после короткого окна: инвариант «пуст и
	// остаётся пустым» — никакая запись, принятая старым коллектором
	// до закрытия канала, не дописывается позже.
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(100)
	checkEmpty("after window")
}

// waitLeader — poll Report() всех подключённых узлов до появления лидера.
// Не вызывает t.Fatal (в отличие от CheckSingleLeader) — используется из
// вспомогательных горутин стресс-тестов; недостижимость условия
// выражается возвратом -1.
func (h *Harness) waitLeader() int {
	deadline := time.Now().Add(_commitBudgetAfterFailover)
	for time.Now().Before(deadline) {
		for i := 0; i < h.n; i++ {
			if h.connected[i] {
				_, _, isLeader := h.cluster[i].Report()
				if isLeader {
					return i
				}
			}
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
	return -1
}
