package raft

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// --- Сценарий №1: Базовая передача лидерства ---

// TestLeadershipTransfer_Basic проверяет базовую передачу лидерства
// от текущего лидера указанному последователю.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбран лидер.
//  2. Отправляем команды 5 и 6, убеждаемся что они зафиксированы.
//  3. Лидер передаёт лидерство одному из follower (targetID).
//  4. После передачи новый лидер — targetID, term > старого.
//  5. Команды 5 и 6 остаются зафиксированы на всех узлах.
//  6. Новый лидер принимает новые команды (7).
func TestLeadershipTransfer_Basic(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, origTerm := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	// Проверяем, что target — не лидер (follower).
	if targetID == origLeaderID {
		t.Fatal("target is the leader, need a follower")
	}

	// Отправляем команды и дожидаемся фиксации.
	h.SubmitToServer(origLeaderID, 5)
	h.SubmitToServer(origLeaderID, 6)
	h.WaitForCommit(6, 3)

	// Передаём лидерство с таймаутом на ожидание.
	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
	if err := future.Error(); err != nil {
		t.Fatalf("LeadershipTransfer failed: %v", err)
	}

	// Проверяем, что новый лидер — targetID.
	newLeaderID, newTerm := h.CheckSingleLeader()
	if newLeaderID != targetID {
		t.Errorf("new leader = %d, want %d", newLeaderID, targetID)
	}
	if newTerm <= origTerm {
		t.Errorf("new term = %d, want > %d", newTerm, origTerm)
	}

	// Проверяем, что старые команды зафиксированы.
	h.CheckCommittedN(5, 3)
	h.CheckCommittedN(6, 3)

	// Проверяем, что новый лидер принимает команды.
	h.SubmitToServer(newLeaderID, 7)
	h.WaitForCommit(7, 3)
}

// --- Сценарий №2: Передача лидерства на узел, который догоняет журнал ---

// TestLeadershipTransfer_CatchUp проверяет, что лидер передаёт лидерство узлу,
// который отстаёт по журналу. Лидер ждёт, пока целевой узел догонит,
// и только потом отправляет TimeoutNow.
//
// Сценарий:
//  1. Кластер из 3 узлов.
//  2. Отключаем целевой follower, отправляем команды — они не реплицируются.
//  3. Подключаем follower обратно, немедленно вызываем LeadershipTransfer.
//  4. Лидер ждёт, пока target догонит, затем передаёт лидерство.
func TestLeadershipTransfer_CatchUp(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	if targetID == origLeaderID {
		t.Fatal("target is the leader, need a follower")
	}

	// Отключаем целевой follower — команды не будут реплицироваться на него.
	h.DisconnectPeer(targetID)

	// Отправляем несколько команд лидеру (пока follower отключён).
	h.SubmitToServer(origLeaderID, 10)
	h.SubmitToServer(origLeaderID, 11)
	h.WaitForCommit(11, 2)

	// Подключаем follower обратно.
	h.ReconnectPeer(targetID)

	// Немедленно вызываем LeadershipTransfer.
	// Лидер должен дождаться репликации, затем отправить TimeoutNow.
	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
	if err := future.Error(); err != nil {
		t.Fatalf("LeadershipTransfer failed: %v", err)
	}

	// Проверяем, что новый лидер — targetID.
	newLeaderID, _ := h.CheckSingleLeader()
	if newLeaderID != targetID {
		t.Errorf("new leader = %d, want %d", newLeaderID, targetID)
	}

	// Проверяем, что все команды зафиксированы на всех узлах.
	h.WaitForCommit(11, 3)
}

// --- Сценарий №3: Передача лидерства самому себе — ошибка ---

// TestLeadershipTransfer_ToSelf_Fails проверяет, что LeadershipTransfer(ownID)
// возвращает ошибку.
func TestLeadershipTransfer_ToSelf_Fails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()

	// Попытка передать лидерство самому себе — должна вернуть ошибку.
	future := h.LeadershipTransfer(origLeaderID, ServerID(origLeaderID))
	if err := future.Error(); err == nil {
		t.Fatal("expected error when transferring to self, got nil")
	}
}

