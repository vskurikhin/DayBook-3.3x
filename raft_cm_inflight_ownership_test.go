package raft

import (
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// Тесты инвариантов владения флагом inflightAE и монотонности индексов
// репликации: на одного соседа не более одной живой горутины репликации
// при любом пути вызова, и leaderState.nextIndex/matchIndex не уменьшаются
// поздними ответами старых отправок.

// TestApplyAESuccessLocked_MonotonicIndices проверяет монотонность продвижения
// индексов репликации в leaderState: поздний ответ более старой отправки с
// меньшим ni+sentEntries не уменьшает nextIndex и matchIndex соседа.
//
// Сценарий:
//  1. Сосед 1: nextIndex=10, matchIndex=5.
//  2. Успешный ответ свежей отправки (ni=10, sentEntries=2) → nextIndex=12,
//     matchIndex=11.
//  3. Успешный ответ более старой отправки (ni=10, sentEntries=1) → новые
//     значения (11) меньше текущих; продвижение только вперёд — они не
//     применяются.
//
// Значения читаются напрямую из leaderState; commitmentTracker для проверки
// не используется, поскольку он фильтрует убывающие значения самостоятельно
// и не различает исправленный и неисправленный код.
//
// До изменения (безусловное присваивание nextIndex = ni + sentEntries) шаг 3
// уменьшил бы nextIndex до 11 и matchIndex до 10 — тест падает.
func TestApplyAESuccessLocked_MonotonicIndices(t *testing.T) {
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: 10},
			matchIndex: map[int]int{1: 5},
		},
		cmState: cmState{
			state:             Leader,
			currentTerm:       1,
			lastLogIndex:      11,
			lastLogTerm:       1,
			lastSnapshotIndex: -1,
			lastSnapshotTerm:  -1,
			commitIndex:       5,
			termIndexMap:      map[int]int{1: 11},
			log: []LogEntry{
				{Index: 6, Term: 1},
				{Index: 7, Term: 1},
				{Index: 8, Term: 1},
				{Index: 9, Term: 1},
				{Index: 10, Term: 1},
				{Index: 11, Term: 1},
			},
			configurations: configurations{
				committed: Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
				latest:    Configuration{ConfigServers: []ConfigServer{{ID: 0, Suffrage: Voter}, {ID: 1, Suffrage: Voter}}},
			},
		},
		id: 0,
	}
	cm.leaderState.commitmentTracker = newCommitmentTracker(cm.id, 1, 0, make(chan int, 1))
	cm.leaderState.commitmentTracker.setConfiguration([]int{0, 1}, cm.lookupTermLocked)

	// Успешный ответ свежей отправки: ni=10, sentEntries=2 → nextIndex=12, matchIndex=11.
	cm.applyAESuccessLocked(1, 10, 2)
	if cm.leaderState.nextIndex[1] != 12 || cm.leaderState.matchIndex[1] != 11 {
		t.Fatalf("после свежей отправки nextIndex=%d matchIndex=%d, want 12 и 11",
			cm.leaderState.nextIndex[1], cm.leaderState.matchIndex[1])
	}

	// Поздний ответ старой отправки: ni=10, sentEntries=1 → newNextIndex=11 (< 12).
	// Продвижение только вперёд: индексы не должны уменьшиться.
	cm.applyAESuccessLocked(1, 10, 1)
	if cm.leaderState.nextIndex[1] != 12 || cm.leaderState.matchIndex[1] != 11 {
		t.Fatalf("поздний ответ старой отправки уменьшил индексы: nextIndex=%d matchIndex=%d, want 12 и 11",
			cm.leaderState.nextIndex[1], cm.leaderState.matchIndex[1])
	}
}

// TestInflightAE_NonOwnerDoesNotClearFlag проверяет, что горутина без владения
// флагом inflightAE не снимает чужой флаг: сброс выполняется только
// владельцем (ownsInflight=true).
//
// Это прямой регресс на дефект, когда defer безусловно обнулял флаг,
// принадлежащий параллельной горутине, запущенной другим путём вызова.
//
// Сценарий:
//  1. Флаг соседа 1 установлен (занят живой горутиной).
//  2. Вызов leaderSendAEsToPeer с ownsInflight=false (имитация прямого
//     вызова без захвата): после завершения флаг остаётся установленным.
//  3. Вызов leaderSendAEsToPeer с ownsInflight=true: владелец снимает флаг.
func TestInflightAE_NonOwnerDoesNotClearFlag(t *testing.T) {
	cm := newRejectionTestCM(&mockTransportAE{})

	// Флаг занят «чужой» горутиной.
	cm.leaderState.inflightAE[1].Store(true)

	// Горутина без владения завершается: она не должна снимать чужой флаг.
	cm.leaderSendAEsToPeer(1, 1, 0, false)
	if !cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = false, want true (не-владелец снял чужой флаг)")
	}

	// Владелец флаг снимает на каждом выходе.
	cm.leaderState.inflightAE[1].Store(false)
	cm.leaderSendAEsToPeer(1, 1, 0, true)
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (владелец не снял флаг)")
	}
}

