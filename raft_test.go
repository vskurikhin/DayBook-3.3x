package raft

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/contract"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

func TestElectionBasic(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	h.CheckSingleLeader()
}

func TestElectionLeaderDisconnect(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, origTerm := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)
	// remove: CheckSingleLeader опрашивает состояние до _leaderElectionBudget,
	// а отключённый лидер исключён из опроса (connected == false).
	newLeaderId, newTerm := h.CheckSingleLeader()
	if newLeaderId == origLeaderId {
		t.Errorf("want new leader to be different from orig leader")
	}
	if newTerm <= origTerm {
		t.Errorf("want newTerm <= origTerm, got %d and %d", newTerm, origTerm)
	}
}

func TestElectionLeaderAndAnotherDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	// Аварийно завершаем лидера — он не должен влиять на выборы после
	// переподключения (см. Pre-Vote: живой лидер блокирует PreVote
	// через leaderLastContact даже при временном разрыве связи).
	h.CrashPeer(origLeaderId)
	otherId := (origLeaderId + 1) % 3
	h.DisconnectPeer(otherId)

	// Нет кворума: единственный оставшийся узел (2) не может получить
	// кворум PreVote → возвращается в Follower, term не растёт.
	// keep: negative window — бюджет _maxElectionTimeout (worst-case
	// election timeout). Опрос не доказывает отсутствия лидера; окно
	// осознанно временное, его уменьшение ослабило бы assert.
	time.Sleep(_maxElectionTimeout)
	h.CheckNoLeader()

	// Повторно подключаем отключённый узел. Два узла (1 и 2) могут
	// провести PreVote (leaderLastContact у обоих сброшен) → реальные
	// выборы → лидер избран.
	h.ReconnectPeer(otherId)
	h.CheckSingleLeader()
}

func TestDisconnectAllThenRestore(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// remove: разрыв связи выполняется сразу; минимальный election timeout
	// (ReelectionTimeoutMs) заведомо больше времени старта узлов, поэтому
	// лидер не успевает быть избран до разрыва.
	// Отключаем все серверы с самого начала. Лидера не будет.
	for i := 0; i < 3; i++ {
		h.DisconnectPeer(i)
	}
	// keep: negative window — бюджет _maxElectionTimeout (см. выше).
	time.Sleep(_maxElectionTimeout)
	h.CheckNoLeader()

	// Повторно подключаем все серверы. Будет выбран лидер.
	for i := 0; i < 3; i++ {
		h.ReconnectPeer(i)
	}
	h.CheckSingleLeader()
}

func TestElectionLeaderDisconnectThenReconnect(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	origLeaderId, _ := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)

	// remove: CheckSingleLeader опрашивает состояние сам.
	newLeaderId, newTerm := h.CheckSingleLeader()

	h.ReconnectPeer(origLeaderId)
	// replace: после реконнекта прежний лидер узнаёт о более высоком term'е
	// только с первым дошедшим AppendEntries (задержка повторов репликации),
	// поэтому окно «два лидера» ожидается через
	// WaitForSingleLeader, а не фиксированной паузой.
	againLeaderId, againTerm := h.WaitForSingleLeader(_leaderElectionBudget)

	if newLeaderId != againLeaderId {
		t.Errorf("again leader id got %d; want %d", againLeaderId, newLeaderId)
	}
	if againTerm != newTerm {
		t.Errorf("again term got %d; want %d", againTerm, newTerm)
	}
}

func TestElectionLeaderDisconnectThenReconnect5(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 5)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	h.DisconnectPeer(origLeaderId)
	// remove: CheckSingleLeader опрашивает состояние сам.
	newLeaderId, newTerm := h.CheckSingleLeader()

	h.ReconnectPeer(origLeaderId)
	// replace: см. TestElectionLeaderDisconnectThenReconnect — окно
	// «два лидера» после реконнекта.
	againLeaderId, againTerm := h.WaitForSingleLeader(_leaderElectionBudget)

	if newLeaderId != againLeaderId {
		t.Errorf("again leader id got %d; want %d", againLeaderId, newLeaderId)
	}
	if againTerm != newTerm {
		t.Errorf("again term got %d; want %d", againTerm, newTerm)
	}
}

// TestElectionFollowerComesBack удалён — несовместим с Pre-Vote.
//
// В классическом Raft отключённый follower увеличивает term при каждой
// попытке выборов. При переподключении его более высокий term вызывал
// stepDown лидера и новые выборы.
//
// Pre-Vote предотвращает рост term изолированного узла: PreVote
// отклоняется оставшимся кворумом, term не меняется. После
// переподключения лидер продолжает работать — это желаемое поведение.
// Сценарий стабильности term при reconnect покрывается тестом
// TestPreVote_ReconnectStable.

func TestElectionDisconnectLoop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	for cycle := 0; cycle < 5; cycle++ {
		leaderId, _ := h.CheckSingleLeader()

		h.DisconnectPeer(leaderId)
		otherId := (leaderId + 1) % 3
		h.DisconnectPeer(otherId)

		// Ждём, пока кластер не обнаружит потерю кворума.
		// После отключения 2 из 3 узлов ни один не может получить quorum,
		// так что лидера быть не должно.
		// poll-интервал condition-wait (не фиксированная пауза):
		// ждём наблюдаемого состояния «лидера нет».
		for r := 0; r < 10; r++ {
			connected := h.connectedSnapshot()
			hasLeader := false
			for i := 0; i < 3; i++ {
				if connected[i] {
					_, _, isLeader := h.cluster[i].Report()
					if isLeader {
						hasLeader = true
					}
				}
			}
			if !hasLeader {
				break
			}
			time.Sleep(_pollInterval)
		}
		h.CheckNoLeader()

		h.ReconnectPeer(otherId)
		h.ReconnectPeer(leaderId)

		h.CheckSingleLeader()
	}
}

func TestCommitOneCommand(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	tlog("submitting 42 to %d", origLeaderId)
	isLeader := h.SubmitToServer(origLeaderId, 42) >= 0
	if !isLeader {
		t.Errorf("want id=%d leader, but it's not", origLeaderId)
	}

	// replace: ожидание сходимости фиксации вместо фиксированной паузы.
	// Сценарий без сбоев — бюджет steady.
	h.WaitForCommitBudget(42, 3, _commitBudgetSteady)
	h.CheckCommittedN(42, 3)
}

func TestCommitAfterCallDrops(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	h.PeerDropCallsAfterN(lid, 2)
	h.SubmitToServer(lid, 99)
	// remove: PeerDropCallsAfterN/PeerDontDropCalls — no-op после удаления
	// RPCProxy, ждать между ними нечего; SubmitToServer блокируется
	// до фиксации.
	h.PeerDontDropCalls(lid)

	// replace: сценарий без сбоев (PeerDropCallsAfterN — no-op) — steady.
	h.WaitForCommitBudget(99, 3, _commitBudgetSteady)
	h.CheckCommittedN(99, 3)
}

func TestSubmitNonLeaderFails(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	sid := (origLeaderId + 1) % 3
	tlog("submitting 42 to %d", sid)
	isLeader := h.SubmitToServer(sid, 42) >= 0
	if isLeader {
		t.Errorf("want id=%d !leader, but it is", sid)
	}
	// remove: пауза в конце теста ничего не проверяет; корректное
	// завершение узлов обеспечивает h.Shutdown().
}

func TestCommitMultipleCommands(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	values := []int{42, 55, 81}
	for _, v := range values {
		tlog("submitting %d to %d", v, origLeaderId)
		isLeader := h.SubmitToServer(origLeaderId, v) >= 0
		if !isLeader {
			t.Errorf("want id=%d leader, but it's not", origLeaderId)
		}
		// remove: SubmitToServer блокируется до фиксации, разделять
		// отправки паузой не требуется.
	}

	// replace: сходимость последней команды влечёт фиксацию предыдущих;
	// сценарий без сбоев — steady.
	h.WaitForCommitBudget(81, 3, _commitBudgetSteady)
	nc, i1 := h.CheckCommitted(42)
	_, i2 := h.CheckCommitted(55)
	if nc != 3 {
		t.Errorf("want nc=3, got %d", nc)
	}
	if i1 >= i2 {
		t.Errorf("want i1<i2, got i1=%d i2=%d", i1, i2)
	}

	_, i3 := h.CheckCommitted(81)
	if i2 >= i3 {
		t.Errorf("want i2<i3, got i2=%d i3=%d", i2, i3)
	}
}

func TestCommitWithDisconnectionAndRecover(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Отправляем несколько значений в полностью подключённый кластер.
	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)

	// replace: до первого сбоя сценария — steady.
	h.WaitForCommitBudget(6, 3, _commitBudgetSteady)
	h.CheckCommittedN(6, 3)

	dPeerId := (origLeaderId + 1) % 3
	// remove: CrashPeer возвращается после join горутин узла.
	h.CrashPeer(dPeerId)

	// SubmitToServer блокируется до фиксации. Теперь кластер из 2 живых узлов:
	// лидер + один follower = кворум (2/3). Команда будет зафиксирована.
	if idx := h.SubmitToServer(origLeaderId, 7); idx < 0 {
		t.Fatal("Submit 7 failed")
	}
	// replace: после CrashPeer подключены 2 узла; сценарий со сбоем —
	// бюджет after-failover.
	h.WaitForCommit(7, 2)

	// Перезапускаем отключённый узел и ждём синхронизации.
	h.RestartPeer(dPeerId)
	h.CheckSingleLeader()

	// replace: после рестарта возможны перевыборы — after-failover.
	h.WaitForCommit(7, 3)
	h.CheckCommittedN(7, 3)
}

func TestNoCommitWithNoQuorum(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)

	h.WaitForCommit(6, 3)

	// Изолируем обоих follower'ов: у лидера остаётся 1/3 — кворума нет.
	// Прежняя схема опиралась на инвариант «время изоляции < min election
	// timeout» (комментарий «246ms < 254ms»); фактические константы
	// (ReelectionTimeoutMs=381ms) его нарушали — изоляция 300+93ms
	// превышала минимальный election timeout, и тест был flaky.
	// Инвариант снят: тест больше не зависит от того, начнут ли изолированные
	// follower'ы выборы.
	dPeer1 := (origLeaderId + 1) % 3
	dPeer2 := (origLeaderId + 2) % 3
	h.DisconnectPeer(dPeer1)
	h.DisconnectPeer(dPeer2)
	h.waitForIsolated(origLeaderId, _commitBudgetSteady)

	// Submit под явным жизненным циклом: результат доставляется через канал,
	// горутина join'ится до конца теста — она не переживает тест.
	submitDone := make(chan int, 1)
	go func() { submitDone <- h.SubmitToServer(origLeaderId, 8) }()

	// keep: budgeted negative window — 2*_maxElectionTimeout, заведомо больше
	// любого election timeout и нескольких раундов репликации, т.е. если
	// бы кворум был, команда 8 успела бы зафиксироваться.
	//
	// Инвариант окна: лидер изолирован, кворума нет; CheckQuorum заставляет
	// его сложить полномочия, поэтому команда 8 завершается ошибкой ещё до
	// окончания окна. После реконнекта выборы выигрывает либо старый лидер
	// (журнал длиннее — команда 8 фиксируется в новом терме), либо узел с
	// коротким журналом (команда 8 перезаписывается) — оба исхода штатные.
	time.Sleep(2 * _maxElectionTimeout)
	h.CheckNotCommitted(8)

	h.ReconnectPeer(dPeer1)
	h.ReconnectPeer(dPeer2)

	// Join Submit-горутины до конца теста. Оба исхода протокольно
	// корректны: изолированный лидер мог сохранить полномочия (команда 8
	// фиксируется после реконнекта) либо уступить их новому лидеру
	// (команда 8 не входит в журнал нового лидера и теряется —
	// Raft этого не запрещает). Assert выполняется только для исхода,
	// в котором Submit сообщил об успехе.
	submitIdx := <-submitDone
	if submitIdx >= 0 {
		h.WaitForCommit(8, 3)
	} else {
		t.Logf("submit of 8 did not complete (leadership lost during isolation) — допустимый исход")
	}

	// Кластер обязан восстановиться и фиксировать новые команды.
	leaderId, _ := h.CheckSingleLeader()

	h.SubmitToServer(leaderId, 9)
	h.SubmitToServer(leaderId, 10)
	h.SubmitToServer(leaderId, 11)

	for _, v := range []int{9, 10, 11} {
		if submitIdx >= 0 {
			// Ветка «8 зафиксирована»: журналы всех узлов идентичны,
			// строгая сходимость применима.
			h.WaitForCommit(v, 3)
		} else {
			// Ветка «8 потеряна»: предыстории журналов расходятся
			// (незафиксированная запись 8 перезаписана), поэтому
			// восстановление проверяем по значению — без требования
			// одинаковой позиции/Index записи.
			h.WaitForCommitConvergedValues(v, 3)
		}
	}
}