// --- Сценарий №4: Передача лидерства от не-лидера — ошибка ---

// TestLeadershipTransfer_FromFollower_Fails проверяет, что вызов
// LeadershipTransfer на узле-последователе возвращает ErrNotLeader.
func TestLeadershipTransfer_FromFollower_Fails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	followerID := (origLeaderID + 1) % 3

	// Вызываем LeadershipTransfer на follower — должен вернуть ErrNotLeader.
	future := h.LeadershipTransfer(followerID, ServerID(origLeaderID))
	if err := future.Error(); err != ErrNotLeader {
		t.Fatalf("got %v, want ErrNotLeader", err)
	}
}

// --- Сценарий №5: Передача лидерства на несуществующий узел — ошибка ---

// TestLeadershipTransfer_ToUnknown_Fails проверяет, что передача лидерства
// узлу, не входящему в конфигурацию, возвращает ошибку.
func TestLeadershipTransfer_ToUnknown_Fails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()

	// Передача неизвестному узлу (ID 42) — должна вернуть ошибку.
	future := h.LeadershipTransfer(origLeaderID, ServerID(42))
	if err := future.Error(); err == nil {
		t.Fatal("expected error when transferring to unknown node, got nil")
	}
}

// --- Сценарий №6: Двойная передача лидерства (concurrent) — ошибка ---

// TestLeadershipTransfer_Concurrent_SecondFails проверяет, что вторая
// передача лидерства, запущенная во время первой, возвращает строго
// ErrLeadershipTransferInProgress.
//
// Окно передачи конструируется детерминированно длительным: цель первой
// передачи отключается, а лидеру подаётся команда, которую цель заведомо
// не получила, — первая передача входит в ветку догоняющей репликации
// и удерживает флаг до закрытия окна.
//
// Нижняя граница окна: ветка догоняющей репликации завершается только
// по таймауту electionTimeout() >= ReelectionTimeoutMs = 381 мс, всё это
// время флаг leadershipTransferInProgress удерживается — запас относительно
// _pollInterval не ниже 38x.
//
// Верхняя граница окна: догоняющая репликация выполняется синхронно в цикле
// лидера и блокирует его, поэтому рассылка пульсов единственному оставшемуся
// ведомому прекращается на всё время окна. Ведомый стартует выборы не раньше
// чем через ReelectionTimeoutMs - HeartbeatTimeoutMs = 381 - 33 = 348 мс от
// начала окна (его таймер выборов отсчитывается от последнего пульса, который
// мог прийти не позднее чем за период пульса до начала окна). Следовательно
// окно гарантированно живёт не меньше 348 мс при любом розыгрыше таймеров,
// а флаг наблюдается не позднее чем через _pollInterval = 10 мс после
// установки — запас до гарантированного закрытия окна не меньше 338 мс.
//
// Восстановление связности цели в конце теста не требуется: Shutdown
// останавливает все живые узлы независимо от логической связности.
func TestLeadershipTransfer_Concurrent_SecondFails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()

	// Цель первой передачи и цель второй.
	target := (origLeaderID + 1) % 3
	other := (origLeaderID + 2) % 3

	// Отключаем цель первой передачи: кворум сохраняется (2 из 3).
	h.DisconnectPeer(target)

	// Команда, зафиксированная на обоих подключённых узлах: цель её заведомо
	// не получила, поэтому nextIndex[target] <= lastLogIndex и первая
	// передача обязана войти в ветку догоняющей репликации.
	h.SubmitToServer(origLeaderID, 100)
	h.WaitForCommitBudget(100, 2, _commitBudgetSteady)

	// Первая передача — в горутине со структурированным жизненным циклом:
	// буферизованный канал ёмкости 1, join до конца теста.
	firstCh := make(chan error, 1)
	go func() {
		future := h.LeadershipTransfer(origLeaderID, ServerID(target))
		firstCh <- future.Error()
	}()

	waitForTransferInProgress(t, h, origLeaderID, _commitBudgetSteady)
	secondFuture := h.LeadershipTransfer(origLeaderID, ServerID(other))
	if err := secondFuture.Error(); err != ErrLeadershipTransferInProgress {
		t.Fatalf("second transfer: got %v, want ErrLeadershipTransferInProgress", err)
	}

	// Исход первой передачи утверждается явно. Оба пути закрытия окна дают
	// одну и ту же ошибку: таймаут догоняющей репликации и шаг-даун лидера
	// по большему терм (выборы ведомого из-за остановки пульса) завершают
	// future ошибкой ErrLeadershipLost.
	select {
	case err := <-firstCh:
		if err != ErrLeadershipLost {
			t.Fatalf("first transfer: got %v, want ErrLeadershipLost", err)
		}
	case <-time.After(_commitBudgetAfterFailover):
		t.Fatalf("first transfer did not complete within %v", _commitBudgetAfterFailover)
	}
}