// TestInflightAE_SingleReplicationGoroutineUnderConcurrentPaths проверяет
// инвариант «на одного соседа не более одной живой горутины репликации» при
// одновременном обращении к рассылке лидера (leaderSendAEs) и к догоняющему
// пути передачи лидерства (leaderSendAEsToPeerIfIdle).
//
// Сценарий:
//  1. Первая отправка через догоняющий путь захватывает флаг и блокируется
//     в транспорте — горутина репликации на соседа 1 остаётся живой.
//  2. Пока она жива, конкурентно вызываются leaderSendAEs (8 горутин) и
//     leaderSendAEsToPeerIfIdle (8 горутин). Все попытки видят занятый флаг
//     и не должны породить второго отправителя.
//  3. Проверяется, что AppendEntries вызван ровно один раз (число входов в
//     leaderSendAEsToPeer без завершения предыдущей не превышает 1).
//  4. Транспорт разблокируется: владеющая горутина завершается и снимает
//     флаг.
func TestInflightAE_SingleReplicationGoroutineUnderConcurrentPaths(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	mock := &mockTransportAE{blockCh: make(chan struct{})}
	cm := newRejectionTestCM(mock)

	// Первая отправка захватывает флаг и блокируется в транспорте.
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	deadline := time.Now().Add(_inmemRPCTimeout)
	for mock.callCount.Load() == 0 {
		if !time.Now().Before(deadline) {
			t.Fatalf("AppendEntries не вызван за %v", _inmemRPCTimeout)
		}
		time.Sleep(_pollInterval)
	}

	// Пока первая горутина жива, конкурентные пути вызова не порождают
	// второго отправителя соседу 1: все CAS видят занятый флаг.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cm.leaderSendAEs()
		}()
		go func() {
			defer wg.Done()
			cm.leaderSendAEsToPeerIfIdle(1, 1)
		}()
	}
	wg.Wait()

	if calls := mock.callCount.Load(); calls != 1 {
		t.Fatalf("AppendEntries calls = %d, want 1 (на соседа активна ровно одна горутина репликации)", calls)
	}

	// Разблокировка: владеющая горутина завершается и снимает флаг.
	close(mock.blockCh)
	cm.wg.Wait()
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (флаг снимается владельцем на каждом выходе)")
	}
}

// TestLeadershipTransfer_UnderConcurrentReplicationLoad проверяет, что передача
// лидерства завершается успешно под конкурентной нагрузкой рассылки лидера
// (leaderSendAEs), без утечек горутин (leaktest).
//
// Под нагрузкой CAS в догоняющем цикле передачи чаще не проходит — догоняющая
// репликация идёт штатным путём, и передача доводится в пределах своего
// таймаута.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбраны лидер и целевой ведомый.
//  2. Команды зафиксированы.
//  3. Запускается конкурентная нагрузка leaderSendAEs на лидере.
//  4. Выполняется передача лидерства целевому узлу.
//  5. Проверяется, что передача успешна и новый лидер — целевой узел.
func TestLeadershipTransfer_UnderConcurrentReplicationLoad(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3
	if targetID == origLeaderID {
		t.Fatal("target is the leader, need a follower")
	}

	// Команды, которые нужно реплицировать целевому узлу.
	h.SubmitToServer(origLeaderID, 5)
	h.SubmitToServer(origLeaderID, 6)
	h.WaitForCommit(6, 3)

	// Конкурентная нагрузка рассылки лидера во время передачи.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.cluster[origLeaderID].leaderSendAEs()
				}
			}
		}()
	}

	// Передача лидерства под нагрузкой.
	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
	err := future.Error()
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("LeadershipTransfer failed under concurrent replication load: %v", err)
	}

	newLeaderID, _ := h.CheckSingleLeader()
	if newLeaderID != targetID {
		t.Errorf("new leader = %d, want %d", newLeaderID, targetID)
	}
}