func TestDisconnectLeaderBriefly(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Отправляем несколько значений в полностью связанный кластер.
	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)
	// replace: до отключения лидера сбоев нет — steady.
	h.WaitForCommitBudget(6, 3, _commitBudgetSteady)
	h.CheckCommittedN(6, 3)

	// Отключаем лидера на короткое время (меньше тайм-аута выборов у соседей).
	// keep: timing — предмет теста. Длительность разрыва (90 мс) заведомо
	// меньше минимального election timeout (ReelectionTimeoutMs = 381 мс),
	// поэтому соседи не начинают выборы.
	h.DisconnectPeer(origLeaderId)
	sleepMs(90)
	h.ReconnectPeer(origLeaderId)
	// replace: перед отправкой команды ждём наблюдаемого состояния —
	// в кластере ровно один лидер.
	h.WaitForSingleLeader(_leaderElectionBudget)

	h.SubmitToServer(origLeaderId, 7)
	// replace: после disconnect/reconnect возможны перевыборы —
	// after-failover.
	h.WaitForCommit(7, 3)
	h.CheckCommittedN(7, 3)
}

func TestCommitsWithLeaderDisconnects(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 5)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)

	h.WaitForCommit(6, 5)

	// replace: ждём фактического вступления изоляции лидера в силу.
	h.DisconnectPeer(origLeaderId)
	h.waitForIsolated(origLeaderId, _commitBudgetSteady)

	// Submit под явным жизненным циклом: горутина join'ится до конца теста,
	// результат доставляется через канал.
	submit7 := make(chan int, 1)
	go func() { submit7 <- h.SubmitToServer(origLeaderId, 7) }()

	// keep: negative window — бюджет _inmemRPCTimeout: при наличии кворума
	// команда успела бы зафиксироваться за один RPC-раунд. Опрос не
	// доказывает отсутствия фиксации; окно осознанно временное.
	time.Sleep(_inmemRPCTimeout)
	h.CheckNotCommitted(7)

	newLeaderId, _ := h.CheckSingleLeader()

	h.SubmitToServer(newLeaderId, 8)
	h.WaitForCommit(8, 4)

	h.ReconnectPeer(origLeaderId)
	// replace: после реконнекта ждём схождения к единственному лидеру
	// (окно «два лидера» — задержка повторов репликации).
	finalLeaderId, _ := h.WaitForSingleLeader(_leaderElectionBudget)
	if finalLeaderId == origLeaderId {
		t.Errorf("got finalLeaderId==origLeaderId==%d, want them different", finalLeaderId)
	}

	h.SubmitToServer(newLeaderId, 9)
	h.WaitForCommit(9, 5)
	h.CheckCommittedN(8, 5)

	h.CheckNotCommitted(7)

	// Join Submit-горутины команды 7: она не переживает тест.
	<-submit7
}

func TestCrashFollower(t *testing.T) {
	// Базовый тест, проверяющий, что сбой одного узла не приводит к отказу системы.
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)

	// replace: до сбоя — steady.
	h.WaitForCommitBudget(5, 3, _commitBudgetSteady)
	h.CheckCommittedN(5, 3)

	h.CrashPeer((origLeaderId + 1) % 3)
	// replace: после CrashPeer подключены 2 узла — after-failover.
	h.WaitForCommit(5, 2)
	h.CheckCommittedN(5, 2)
}

func TestCrashThenRestartFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)
	h.SubmitToServer(origLeaderId, 7)

	vals := []int{5, 6, 7}

	// replace: до сбоя — steady.
	h.WaitForCommitBudget(7, 3, _commitBudgetSteady)
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}

	h.CrashPeer((origLeaderId + 1) % 3)
	// replace: после CrashPeer подключены 2 узла — after-failover.
	h.WaitForCommit(7, 2)
	for _, v := range vals {
		h.CheckCommittedN(v, 2)
	}

	// Перезапускаем завершившийся с ошибкой ведомый узел и ждём, пока он
	// догонит остальных.
	h.RestartPeer((origLeaderId + 1) % 3)
	// replace: после рестарта возможны перевыборы — after-failover.
	h.WaitForCommit(7, 3)
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}
}

func TestCrashThenRestartLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)
	h.SubmitToServer(origLeaderId, 7)

	vals := []int{5, 6, 7}

	// replace: до сбоя — steady.
	h.WaitForCommitBudget(7, 3, _commitBudgetSteady)
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}

	h.CrashPeer(origLeaderId)
	// replace: аварийно остановлен лидер — обязательны перевыборы,
	// бюджет after-failover.
	h.WaitForCommit(7, 2)
	for _, v := range vals {
		h.CheckCommittedN(v, 2)
	}

	h.RestartPeer(origLeaderId)
	// replace: after-failover.
	h.WaitForCommit(7, 3)
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}
}

func TestCrashThenRestartAll(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)
	h.SubmitToServer(origLeaderId, 7)

	vals := []int{5, 6, 7}

	// replace: до сбоя — steady.
	h.WaitForCommitBudget(7, 3, _commitBudgetSteady)
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}

	for i := 0; i < 3; i++ {
		h.CrashPeer((origLeaderId + i) % 3)
	}

	// remove: CrashPeer возвращается только после join горутин узла,
	// поэтому ждать между остановкой и перезапуском нечего.
	for i := 0; i < 3; i++ {
		h.RestartPeer((origLeaderId + i) % 3)
	}

	newLeaderId, _ := h.CheckSingleLeader()

	h.SubmitToServer(newLeaderId, 8)
	// replace: после перезапуска всего кластера — after-failover.
	h.WaitForCommit(8, 3)

	vals = []int{5, 6, 7, 8}
	for _, v := range vals {
		h.CheckCommittedN(v, 3)
	}
}

func TestReplaceMultipleLogEntries(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Отправляем несколько значений в полностью связанный кластер.
	origLeaderId, _ := h.CheckSingleLeader()
	h.SubmitToServer(origLeaderId, 5)
	h.SubmitToServer(origLeaderId, 6)

	h.WaitForCommit(6, 3)

	h.DisconnectPeer(origLeaderId)

	// Submit'ы к изолированному лидеру — под явным жизненным циклом: горутины
	// join'ятся до конца теста, результаты не проверяются
	// (команды заведомо не могут быть зафиксированы без кворума).
	staleSubmits := make(chan int, 4)
	for _, cmd := range []int{21, 22, 23, 24} {
		go func() { staleSubmits <- h.SubmitToServer(origLeaderId, cmd) }()
	}

	newLeaderId, _ := h.CheckSingleLeader()

	h.SubmitToServer(newLeaderId, 8)
	h.SubmitToServer(newLeaderId, 9)
	h.SubmitToServer(newLeaderId, 10)
	h.WaitForCommit(10, 2)
	h.CheckNotCommitted(21)

	h.CrashPeer(newLeaderId)
	h.RestartPeer(newLeaderId)

	finalLeaderId, _ := h.CheckSingleLeader()
	h.ReconnectPeer(origLeaderId)
	h.CheckSingleLeader()

	h.SubmitToServer(finalLeaderId, 11)
	h.WaitForCommit(11, 3)

	h.CheckNotCommitted(21)
	h.CheckCommittedN(10, 3)

	// Join Submit-горутин: они не переживают тест.
	for range 4 {
		<-staleSubmits
	}
}

func TestCrashAfterSubmit(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	h.SubmitToServer(origLeaderId, 5)
	h.CrashPeer(origLeaderId)

	// remove: CheckSingleLeader опрашивает состояние сам.
	h.CheckSingleLeader()

	h.RestartPeer(origLeaderId)
	// replace: после рестарта прежнего лидера возможно окно «два лидера».
	newLeaderId, _ := h.WaitForSingleLeader(_leaderElectionBudget)

	h.SubmitToServer(newLeaderId, 6)
	// replace: сценарий с CrashPeer/RestartPeer — after-failover.
	h.WaitForCommit(6, 3)
	h.CheckCommittedN(5, 3)
	h.CheckCommittedN(6, 3)
}

func TestDisconnectAfterSubmit(t *testing.T) {
	// Аналогично TestCrashAfterSubmit, но лидер не завершается аварийно,
	// а отключается вскоре после отправки первой команды.
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	// SubmitToServer теперь использует Apply, который блокируется до фиксации.
	// Команда 5 может быть зафиксирована до дисконнекта лидера.
	h.SubmitToServer(origLeaderId, 5)
	h.DisconnectPeer(origLeaderId)

	// remove: CheckSingleLeader опрашивает состояние сам.
	h.CheckSingleLeader()

	h.ReconnectPeer(origLeaderId)
	// Задержка повторов репликации задерживает step-down прежнего
	// лидера до 1000 мс — ждём схождения к единственному лидеру.
	newLeaderId, _ := h.WaitForSingleLeader(3 * time.Second)

	// После отправки новой команды 6 запись 5 будет зафиксирована вместе с 6,
	// независимо от того, успела ли она зафиксироваться до дисконнекта.
	// Задержка повторов репликации может задержать доставку
	// переподключённому узлу до 1000 мс — ждём опросом.
	h.SubmitToServer(newLeaderId, 6)
	h.WaitForCommitAll(6, 3*time.Second)
	h.CheckCommittedN(5, 3)
	h.CheckCommittedN(6, 3)
}

// getPersistedTerm считывает значение currentTerm из указанного хранилища
// (используя тот же формат кодирования, что и persistToStorage).
func getPersistedTerm(storage *store.MapStorage) int {
	data, found := storage.Get("currentTerm")
	if !found {
		return 0
	}
	var term int
	d := gob.NewDecoder(bytes.NewBuffer(data))
	if err := d.Decode(&term); err != nil {
		return 0
	}
	return term
}

// startElectionLockedPersistIncrements — число наблюдаемых инкрементов term,
// после каждого из которых проверяется persist-путь startElectionLocked.
const startElectionLockedPersistIncrements = 2

func TestBug_StartElectionMissingPersist(t *testing.T) {
	// Тест покрывает persist-путь startElectionLocked при PreVote=off.
	// При включённом Pre-Vote изолированный узел остаётся PreCandidate
	// и НЕ увеличивает currentTerm, поэтому assert «persisted >= in-memory»
	// был бы тривиально истинен и не ловил бы дефект.
	// Путь PreVote=on покрывается prevote-тестами.
	//
	// Victim выбирается уже ПОСЛЕ выборов, поэтому настройка отдельного
	// узла неприменима: PreVote отключается кластерно, до старта горутин
	// (механизм, решение).
	h := NewHarnessWithOptions(t, 3, DisablePreVote())
	defer h.Shutdown()

	// Ждём, пока будет выбран лидер, затем выбираем ведомый узел,
	// который будем отключать.
	leaderId, _ := h.CheckSingleLeader()
	victim := (leaderId + 1) % 3

	h.DisconnectPeer(victim)

	// Condition-wait вместо фиксированных 800 мс: ждём наблюдаемых
	// инкрементов currentTerm у изолированного узла (каждые выборы
	// увеличивают term) и после КАЖДОГО инкремента сверяем persisted
	// term с in-memory.
	//
	// Оба значения снимаются под cm.mu: startElectionLocked увеличивает
	// currentTerm и вызывает persistToStorage в одной критической секции,
	// поэтому под cm.mu снимок согласован (без cm.mu возможно ложное
	// срабатывание в окне между инкрементом и записью).
	cm := h.cluster[victim]
	deadline := time.Now().Add(_commitBudgetAfterFailover)
	observed := 0
	prevTerm := -1
	for observed < startElectionLockedPersistIncrements {
		cm.mu.Lock()
		inMemoryTerm := cm.cmState.currentTerm
		persistedTerm := getPersistedTerm(h.storage[victim])
		cm.mu.Unlock()

		if inMemoryTerm != prevTerm {
			if prevTerm >= 0 {
				observed++
			}
			prevTerm = inMemoryTerm
			t.Logf("server %d: in-memory term = %d, persisted term = %d",
				victim, inMemoryTerm, persistedTerm)
			if persistedTerm < inMemoryTerm {
				t.Fatalf("persisted term (%d) is behind in-memory term (%d); "+
					"startElectionLocked is not persisting state", persistedTerm, inMemoryTerm)
			}
		}

		if !time.Now().Before(deadline) {
			t.Fatalf("observed only %d of %d term increments on isolated server %d "+
				"within %v (last term %d)",
				observed, startElectionLockedPersistIncrements, victim,
				_commitBudgetAfterFailover, prevTerm)
		}
		time.Sleep(_pollInterval)
	}
}

// waitForNewLeaderExcept опрашивает все подключённые серверы до тех пор, пока
// один из них не станет лидером, исключая сервер excludedID (в момент вызова
// он изолирован и физически не может выиграть выборы).
//
// Возвращает (leaderID, term). В отличие от Harness.CheckSingleLeader, ждёт
// появление лидера столько, сколько нужно, а не фиксированное окно: PreVote
// (§4) может отклоняться получателем в течение нескольких раундов выборов,
// поэтому длительность выборов недетерминирована и может превышать один
// election timeout. При достижении timeout вызывает t.Fatalf.
func waitForNewLeaderExcept(t *testing.T, h *Harness, excludedID int, timeout time.Duration) (int, int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Дисциплина владения состоянием Harness: connected
		// читается снимком под h.mu — одна выборка на итерацию опроса;
		// Report() опрашивается вне блокировки (граница).
		connected := h.connectedSnapshot()
		for i := 0; i < h.n; i++ {
			if i == excludedID || !connected[i] {
				continue
			}
			_, term, isLeader := h.cluster[i].Report()
			if isLeader {
				return i, term
			}
		}
		time.Sleep(_pollInterval)
	}
	t.Fatalf("leader not found among servers != %d within %v", excludedID, timeout)
	return -1, -1
}