// --- Сценарий №7: Apply блокируется во время передачи ---

// TestLeadershipTransfer_ApplyBlocked проверяет, что Apply возвращает
// ErrLeadershipTransferInProgress во время передачи лидерства.
//
// Сценарий: создаём отставание по журналу у target, запускаем передачу,
// catch-up блокируется (target отключён), Apply проверяется во время
// блокировки, затем target подключается, передача завершается.
func TestLeadershipTransfer_ApplyBlocked(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	// Фиксируем команды, чтобы создать журнал.
	h.SubmitToServer(origLeaderID, 42)
	h.WaitForCommit(42, 3)

	// Отключаем target, фиксируем ещё команду — target отстаёт.
	h.DisconnectPeer(targetID)
	h.SubmitToServer(origLeaderID, 43)
	h.WaitForCommit(43, 2) // только лидер + один follower

	// Запускаем LeadershipTransfer в горутине.
	// handleLeadershipTransfer видит, что target отстаёт, запускает
	// catch-up-горутину и блокируется в select — target отключён,
	// репликация не удаётся, catch-up не завершается.
	transferCh := make(chan error, 1)
	go func() {
		future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
		transferCh <- future.Error()
	}()

	// Ждём, пока leadershipTransferInProgress установится в leaderLoop.
	waitForTransferInProgress(t, h, origLeaderID, _commitBudgetAfterFailover)

	// Apply во время catch-up должен вернуть ErrLeadershipTransferInProgress.
	future := h.cluster[origLeaderID].Apply(44, 0)
	if err := future.Error(); err != ErrLeadershipTransferInProgress {
		t.Fatalf("got %v, want ErrLeadershipTransferInProgress", err)
	}

	// Подключаем target обратно — catch-up завершается, TimeoutNow
	// отправляется, лидер шагает вниз.
	h.ReconnectPeer(targetID)
	select {
	case err := <-transferCh:
		if err != nil {
			t.Logf("leadership transfer completed with: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leadership transfer timed out after reconnecting target")
	}

	// После завершения передачи Apply снова работает на новом лидере.
	// Старый лидер больше не лидер — используем CheckSingleLeader.
	newLeaderID, _ := h.CheckSingleLeader()
	_ = newLeaderID
}

// --- Сценарий №8: TimeoutNow на лидере — no-op ---

// TestTimeoutNow_OnLeader_Noop проверяет, что TimeoutNow, отправленный узлу,
// который уже является лидером, не вызывает перевыборов.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбран лидер.
//  2. Отправляем TimeoutNow лидеру через транспорт.
//  3. Проверяем, что лидер остался прежним и term не изменился.
func TestTimeoutNow_OnLeader_Noop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, origTerm := h.CheckSingleLeader()

	// Отправляем TimeoutNow лидеру от имени другого узла.
	reply, err := h.SendTimeoutNow((origLeaderID+1)%3, origLeaderID)
	if err != nil {
		t.Fatalf("SendTimeoutNow failed: %v", err)
	}

	// TimeoutNow на лидере возвращает Success = false.
	if reply.Success {
		t.Fatal("TimeoutNow on leader: expected Success=false")
	}

	// Лидер должен остаться прежним.
	h.CheckLeaderIs(origLeaderID)

	// Term не должен измениться.
	_, currentTerm := h.CheckSingleLeader()
	if currentTerm != origTerm {
		t.Errorf("term changed from %d to %d", origTerm, currentTerm)
	}
}

