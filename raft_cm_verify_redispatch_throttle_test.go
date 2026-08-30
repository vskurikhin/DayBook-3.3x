package raft

// Тесты троттлинга немедленной перерассылки AppendEntries (окно
// verifyRedispatchMinInterval) — критерии AC-2…AC-5 задачи. Механизм:
// после фактической перерассылки соседу ставится метка окна; повторная
// перерассылка в пределах интервала подавляется (счётчик
// verifyRedispatchSuppressed), после истечения — возобновляется (счётчик
// verifyRedispatched). Окно не переживает смену лидерства, а частота
// перерассылок ограничена по построению.

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

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