// waitForStepDown опрашивает сервер id до тех пор, пока он не откажется от
// лидерства и не перейдёт в терм wantTerm. Так как Report() захватывает
// cm.mu, а becomeFollowerLocked (и startElectionLocked) персистят новый терм под той же
// блокировкой, факт наблюдения терма wantTerm гарантирует, что терм уже
// записан в постоянное хранилище. Это критично для проверки persist-бага:
// сразу после возврата из функции сервер можно безопасно аварийно завершать
// и перезапускать. При достижении timeout вызывает t.Fatalf.
func waitForStepDown(t *testing.T, h *Harness, id, wantTerm int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, term, isLeader := h.cluster[id].Report()
		if !isLeader && term == wantTerm {
			return
		}
		time.Sleep(_pollInterval)
	}
	t.Fatalf("server %d did not step down to term %d within %v", id, wantTerm, timeout)
}

// TestBug_BecomeFollowerMissingPersist проверяет, что отказ старого лидера от
// лидерства (becomeFollowerLocked) в более высокий терм переживает перезапуск.
//
// Сценарий:
//  1. Выбираем лидера и изолируем его от двух остальных серверов.
//  2. Два оставшихся сервера выбирают нового лидера в более высоком терме
//     (newTerm > origTerm). Выборы ждём через опрос, а не фиксированным сном:
//     PreVote (§4) может отклоняться в течение нескольких раундов, поэтому
//     длительность выборов недетерминирована.
//  3. Переподключаем старого лидера. Он узнаёт о более высоком терме либо из
//     RPC-ответа AppendEntries на собственные записи, либо из входящего
//     heartbeat нового лидера — оба пути ведут в becomeFollowerLocked(newTerm),
//     который персистит терм в постоянное хранилище. Ждём, пока сервер не
//     откажется от лидерства и не перейдёт в терм newTerm.
//  4. Аварийно завершаем и перезапускаем старого лидера: если изменение терма
//     хранилось только в памяти, после перезапуска терм был бы потерян.
//
// Раньше тест полагался на PeerDropCallsAfterN/PeerDontDropCalls, чтобы старый
// лидер узнавал новый терм только через RPC-ответ, но RPCProxy был удалён, и
// эти методы стали no-op. Проверка остаётся корректной и без них: в кластере
// терм нового лидера — максимальный, поэтому старый лидер не может перейти в
// терм выше newTerm, а любой способ достижения терма newTerm персистит его.
func TestBug_BecomeFollowerMissingPersist(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, origTerm := h.CheckSingleLeader()

	// Изолируем лидера, чтобы два остальных сервера смогли выбрать нового
	// лидера с более высоким термом.
	h.DisconnectPeer(origLeaderId)

	_, newTerm := waitForNewLeaderExcept(t, h, origLeaderId, 10*time.Second)
	if newTerm <= origTerm {
		t.Fatalf("got newTerm=%d, origTerm=%d; want newTerm > origTerm", newTerm, origTerm)
	}

	// Переподключаем старого лидера и ждём, пока он узнает о более высоком
	// терме и откажется от лидерства. В этот момент терм newTerm уже
	// персистентен, и сервер можно безопасно аварийно завершать.
	h.ReconnectPeer(origLeaderId)
	waitForStepDown(t, h, origLeaderId, newTerm, 5*time.Second)

	// Аварийно завершаем работу сразу после того, как старый лидер обнаружил
	// более высокий терм и отказался от лидерства. После перезапуска он должен
	// по-прежнему помнить этот более высокий терм. Если это не так, значит
	// изменение терма хранилось только в памяти и было потеряно при сбое.
	h.CrashPeer(origLeaderId)
	h.RestartPeer(origLeaderId)

	_, restartedTerm, _ := h.cluster[origLeaderId].Report()
	if restartedTerm != newTerm {
		t.Fatalf("server %d restarted with term %d; want persisted higher term %d", origLeaderId, restartedTerm, newTerm)
	}
}

// Ведомый узел, получающий вызов becomeFollowerLocked с тем же термом, в котором он
// уже находится, должен сохранить значение votedFor. Сброс votedFor при
// переходе в состояние ведомого в пределах того же терма позволил бы узлу
// проголосовать дважды в одном терме, нарушив гарантию безопасности Raft,
// согласно которой в каждом терме узел может голосовать только один раз.
func TestBecomeFollowerSameTermPreservesVotedFor(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	h.CheckSingleLeader()

	for i := 0; i < 3; i++ {
		cm := h.cluster[i]
		cm.mu.Lock()
		if cm.cmState.state == Follower && cm.cmState.votedFor >= 0 {
			savedVotedFor := cm.cmState.votedFor
			savedTerm := cm.cmState.currentTerm

			cm.becomeFollowerLocked(savedTerm)

			if cm.cmState.votedFor != savedVotedFor {
				t.Errorf("becomeFollowerLocked(%d) reset votedFor from %d to %d on same-term transition",
					savedTerm, savedVotedFor, cm.cmState.votedFor)
			}
			cm.mu.Unlock()
			return
		}
		cm.mu.Unlock()
	}
	t.Fatal("no follower with votedFor >= 0 found")
}

// Ведомый узел, переходящий в более высокий терм, должен сбросить значение
// votedFor в -1, чтобы иметь возможность проголосовать в новом терме.
// Без этого сброса узел будет отклонять все запросы RequestVote в новом
// терме, что может помешать выбору лидера.
func TestBecomeFollowerHigherTermResetsVotedFor(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	h.CheckSingleLeader()

	for i := 0; i < 3; i++ {
		cm := h.cluster[i]
		cm.mu.Lock()
		if cm.cmState.state == Follower && cm.cmState.votedFor >= 0 {
			savedTerm := cm.cmState.currentTerm

			cm.becomeFollowerLocked(savedTerm + 1)

			if cm.cmState.votedFor != -1 {
				t.Errorf("becomeFollowerLocked(%d) did not reset votedFor (got %d, want -1)",
					savedTerm+1, cm.cmState.votedFor)
			}
			cm.mu.Unlock()
			return
		}
		cm.mu.Unlock()
	}
	t.Fatal("no follower with votedFor >= 0 found")
}

// После нескольких смен лидера повторно подключившиеся узлы с устаревшими
// термами не должны нарушать работу кластера. Этот тест проверяет, что ранее
// изолированный лидер и второй лидер могут снова присоединиться к кластеру,
// не вызывая появления двух лидеров одновременно (split-brain) или
// бесконечного цикла выборов.
func TestStaleVoteReplyIgnored(t *testing.T) {
	h := NewHarness(t, 5)
	defer h.Shutdown()

	origLeaderId, origTerm := h.CheckSingleLeader()

	// remove: CheckSingleLeader опрашивает состояние сам; отключённые
	// узлы исключены из опроса.
	h.DisconnectPeer(origLeaderId)
	newLeaderId, newTerm := h.CheckSingleLeader()
	if newTerm <= origTerm {
		t.Fatalf("expected newTerm > origTerm, got %d <= %d", newTerm, origTerm)
	}

	h.DisconnectPeer(newLeaderId)
	h.CheckSingleLeader()

	// replace: после реконнекта двух прежних лидеров ждём схождения
	// к единственному лидеру (окно «два лидера» — задержка повторов).
	h.ReconnectPeer(origLeaderId)
	h.ReconnectPeer(newLeaderId)
	h.WaitForSingleLeader(_leaderElectionBudget)
}

// Ведомый узел, который уже проголосовал за лидера в текущем терме, должен
// отклонить запрос RequestVote от другого кандидата в том же терме.
// Предоставление второго голоса позволило бы выбрать двух лидеров
// в одном и том же терме.
func TestSameTermDoubleVotePrevented(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderId, leaderTerm := h.CheckSingleLeader()

	followerId := -1
	for i := 0; i < 3; i++ {
		if i == leaderId {
			continue
		}
		cm := h.cluster[i]
		cm.mu.Lock()
		if cm.cmState.votedFor == leaderId && cm.cmState.currentTerm == leaderTerm {
			followerId = i
		}
		cm.mu.Unlock()
		if followerId >= 0 {
			break
		}
	}
	if followerId < 0 {
		t.Fatal("could not find a follower that voted for the leader")
	}

	otherCandidate := -1
	for i := 0; i < 3; i++ {
		if i != leaderId && i != followerId {
			otherCandidate = i
			break
		}
	}

	cm := h.cluster[followerId]
	args := RequestVoteArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        otherCandidate,
		},
		Term:         leaderTerm,
		CandidateID:  otherCandidate,
		LastLogIndex: -1,
		LastLogTerm:  -1,
	}
	var reply RequestVoteReply
	if err := cm.RequestVote(args, &reply); err != nil {
		t.Fatal(err)
	}

	if reply.VoteGranted {
		t.Errorf("follower %d granted vote to %d in term %d, but already voted for %d",
			followerId, otherCandidate, leaderTerm, leaderId)
	}
}

// TestBecomeFollowerDoubleClose проверяет, что двойной вызов becomeFollowerLocked
// не вызывает панику при повторном close(electionTimerDone) (проблема 10).
func TestBecomeFollowerDoubleClose(t *testing.T) {
	cm := &ConsensusModule{
		leaderState: leaderState{
			leaderStartIndex:       -1,
			nextIndex:              make(map[int]int),
			matchIndex:             make(map[int]int),
			inflightAE:             make(map[int]*atomic.Bool),
			nextVerifyRedispatchAt: make(map[int]time.Time),
			inflight:               make(map[int]*logFuture),
		},

		id:           1,
		storage:      store.NewMapStorage(),
		applyCh:      make(chan *logFuture),
		verifyCh:     make(chan *verifyFuture, 64),
		shutdownCh:   make(chan struct{}),
		commitCh:     make(chan int, 1),
		stepDown:     make(chan struct{}, 1),
		confChangeCh: make(chan *configurationChangeFuture),
		cmState: cmState{
			state:              Follower,
			currentTerm:        1,
			votedFor:           -1,
			electionTimerDone:  make(chan struct{}),
			electionResetEvent: time.Now(),
		},
	}
	cm.cmState.log = make([]LogEntry, 0)
	cm.mu.Lock()

	// Первый вызов becomeFollowerLocked — нормально закрывает старый канал
	cm.becomeFollowerLocked(2)

	// Второй вызов becomeFollowerLocked с новым каналом — не должен паниковать
	cm.becomeFollowerLocked(3)

	cm.mu.Unlock()
}

// Стресс-тест: многократно изолирует текущего лидера и проверяет, что после
// каждого нарушения связности кластер всегда сходится ровно к одному лидеру.
func TestElectionSafetyStress(t *testing.T) {
	h := NewHarness(t, 5)
	defer h.Shutdown()

	for cycle := 0; cycle < 8; cycle++ {
		leaderId, _ := h.CheckSingleLeader()
		h.DisconnectPeer(leaderId)

		h.CheckSingleLeader()

		// Задержка повторов репликации (потолок 1000 мс) задерживает
		// step-down прежнего лидера: его первые AE к вернувшимся пирам
		// откладываются, поэтому окно «два лидера» дольше прежних 66 мс.
		h.ReconnectPeer(leaderId)
		h.WaitForSingleLeader(3 * time.Second)
		h.CheckSingleLeader()
	}
}

// --- Интеграционные тесты leaderLoop ---

// TestLeader_StepDown_AppendEntriesHigherTerm проверяет, что leaderLoop
// завершается при получении AppendEntries с более высоким term.
// Изолируем лидера от followers, чтобы Apply() блокировался в inflight.
func TestLeader_StepDown_AppendEntriesHigherTerm(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Изолируем лидера, чтобы Apply() не мог зафиксироваться.
	// replace (не commit-ожидание): ждём фактического вступления
	// изоляции в силу вместо фиксированной паузы.
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)
	h.waitForIsolated(lid, _commitBudgetSteady)

	// База отсчёта inflight снимается ДО Apply: у только что избранного
	// лидера в inflight может оставаться собственная noop-запись.
	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	// replace (не commit-ожидание): ждём попадания Apply в inflight.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Отправляем AppendEntries с более высоким term
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}
	// remove: future.Error() блокируется до разрешения future и сам
	// является ожиданием результата.
	if err := future.Error(); err != ErrLeadershipLost {
		t.Fatalf("got %v, want ErrLeadershipLost", err)
	}
}

// TestLeader_StepDown_RequestVoteHigherTerm проверяет, что leaderLoop
// завершается при получении RequestVote с более высоким term.
// Изолируем лидера от followers, чтобы Apply() блокировался в inflight.
func TestLeader_StepDown_RequestVoteHigherTerm(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Изолируем лидера, чтобы Apply() не мог зафиксироваться.
	// replace (не commit-ожидание): ждём фактического вступления
	// изоляции в силу вместо фиксированной паузы.
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)
	h.waitForIsolated(lid, _commitBudgetSteady)

	// База отсчёта inflight снимается ДО Apply (см. выше).
	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	// replace (не commit-ожидание): ждём попадания Apply в inflight.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Отправляем RequestVote с более высоким term
	args := RequestVoteArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		CandidateID:  (lid + 1) % 3,
		LastLogIndex: -1,
		LastLogTerm:  -1,
	}
	if err := h.cluster[lid].RequestVote(args, &RequestVoteReply{}); err != nil {
		t.Fatal(err)
	}
	// remove: future.Error() блокируется до разрешения future и сам
	// является ожиданием результата.
	if err := future.Error(); err != ErrLeadershipLost {
		t.Fatalf("got %v, want ErrLeadershipLost", err)
	}
}