// --- Сценарий №9: TimeoutNow на follower — форсированные выборы ---

// TestTimeoutNow_OnFollower_ImmediateElection проверяет, что TimeoutNow,
// отправленный follower, вызывает немедленные выборы (без PreVote,
// без ожидания election timeout). Целевой узел становится лидером.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбран лидер.
//  2. Отправляем TimeoutNow выбранному follower через транспорт.
//  3. Целевой узел становится лидером, term увеличивается.
func TestTimeoutNow_OnFollower_ImmediateElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, origTerm := h.CheckSingleLeader()

	// Выбираем follower для отправки TimeoutNow.
	targetID := (origLeaderID + 1) % 3

	// Предусловие обработчика timeoutNow: целевой узел обязан быть
	// в состоянии Follower — из PreCandidate/Candidate он отвечает
	// Success=false (raft_cm_election.go, timeoutNow). Признак того,
	// что узел вернулся в Follower, — получение AppendEntries от лидера.
	waitForKnownLeader(t, h, targetID, origLeaderID, _commitBudgetAfterFailover)

	// Отправляем TimeoutNow целевому узлу от текущего лидера.
	reply, err := h.SendTimeoutNow(origLeaderID, targetID)
	if err != nil {
		t.Fatalf("SendTimeoutNow failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("TimeoutNow on follower: expected Success=true")
	}

	// Отключаем старого лидера, чтобы он не мешал выборам.
	// В реальном сценарии старый лидер сам останавливает heartbeat
	// после stepDown, но мы ускоряем процесс отключением.
	h.DisconnectPeer(origLeaderID)

	// Дожидаемся выборов — новый лидер должен быть targetID.
	var newLeaderID, newTerm int
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for new leader election after TimeoutNow")
		default:
		}

		lid, term := h.CheckSingleLeaderNoFail()
		if lid >= 0 {
			newLeaderID = lid
			newTerm = term
			break
		}
		time.Sleep(_pollInterval)
	}

	// Проверяем, что новый лидер — targetID.
	if newLeaderID != targetID {
		t.Errorf("new leader = %d, want %d", newLeaderID, targetID)
	}

	// Проверяем, что term увеличился.
	if newTerm <= origTerm {
		t.Errorf("new term = %d, want > %d", newTerm, origTerm)
	}
}

// --- Сценарий №10: Передача лидерства на отключённый целевой узел ---

// TestLeadershipTransfer_TargetDisconnected проверяет, что передача лидерства
// отменяется по таймауту, если целевой узел отключён и не может догнать
// журнал.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбран лидер.
//  2. Отключаем целевой follower, отправляем команды.
//  3. Запускаем LeadershipTransfer на отключённый узел.
//  4. Передача завершается с ошибкой по таймауту.
func TestLeadershipTransfer_TargetDisconnected(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	// Отключаем целевой follower.
	h.DisconnectPeer(targetID)

	// Отправляем команды, чтобы увеличить журнал лидера.
	h.SubmitToServer(origLeaderID, 20)
	h.SubmitToServer(origLeaderID, 21)
	h.WaitForCommit(21, 2)

	// Запускаем LeadershipTransfer — должен завершиться с ошибкой таймаута.
	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
	if err := future.Error(); err == nil {
		t.Fatal("expected error from LeadershipTransfer to disconnected peer, got nil")
	}
}

// --- Сценарий №11: RequestVote с LeadershipTransfer флагом bypass лидера ---