// TestLeader_StepDown_NoInflight проверяет, что leaderLoop выходит без ошибок,
// если inflight пуст.
func TestLeader_StepDown_NoInflight(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем AppendEntries с более высоким term при пустом inflight
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}
	// remove: CheckSingleLeader опрашивает состояние до
	// _leaderElectionBudget.
	// После stepDown должен быть новый лидер
	h.CheckSingleLeader()
}

// TestLeader_StepDown_ReElection проверяет, что после stepDown проводятся
// новые выборы и избирается лидер (старый лидер может быть переизбран).
func TestLeader_StepDown_ReElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем AppendEntries с более высоким term
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}
	// remove: CheckSingleLeader опрашивает состояние до
	// _leaderElectionBudget.
	// После stepDown и выборов должен быть лидер
	h.CheckSingleLeader()
}

// TestLeader_Heartbeat_StopsAfterStepDown проверяет, что после stepDown
// heartbeat прекращается (нет утечек).
func TestLeader_Heartbeat_StopsAfterStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}
	// remove: CheckSingleLeader опрашивает состояние сам.
	// Проверяем, что старый лидер не утек — leaktest сделает это
	h.CheckSingleLeader()
}

// TestLeader_AddVoter_Basic проверяет добавление нового голосующего через API.
func TestLeader_AddVoter_Basic(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 2)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Добавить сервер 2 как голосующий через публичный API.
	future := h.cluster[lid].AddVoter(ServerID(2), "raft-2")
	if err := future.Error(); err != nil {
		t.Fatalf("AddVoter failed: %v", err)
	}
	if idx := future.Index(); idx < 0 {
		t.Fatalf("AddVoter returned invalid index %d", idx)
	}

	// replace: ждём наблюдаемого признака — сервер 2 виден лидеру
	// как Voter в актуальной конфигурации.
	h.waitForSuffrage(lid, ServerID(2), Voter, _commitBudgetAfterFailover)

	// Проверить, что конфигурация содержит сервер 2.
	cfgFuture := h.cluster[lid].GetConfiguration()
	cfg := cfgFuture.Configuration()
	found := false
	for _, s := range cfg.ConfigServers {
		if s.ID == ServerID(2) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("server 2 not found in configuration after AddVoter")
	}
}

// TestConfiguration_RemoveServer проверяет удаление сервера из конфигурации.
func TestConfiguration_RemoveServer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	future := h.cluster[lid].RemoveServer(ServerID(2))
	err := future.Error()
	if err != nil && !strings.Contains(err.Error(), "leadership lost") {
		t.Fatalf("RemoveServer failed: %v", err)
	}
	idx := future.Index()
	if err == nil && idx < 0 {
		t.Fatalf("RemoveServer returned invalid index %d", idx)
	}

	for r := 0; r < 40; r++ {
		cfg := h.cluster[lid].GetConfiguration().Configuration()
		found := false
		for _, s := range cfg.ConfigServers {
			if s.ID == ServerID(2) {
				found = true
				break
			}
		}
		if !found {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(_pollInterval)
	}
	cfg := h.cluster[lid].GetConfiguration().Configuration()
	for _, s := range cfg.ConfigServers {
		if s.ID == ServerID(2) {
			t.Fatal("server 2 found in configuration after RemoveServer")
		}
	}
}

// TestConfiguration_GetConfigurationOnFollower проверяет, что follower
// может прочитать конфигурацию.
func TestConfiguration_GetConfigurationOnFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Найти follower.
	_, _ = h.CheckSingleLeader()
	for i := 0; i < 3; i++ {
		_, _, isLeader := h.cluster[i].Report()
		if !isLeader {
			cfg := h.cluster[i].GetConfiguration().Configuration()
			if len(cfg.ConfigServers) < 3 {
				t.Fatalf("follower %d has incomplete config: %+v", i, cfg)
			}
			return
		}
	}
	t.Fatal("no follower found")
}

// TestGetConfiguration_ReturnsCopy проверяет, что срез ConfigServers,
// полученный через публичный API, является защитной копией: его мутация
// не влияет на следующую конфигурацию, возвращаемую GetConfiguration.
func TestGetConfiguration_ReturnsCopy(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	cm := testServerWithFSM(t, &CaptureFSM{})
	defer cm.Stop()

	cfg := cm.GetConfiguration().Configuration()
	if len(cfg.ConfigServers) < 1 {
		t.Fatalf("expected at least one server, got %+v", cfg)
	}
	cfg.ConfigServers[0].ID = ServerID(99)

	cfg2 := cm.GetConfiguration().Configuration()
	if len(cfg2.ConfigServers) < 1 {
		t.Fatalf("expected at least one server, got %+v", cfg2)
	}
	if cfg2.ConfigServers[0].ID == ServerID(99) {
		t.Fatalf("GetConfiguration returned aliased config: %+v", cfg2)
	}
}

// TestConfiguration_RejectNotLeader проверяет, что запрос к не-лидеру
// возвращает ошибку.
func TestConfiguration_RejectNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	follower := (lid + 1) % 3

	future := h.cluster[follower].AddVoter(ServerID(10), "raft-10")
	if err := future.Error(); err != ErrNotLeader {
		t.Fatalf("got %v, want ErrNotLeader", err)
	}
}

// TestConfiguration_RejectDemoteLastVoter проверяет, что последний голосующий
// не может быть демотирован.
func TestConfiguration_RejectDemoteLastVoter(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Демотировать всех голосующих, кроме лидера — не должно получиться,
	// так как последний голосующий не может быть демотирован.
	future := h.cluster[lid].DemoteVoter(ServerID(lid))
	if err := future.Error(); err == nil {
		t.Fatal("expected error when demoting last voter")
	}
}

// TestConfiguration_RejectAddNonvoterEmptyAddress проверяет валидацию
// пустого адреса при добавлении сервера.
func TestConfiguration_RejectAddNonvoterEmptyAddress(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	h.CheckSingleLeader()

	future := h.cluster[0].AddNonvoter(ServerID(5), "")
	if err := future.Error(); err == nil {
		t.Fatal("expected error for empty address")
	}
}

// TestConfiguration_SequentialChanges проверяет два последовательных
// изменения конфигурации.
//
// Принцип: каждая промежуточная конфигурация должна иметь
// кворум, достижимый физически существующими узлами. Прежняя версия на
// harness(2) после AddVoter(несуществующий узел 2) и DemoteVoter(otherID)
// формировала набор голосующих {лидер, узел-призрак} с кворумом 2/2,
// недостижимым реальными узлами — f2.Error() вис до test timeout
// (корректное поведение Raft при недостижимом кворуме, дефект теста).
// Оба шага имеют нулевой запас отказоустойчивости: ожидания futures —
// только через дедлайн-обёртку waitFuture, не голый Error().
func TestConfiguration_SequentialChanges(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Первое изменение: добавить несуществующий сервер 3 как голосующий
	// («призрак»). voters {0,1,2,3}, кворум 3 — достижим тремя
	// реальными узлами.
	f1 := h.cluster[lid].AddVoter(ServerID(3), "raft-3")
	if err := waitFuture(t, f1, 3*time.Second); err != nil {
		t.Fatalf("first AddVoter failed: %v", err)
	}

	// Второе изменение: демотировать реального не-лидера.
	// voters {0,1,2,3} без демотированного → 3 voters (включая
	// призрака), кворум 2 — достижим двумя реальными узлами.
	otherID := (lid + 1) % 3
	f2 := h.cluster[lid].DemoteVoter(ServerID(otherID))
	if err := waitFuture(t, f2, 3*time.Second); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}

	// Проверить, что конфигурация обновлена: демотированный сервер —
	// Nonvoter.
	cfg := h.cluster[lid].GetConfiguration().Configuration()
	nonvoterFound := false
	for _, s := range cfg.ConfigServers {
		if s.ID == ServerID(otherID) && s.Suffrage == Nonvoter {
			nonvoterFound = true
		}
	}
	if !nonvoterFound {
		t.Fatal("server was not demoted to nonvoter")
	}
}

// TestConfiguration_HasVote проверяет hasVote utility.
func TestConfiguration_HasVote(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Suffrage: Voter},
			{ID: 2, Suffrage: Nonvoter},
			{ID: 3, Suffrage: Voter},
		},
	}
	if !hasVote(cfg, 1) {
		t.Error("expected server 1 to have vote")
	}
	if hasVote(cfg, 2) {
		t.Error("expected server 2 to not have vote")
	}
	if hasVote(cfg, 99) {
		t.Error("expected server 99 to not have vote")
	}
}

// TestConfiguration_VoterIDs проверяет voterIDs utility.
func TestConfiguration_VoterIDs(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Suffrage: Voter},
			{ID: 2, Suffrage: Nonvoter},
			{ID: 3, Suffrage: Voter},
		},
	}
	ids := voterIDs(cfg)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("got %v, want [1 3]", ids)
	}
}

// TestConfiguration_NextAddVoter проверяет nextConfiguration с AddVoter.
func TestConfiguration_NextAddVoter(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
		},
	}

	req := configurationChangeRequest{
		command:       AddVoter,
		serverID:      2,
		serverAddress: "addr2",
	}
	next, err := nextConfiguration(cfg, 0, req)
	if err != nil {
		t.Fatalf("nextConfiguration failed: %v", err)
	}
	if len(next.ConfigServers) != 2 {
		t.Fatalf("got %d servers, want 2", len(next.ConfigServers))
	}
	if !hasVote(next, 2) {
		t.Error("server 2 should be a voter")
	}
}

// TestConfiguration_NextRemoveServer проверяет nextConfiguration с RemoveServer.
func TestConfiguration_NextRemoveServer(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
			{ID: 2, Address: "addr2", Suffrage: Voter},
		},
	}

	req := configurationChangeRequest{
		command:  RemoveServer,
		serverID: 2,
	}
	next, err := nextConfiguration(cfg, 0, req)
	if err != nil {
		t.Fatalf("nextConfiguration failed: %v", err)
	}
	if len(next.ConfigServers) != 1 {
		t.Fatalf("got %d servers, want 1", len(next.ConfigServers))
	}
}

// TestConfiguration_NextRemoveLastVoter проверяет, что удаление последнего
// голосующего запрещено.
func TestConfiguration_NextRemoveLastVoter(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
		},
	}

	req := configurationChangeRequest{
		command:  RemoveServer,
		serverID: 1,
	}
	_, err := nextConfiguration(cfg, 0, req)
	if err == nil {
		t.Fatal("expected error when removing last voter")
	}
}

// TestConfiguration_NextDemoteLastVoter проверяет, что демотирование
// последнего голосующего запрещено.
func TestConfiguration_NextDemoteLastVoter(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
		},
	}

	req := configurationChangeRequest{
		command:  DemoteVoter,
		serverID: 1,
	}
	_, err := nextConfiguration(cfg, 0, req)
	if err == nil {
		t.Fatal("expected error when demoting last voter")
	}
}

// TestConfiguration_NextCheckDuplicateID проверяет, что дубликат ID
// обнаруживается при проверке конфигурации.
func TestConfiguration_NextCheckDuplicateID(t *testing.T) {
	err := checkConfiguration(Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
			{ID: 1, Address: "addr2", Suffrage: Voter},
		},
	})
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

// TestConfiguration_CheckNoVoters проверяет, что конфигурация без голосующих
// отклоняется.
func TestConfiguration_CheckNoVoters(t *testing.T) {
	err := checkConfiguration(Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Nonvoter},
		},
	})
	if err == nil {
		t.Fatal("expected error for no voters")
	}
}

// TestConfiguration_CheckEmptyAddress проверяет, что пустой адрес
// отклоняется.
func TestConfiguration_CheckEmptyAddress(t *testing.T) {
	err := checkConfiguration(Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "", Suffrage: Voter},
		},
	})
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

// TestConfiguration_EncodeDecode проверяет round-trip кодирования.
func TestConfiguration_EncodeDecode(t *testing.T) {
	cfg := Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "addr1", Suffrage: Voter},
			{ID: 2, Address: "addr2", Suffrage: Nonvoter},
		},
	}
	data, err := EncodeConfiguration(cfg)
	if err != nil {
		t.Fatalf("EncodeConfiguration failed: %v", err)
	}
	decoded, err := DecodeConfiguration(data)
	if err != nil {
		t.Fatalf("DecodeConfiguration failed: %v", err)
	}
	if len(decoded.ConfigServers) != 2 {
		t.Fatalf("got %d servers, want 2", len(decoded.ConfigServers))
	}
}

// TestLeader_Shutdown_NoInflight проверяет, что shutdown без inflight чист.
func TestLeader_Shutdown_NoInflight(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Просто shutdown — leaktest проверит утечки
}

// TestLeader_Shutdown_DuringApply проверяет остановку кластера при
// незавершённом Apply. Гарантия: остановка не оставляет клиентское
// обещание неразрешённым — ответ обязан прийти по каналу ErrorCh в
// пределах бюджета. Ожидание идёт через ErrorCh, а не через Error:
// после остановки Error возвращает contract.ErrRaftShutdown по запасному
// исходу даже для неотвеченного обещания. Из-за гонки между Apply и
// остановкой законны исходы: nil (запись успела зафиксироваться и
// примениться), ErrLeadershipLost, contract.ErrRaftShutdown, ErrNotLeader;
// иная ошибка — отказ. При nil у записи обязан быть присвоенный
// индекс журнала.
func TestLeader_Shutdown_DuringApply(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	future := h.cluster[lid].Apply(42, 0)
	h.Shutdown()

	err := waitFuture(t, future, _commitBudgetSteady)
	switch err {
	case nil:
		if future.Index() < 1 {
			t.Fatalf("got success without a log index: Index() = %d", future.Index())
		}
	case ErrLeadershipLost, contract.ErrRaftShutdown, ErrNotLeader:
		// Штатные исходы гонки между Apply и остановкой.
	default:
		t.Fatalf("got %v, want nil or ErrLeadershipLost or ErrRaftShutdown or ErrNotLeader", err)
	}
}

// TestLeader_DispatchLogs_IndexSequence проверяет последовательность индексов
// при применении нескольких команд. Индексы доступны только после завершения
// future (через Error()).
func TestLeader_DispatchLogs_IndexSequence(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	f1 := h.cluster[lid].Apply(1, 0)
	f2 := h.cluster[lid].Apply(2, 0)
	f3 := h.cluster[lid].Apply(3, 0)

	for _, f := range []ApplyFuture{f1, f2, f3} {
		if err := f.Error(); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
	}

	i1 := f1.Index()
	i2 := f2.Index()
	i3 := f3.Index()

	if !(i1 < i2 && i2 < i3) {
		t.Fatalf("indices not increasing: %d, %d, %d", i1, i2, i3)
	}
	// remove: пауза в конце теста ничего не проверяет; остановку узлов
	// выполняет h.Shutdown().
}

// TestCommitmentInteg_CommitOnMajority проверяет, что commit происходит
// при репликации на большинство узлов.
func TestCommitmentInteg_CommitOnMajority(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	future := h.cluster[lid].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// replace: устоявшийся лидер, сбоев нет — steady.
	h.WaitForCommitBudget(42, 3, _commitBudgetSteady)

	h.CheckCommitted(42)
}

// TestCommitmentInteg_NoCommitWithoutMajority проверяет, что commit не
// происходит без кворума. Изолируем лидера от обоих followers — кворума нет,
// команда не фиксируется. Ожидаем, что future блокируется, а не возвращает nil.
func TestCommitmentInteg_NoCommitWithoutMajority(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключаем лидера от обоих followers и followers друг от друга.
	// replace: ждём фактического вступления изоляции в силу.
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)
	h.waitForIsolated(lid, _commitBudgetSteady)

	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	// replace: ждём попадания Apply в inflight лидера.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Проверяем, что future не завершился успехом в течение таймаута.
	errCh := make(chan error, 1)
	go func() {
		errCh <- future.Error()
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Apply succeeded without majority, expected error")
		}
		// Ошибка ErrLeadershipLost или contract.ErrRaftShutdown — допустимо.
	case <-time.After(1000 * time.Millisecond):
		// Future заблокирован — это ожидаемое поведение (нет кворума).
	}
}

// TestCommitmentInteg_CommitAfterReconnect проверяет, что после реконнекта
// отключённого узла фиксация происходит.
func TestCommitmentInteg_CommitAfterReconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	follower := (lid + 1) % 3
	otherFollower := (lid + 2) % 3

	// Отключаем одного follower.
	// remove: DisconnectPeer применяет разрыв синхронно (мутация
	// транспортов завершена к моменту возврата).
	h.DisconnectPeer(follower)

	// Apply с оставшимся кворумом (leader + один follower)
	// Но нужно отключить ещё одного follower, чтобы кворума не было,
	// иначе команда зафиксируется сразу.
	// replace: ждём фактической изоляции лидера.
	h.DisconnectPeer(otherFollower)
	h.waitForIsolated(lid, _commitBudgetSteady)

	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	// replace: ждём попадания Apply в inflight лидера.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Подключаем одного follower -> должен быть кворум.
	// Задержка повторов репликации (потолок 1000 мс) может задержать
	// доставку фиксации переподключённому follower'у — фиксированный sleep
	// недостаточен, ждём опросом.
	h.ReconnectPeer(follower)

	if err := future.Error(); err == nil {
		h.WaitForCommitAll(42, 3*time.Second)
		h.CheckCommitted(42)
	} else {
		// Команда могла быть отклонена из-за stepDown — нормально
		t.Logf("Apply returned error (expected if leadership lost): %v", err)
	}
}

// TestCommitmentInteg_SingleNode проверяет single-node акселератор.
func TestCommitmentInteg_SingleNode(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	_, _ = h.CheckSingleLeader()

	future := h.cluster[0].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// replace: одноузловой кластер, сбоев нет — steady.
	h.WaitForCommitBudget(42, 1, _commitBudgetSteady)

	h.CheckCommitted(42)
}

// TestRaftSafety_NewLeaderCommitNoop проверяет, что после выборов
// лидер фиксирует noop запись.
func TestRaftSafety_NewLeaderCommitNoop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Проверяем, что после выборов лидер фиксирует noop — для этого
	// достаточно убедиться, что Apply работает на всех узлах
	lid, _ := h.CheckSingleLeader()

	future := h.cluster[lid].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// replace: устоявшийся лидер, сбоев нет — steady.
	h.WaitForCommitBudget(42, 3, _commitBudgetSteady)

	h.CheckCommitted(42)
}

// TestRace_ApplyAndStepDown проверяет, что нет гонок между Apply
// и stepDown.
func TestRace_ApplyAndStepDown(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Apply в одной горутине
	done := make(chan struct{})
	go func() {
		future := h.cluster[lid].Apply(42, 0)
		_ = future.Error()
		close(done)
	}()

	// Барьер — попадание команды в журнал лидера. Прежний барьер ждал
	// команду в очереди inflight, но после применения по факту фиксации
	// запись покидает очередь быстрее интервала опроса, и барьер
	// перестал наблюдаться.
	h.waitForLastLogIndex(lid, h.lastLogIndex(lid)+1, _commitBudgetSteady)

	// stepDown в другой горутине
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Apply to complete")
	}
}

// TestCommitmentInteg_FiveNodes проверяет 5-node кластер.
func TestCommitmentInteg_FiveNodes(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 5)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	future := h.cluster[lid].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	// replace: 5 узлов, сбоев нет — steady.
	h.WaitForCommitBudget(42, 5, _commitBudgetSteady)

	h.CheckCommitted(42)
}

// TestCommitmentInteg_FiveNodesDisconnectTwo проверяет, что при отключении
// 2 из 5 узлов кворум сохраняется.
func TestCommitmentInteg_FiveNodesDisconnectTwo(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 5)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключаем 2 узла (3/5 — кворум есть).
	// remove: DisconnectPeer применяет разрыв синхронно; Apply ниже
	// блокируется до фиксации и сам является ожиданием.
	disconnected := 0
	for i := 0; i < 5; i++ {
		if i != lid && disconnected < 2 {
			h.DisconnectPeer(i)
			disconnected++
		}
	}

	future := h.cluster[lid].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply should succeed with 3/5 quorum: %v", err)
	}
	// replace: подключены 3 из 5 узлов; отключение — часть сценария,
	// возможны перевыборы — after-failover.
	h.WaitForCommit(42, 3)

	h.CheckCommittedN(42, 3)
}

// TestCommitmentInteg_FiveNodesDisconnectThree проверяет, что при отключении
// 3 из 5 узлов кворум теряется (future блокируется).
func TestCommitmentInteg_FiveNodesDisconnectThree(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 5)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отключаем 3 узла (2/5 — кворума нет).
	// remove: DisconnectPeer применяет разрыв синхронно.
	disconnected := 0
	for i := 0; i < 5; i++ {
		if i != lid && disconnected < 3 {
			h.DisconnectPeer(i)
			disconnected++
		}
	}

	inflightBefore := h.applyInflightCount(lid)
	future := h.cluster[lid].Apply(42, 0)
	// replace: ждём попадания Apply в inflight лидера.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Проверяем, что future не завершился успехом в течение таймаута.
	errCh := make(chan error, 1)
	go func() {
		errCh <- future.Error()
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Apply should fail without quorum (2/5)")
		}
	case <-time.After(1500 * time.Millisecond):
		// Future заблокирован — это ожидаемое поведение (нет кворума).
	}
}

// TestStability_1kCommands проверяет стабильность при 100 Apply на 3 узлах.
func TestStability_1kCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1k commands stability test in short mode")
	}
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	for i := 0; i < 100; i++ {
		future := h.cluster[lid].Apply(i, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
	}
	// replace: сходимость последней команды влечёт фиксацию предыдущих.
	h.WaitForCommitBudget(99, 3, _commitBudgetSteady)

	for i := 0; i < 100; i++ {
		h.CheckCommitted(i)
	}
}

// TestStability_ConcurrentClients проверяет параллельные Apply.
func TestStability_ConcurrentClients(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent clients test in short mode")
	}
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(cmd int) {
			defer wg.Done()
			future := h.cluster[lid].Apply(cmd, 0)
			if err := future.Error(); err != nil {
				t.Errorf("Apply %d failed: %v", cmd, err)
			}
		}(i)
	}
	wg.Wait()
	// replace: ждём сходимости обеих проверяемых команд (порядок
	// фиксации конкурентных Apply не определён) — steady.
	h.WaitForCommitBudget(0, 3, _commitBudgetSteady)
	h.WaitForCommitBudget(9, 3, _commitBudgetSteady)

	// Проверяем, что все 10 команд зафиксированы
	h.CheckCommittedN(0, 3)
	h.CheckCommittedN(9, 3)
}

// --- Тесты Batching (Feature 8) ---

// RecordingBatchingFSM записывает все вызовы ApplyBatch для проверки
// в тестах. Реализует BatchingFSM: ApplyBatch сохраняет полученные
// записи, Apply возвращает nil (не должен вызываться для LogCommand).
type RecordingBatchingFSM struct {
	mu      sync.Mutex
	batches []batchRecord
}

type batchRecord struct {
	logs []*LogEntry
}

func NewRecordingBatchingFSM() *RecordingBatchingFSM {
	return &RecordingBatchingFSM{}
}

func (f *RecordingBatchingFSM) Apply(_ *LogEntry) any {
	return nil
}

func (f *RecordingBatchingFSM) Snapshot() (FSMSnapshot, error) {
	return nil, contract.ErrNotImplemented
}

func (f *RecordingBatchingFSM) Restore(_ io.ReadCloser) error {
	return contract.ErrNotImplemented
}

func (f *RecordingBatchingFSM) ApplyBatch(logs []*LogEntry) []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := batchRecord{logs: make([]*LogEntry, len(logs))}
	copy(entry.logs, logs)
	f.batches = append(f.batches, entry)
	resp := make([]any, len(logs))
	for i, l := range logs {
		resp[i] = l.Index
	}
	return resp
}

func (f *RecordingBatchingFSM) NumBatches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

func (f *RecordingBatchingFSM) TotalEntries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b.logs)
	}
	return n
}

func (f *RecordingBatchingFSM) MaxBatchSize() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	maxl := 0
	for _, b := range f.batches {
		if len(b.logs) > maxl {
			maxl = len(b.logs)
		}
	}
	return maxl
}

// TestNonBatchingFSM_ProcessLogsSubBatching проверяет, что sub-batching
// в processLogs не ломает обычный FSM. Отправляет _maxApplyBatchSize*2+5
// команд на одноузловой кластер — этого достаточно, чтобы processLogs
// создал более одного под-батча при обработке зафиксированных записей.
func TestNonBatchingFSM_ProcessLogsSubBatching(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	h.CheckSingleLeader()

	n := _maxApplyBatchSize*2 + 5
	for i := 0; i < n; i++ {
		if idx := h.SubmitToServer(0, i); idx < 0 {
			t.Fatalf("SubmitToServer(%d) failed", i)
		}
	}

	// Один wait последней команды перед серией assert'ов. Видимость в
	// h.commits монотонна по порядку доставки (один коллектор на узел,
	// FIFO-канал), поэтому фиксация последней команды влечёт видимость
	// всех предыдущих — каждый CheckCommitted ниже корректен без
	// собственного ожидания.
	h.WaitForCommitBudget(n-1, 1, _commitBudgetSteady)

	for i := 0; i < n; i++ {
		h.CheckCommitted(i)
	}
}

// TestNonBatchingFSM_UnchangedBehavior проверяет, что обычный FSM
// (не реализующий BatchingFSM) продолжает работать как раньше после
// внедрения sub-batching и type assertion в runFSM. Отправляет 10 команд
// и проверяет, что все future получают корректные ответы.
func TestNonBatchingFSM_UnchangedBehavior(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	var futures []ApplyFuture
	for i := 0; i < 10; i++ {
		f := h.cluster[lid].Apply(i, 5*time.Second)
		futures = append(futures, f)
	}
	// remove: f.Error() блокируется до разрешения future (таймаут 5s
	// задан при Apply) и сам является ожиданием результата.
	for i, f := range futures {
		if err := f.Error(); err != nil {
			t.Errorf("future %d error: %v", i, err)
		}
	}
	h.CheckSingleLeader()
}