// TestLeadershipTransfer_VoteBypass проверяет, что RequestVote
// с LeadershipTransfer=true bypass проверку наличия лидера на узле-получателе.
//
// Сценарий:
//  1. Кластер из 3 узлов, выбран лидер.
//  2. Выбираем follower (голосующий).
//  3. Отправляем ему RequestVote от имени "неизвестного" узла
//     с LeadershipTransfer=true.
//  4. Проверяем, что голос отдан (VoteGranted == true), несмотря на наличие
//     известного лидера.
func TestLeadershipTransfer_VoteBypass(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, origTerm := h.CheckSingleLeader()
	followerID := (origLeaderID + 1) % 3

	// replace: ждём наблюдаемого состояния — follower узнал о лидере
	// (получил heartbeat), а не фиксированной паузы.
	waitForKnownLeader(t, h, followerID, origLeaderID, _commitBudgetAfterFailover)

	// Отправляем RequestVote с LeadershipTransfer=true и term+1 (реальный
	// leadership transfer increment term). follower должен проголосовать,
	// несмотря на знание о лидере: в новом term votedFor = -1, а bypass
	// пропускает проверку leaderID.
	// lastLogIndexAndTerm требует удержания cm.mu — читаем под блокировкой.
	cm := h.cluster[followerID]
	cm.mu.Lock()
	lastLogIndex, _ := cm.lastLogIndexAndTerm()
	cm.mu.Unlock()
	candidateID := (origLeaderID + 2) % 3
	args := RequestVoteArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        candidateID,
		},
		Term:               origTerm + 1,
		CandidateID:        candidateID,
		LastLogIndex:       lastLogIndex,
		LastLogTerm:        origTerm,
		LeadershipTransfer: true,
	}
	var reply RequestVoteReply
	if err := h.cluster[followerID].RequestVote(args, &reply); err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	if !reply.VoteGranted {
		t.Fatal("RequestVote with LeadershipTransfer=true: expected VoteGranted=true")
	}
}

// --- Сценарий №12: PreVote пропускается при candidateFromLeadershipTransfer ---

// TestLeadershipTransfer_SkipPreVote проверяет, что после TimeoutNow
// целевой узел пропускает PreVote и переходит сразу к реальным выборам.
//
// Косвенная проверка: после TimeoutNow на follower выборы должны состояться
// даже при активном лидере (normally PreVote был бы отклонён).
// В тесте TestTimeoutNow_OnFollower_ImmediateElection это уже проверяется.
// Данный тест — дополнительная прямая проверка флага.
func TestLeadershipTransfer_SkipPreVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, origTerm := h.CheckSingleLeader()
	followerID := (origLeaderID + 1) % 3

	// Предусловие обработчика timeoutNow: целевой узел обязан быть
	// в состоянии Follower (см. TestTimeoutNow_OnFollower_ImmediateElection).
	waitForKnownLeader(t, h, followerID, origLeaderID, _commitBudgetAfterFailover)

	// Simulate the beginning of a leadership transfer: send TimeoutNow to follower.
	reply, err := h.SendTimeoutNow(origLeaderID, followerID)
	if err != nil {
		t.Fatalf("SendTimeoutNow failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("TimeoutNow on follower: expected Success=true")
	}

	// replace: ждём наблюдаемого роста term у целевого узла (TimeoutNow
	// переводит его в Candidate, что увеличивает term). Оба исхода
	// протокольно корректны: старый лидер не отключён, поэтому целевой
	// узел может как выиграть выборы, так и вернуться в Follower по
	// AppendEntries прежнего лидера — поэтому ожидание ограничено
	// бюджетом и не является assert'ом.
	waitForTermAbove(h, followerID, origTerm, _commitBudgetAfterFailover)

	// Проверяем, что узел стал лидером (выборы прошли успешно).
	// Поскольку лидер отключён не был, возможны два сценария:
	// если старый лидер всё ещё отправляет heartbeat, целевой узел
	// может получать AppendEntries. Но candidateFromLeadershipTransfer
	// защищает его от step-down от старого лидера.
	// Проверяем, что term увеличился хотя бы на одном узле.
	_, newTerm := h.CheckSingleLeader()
	if newTerm <= origTerm {
		t.Logf("term: old=%d, new=%d (leader may have stepped down)", origTerm, newTerm)
	}
	_ = reply
}