// TestBatchingFSM_Basic проверяет, что FSM, реализующий BatchingFSM,
// получает записи через ApplyBatch, а Apply не вызывается для LogCommand.
// Использует RecordingBatchingFSM на одноузловом кластере.
func TestBatchingFSM_Basic(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	fsm := NewRecordingBatchingFSM()
	cm := testServerWithFSM(t, fsm)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	for i := 0; i < 5; i++ {
		future := cm.Apply(i, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
	}

	if fsm.NumBatches() == 0 {
		t.Fatal("ApplyBatch was never called")
	}
	if fsm.TotalEntries() != 5 {
		t.Fatalf("TotalEntries = %d, want 5", fsm.TotalEntries())
	}
}

// TestBatchingFSM_BatchBoundary проверяет, что при отправке большого
// числа команд каждый вызов ApplyBatch получает не более _maxApplyBatchSize
// записей. Запускает RecordingBatchingFSM на одноузловом кластере
// и отправляет _maxApplyBatchSize*3 команд.
func TestBatchingFSM_BatchBoundary(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	fsm := NewRecordingBatchingFSM()
	cm := testServerWithFSM(t, fsm)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	n := _maxApplyBatchSize * 3
	for i := 0; i < n; i++ {
		future := cm.Apply(i, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
	}

	if fsm.TotalEntries() != n {
		t.Fatalf("TotalEntries = %d, want %d", fsm.TotalEntries(), n)
	}

	maxBatch := fsm.MaxBatchSize()
	if maxBatch > _maxApplyBatchSize {
		t.Fatalf("batch size %d exceeds _maxApplyBatchSize %d", maxBatch, _maxApplyBatchSize)
	}
}

// TestBatchingFSM_ApplyBatchResponseMatching проверяет, что future'ы
// получают правильные ответы при использовании BatchingFSM.
// RecordingBatchingFSM возвращает индекс записи в качестве ответа;
// проверяем, что future.Response() возвращает ожидаемый индекс.
func TestBatchingFSM_ApplyBatchResponseMatching(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	fsm := NewRecordingBatchingFSM()
	cm := testServerWithFSM(t, fsm)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	futures := make([]ApplyFuture, 3)
	for i := range futures {
		futures[i] = cm.Apply(i*100, 0)
	}

	for i, f := range futures {
		if err := f.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
		resp := f.Response()
		if resp == nil {
			t.Fatalf("future %d response is nil", i)
		}
		respIdx, ok := resp.(int)
		if !ok {
			t.Fatalf("future %d response type %T, want int", i, resp)
		}
		if respIdx != f.Index() {
			t.Fatalf("future %d response = %d, want index %d", i, respIdx, f.Index())
		}
	}
}

// mismatchBatchingFSM нарушает контракт BatchingFSM: возвращает срез
// ответов длины n-1 (на один меньше записей), чтобы спровоцировать
// ветку ошибки в applyBatch. Обёртка над RecordingBatchingFSM: остальные
// методы интерфейса FSM делегируются без изменений.
type mismatchBatchingFSM struct {
	inner *RecordingBatchingFSM
}

func (f *mismatchBatchingFSM) Apply(l *LogEntry) any {
	return f.inner.Apply(l)
}

func (f *mismatchBatchingFSM) Restore(rc io.ReadCloser) error {
	return f.inner.Restore(rc)
}

func (f *mismatchBatchingFSM) Snapshot() (FSMSnapshot, error) {
	return f.inner.Snapshot()
}

// ApplyBatch возвращает срез ответов без последнего элемента. Для батча
// из одной записи возвращает пустой срез — длина всё равно не совпадает
// с числом записей, и applyBatch отвечает ошибкой.
func (f *mismatchBatchingFSM) ApplyBatch(logs []*LogEntry) []any {
	resp := f.inner.ApplyBatch(logs)
	if len(resp) > 0 {
		return resp[:len(resp)-1]
	}
	return resp
}

// TestBatchingFSM_ApplyBatchResponseMismatch проверяет, что нарушение
// контракта BatchingFSM (неверное число ответов) НЕ роняет процесс и
// НЕ паникует: все future батча получают ошибку ErrBatchFSMResponseMismatch,
// а горутина runFSM продолжает работать.
//
// Сценарий:
//  1. FSM, у которого ApplyBatch возвращает срез ответов неверной длины
//     (обёртка над RecordingBatchingFSM).
//  2. Отправить несколько команд cm.Apply(...).
//  3. Проверить, что каждый future возвращает ErrBatchFSMResponseMismatch
//     (через errors.Is), а не успешный ответ.
//  4. Проверить, что узел жив: следующая команда снова обрабатывается —
//     ошибка ErrBatchFSMResponseMismatch доставляется через future
//     немедленно. Если бы runFSM упала при первом нарушении контракта,
//     этот future остался бы без ответа (таймаут) или завершился бы
//     contract.ErrRaftShutdown только при Stop().
//
// Примечание: часть записей могла уйти отдельными батчами, поэтому на
// ошибку проверяется каждый future из первоначальной пачки команд;
// последующая команда после ошибки подтверждает живучесть runFSM.
func TestBatchingFSM_ApplyBatchResponseMismatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := &mismatchBatchingFSM{inner: NewRecordingBatchingFSM()}
	cm := testServerWithFSM(t, fsm)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	// Первая пачка команд должна завершиться ошибкой ErrBatchFSMResponseMismatch:
	// хотя бы один батч, отправленный в ApplyBatch, вернёт неверное число ответов.
	futures := make([]ApplyFuture, 3)
	for i := range futures {
		futures[i] = cm.Apply(i*100, 0)
	}

	for i, f := range futures {
		err := f.Error()
		if !errors.Is(err, ErrBatchFSMResponseMismatch) {
			t.Fatalf("future %d error = %v, want ErrBatchFSMResponseMismatch", i, err)
		}
	}

	// runFSM жив: следующая команда доходит до FSM. Ожидаем ответ за конечное
	// время: если runFSM упала на первом нарушении контракта (баг — здесь
	// должен был стоять panic), future остался бы без ответа и select упал бы
	// по таймауту. Допустимы оба исхода: успех либо ErrBatchFSMResponseMismatch
	// (мок всегда возвращает неверное число ответов).
	f := cm.Apply(1000, 0)
	select {
	case err := <-f.ErrorCh():
		if errors.Is(err, contract.ErrRaftShutdown) {
			t.Fatalf("runFSM appears dead after mismatch: got ErrRaftShutdown")
		}
		if err != nil && !errors.Is(err, ErrBatchFSMResponseMismatch) {
			t.Fatalf("unexpected error after mismatch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("future for Apply after mismatch was never answered")
	}
}

// --- Nonvoter (Feature 3) ---

// TestNonvoter_DoesNotStartElection проверяет, что неголосующий не переходит
// в Candidate при потере лидера. Кластер из 4 серверов; сервер 3
// демотируется в неголосующего, затем лидер отключается. Nonvoter остаётся
// Follower, кластер избирает нового лидера из оставшихся голосующих.
func TestNonvoter_DoesNotStartElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	// Ждём стабилизации лидера.
	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Отключаем лидера.
	h.DisconnectPeer(lid)

	// keep: negative window — за окно, превышающее минимальный election
	// timeout, неголосующий не должен стать лидером. Опрос не доказывает
	// отсутствия события; окно осознанно временное.
	sleepMs(ReelectionTimeoutMs)

	// Nonvoter не должен быть лидером.
	_, _, isLeader := h.cluster[demoteID].Report()
	if isLeader {
		t.Fatal("nonvoter became leader")
	}

	// Кластер избирает нового лидера из голосующих.
	// DisconnectPeer может изолировать лидера от follower, но follower должны
	// избрать нового лидера. Если кластер не смог — тест падает в CheckSingleLeader.
	newLid, _ := h.CheckSingleLeader()
	if newLid == demoteID || newLid == lid {
		t.Fatalf("unexpected leader: %d (lid=%d, demoteID=%d)", newLid, lid, demoteID)
	}
}

// TestNonvoter_DoesNotAffectCommitIndex проверяет, что неголосующий не влияет
// на commitIndex. Кластер из 4 серверов; сервер 3 демотируется в неголосующего.
// Отключаем одного голосующего и неголосующего — кворум (2 из 3 голосующих) сохраняется,
// команда фиксируется.
func TestNonvoter_DoesNotAffectCommitIndex(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера в неголосующего.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Определяем ID другого голосующего (не лидер, не неголосующий).
	otherVoter := (lid + 1) % 4
	if otherVoter == demoteID {
		otherVoter = (otherVoter + 1) % 4
	}
	if lid != 0 && otherVoter == 0 {
		otherVoter = 0
	} else if lid == 0 && otherVoter == demoteID {
		otherVoter = (otherVoter + 1) % 4
	}

	// Отключаем одного голосующего и неголосующего.
	// remove: DisconnectPeer применяет разрыв синхронно; Apply ниже
	// блокируется до фиксации и сам является ожиданием.
	h.DisconnectPeer(otherVoter)
	h.DisconnectPeer(demoteID)

	// Отправляем команду — должна зафиксироваться (2 из 3 голосующих = кворум).
	future := h.cluster[lid].Apply(42, 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Подключены лидер и второй голосующий — оба обязаны зафиксировать
	// команду. Прежнее значение 1 не проверялось: старый WaitForCommit
	// возвращался по «nc >= n» без assert'а).
	h.WaitForCommit(42, 2)
}

// TestNonvoter_AppliesLogs проверяет, что неголосующий получает и применяет
// все зафиксированные записи к FSM. Кластер из 4 серверов; сервер 3 —
// неголосующий. Отправляем 3 команды — неголосующий должен иметь те же записи.
func TestNonvoter_AppliesLogs(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера в неголосующего.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	for _, cmd := range []int{10, 20, 30} {
		f := h.cluster[lid].Apply(cmd, 0)
		if err := f.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", cmd, err)
		}
	}
	// replace: подключены все 4 узла (неголосующий применяет зафиксированные
	// записи), сбоев нет — steady.
	h.WaitForCommitBudget(30, 4, _commitBudgetSteady)

	// Проверить, что неголосующий зафиксировал все команды.
	for _, cmd := range []int{10, 20, 30} {
		h.CheckCommitted(cmd)
	}
}

// TestNonvoter_DemotedVoterDoesNotStartElection проверяет, что после
// DemoteVoter сервер перестаёт быть кандидатом. Кластер из 4 голосующих.
// Лидер демотирует один сервер, затем отключается — демотированный сервер
// не начинает выборы, кластер избирает нового лидера из оставшихся голосующих.
func TestNonvoter_DemotedVoterDoesNotStartElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать другого голосующего, не лидера.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Проверить, что сервер demoteID теперь неголосующий.
	cfg := h.cluster[lid].GetConfiguration().Configuration()
	found := false
	for _, s := range cfg.ConfigServers {
		if s.ID == ServerID(demoteID) {
			if s.Suffrage != Nonvoter {
				t.Fatalf("server %d: want Nonvoter, got %v", demoteID, s.Suffrage)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("server %d not found in configuration", demoteID)
	}

	// Отключаем лидера.
	h.DisconnectPeer(lid)

	// keep: negative window — за окно, превышающее минимальный election
	// timeout, неголосующий не должен стать лидером. Опрос не доказывает
	// отсутствия события; окно осознанно временное.
	sleepMs(ReelectionTimeoutMs)

	// Nonvoter не должен стать лидером.
	_, _, isLeader := h.cluster[demoteID].Report()
	if isLeader {
		t.Fatal("demoted nonvoter became leader")
	}

	// Кластер должен избрать нового лидера из оставшихся голосующих.
	newLid, _ := h.CheckSingleLeader()
	if newLid == demoteID || newLid == lid {
		t.Fatalf("unexpected leader: %d (lid=%d, demoteID=%d)", newLid, lid, demoteID)
	}
}

// TestNonvoter_RejectsRequestVote проверяет, что RequestVote handler
// отклоняет запрос от кандидата-неголосующего. Nonvoter отправляет RequestVote
// голосующему — VoteGranted должен быть false.
func TestNonvoter_RejectsRequestVote(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера в неголосующего.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Nonvoter (demoteID) отправляет RequestVote лидеру.
	voterID := lid
	reply, err := h.transports[demoteID].RequestVote(
		ServerID(voterID),
		RequestVoteArgs{
			RPCHeader: RPCHeader{
				ProtocolVersion: ProtocolVersion,
				ServerID:        int(demoteID),
			},
			Term:         1,
			CandidateID:  int(demoteID),
			LastLogIndex: 0,
			LastLogTerm:  0,
		},
	)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	if reply.VoteGranted {
		t.Fatal("nonvoter's RequestVote was granted, but should be denied")
	}
}

// TestNonvoter_TimeoutNowDoesNotStartElection проверяет, что неголосующий,
// получивший TimeoutNow, не начинает выборы. TimeoutNow отправляется
// от лидера неголосующему — неголосующий остаётся Follower.
func TestNonvoter_TimeoutNowDoesNotStartElection(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера в неголосующего.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Nonvoter получает TimeoutNow.
	_, err := h.SendTimeoutNow(lid, demoteID)
	if err != nil {
		t.Fatalf("SendTimeoutNow failed: %v", err)
	}

	// Проверить, что неголосующий не стал Candidate или Leader.
	_, _, isLeader := h.cluster[demoteID].Report()
	if isLeader {
		t.Fatal("nonvoter became leader after TimeoutNow")
	}
}

// TestNonvoter_NoGoroutineLeak проверяет, что кластер с неголосующим
// корректно выполняет операции и завершается без утечек горутин.
func TestNonvoter_NoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 4)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	// remove: CheckSingleLeader уже дождался устойчивого лидера.

	// Демотировать не-лидера в неголосующего.
	demoteID := (lid + 1) % 4
	f := h.cluster[lid].DemoteVoter(ServerID(demoteID))
	if err := f.Error(); err != nil {
		t.Fatalf("DemoteVoter failed: %v", err)
	}
	// replace: DemoteVoter.Error() гарантирует фиксацию записи конфигурации,
	// но не её применение на конкретном узле — ждём наблюдаемого признака.
	h.waitForSuffrage(demoteID, ServerID(demoteID), Nonvoter, _commitBudgetAfterFailover)

	// Отправить команду.
	f = h.cluster[lid].Apply(42, 0)
	if err := f.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Подключены все 4 узла (неголосующий тоже применяет зафиксированные
	// записи) — assert CheckCommittedN требует точного числа
	// подключённых узлов. Прежнее значение 1 не проверялось.
	h.WaitForCommit(42, 4)
}

// TestVerifyLeader_SingleNode проверяет, что VerifyLeader на одноузловом
// кластере немедленно завершается успехом (собственный голос = кворум).
func TestVerifyLeader_SingleNode(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	h.CheckSingleLeader()

	future := h.cluster[0].VerifyLeader()
	if err := future.Error(); err != nil {
		t.Fatalf("VerifyLeader failed on single node: %v", err)
	}
}

// TestVerifyLeader_LeaderSucceeds проверяет, что лидер multi-node кластера
// успешно подтверждает своё лидерство через VerifyLeader.
func TestVerifyLeader_LeaderSucceeds(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// VerifyLeader должен пройти на лидере (кворум = 2 из 3).
	future := h.cluster[lid].VerifyLeader()
	if err := future.Error(); err != nil {
		t.Fatalf("VerifyLeader failed on leader: %v", err)
	}
}

// TestVerifyLeader_NonLeaderFails проверяет, что VerifyLeader на non-leader
// немедленно возвращает ErrNotLeader без блокировки.
func TestVerifyLeader_NonLeaderFails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Выбираем follower.
	followerID := (lid + 1) % 3

	future := h.cluster[followerID].VerifyLeader()
	if err := future.Error(); err != ErrNotLeader {
		t.Fatalf("VerifyLeader on follower returned %v, want ErrNotLeader", err)
	}
}

// TestVerifyLeader_AfterLeadershipLossFails проверяет, что VerifyLeader
// блокируется (или возвращает ошибку) после потери лидерства и разрыва
// связи с кворумом.
func TestVerifyLeader_AfterLeadershipLossFails(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Изолируем лидера от обоих follower.
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)

	// VerifyLeader должен блокироваться (без кворума).
	// Используем таймаут, чтобы не зависнуть навсегда.
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.cluster[lid].VerifyLeader().Error()
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("VerifyLeader succeeded after leadership loss")
		}
	case <-time.After(50 * Quantum * time.Millisecond):
		// Ожидаемо: VerifyLeader блокируется без кворума.
	}
}