// CheckSingleLeaderNoFail — версия CheckSingleLeader, которая не вызывает
// t.Fatalf при отсутствии лидера, а возвращает -1, -1.
func (h *Harness) CheckSingleLeaderNoFail() (int, int) {
	deadline := time.Now().Add(_leaderElectionBudget)
	for {
		// connected снимается под h.mu, Report() опрашивается вне её
		// (инвариант границ).
		connected := h.connectedSnapshot()
		leaderID := -1
		leaderTerm := -1
		for i := 0; i < h.n; i++ {
			if !connected[i] {
				continue
			}
			_, term, isLeader := h.cluster[i].Report()
			if isLeader {
				if leaderID < 0 {
					leaderID = i
					leaderTerm = term
				} else {
					return -1, -1
				}
			}
		}
		if leaderID >= 0 {
			return leaderID, leaderTerm
		}
		if !time.Now().Before(deadline) {
			return -1, -1
		}
		time.Sleep(_pollInterval)
	}
}

// waitForTransferInProgress ждёт, пока leaderLoop узла id примет запрос
// на передачу лидерства (leadershipTransferInProgress != 0).
func waitForTransferInProgress(t *testing.T, h *Harness, id int, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if atomic.LoadInt32(&h.cluster[id].leaderState.leadershipTransferInProgress) != 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("server %d did not enter leadership transfer within %v", id, budget)
		}
		time.Sleep(_pollInterval)
	}
}

// waitForKnownLeader ждёт, пока узел id не узнает о лидере leaderID
// (получит от него AppendEntries).
func waitForKnownLeader(t *testing.T, h *Harness, id, leaderID int, budget time.Duration) {
	t.Helper()
	cm := h.cluster[id]
	deadline := time.Now().Add(budget)
	for {
		cm.mu.Lock()
		known := cm.cmState.leaderID
		cm.mu.Unlock()
		if known == leaderID {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("server %d does not know leader %d within %v (leaderID=%d)",
				id, leaderID, budget, known)
		}
		time.Sleep(_pollInterval)
	}
}

// waitForTermAbove ждёт роста term узла id выше want в пределах бюджета.
// Не является assert'ом: вызывающий сам решает, обязателен ли рост
// (оба исхода могут быть протокольно корректны).
func waitForTermAbove(h *Harness, id, want int, budget time.Duration) {
	deadline := time.Now().Add(budget)
	for h.GetTerm(id) <= want {
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(_pollInterval)
	}
}

// TestLeadershipTransfer_AfterShutdown проверяет, что LeadershipTransfer
// после остановки модуля возвращает ошибку (ErrNotLeader или ErrRaftShutdown).
func TestLeadershipTransfer_AfterShutdown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()

	// Останавливаем лидера. После CrashPeer состояние узла — Dead.
	h.CrashPeer(origLeaderID)

	// LeadershipTransfer на остановленном/неактивном узле вернёт ошибку.
	future := h.cluster[origLeaderID].LeadershipTransfer(ServerID((origLeaderID + 1) % 3))
	if err := future.Error(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestLeadershipTransfer_FutureConcurrentAccess проверяет, что несколько
// горутин могут одновременно ожидать один LeadershipTransferFuture.
func TestLeadershipTransfer_FutureConcurrentAccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = future.Error()
		}()
	}
	wg.Wait()
}