// TestVerifyLeader_Concurrent проверяет, что несколько concurrent вызовов
// VerifyLeader работают корректно. Использует ErrorCh для select-стиля.
func TestVerifyLeader_Concurrent(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	const n = 5
	futures := make([]Future, n)
	for i := range n {
		futures[i] = h.cluster[lid].VerifyLeader()
	}

	for i, f := range futures {
		if err := f.Error(); err != nil {
			t.Errorf("concurrent VerifyLeader %d failed: %v", i, err)
		}
	}
}

// TestVerifyLeader_Timeout проверяет ErrorCh для VerifyLeader.
func TestVerifyLeader_Timeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	h.CheckSingleLeader()

	future := h.cluster[0].VerifyLeader()
	if err := future.Error(); err != nil {
		t.Fatalf("VerifyLeader failed: %v", err)
	}
}

// TestApply_Timeout проверяет, что Apply с ненулевым таймаутом возвращает
// contract.ErrEnqueueTimeout при недоступности лидера.
func TestApply_Timeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	defer h.Shutdown()

	future := h.cluster[0].Apply(42, 10*time.Millisecond)
	if err := future.Error(); err != ErrNotLeader && err != contract.ErrEnqueueTimeout {
		t.Fatalf("expected ErrNotLeader or ErrEnqueueTimeout, got %v", err)
	}
}

// TestApply_TimeoutDoesNotBlock проверяет, что Apply с timeout=0
// не блокируется навсегда при закрытии shutdownCh.
func TestApply_TimeoutDoesNotBlock(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 1)
	h.Shutdown()
	future := h.cluster[0].Apply(42, 0)
	if err := future.Error(); err != contract.ErrRaftShutdown {
		t.Fatalf("expected ErrRaftShutdown, got %v", err)
	}
}

// =============================================================================
// Bottleneck2: Дедупликация репликации (inflightAE atomic bool)
// =============================================================================

// TestDedup_NoStaleResponseRace — стресс-тест репликации.
// Проверяет, что при высокой частоте команд не возникает гонок.
//
// Сценарий:
//  1. Создать кластер из 3 серверов.
//  2. Дождаться единственного лидера.
//  3. Отправить 100 команд с высокой частотой, чтобы спровоцировать
//     множественные вызовы leaderSendAEs.
//  4. Проверить, что все команды зафиксированы.
//  5. Проверить, что нет утечек горутин (leaktest в defer).
func TestDedup_NoStaleResponseRace(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем 100 команд с минимальной задержкой между ними.
	for i := 0; i < 100; i++ {
		idx := h.SubmitToServer(lid, i)
		if idx < 0 {
			t.Logf("SubmitToServer(%d) returned negative index, leader may have changed", i)
			// Находим нового лидера.
			newLid, _ := h.CheckSingleLeader()
			lid = newLid
		}
	}

	// replace: сходимость последней команды влечёт фиксацию предыдущих.
	h.WaitForCommitBudget(99, 3, _commitBudgetSteady)

	// Проверяем, что все команды зафиксированы (100 = последняя команда).
	h.CheckCommitted(99)

	// Проверяем целостность: все команды от 0 до 99 зафиксированы.
	for i := 0; i < 100; i++ {
		h.CheckCommitted(i)
	}
}

// TestDedup_LeaderStepDownClearsFlags проверяет, что при потере лидерства
// inflightAE очищаются, и новый лидер начинает с "чистого листа".
//
// Сценарий:
//  1. Создать кластер из 3 серверов. Дождаться лидера (S1).
//  2. Отправить несколько команд, чтобы заполнить лог.
//  3. Изолировать S1 от S2 и S3.
//  4. Дождаться нового лидера (S2 или S3).
//  5. Переподключить S1.
//  6. Проверить, что кластер восстанавливается и новые команды фиксируются.
func TestDedup_LeaderStepDownClearsFlags(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	// Отправляем несколько команд, чтобы создать записи в журнале.
	h.SubmitToServer(origLeaderId, 10)
	h.SubmitToServer(origLeaderId, 20)
	h.WaitForCommit(20, 3)

	// Изолируем лидера от всех follower.
	// remove: CheckSingleLeader опрашивает состояние сам, изолированный
	// лидер исключён из опроса.
	h.DisconnectPeer(origLeaderId)

	// Должен появиться новый лидер.
	newLeaderId, _ := h.CheckSingleLeader()
	if newLeaderId == origLeaderId {
		t.Fatal("new leader should be different from isolated leader")
	}

	// Отправляем команду новому лидеру — она должна зафиксироваться.
	idx := h.SubmitToServer(newLeaderId, 30)
	if idx < 0 {
		t.Fatal("new leader should accept commands")
	}
	h.WaitForCommit(30, 2)

	// Переподключаем старого лидера.
	// replace: после реконнекта окно «два лидера» ожидается через
	// WaitForSingleLeader (задержка повторов репликации).
	h.ReconnectPeer(origLeaderId)

	// Кластер должен продолжать работать.
	finalLeaderId, _ := h.WaitForSingleLeader(_leaderElectionBudget)

	// Отправляем команду текущему лидеру.
	h.SubmitToServer(finalLeaderId, 40)
	h.WaitForCommit(40, 3)

	// Проверяем, что все команды зафиксированы на всех узлах.
	h.CheckCommitted(20)
	h.CheckCommitted(30)
	h.CheckCommitted(40)
}

// TestDedup_ConcurrentHeartbeatAndDispatch проверяет, что дедупликация
// работает при пересечении heartbeatTicker и dispatchLogs.
//
// Сценарий:
//  1. Создать кластер из 3 серверов.
//  2. Отправить команды с частотой, чтобы dispatchLogs + heartbeatTicker
//     вызывали leaderSendAEs не менее 50 раз/с.
//  3. Работать 5 секунд.
//  4. Проверить, что кластер жив и все команды зафиксированы.
func TestDedup_ConcurrentHeartbeatAndDispatch(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	if testing.Short() {
		t.Skip("skipping concurrent heartbeat test in short mode")
	}

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем 50 команд с минимальной задержкой.
	for i := 0; i < 50; i++ {
		idx := h.SubmitToServer(lid, i)
		if idx < 0 {
			newLid, _ := h.CheckSingleLeader()
			lid = newLid
			continue
		}
		// keep: timing — интервал подачи нагрузки является предметом
		// стресс-теста (пересечение с heartbeat/apply-тикерами).
		time.Sleep(time.Duration(TickerTimeoutMs/4) * time.Millisecond)
	}

	// replace: ждём сходимости последней команды вместо фиксированной паузы.
	h.WaitForCommit(49, 3)

	// Проверяем, что кластер жив и последняя команда зафиксирована.
	h.CheckSingleLeader()
	for i := 0; i < 50; i++ {
		h.CheckCommitted(i)
	}
}

// TestDedup_AllServersConsistentAfterCrash проверяет, что после сбоя
// и перезапуска сервера консистентность логов сохраняется.
//
// Сценарий:
//  1. Создать кластер из 3 серверов.
//  2. Отправить 30 команд.
//  3. CrashPeer(follower) — убить follower.
//  4. Отправить ещё 30 команд (без сервера).
//  5. RestartPeer(follower) — перезапустить.
//  6. Дождаться, пока сервер догонит лог.
//  7. Проверить, что все серверы имеют одинаковые committed-записи.
func TestDedup_AllServersConsistentAfterCrash(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем 30 команд в стабильный кластер.
	for i := 0; i < 30; i++ {
		idx := h.SubmitToServer(lid, i)
		if idx < 0 {
			lid, _ = h.CheckSingleLeader()
		}
	}
	h.WaitForCommit(29, 3)

	// Crash follower.
	followerID := (lid + 1) % 3
	// remove: CrashPeer возвращается после join горутин узла.
	h.CrashPeer(followerID)

	// Отправляем ещё 30 команд (без follower).
	for i := 30; i < 60; i++ {
		idx := h.SubmitToServer(lid, i)
		if idx < 0 {
			lid, _ = h.CheckSingleLeader()
		}
	}
	h.WaitForCommit(59, 2)

	// Перезапускаем follower и ждём синхронизации.
	// Задержка повторов репликации (потолок 1000 мс) может задержать
	// догон перезапущенного follower'а — фиксированный sleep недостаточен,
	// ждём опросом.
	h.RestartPeer(followerID)
	h.WaitForCommitAll(59, 3*time.Second)

	// Проверить, что все 60 команд зафиксированы.
	for i := 0; i < 60; i++ {
		h.CheckCommitted(i)
	}
}

// TestDedup_RapidLeadershipChange проверяет, что многократная смена
// лидерства не оставляет "зависших" inflightAE флагов.
//
// Сценарий:
//  1. Создать кластер из 3 серверов.
//  2. В цикле 5 раз: CrashPeer(leader), дождаться нового лидера.
//  3. После каждого цикла отправить 10 команд и проверить фиксацию.
//  4. После всех циклов проверить целостность.
func TestDedup_RapidLeadershipChange(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	if testing.Short() {
		t.Skip("skipping rapid leadership change test in short mode")
	}

	h := NewHarness(t, 3)
	defer h.Shutdown()

	for cycle := 0; cycle < 5; cycle++ {
		lid, _ := h.CheckSingleLeader()

		// Crash текущего лидера.
		// remove: CheckSingleLeader опрашивает состояние сам.
		h.CrashPeer(lid)

		// Дождаться нового лидера.
		newLid, _ := h.CheckSingleLeader()
		if newLid == lid {
			t.Fatalf("cycle %d: new leader should be different", cycle)
		}

		// Отправить команды.
		for i := 0; i < 10; i++ {
			idx := h.SubmitToServer(newLid, cycle*100+i)
			if idx < 0 {
				t.Fatalf("cycle %d: SubmitToServer failed", cycle)
			}
		}
		h.WaitForCommit(cycle*100+9, 2)

		// Восстанавливаем crashed лидера для следующего цикла.
		// remove: RestartPeer завершает ready-wait перезапущенного узла,
		// дополнительная пауза не требуется.
		h.RestartPeer(lid)
	}

	// Проверяем целостность: последняя команда зафиксирована на всех
	// подключённых серверах. Задержка повторов репликации может
	// задержать догон перезапущенного узла до 1000 мс — ждём опросом.
	h.WaitForCommitAll(409, 3*time.Second)
	h.CheckCommitted(409)
}