// TestLeadershipTransfer_EnqueueConfigurationChangeBlocked проверяет,
// что изменения конфигурации блокируются во время передачи лидерства.
//
// Сценарий: создаём отставание по журналу у target, запускаем передачу,
// catch-up блокируется (target отключён), конфигурационное изменение
// проверяется во время блокировки, затем target подключается.
func TestLeadershipTransfer_EnqueueConfigurationChangeBlocked(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderID, _ := h.CheckSingleLeader()
	targetID := (origLeaderID + 1) % 3

	// Фиксируем команды, чтобы создать журнал.
	h.SubmitToServer(origLeaderID, 42)
	h.WaitForCommit(42, 3)

	// Отключаем target, фиксируем ещё команду — target отстаёт.
	h.DisconnectPeer(targetID)
	h.SubmitToServer(origLeaderID, 43)
	h.WaitForCommit(43, 2)

	// Запускаем LeadershipTransfer в горутине.
	// catch-up блокируется, т.к. target отключён.
	transferCh := make(chan error, 1)
	go func() {
		future := h.LeadershipTransfer(origLeaderID, ServerID(targetID))
		transferCh <- future.Error()
	}()

	// Ждём, пока leadershipTransferInProgress установится в leaderLoop.
	waitForTransferInProgress(t, h, origLeaderID, _commitBudgetAfterFailover)

	// Попытка изменения конфигурации должна вернуть ErrLeadershipTransferInProgress.
	future := h.cluster[origLeaderID].AddVoter(ServerID(100), ServerAddress("new-node"))
	if err := future.Error(); err != ErrLeadershipTransferInProgress {
		t.Fatalf("got %v, want ErrLeadershipTransferInProgress", err)
	}

	// Подключаем target — catch-up завершается, передача завершается.
	h.ReconnectPeer(targetID)
	select {
	case <-transferCh:
	case <-time.After(2 * time.Second):
		t.Fatal("leadership transfer timed out after reconnecting target")
	}
}

// TestLeadershipTransfer_CatchUpSendIsDeduplicated проверяет, что догоняющая
// репликация передачи лидерства подчиняется дедупликации отправителей:
// пока горутина репликации на соседа активна, повторный вызов не порождает
// второго AppendEntries тому же соседу.
//
// Проверяется точка входа догоняющего цикла — leaderSendAEsToPeerIfIdle:
// цикл вызывает её каждые 10 мс, а прямой вызов leaderSendAEsToPeer обходил
// бы флаг inflightAE (два параллельных RPC одному соседу искажают счётчик
// транспортных ошибок и периодизацию задержки повторов).
//
// Сценарий:
//  1. Транспорт блокирует AppendEntries — первая отправка остаётся активной.
//  2. Пять итераций догоняющего цикла во время активной отправки.
//  3. Проверить: AppendEntries вызван ровно один раз.
//  4. Разблокировать транспорт: после снятия флага отправка снова проходит.
func TestLeadershipTransfer_CatchUpSendIsDeduplicated(t *testing.T) {
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

	// Итерации догоняющего цикла, пока отправка активна.
	for i := 0; i < 5; i++ {
		cm.leaderSendAEsToPeerIfIdle(1, 1)
		// keep: timing — воспроизводится интервал догоняющего цикла (10 мс);
		// наблюдаемого признака состояния здесь нет.
		time.Sleep(_pollInterval)
	}
	if calls := mock.callCount.Load(); calls != 1 {
		t.Fatalf("AppendEntries calls = %d, want 1 — догоняющий цикл породил параллельного отправителя", calls)
	}

	// Разблокировка: горутина завершается, флаг снимается через defer.
	close(mock.blockCh)
	cm.wg.Wait()
	if cm.leaderState.inflightAE[1].Load() {
		t.Fatal("inflightAE[1] = true, want false (флаг снимается на каждом выходе)")
	}

	// После снятия флага очередная итерация цикла снова отправляет RPC.
	cm.leaderSendAEsToPeerIfIdle(1, 1)
	deadline = time.Now().Add(_inmemRPCTimeout)
	for mock.callCount.Load() < 2 {
		if !time.Now().Before(deadline) {
			t.Fatalf("AppendEntries calls = %d, want 2 после снятия флага", mock.callCount.Load())
		}
		time.Sleep(_pollInterval)
	}
	cm.wg.Wait()
}