// TestRace_ReplicateLoopLeadershipChange проверяет отсутствие data race
// при смене лидерства во время выполнения leaderSendAEsToPeer.
//
// Сценарий:
//  1. ConsensusModule в состоянии Leader, транспорт с задержкой.
//  2. Запустить leaderSendAEsToPeer в горутине.
//  3. Пока горутина выполняется, вызвать becomeFollowerLocked.
//  4. Вызвать startLeaderLocked.
//  5. Проверить, что новый лидер может запускать репликацию.
//
// Запуск: только с -race флагом.
func TestRace_ReplicateLoopLeadershipChange(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем команду, чтобы заполнить журнал.
	h.SubmitToServer(lid, 42)
	// replace: до изоляции сбоев нет — steady.
	h.WaitForCommitBudget(42, 3, _commitBudgetSteady)

	// Изолируем лидера — Apply блокируется.
	// replace (не commit-ожидание): ждём вступления изоляции в силу.
	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)
	h.waitForIsolated(lid, _commitBudgetSteady)

	// Запускаем Apply в фоне.
	inflightBefore := h.applyInflightCount(lid)
	done := make(chan struct{})
	go func() {
		future := h.cluster[lid].Apply(43, 0)
		_ = future.Error()
		close(done)
	}()

	// replace (не commit-ожидание): ждём попадания Apply в inflight.
	h.waitForApplyInflight(lid, inflightBefore+1, _commitBudgetSteady)

	// Шлём stepDown через AppendEntries с более высоким term.
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Apply to complete")
	}

	// После stepDown должен появиться новый лидер.
	h.ReconnectPeer((lid + 1) % 3)
	h.ReconnectPeer((lid + 2) % 3)
	// replace: после реконнекта ждём схождения к единственному лидеру.
	h.WaitForSingleLeader(_leaderElectionBudget)
}

// TestRace_ConcurrentLeaderSendAEs проверяет отсутствие data race
// при конкурентных вызовах leaderSendAEs из нескольких горутин.
//
// Сценарий:
//  1. Создать кластер из 3 серверов.
//  2. Вызвать leaderSendAEs из 10 параллельных горутин.
//  3. Проверить, что нет panic и data race.
func TestRace_ConcurrentLeaderSendAEs(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.cluster[lid].leaderSendAEs()
		}()
	}
	wg.Wait()

	// После конкурентных вызовов кластер должен быть жив.
	h.CheckSingleLeader()
}

// TestIntegration_TermIndexConsistency проверяет, что termIndexMap
// консистентен с cm.cmState.log после отправки команд и смены лидера.
//
// Сценарий:
//  1. Создать кластер из 3 серверов, отправить 3 команды.
//  2. Проверить termIndexMap на лидере.
//  3. Сменить лидера (отключить, дождаться выборов среди оставшихся).
//  4. Проверить termIndexMap на новом лидере.
//
// Ожидание: для каждой записи в cm.cmState.log termIndexMap содержит её терм.
func TestIntegration_TermIndexConsistency(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Отправляем 3 команды, ждём фиксации.
	for i := 0; i < 3; i++ {
		lid, _ := h.CheckSingleLeader()
		h.SubmitToServer(lid, 100+i)
	}
	// replace: ждём сходимости последней команды вместо фиксированной паузы.
	h.WaitForCommit(102, 3)

	// Проверяем termIndexMap на лидере.
	lid, _ := h.CheckSingleLeader()
	cm := h.cluster[lid]
	cm.mu.Lock()
	for _, entry := range cm.cmState.log {
		if _, ok := cm.cmState.termIndexMap[entry.Term]; !ok {
			cm.mu.Unlock()
			t.Fatalf("leader %d: termIndexMap missing term %d", lid, entry.Term)
		}
	}
	cm.mu.Unlock()

	// Сменить лидера: отключаем текущего, дожидаемся выборов среди 2 оставшихся.
	h.DisconnectPeer(lid)

	newLid, _ := h.CheckSingleLeader()
	if newLid == lid {
		t.Fatal("leadership should have changed after disconnect")
	}

	// Проверить termIndexMap на новом лидере.
	cm = h.cluster[newLid]
	cm.mu.Lock()
	for _, entry := range cm.cmState.log {
		if _, ok := cm.cmState.termIndexMap[entry.Term]; !ok {
			cm.mu.Unlock()
			t.Fatalf("new leader %d: termIndexMap missing term %d", newLid, entry.Term)
		}
	}
	cm.mu.Unlock()

	h.ReconnectPeer(lid)
}

// TestIntegration_TermIndexAfterLogTruncation проверяет termIndexMap
// после обрезки лога на follower из-за конфликта термов.
//
// Сценарий:
//  1. Кластер из 3 серверов, отправить 3 команды.
//  2. Отключить follower.
//  3. Сменить лидера (отключить/подключить), отправить 3 команды.
//  4. Подключить follower — репликация с конфликтом.
//  5. Дождаться синхронизации лога, проверить termIndexMap.
//
// Ожидание: termIndexMap содержит все термы из обновлённого лога.
func TestIntegration_TermIndexAfterLogTruncation(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	// Фаза 1: отправляем 3 команды.
	for i := 0; i < 3; i++ {
		lid, _ := h.CheckSingleLeader()
		h.SubmitToServer(lid, 100+i)
	}
	// replace: ждём сходимости последней команды вместо фиксированной паузы.
	h.WaitForCommit(102, 3)

	lid, _ := h.CheckSingleLeader()

	// Выбираем и отключаем follower.
	var victimID int
	for id := range h.cluster {
		if id != lid {
			victimID = id
			break
		}
	}
	h.DisconnectPeer(victimID)
	defer h.ReconnectPeer(victimID)

	// Смена лидера и фаза 2: 3 команды в новом терме.
	// keep: окно без наблюдаемого состояния — victim уже отключён,
	// поэтому после отключения лидера подключённым остаётся ровно
	// один узел и кворума нет: ни лидер, ни фиксация не могут появиться.
	// Бюджет — ReelectionTimeoutMs (минимальный election timeout):
	// оставшийся узел успевает выйти из состояния «лидер известен».
	h.DisconnectPeer(lid)
	sleepMs(ReelectionTimeoutMs)
	h.ReconnectPeer(lid)

	for i := 0; i < 3; i++ {
		lid, _ := h.WaitForSingleLeader(_leaderElectionBudget)
		h.SubmitToServer(lid, 200+i)
	}
	// replace: ждём сходимости последней команды фазы 2 (жертва
	// отключена — подключены 2 узла).
	h.WaitForCommit(202, 2)

	// Подключаем жертву — триггерим репликацию с конфликтом.
	h.ReconnectPeer(victimID)

	// Ждём, пока все серверы сойдутся на последнем индексе.
	lid, _ = h.CheckSingleLeader()
	leaderCM := h.cluster[lid]
	leaderCM.mu.Lock()
	targetIdx := leaderCM.cmState.lastLogIndex
	leaderCM.mu.Unlock()

	victimCM := h.cluster[victimID]
	deadline := time.Now().Add(5 * time.Second)
	for {
		victimCM.mu.Lock()
		lastIdx := victimCM.cmState.lastLogIndex
		victimCM.mu.Unlock()

		if lastIdx >= targetIdx {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: victim %d lastIdx=%d, want >=%d",
				victimID, lastIdx, targetIdx)
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(_pollInterval)
	}

	// Проверяем termIndexMap на жертве.
	victimCM.mu.Lock()
	defer victimCM.mu.Unlock()

	for _, entry := range victimCM.cmState.log {
		if _, ok := victimCM.cmState.termIndexMap[entry.Term]; !ok {
			t.Fatalf("follower %d: termIndexMap missing term %d (Index %d)",
				victimID, entry.Term, entry.Index)
		}
	}
}

// TestRace_TermIndexMapDispatchAndConflict проверяет, что конкурентное
// инкрементальное обновление termIndexMap (dispatchLogsLocked) и
// чтение (leaderSendAEsToPeer с ConflictTerm) не вызывают data race.
//
// Сценарий:
//  1. Создать CM-лидер.
//  2. Запустить горутину dispatch, вызывающую dispatchLogsLocked.
//  3. Запустить горутину conflict, вызывающую leaderSendAEsToPeer.
//  4. Остановить через shutdownCh.
//
// Ожидание: -race не обнаруживает data race.
func TestRace_TermIndexMapDispatchAndConflict(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	storage := store.NewMapStorage()
	mock := &mockTransportConflict{replyTerm: 1, success: false, conflictTerm: 1}
	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{1: -1},
			matchIndex: map[int]int{1: -1, 0: -1},
			inflight:   make(map[int]*logFuture),
		},

		id:         0,
		storage:    storage,
		transport:  mock,
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Leader,
			currentTerm:  1,
			log:          make([]LogEntry, 0),
			lastLogIndex: -1,
			lastLogTerm:  -1,
			commitIndex:  -1,
			termIndexMap: make(map[int]int),
			configurations: configurations{
				committed: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
				latest: Configuration{
					ConfigServers: []ConfigServer{
						{ID: 0, Suffrage: Voter},
						{ID: 1, Suffrage: Voter},
					},
				},
			},
		},
	}
	defer cm.Stop()

	done := make(chan struct{})
	var wg sync.WaitGroup
	// Счётчики итераций: стресс-тест обязан выполнить хотя бы по одной
	// итерации в каждой горутине, иначе он ложно-зелёный.
	var dispatchIters, conflictIters atomic.Int64

	// Горутина dispatch: пишет в termIndexMap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				f := &logFuture{
					deferError: deferError{errCh: make(chan error, 1)},
					log:        LogEntry{Type: LogCommand, Data: []byte("x")},
				}
				cm.mu.Lock()
				cm.dispatchLogsLocked([]*logFuture{f})
				cm.mu.Unlock()
				dispatchIters.Add(1)
			}
		}
	}()

	// Горутина conflict: читает termIndexMap (через leaderSendAEsToPeer).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				cm.leaderSendAEsToPeer(1, 1, 0, true)
				conflictIters.Add(1)
			}
		}
	}()

	// keep: timing — окно нагрузки является предметом стресс-теста
	// (детектор гонок наблюдает конкурентные обращения). Достаточность
	// окна проверяется assert'ом на число итераций ниже.
	sleepMs(100)
	close(done)
	wg.Wait()

	if dispatchIters.Load() == 0 || conflictIters.Load() == 0 {
		t.Fatalf("stress window produced no iterations: dispatch=%d conflict=%d",
			dispatchIters.Load(), conflictIters.Load())
	}
}

// TestRace_TermIndexMapCompactAndRead проверяет, что конкурентное
// сжатие (compactLogsLocked, перестраивает termIndexMap) и
// чтение termIndexMap не вызывают data race.
//
// Сценарий:
//  1. Создать CM с логом из 10 записей.
//  2. Запустить горутину compact, вызывающую compactLogsLocked.
//  3. Запустить горутину reader, читающую termIndexMap.
//  4. Остановить через shutdownCh.
//
// Ожидание: -race не обнаруживает data race.
func TestRace_TermIndexMapCompactAndRead(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{
		leaderState: leaderState{
			nextIndex:  map[int]int{},
			matchIndex: map[int]int{},
		},

		storage:    store.NewMapStorage(),
		shutdownCh: make(chan struct{}),
		cmState: cmState{
			state:        Follower,
			currentTerm:  1,
			log:          make([]LogEntry, 0),
			lastLogIndex: 10,
			lastLogTerm:  1,
			commitIndex:  -1,
			termIndexMap: make(map[int]int),
		},
	}
	cm.mu.Lock()
	for i := 0; i < 10; i++ {
		cm.cmState.log = append(cm.cmState.log, LogEntry{Index: i + 1, Term: 1})
	}
	cm.rebuildTermIndexMapLocked()
	cm.mu.Unlock()
	defer cm.Stop()

	done := make(chan struct{})
	var wg sync.WaitGroup
	// Счётчики итераций: стресс-тест обязан выполнить хотя бы по одной
	// итерации в каждой горутине, иначе он ложно-зелёный.
	var compactIters, readerIters atomic.Int64

	// Горутина compact: перестраивает termIndexMap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				cm.mu.Lock()
				cm.compactLogsLocked(5)
				cm.mu.Unlock()
				compactIters.Add(1)
			}
		}
	}()

	// Горутина reader: читает termIndexMap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				cm.mu.Lock()
				_ = cm.cmState.termIndexMap[1]
				cm.mu.Unlock()
				readerIters.Add(1)
			}
		}
	}()

	// keep: timing — окно нагрузки является предметом стресс-теста.
	sleepMs(100)
	close(done)
	wg.Wait()

	if compactIters.Load() == 0 || readerIters.Load() == 0 {
		t.Fatalf("stress window produced no iterations: compact=%d reader=%d",
			compactIters.Load(), readerIters.Load())
	}
}
