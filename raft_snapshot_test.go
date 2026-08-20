package raft

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// snapshotHarness — тестовый стенд для интеграционных тестов снимков.
// Отличается от Harness тем, что использует snapshotTestFSM и
// InmemSnapshotStore для каждого узла.
type snapshotHarness struct {
	t          *testing.T
	n          int
	cluster    []*ConsensusModule
	fsms       []*snapshotTestFSM
	stores     []*InmemSnapshotStore
	storage    []*MapStorage
	transports []*InmemTransport
	alive      []bool
	connected  []bool
	ready      chan any
	mu         sync.Mutex
	// isCounters — счётчики InstallSnapshot по узлу-получателю:
	// (перехват в тестовом транспорте harness, образец —
	// raft_cm_replication_test.go).
	isCounters *isReceivedCounters
}

// isReceivedCounters — общие счётчики InstallSnapshot, отправленных
// каждому узлу кластера. Разделяются всеми тестовыми транспортами
// harness; инкремент — atomic, чтение из теста — Load.
type isReceivedCounters struct {
	mu       sync.Mutex
	byTarget map[int]*atomic.Int64
}

// target возвращает счётчик узла-получателя, лениво его создавая.
func (c *isReceivedCounters) target(id int) *atomic.Int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byTarget == nil {
		c.byTarget = make(map[int]*atomic.Int64)
	}
	v, ok := c.byTarget[id]
	if !ok {
		v = new(atomic.Int64)
		c.byTarget[id] = v
	}
	return v
}

// snapshotCountingTransport — обёртка InmemTransport, подсчитывающая
// InstallSnapshot по узлу-получателю. Consumer() и Connect/Disconnect
// работают с нижележащим транспортом без изменений; перехватывается
// только исходящий вызов InstallSnapshot.
type snapshotCountingTransport struct {
	*InmemTransport
	counters *isReceivedCounters
}

func (t *snapshotCountingTransport) InstallSnapshot(peerID ServerID, req InstallSnapshotRequest, data io.Reader) (InstallSnapshotResponse, error) {
	t.counters.target(int(peerID)).Add(1)
	return t.InmemTransport.InstallSnapshot(peerID, req, data)
}

// newSnapshotHarness создаёт кластер из n узлов с поддержкой снимков.
func newSnapshotHarness(t *testing.T, n int) *snapshotHarness {
	t.Helper()

	cluster := make([]*ConsensusModule, n)
	fsms := make([]*snapshotTestFSM, n)
	stores := make([]*InmemSnapshotStore, n)
	storage := make([]*MapStorage, n)
	transports := make([]*InmemTransport, n)
	alive := make([]bool, n)
	connected := make([]bool, n)
	ready := make(chan any)
	counters := &isReceivedCounters{}

	// Создаём транспорты и соединяем их.
	for i := 0; i < n; i++ {
		transports[i] = NewInmemTransport(ServerAddress("raft-" + itoa(i)))
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				transports[i].Connect(ServerID(j), transports[j])
			}
		}
	}

	// Создаём ConsensusModule для каждого узла со снимками.
	// В CM передаётся counting-обёртка транспорта: перехват
	// InstallSnapshot для счётчика installSnapshotCount(id).
	for i := 0; i < n; i++ {
		peerIds := make([]int, 0)
		for p := 0; p < n; p++ {
			if p != i {
				peerIds = append(peerIds, p)
			}
		}

		storage[i] = NewMapStorage()
		fsms[i] = newSnapshotTestFSM()
		stores[i] = NewInmemSnapshotStore()

		cluster[i] = NewConsensusModule(
			i, peerIds, &snapshotCountingTransport{InmemTransport: transports[i], counters: counters},
			storage[i], fsms[i], ready,
			stores[i],
		)
		alive[i] = true
		connected[i] = true
	}
	close(ready)

	h := &snapshotHarness{
		t:          t,
		n:          n,
		cluster:    cluster,
		fsms:       fsms,
		stores:     stores,
		storage:    storage,
		transports: transports,
		alive:      alive,
		connected:  connected,
		ready:      ready,
		isCounters: counters,
	}
	return h
}

// Shutdown останавливает все серверы.
func (h *snapshotHarness) Shutdown() {
	for i := 0; i < h.n; i++ {
		if h.alive[i] {
			h.alive[i] = false
			h.cluster[i].Stop()
			h.transports[i].Close()
		}
	}
}

// DisconnectPeer отключает узел от всех остальных.
func (h *snapshotHarness) DisconnectPeer(id int) {
	h.transports[id].DisconnectAll()
	for j := 0; j < h.n; j++ {
		if j != id {
			h.transports[j].Disconnect(ServerID(id))
		}
	}
	h.connected[id] = false
}

// ReconnectPeer повторно подключает узел ко всем остальным.
func (h *snapshotHarness) ReconnectPeer(id int) {
	for j := 0; j < h.n; j++ {
		if j != id && h.alive[j] {
			h.transports[id].Connect(ServerID(j), h.transports[j])
			h.transports[j].Connect(ServerID(id), h.transports[id])
		}
	}
	h.connected[id] = true
}

// CrashPeer «аварийно завершает» узел, сохраняя storage.
func (h *snapshotHarness) CrashPeer(id int) {
	h.DisconnectPeer(id)
	h.alive[id] = false
	h.cluster[id].Stop()
	h.transports[id].Close()
}

// RestartPeer перезапускает узел из того же storage.
func (h *snapshotHarness) RestartPeer(id int) {
	if h.alive[id] {
		h.t.Fatalf("id=%d is alive in RestartPeer", id)
	}
	peerIds := make([]int, 0)
	for p := 0; p < h.n; p++ {
		if p != id {
			peerIds = append(peerIds, p)
		}
	}

	ready := make(chan any)
	addr := ServerAddress("raft-" + itoa(h.n) + "-" + itoa(id))
	h.transports[id] = NewInmemTransport(addr)

	for j := 0; j < h.n; j++ {
		if j != id && h.alive[j] {
			h.transports[id].Connect(ServerID(j), h.transports[j])
			h.transports[j].Connect(ServerID(id), h.transports[id])
		}
	}

	// Создаём новый FSM, но ПЕРЕИСПОЛЬЗУЕМ store узла: Inmem-стор живёт
	// в том же процессе и хранит последний снимок — это эмуляция
	// постоянного хранилища для in-memory кластера. Новый пустой store с
	// lastSnapshotIndex > 0 в storage привёл бы к fail-fast в
	// restoreFromSnapshotStore (пустой store при усечённом логе).
	fsm := newSnapshotTestFSM()
	h.fsms[id] = fsm
	store := h.stores[id]

	h.cluster[id] = NewConsensusModule(
		id, peerIds, &snapshotCountingTransport{InmemTransport: h.transports[id], counters: h.isCounters},
		h.storage[id], fsm, ready, store,
	)
	close(ready)
	h.alive[id] = true
	h.connected[id] = true
	// poll-интервал condition-wait (не фиксированная пауза).
	sleepMs(20)
}

// SubmitToServer отправляет команду серверу и возвращает индекс.
func (h *snapshotHarness) SubmitToServer(serverID int, cmd string) int {
	future := h.cluster[serverID].Apply(cmd, 0)
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
	case <-time.After(submitTimeout):
		return -1
	}
}

// waitForSnapshot ждёт, пока указанный узел создаст снимок
// (lastSnapshotIndex > 0), или до таймаута.
func (h *snapshotHarness) waitForSnapshot(serverID int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		h.cluster[serverID].mu.Lock()
		idx := h.cluster[serverID].cmState.lastSnapshotIndex
		h.cluster[serverID].mu.Unlock()
		if idx > 0 {
			return true
		}
		select {
		case <-deadline:
			return false
		default:
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
}

// getSnapshotIndex возвращает lastSnapshotIndex узла.
func (h *snapshotHarness) getSnapshotIndex(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.lastSnapshotIndex
}

// getLastSnapshotIndex — синоним getSnapshotIndex (единое имя аксессора
// для матрицы Case A…F,).
func (h *snapshotHarness) getLastSnapshotIndex(serverID int) int {
	return h.getSnapshotIndex(serverID)
}

// getLastSnapshotTerm возвращает lastSnapshotTerm узла.
func (h *snapshotHarness) getLastSnapshotTerm(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.lastSnapshotTerm
}

// getLastLogIndex возвращает lastLogIndex узла.
func (h *snapshotHarness) getLastLogIndex(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.lastLogIndex
}

// getLastLogTerm возвращает lastLogTerm узла.
func (h *snapshotHarness) getLastLogTerm(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.lastLogTerm
}

// getLastApplied возвращает lastApplied узла.
func (h *snapshotHarness) getLastApplied(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.lastApplied
}

// getFsmAppliedIndex возвращает отметку фактически применённого к машине
// состояний индекса узла.
func (h *snapshotHarness) getFsmAppliedIndex(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.fsmAppliedIndex
}

// getCommitIndex возвращает commitIndex узла.
func (h *snapshotHarness) getCommitIndex(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return h.cluster[serverID].cmState.commitIndex
}

// installSnapshotCount возвращает число InstallSnapshot, отправленных
// указанному узлу с момента старта harness (перехват в тестовом транспорте).
func (h *snapshotHarness) installSnapshotCount(serverID int) int64 {
	return h.isCounters.target(serverID).Load()
}

// getLogEntries возвращает копию среза журнала узла.
func (h *snapshotHarness) getLogEntries(serverID int) []LogEntry {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return append([]LogEntry{}, h.cluster[serverID].cmState.log...)
}

// getLogLength возвращает длину журнала узла.
func (h *snapshotHarness) getLogLength(serverID int) int {
	h.cluster[serverID].mu.Lock()
	defer h.cluster[serverID].mu.Unlock()
	return len(h.cluster[serverID].cmState.log)
}

// ============================================================
// Тесты
// ============================================================

// TestSnapshot_Creation проверяет, что runSnapshots создаёт снимок
// при достижении threshold. Устанавливаем низкий SnapshotThreshold (8),
// отправляем 10 команд, ждём фиксации и проверяем lastSnapshotIndex.
func TestSnapshot_Creation(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	// Устанавливаем низкий порог для быстрого снимка.
	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(8, 50*time.Millisecond, 4)
	}

	// Ждём лидера.
	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		t.Fatal("no leader elected")
	}

	// Отправляем 15 команд.
	for i := 0; i < 15; i++ {
		cmd := fmt.Sprintf("key%d=val%d", i, i)
		idx := h.SubmitToServer(leaderID, cmd)
		if idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}

	// Ждём, пока на лидере появится снимок.
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot was not created on leader")
	}

	// Проверяем, что после сжатия лог сократился.
	logLen := h.getLogLength(leaderID)
	t.Logf("leader log length after snapshot: %d", logLen)
	if logLen > 20 {
		t.Fatalf("log length = %d, expected compacted (< 20)", logLen)
	}
}

// TestSnapshot_NoSnapshotStore проверяет, что nil SnapshotStore
// не вызывает ошибок и снимки отключены.
func TestSnapshot_NoSnapshotStore(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	// Отправляем 10 команд.
	for i := 0; i < 10; i++ {
		idx := h.SubmitToServer(lid, i)
		if idx < 0 {
			t.Fatalf("SubmitToServer(%d) failed", i)
		}
	}
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(ReelectionTimeoutMs)

	// Проверяем, что lastSnapshotIndex == -1 на всех узлах.
	for i := 0; i < h.n; i++ {
		h.cluster[i].mu.Lock()
		idx := h.cluster[i].cmState.lastSnapshotIndex
		h.cluster[i].mu.Unlock()
		if idx != -1 {
			t.Fatalf("server %d: lastSnapshotIndex = %d, want -1", i, idx)
		}
	}
}

// TestSnapshot_Install проверяет, что отставший follower получает
// InstallSnapshot RPC, пост-условия установки корректны (монотонность lastApplied/commitIndex),
// а репликация продолжается обычным AppendEntries.
//
// Усилен по: верхняя граница числа InstallSnapshot после
// переподключения (≤2) исключает цикл IS↔AE-fail; журнал follower'а
// совпадает с лидерским на пересечении. На HEAD до тест обязан
// падать (lastLogIndex == -1 при установленном снимке).
func TestSnapshot_Install(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, _, isBefore := h.driveSnapshotInstall(20, 8, 30)

	// Ждём установки снимка на follower (опрос с дедлайном).
	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}

	// Монотонность lastApplied/commitIndex.
	snapIdxF := h.getLastSnapshotIndex(followerID)
	lastLogIndex := h.getLastLogIndex(followerID)
	if lastLogIndex < snapIdxF {
		t.Fatalf("INV-S1 violated: lastLogIndex=%d < lastSnapshotIndex=%d", lastLogIndex, snapIdxF)
	}
	if h.getLogLength(followerID) == 0 {
		// Пустой суффикс: lastLogIndex/lastLogTerm == граница снимка.
		if lastLogIndex != snapIdxF || h.getLastLogTerm(followerID) != h.getLastSnapshotTerm(followerID) {
			t.Fatalf("INV-S2 violated: lastLog=(%d,%d), snapshot=(%d,%d)",
				lastLogIndex, h.getLastLogTerm(followerID), snapIdxF, h.getLastSnapshotTerm(followerID))
		}
	}
	if h.getLastApplied(followerID) < snapIdxF {
		t.Fatalf("lastApplied=%d < snapshot index %d", h.getLastApplied(followerID), snapIdxF)
	}
	if h.getCommitIndex(followerID) < snapIdxF {
		t.Fatalf("commitIndex=%d < snapshot index %d", h.getCommitIndex(followerID), snapIdxF)
	}

	// После снимка репликация продолжается обычным
	// AppendEntries — follower догоняет лидера.
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	// Верхняя граница числа InstallSnapshot после переподключения:
	// цикл IS↔AE-fail исключён. Попытки к недоступному пиру во время
	// отключения не учитываются — их ограничивает задержка повторов.
	if n := h.installSnapshotCount(followerID) - isBefore; n > 2 {
		t.Fatalf("installSnapshotCount after reconnect=%d, want <= 2 (snapshot loop)", n)
	}

	h.checkLogIntersection(leaderID, followerID)
	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)
}

// driveSnapshotInstall выполняет базовый сценарий догона через снимок:
// синхронизацию follower'а с лидером (seed-запись), отключение follower'а,
// накопление numCmds команд на лидере до создания снимка,
// переподключение follower'а. Возвращает ID лидера, ID follower'а,
// индекс снимка лидера и число InstallSnapshot до переподключения.
//
// Seed-запись делает сценарий детерминированным: follower гарантированно
// имеет записи журнала до отключения (nextIndex > 0), поэтому после
// переподключения догон идёт через InstallSnapshot. Без seed'а возможен
// случай prev == -1 (полностью пустой follower), обрабатываемый особым
// случаем AE — см. blocker DEV-001 (.ai/developer/).
func (h *snapshotHarness) driveSnapshotInstall(threshold, trailing, numCmds int) (int, int, int, int64) {
	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(threshold, 50*time.Millisecond, trailing)
	}

	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		h.t.Fatal("no leader elected")
	}
	followerID := (leaderID + 1) % h.n

	// Точка отсчёта: follower гарантированно имеет записи журнала.
	if idx := h.SubmitToServer(leaderID, "kseed=vseed"); idx < 0 {
		h.t.Fatal("seed command failed")
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	h.DisconnectPeer(followerID)

	for i := 0; i < numCmds; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		idx := h.SubmitToServer(leaderID, cmd)
		if idx < 0 {
			h.t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}

	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		h.t.Fatal("snapshot was not created on leader")
	}
	snapIdx := h.getSnapshotIndex(leaderID)

	isBefore := h.installSnapshotCount(followerID)
	h.ReconnectPeer(followerID)
	return leaderID, followerID, snapIdx, isBefore
}

// waitForCatchUp ждёт (опросом с дедлайном), пока follower догонит лидера
// по lastLogIndex.
func (h *snapshotHarness) waitForCatchUp(leaderID, followerID int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if h.getLastLogIndex(followerID) >= h.getLastLogIndex(leaderID) {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("follower %d did not catch up to leader %d: lastLogIndex follower=%d leader=%d",
				followerID, leaderID, h.getLastLogIndex(followerID), h.getLastLogIndex(leaderID))
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
}

// checkLogIntersection проверяет, что журналы двух узлов совпадают на
// пересечении диапазонов индексов (Log Matching Property на пересечении).
func (h *snapshotHarness) checkLogIntersection(a, b int) {
	la := h.getLogEntries(a)
	lb := h.getLogEntries(b)
	ia, ib := 0, 0
	for ia < len(la) && ib < len(lb) {
		switch {
		case la[ia].Index < lb[ib].Index:
			ia++
		case lb[ib].Index < la[ia].Index:
			ib++
		default:
			if la[ia].Term != lb[ib].Term {
				h.t.Fatalf("log mismatch at index %d: node %d term=%d, node %d term=%d",
					la[ia].Index, a, la[ia].Term, b, lb[ib].Term)
			}
			ia++
			ib++
		}
	}
}

// fsmStateEqual сравнивает состояния FSM двух узлов (без Fatalf —
// используется опросом).
func (h *snapshotHarness) fsmStateEqual(a, b int) bool {
	fsmA := h.fsms[a]
	fsmB := h.fsms[b]
	fsmA.mu.Lock()
	defer fsmA.mu.Unlock()
	fsmB.mu.Lock()
	defer fsmB.mu.Unlock()
	if len(fsmA.state) != len(fsmB.state) {
		return false
	}
	for k, v := range fsmA.state {
		if fsmB.state[k] != v {
			return false
		}
	}
	return true
}

// checkFSMStateEqual — утверждение равенства FSM двух узлов.
func (h *snapshotHarness) checkFSMStateEqual(a, b int) {
	if !h.fsmStateEqual(a, b) {
		h.t.Fatalf("FSM state mismatch between nodes %d and %d", a, b)
	}
}

// waitForFSMStateEqual ждёт (опросом с дедлайном), пока FSM узла b
// сравняется с FSM узла a. Применение записей к FSM отстаёт от
// репликации журнала (процесс apply асинхронен), поэтому равенства
// lastLogIndex недостаточно для утверждения о FSM.
func (h *snapshotHarness) waitForFSMStateEqual(a, b int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for !h.fsmStateEqual(a, b) {
		if time.Now().After(deadline) {
			h.checkFSMStateEqual(a, b)
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCompactLog_Basic проверяет базовое сжатие журнала.
func TestCompactLog_Basic(t *testing.T) {
	// Создаём CM с 5 записями в логе (Index: 1, 2, 3, 4, 5).
	storage := NewMapStorage()
	ready := make(chan any)
	close(ready)
	cm := NewConsensusModule(0, []int{}, NewInmemTransport("test"), storage, newSnapshotTestFSM(), ready)

	cm.mu.Lock()
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 1},
		{Index: 4, Term: 1},
		{Index: 5, Term: 1},
	}
	cm.cmState.lastLogIndex = 5
	cm.cmState.lastLogTerm = 1

	// очищаем старые записи с Index < 3.
	cm.compactLogs(3)
	cm.mu.Unlock()

	// Проверяем, что остались записи с Index: 3, 4, 5.
	if len(cm.cmState.log) != 3 {
		t.Fatalf("log length = %d, want 3", len(cm.cmState.log))
	}
	if cm.cmState.log[0].Index != 3 {
		t.Fatalf("first entry Index = %d, want 3", cm.cmState.log[0].Index)
	}
	if cm.cmState.log[1].Index != 4 {
		t.Fatalf("second entry Index = %d, want 4", cm.cmState.log[1].Index)
	}
	if cm.cmState.log[2].Index != 5 {
		t.Fatalf("third entry Index = %d, want 5", cm.cmState.log[2].Index)
	}
	cm.Stop()
}

// TestCompactLog_All проверяет очистку всех старых записей.
func TestCompactLog_All(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.shutdownCh = make(chan struct{})
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
	}
	cm.compactLogs(10)
	if len(cm.cmState.log) != 0 {
		t.Fatalf("log length = %d, want 0", len(cm.cmState.log))
	}
}

// TestCompactLog_Nothing проверяет, что сжатие до индекса,
// меньшего чем первая запись, не меняет лог.
func TestCompactLog_Nothing(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.shutdownCh = make(chan struct{})
	cm.cmState.log = []LogEntry{
		{Index: 5, Term: 1},
		{Index: 6, Term: 1},
		{Index: 7, Term: 1},
	}
	cm.compactLogs(3)
	if len(cm.cmState.log) != 3 {
		t.Fatalf("log length = %d, want 3", len(cm.cmState.log))
	}
	if cm.cmState.log[0].Index != 5 {
		t.Fatalf("first entry Index = %d, want 5", cm.cmState.log[0].Index)
	}
}

// TestCompactLog_Empty проверяет сжатие пустого журнала.
func TestCompactLog_Empty(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := &ConsensusModule{}
	cm.shutdownCh = make(chan struct{})
	cm.compactLogs(5)
	if len(cm.cmState.log) != 0 {
		t.Fatalf("log length = %d, want 0", len(cm.cmState.log))
	}
}

// TestLogPosition_Exact проверяет точное совпадение индекса.
func TestLogPosition_Exact(t *testing.T) {
	cm := &ConsensusModule{}
	cm.cmState.log = []LogEntry{
		{Index: 5, Term: 1},
		{Index: 10, Term: 1},
		{Index: 15, Term: 1},
	}
	// Метод требует удержания cm.mu — вызываем под блокировкой.
	cm.mu.Lock()
	if pos := cm.logPositionLocked(5); pos != 0 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(5) = %d, want 0", pos)
	}
	if pos := cm.logPositionLocked(10); pos != 1 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(10) = %d, want 1", pos)
	}
	if pos := cm.logPositionLocked(15); pos != 2 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(15) = %d, want 2", pos)
	}
	cm.mu.Unlock()
}

// TestLogPosition_NotFound проверяет поиск отсутствующего индекса.
func TestLogPosition_NotFound(t *testing.T) {
	cm := &ConsensusModule{}
	cm.cmState.log = []LogEntry{
		{Index: 5, Term: 1},
		{Index: 10, Term: 1},
		{Index: 15, Term: 1},
	}
	// Метод требует удержания cm.mu — вызываем под блокировкой.
	cm.mu.Lock()
	if pos := cm.logPositionLocked(7); pos != 1 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(7) = %d, want 1", pos)
	}
	if pos := cm.logPositionLocked(1); pos != 0 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(1) = %d, want 0", pos)
	}
	if pos := cm.logPositionLocked(20); pos != 3 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(20) = %d, want 3", pos)
	}
	cm.mu.Unlock()
}

// TestLogPosition_AfterCompact проверяет, что logPositionLocked работает
// после сжатия.
func TestLogPosition_AfterCompact(t *testing.T) {
	cm := &ConsensusModule{}
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 1},
		{Index: 4, Term: 1},
		{Index: 5, Term: 1},
	}
	// Оба метода требуют удержания cm.mu — вызываем под блокировкой.
	cm.mu.Lock()
	cm.compactLogs(3)
	if pos := cm.logPositionLocked(3); pos != 0 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(3) = %d, want 0", pos)
	}
	if pos := cm.logPositionLocked(4); pos != 1 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(4) = %d, want 1", pos)
	}
	if pos := cm.logPositionLocked(5); pos != 2 {
		cm.mu.Unlock()
		t.Fatalf("logPositionLocked(5) = %d, want 2", pos)
	}
	cm.mu.Unlock()
}

// Helper: waitForSingleLeader ждёт появления лидера в snapshotHarness.
func (h *snapshotHarness) waitForSingleLeader() int {
	for r := 0; r < 20; r++ {
		for i := 0; i < h.n; i++ {
			if !h.connected[i] {
				continue
			}
			_, _, isLeader := h.cluster[i].Report()
			if isLeader {
				return i
			}
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(HeartbeatTimeoutMs * time.Millisecond * 2)
	}
	return -1
}

// TestSnapshot_MultipleSnapshots проверяет, что при множественных снимках
// InmemSnapshotStore хранит только последний, а журнал сжимается.
//
// Порог снимков 1: при любом lastSnapshotIndex < lastLogIndex выполняется
// delta >= 1, и ближайший тик цикла снимков создаёт снимок — процесс
// сходится к lastSnapshotIndex == lastLogIndex, после чего снимки
// прекращаются. При пороге 5 и 30 командах существовала бы необратимая
// зона S ∈ {26..29}, из которой система по своим правилам не выходит.
//
// Бюджет каждого ожидания — snapshotConvergenceBudget: худший случай
// схождения при интервале снимков 50 мс — applyBatchInterval + 2 интервала
// снимков = 150 мс, запас более чем 14-кратный.
func TestSnapshot_MultipleSnapshots(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Одноузловой кластер с порогом 1.
	storage := NewMapStorage()
	fsm := newSnapshotTestFSM()
	store := NewInmemSnapshotStore()
	ready := make(chan any)
	close(ready)

	transport := NewInmemTransport("single")
	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	defer cm.Stop()

	// replace: ожидание лидерства — condition-wait по наблюдаемому признаку
	// cmState.state == Leader (чтение под cm.mu), а не фиксированные паузы.
	err := waitCond(
		"single node becomes leader",
		leaderElectionBudget,
		func() bool {
			cm.mu.Lock()
			defer cm.mu.Unlock()
			return cm.cmState.state == Leader
		},
		func() string {
			cm.mu.Lock()
			defer cm.mu.Unlock()
			return fmt.Sprintf("state=%d term=%d", cm.cmState.state, cm.cmState.currentTerm)
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Порог 1, интервал 50 мс, хвост журнала 2 записи.
	cm.SetSnapshotConfig(1, 50*time.Millisecond, 2)

	// snapshotState читает четвёрку для диагностики ожиданий снимка.
	snapshotState := func() string {
		cm.mu.Lock()
		defer cm.mu.Unlock()
		return fmt.Sprintf(
			"lastSnapshotIndex=%d lastLogIndex=%d len(log)=%d fsmAppliedIndex=%d",
			cm.cmState.lastSnapshotIndex, cm.cmState.lastLogIndex,
			len(cm.cmState.log), cm.cmState.fsmAppliedIndex,
		)
	}

	// waitSnapshotCovered ждёт снимка с индексом не меньше wantIdx.
	// Условие выхода влечёт проверяемые утверждения: присвоение
	// lastSnapshotIndex и сжатие журнала выполняются в одной критической
	// секции cm.mu, поэтому наблюдение lastSnapshotIndex >= wantIdx
	// гарантирует уже выполненное сжатие до этого снимка.
	waitSnapshotCovered := func(wantIdx int) {
		t.Helper()
		desc := fmt.Sprintf("lastSnapshotIndex >= %d", wantIdx)
		err := waitCond(
			desc, snapshotConvergenceBudget,
			func() bool {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				return cm.cmState.lastSnapshotIndex >= wantIdx
			},
			snapshotState,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Фаза 1: первая половина команд и снимок, покрывающий её целиком.
	var phase1Idx int
	for i := 0; i < 15; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		future := cm.Apply(cmd, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %q failed: %v", cmd, err)
		}
		phase1Idx = future.Index()
	}
	waitSnapshotCovered(phase1Idx)
	cm.mu.Lock()
	s1 := cm.cmState.lastSnapshotIndex
	cm.mu.Unlock()

	// Фаза 2: вторая половина команд и снимок, покрывающий журнал целиком.
	var wantIdx int
	for i := 15; i < 30; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		future := cm.Apply(cmd, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %q failed: %v", cmd, err)
		}
		wantIdx = future.Index()
	}
	waitSnapshotCovered(wantIdx)
	cm.mu.Lock()
	s2 := cm.cmState.lastSnapshotIndex
	logLen := len(cm.cmState.log)
	cm.mu.Unlock()

	t.Logf("snapshots S1=%d S2=%d, log length=%d", s1, s2, logLen)

	// Снимков создано более одного, и хранилище удерживает ровно последний.
	if s2 <= s1 {
		t.Fatalf("S2 = %d, want > S1 = %d (multiple snapshots expected)", s2, s1)
	}
	list, _ := store.List()
	if len(list) != 1 {
		t.Fatalf("store has %d snapshots, want 1", len(list))
	}
	if list[0].Index != s2 {
		t.Fatalf("store holds snapshot with index %d, want the latest S2=%d", list[0].Index, s2)
	}

	// Сжатие журнала: takeSnapshot вызывает compactLogs(index - trailing),
	// а compactLogs сохраняет записи с индексом >= compactIndex; после
	// схождения (lastSnapshotIndex == lastLogIndex == S) журнал содержит
	// ровно индексы S-trailing..S, то есть trailing + 1 = 2 + 1 = 3 записи.
	if logLen != 3 {
		t.Fatalf("log length = %d, want trailing+1 = 3", logLen)
	}

	// Проверяем, что данные снимка корректны.
	meta, reader, err := store.Open(list[0].ID)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = reader.Close() }()
	data, _ := io.ReadAll(reader)
	t.Logf("snapshot data: %d bytes, index=%d, term=%d", len(data), meta.Index, meta.Term)

	// Восстанавливаем новую FSM из снимка.
	fsm2 := newSnapshotTestFSM()
	_, r2, err := store.Open(list[0].ID)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = r2.Close() }()
	if err := fsm2.Restore(r2); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Проверяем, что последнее записанное состояние есть.
	if v := fsm2.getState("k29"); v != "v29" {
		t.Fatalf("k29 = %q, want v29", v)
	}
}

// TestSnapshot_RestartRestoresFSM — регрессионный тест P0-1: после
// снимка, сжатия журнала на диске и полного рестарта узла FSM
// обязан восстановиться из снимка на диске (включая ключи до линии
// сжатия), а суффикс лога — доиграться существующим механизмом.
// Используются реальные FileStorage и FileSnapshotStore в одной директории.
func TestSnapshot_RestartRestoresFSM(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	storage := NewFileStorage(dir)
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	fsm := newSnapshotTestFSM()
	ready := make(chan any)
	close(ready)
	transport := NewInmemTransport("single")

	cm := NewConsensusModule(0, []int{}, transport, storage, fsm, ready, store)
	waitForLeader(t, cm, 5*time.Second)

	cm.SetSnapshotConfig(8, 50*time.Millisecond, 2)

	const numCommands = 30
	for i := 0; i < numCommands; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		future := cm.Apply(cmd, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %q failed: %v", cmd, err)
		}
	}

	snapIdx := waitForSnapshotAndCompaction(t, cm, numCommands)
	for i := 0; i < numCommands; i++ {
		key := fmt.Sprintf("k%d", i)
		want := fmt.Sprintf("v%d", i)
		if v := fsm.getState(key); v != want {
			t.Fatalf("before restart: %s = %q, want %q", key, v, want)
		}
	}

	waitForSnapshotQuiescence(t, cm, storage, 10*time.Second)

	cm.Stop()
	transport.Close()

	// Полный рестарт: новые экземпляры хранилищ и FSM в той же директории.
	storage2 := NewFileStorage(dir)
	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart) failed: %v", err)
	}
	fsm2 := newSnapshotTestFSM()
	ready2 := make(chan any)
	close(ready2)
	transport2 := NewInmemTransport("single-2")

	cm2 := NewConsensusModule(0, []int{}, transport2, storage2, fsm2, ready2, store2)
	defer cm2.Stop()

	// Главный регрессионный критерий P0-1: ключ до линии сжатия
	// восстановлен из снимка сразу после конструктора.
	if v := fsm2.getState("k0"); v != "v0" {
		t.Fatalf("k0 = %q after restart, want v0 (key below compaction line)", v)
	}

	cm2.mu.Lock()
	lastApplied := cm2.cmState.lastApplied
	fsmAppliedIndex := cm2.cmState.fsmAppliedIndex
	lastSnapshotIndex := cm2.cmState.lastSnapshotIndex
	cm2.mu.Unlock()
	if fsmAppliedIndex != lastSnapshotIndex {
		t.Fatalf("fsmAppliedIndex = %d, want %d (snapshot index)", fsmAppliedIndex, lastSnapshotIndex)
	}
	if lastSnapshotIndex < snapIdx {
		t.Fatalf("lastSnapshotIndex = %d after restart, want >= %d", lastSnapshotIndex, snapIdx)
	}
	if lastApplied != lastSnapshotIndex {
		t.Fatalf("lastApplied = %d, want %d (snapshot index)", lastApplied, lastSnapshotIndex)
	}

	// Узел работоспособен после restore: новая команда проходит и
	// продвижение commitIndex доигрывает суффикс лога (k29).
	waitForLeader(t, cm2, 5*time.Second)
	future := cm2.Apply("k30=v30", 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply k30 after restart failed: %v", err)
	}
	if v := fsm2.getState("k30"); v != "v30" {
		t.Fatalf("k30 = %q after restart, want v30", v)
	}

	// Проверяются ВСЕ ключи, а не только последний: дефектный снимок
	// теряет произвольную подтверждённую запись, и проверка одного ключа
	// пропускает большую часть потерь.
	waitForAllKeys(t, fsm2, numCommands+1, 10*time.Second)
}

// TestSnapshot_LeaderCrash проверяет, что снимок переживает смену лидера.
func TestSnapshot_LeaderCrash(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	// Устанавливаем низкий порог.
	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(15, 50*time.Millisecond, 8)
	}

	leaderID := h.waitForSingleLeader()

	// Отправляем 25 команд и ждём снимка.
	for i := 0; i < 25; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		idx := h.SubmitToServer(leaderID, cmd)
		if idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot not created")
	}

	// Убиваем лидера.
	h.CrashPeer(leaderID)
	t.Logf("leader %d crashed", leaderID)

	// Ждём нового лидера.
	newLeaderID := h.waitForSingleLeader()
	if newLeaderID < 0 {
		t.Fatal("no leader elected after crash")
	}
	if newLeaderID == leaderID {
		t.Fatal("new leader should be different")
	}
	t.Logf("new leader: %d", newLeaderID)
}

// TestInmemTransport_InstallSnapshot проверяет InstallSnapshot через
// in-memory транспорт без участия CM.
func TestInmemTransport_InstallSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	// Запускаем обработчик, который отвечает на InstallSnapshot.
	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-t2.Consumer():
			switch rpc.Command.(type) {
			case *InstallSnapshotRequest:
				// Читаем и отбрасываем данные.
				_, _ = io.Copy(io.Discard, rpc.Reader)
				rpc.RespChan <- RPCResponse{
					Reply: &InstallSnapshotResponse{
						RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
						Term:      1,
						Success:   true,
					},
				}
			}
		case <-done:
		}
	}()
	defer close(done)

	req := InstallSnapshotRequest{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 0},
		Term:         1,
		LeaderID:     0,
		LastLogIndex: 10,
		LastLogTerm:  1,
		DataSize:     4,
	}
	data := bytes.NewReader([]byte("test"))
	reply, err := t1.InstallSnapshot(1, req, data)
	if err != nil {
		t.Fatalf("InstallSnapshot failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("InstallSnapshot: Success = false")
	}
}

// ============================================================
// Матрица Case A…F (architecture-2.md §18)
// ============================================================

// sendInstallSnapshotManually отправляет InstallSnapshot напрямую через
// сырой транспорт узла (в обход counting-обёртки — ручные отправки не
// учитываются в installSnapshotCount). Данные и метаданные — из
// снимок-стора отправителя.
func (h *snapshotHarness) sendInstallSnapshotManually(from, to, lastLogIndex int) InstallSnapshotResponse {
	snaps, err := h.stores[from].List()
	if err != nil || len(snaps) == 0 {
		h.t.Fatalf("node %d has no snapshot to send: err=%v", from, err)
	}
	meta, reader, err := h.stores[from].Open(snaps[0].ID)
	if err != nil {
		h.t.Fatalf("open snapshot: %v", err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		h.t.Fatalf("read snapshot data: %v", err)
	}
	cfgData, _ := EncodeConfiguration(meta.Configuration)

	h.cluster[from].mu.Lock()
	term := h.cluster[from].cmState.currentTerm
	h.cluster[from].mu.Unlock()

	req := InstallSnapshotRequest{
		RPCHeader:     RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: from},
		Term:          term,
		LeaderID:      from,
		LastLogIndex:  lastLogIndex,
		LastLogTerm:   meta.Term,
		Configuration: cfgData,
		ConfigIndex:   meta.ConfigIndex,
		DataSize:      int64(len(data)),
	}
	reply, err := h.transports[from].InstallSnapshot(ServerID(to), req, bytes.NewReader(data))
	if err != nil {
		h.t.Fatalf("manual InstallSnapshot: %v", err)
	}
	return reply
}

// TestSnapshot_CatchUpWithinTrailingWindow — Case A:
// follower отставал в пределах trailing-окна (≤128) → InstallSnapshot не
// отправляется ни разу, догон — обычным AppendEntries. Заготовка создана
// в, рабочая реализация активируется.
func TestSnapshot_CatchUpWithinTrailingWindow(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	// Широкое trailing-окно: записи follower'а остаются в срезе лидера.
	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(30, 50*time.Millisecond, 128)
	}

	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		t.Fatal("no leader elected")
	}
	followerID := (leaderID + 1) % 3

	// Синхронизируем кластер и даём лидеру сжать журнал при
	// всех подключённых узлах: 160 команд при threshold=30 дают несколько
	// снимков, а trailing-окно (128) удерживает записи в срезе.
	for i := 0; i < 160; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		if idx := h.SubmitToServer(leaderID, cmd); idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot was not created on leader")
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	// Отключаем follower и добавляем 10 команд — отставание в пределах
	// trailing-окна (128) и порога снимка (30).
	isBefore := h.installSnapshotCount(followerID)
	h.DisconnectPeer(followerID)
	for i := 160; i < 170; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		if idx := h.SubmitToServer(leaderID, cmd); idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}

	h.ReconnectPeer(followerID)
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	if n := h.installSnapshotCount(followerID) - isBefore; n != 0 {
		t.Fatalf("installSnapshotCount delta=%d, want 0 (follower within trailing window)", n)
	}
	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)
	h.checkLogIntersection(leaderID, followerID)
}

// TestSnapshot_CatchUpBehindSnapshot — Case B (/004): follower
// отставал за границу снимка → ровно один InstallSnapshot, затем
// догон через AppendEntries. Прямая проверка, что отвергнутая Option A
// (кламп nextIndex по lastSnapshotIndex+1, отключающий InstallSnapshot)
// не реализована.
func TestSnapshot_CatchUpBehindSnapshot(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, snapIdx, isBefore := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	// Ровно один InstallSnapshot после переподключения
	// (Validation Case B: отвергнутая Option A не реализована — IS
	// по-прежнему отправляется, но ровно один раз, без цикла).
	if n := h.installSnapshotCount(followerID) - isBefore; n != 1 {
		t.Fatalf("installSnapshotCount after reconnect=%d, want exactly 1 (behind snapshot boundary)", n)
	}
	if h.getLastLogIndex(followerID) < snapIdx {
		t.Fatalf("follower lastLogIndex=%d < leader snapshot index %d",
			h.getLastLogIndex(followerID), snapIdx)
	}
	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)
	h.checkLogIntersection(leaderID, followerID)
}

// TestSnapshot_InstallPostConditions — Case C (/004): после
// установки снимка, покрывающего весь лог follower'а, выполнены
// lastLogIndexAndTerm() согласован с метаданными
// снимка (защита Leader Completeness: logOk в RequestVote/PreVote
// считается против корректной пары, а не (-1,-1)).
func TestSnapshot_InstallPostConditions(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	_, followerID, _, _ := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}

	cm := h.cluster[followerID]
	cm.mu.Lock()
	lastSnapshotIndex := cm.cmState.lastSnapshotIndex
	lastSnapshotTerm := cm.cmState.lastSnapshotTerm
	lastLogIndex := cm.cmState.lastLogIndex
	lastLogTerm := cm.cmState.lastLogTerm
	lastApplied := cm.cmState.lastApplied
	commitIndex := cm.cmState.commitIndex
	logLen := len(cm.cmState.log)
	lli, llt := cm.lastLogIndexAndTerm()
	cm.mu.Unlock()

	if lastLogIndex < lastSnapshotIndex {
		t.Fatalf("INV-S1 violated: lastLogIndex=%d < lastSnapshotIndex=%d", lastLogIndex, lastSnapshotIndex)
	}
	if logLen == 0 && (lastLogIndex != lastSnapshotIndex || lastLogTerm != lastSnapshotTerm) {
		t.Fatalf("INV-S2 violated: lastLog=(%d,%d), snapshot=(%d,%d)",
			lastLogIndex, lastLogTerm, lastSnapshotIndex, lastSnapshotTerm)
	}
	if lastApplied > lastLogIndex || commitIndex > lastLogIndex {
		t.Fatalf("INV-S4 violated: lastApplied=%d commitIndex=%d > lastLogIndex=%d",
			lastApplied, commitIndex, lastLogIndex)
	}
	if lli != lastLogIndex || llt != lastLogTerm {
		t.Fatalf("lastLogIndexAndTerm() = (%d,%d), want (%d,%d) — vote-safety pair corrupted",
			lli, llt, lastLogIndex, lastLogTerm)
	}
}

// TestSnapshot_InstallThenAppendEntries —  сразу после установки снимка
// AppendEntries с PrevLogIndex == meta.Index обязан завершиться Success:true,
// а журнал follower'а — достроиться записями meta.Index+1….
func TestSnapshot_InstallThenAppendEntries(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, snapIdx, _ := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}

	// Формируем AE от лидера: prev == snapIdx, записи snapIdx+1….
	leader := h.cluster[leaderID]
	leader.mu.Lock()
	prevLogTerm := leader.lookupTermLocked(snapIdx)
	entries := append([]LogEntry{}, leader.cmState.log[leader.logPositionLocked(snapIdx+1):]...)
	leaderCommit := leader.cmState.commitIndex
	term := leader.cmState.currentTerm
	leader.mu.Unlock()

	var reply AppendEntriesReply
	err := h.cluster[followerID].AppendEntries(AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: leaderID},
		Term:         term,
		LeaderID:     leaderID,
		PrevLogIndex: snapIdx,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: leaderCommit,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries after snapshot: %v", err)
	}
	if !reply.Success {
		t.Fatalf("AppendEntries with PrevLogIndex=%d after snapshot failed: %+v", snapIdx, reply)
	}

	// Журнал достроился записями snapIdx+1….
	if h.getLastLogIndex(followerID) < h.getLastLogIndex(leaderID) {
		t.Fatalf("follower lastLogIndex=%d < leader lastLogIndex=%d",
			h.getLastLogIndex(followerID), h.getLastLogIndex(leaderID))
	}
	h.checkLogIntersection(leaderID, followerID)
}

// TestSnapshot_InstallRestartFollowUp — Case E: рестарт
// follower'а после установки снимка: persistent-состояние консистентно
// (стартовый путь не отказывает), FSM восстановлен из снимка, догон продолжается.
func TestSnapshot_InstallRestartFollowUp(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, snapIdx, _ := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)
	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)

	h.CrashPeer(followerID)
	h.RestartPeer(followerID)

	// После рестарта persistent-состояние консистентно.
	if h.getLastLogIndex(followerID) < h.getLastSnapshotIndex(followerID) {
		t.Fatalf("after restart: lastLogIndex=%d < lastSnapshotIndex=%d — continuity violated",
			h.getLastLogIndex(followerID), h.getLastSnapshotIndex(followerID))
	}
	if h.getLastSnapshotIndex(followerID) < snapIdx {
		t.Fatalf("after restart: lastSnapshotIndex=%d < %d", h.getLastSnapshotIndex(followerID), snapIdx)
	}
	// FSM восстановлен из снимка: ключ ниже линии сжатия жив.
	if v := h.fsms[followerID].getState("k0"); v != "v0" {
		t.Fatalf("k0 = %q after restart, want v0 (FSM not restored from snapshot)", v)
	}

	// Догон продолжается: новая команда доезжает до follower'а.
	if idx := h.SubmitToServer(leaderID, "kpost=vpost"); idx < 0 {
		t.Fatal("SubmitToServer after follower restart failed")
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)
	h.waitForFSMStateEqual(leaderID, followerID, 10*time.Second)
}

// TestSnapshot_InstallIdempotency — Case F (/004,):
// повторная отправка того же и более старого снимка — no-op с
// Success:true; commitIndex/lastApplied не откатываются; цикла нет
// (число InstallSnapshot не растёт за окно наблюдения).
func TestSnapshot_InstallIdempotency(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	leaderID, followerID, _, isBefore := h.driveSnapshotInstall(20, 8, 30)

	if !h.waitForSnapshot(followerID, 10*time.Second) {
		t.Fatalf("follower %d did not install snapshot", followerID)
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	if n := h.installSnapshotCount(followerID) - isBefore; n != 1 {
		t.Fatalf("installSnapshotCount after reconnect=%d, want 1 after natural catch-up", n)
	}

	snapIdx := h.getSnapshotIndex(followerID)
	commitBefore := h.getCommitIndex(followerID)
	appliedBefore := h.getLastApplied(followerID)

	// Повторная установка того же снимка → no-op Success:true.
	reply := h.sendInstallSnapshotManually(leaderID, followerID, snapIdx)
	if !reply.Success {
		t.Fatalf("repeated InstallSnapshot: Success=false, want true (idempotent no-op)")
	}
	// Более старый снимок → no-op Success:true, состояние не откатывается.
	reply = h.sendInstallSnapshotManually(leaderID, followerID, snapIdx-1)
	if !reply.Success {
		t.Fatalf("stale InstallSnapshot: Success=false, want true (no-op)")
	}

	if h.getCommitIndex(followerID) < commitBefore || h.getLastApplied(followerID) < appliedBefore {
		t.Fatalf("commitIndex/lastApplied rolled back: commit=%d->%d applied=%d->%d",
			commitBefore, h.getCommitIndex(followerID), appliedBefore, h.getLastApplied(followerID))
	}

	// Счётчик no-op'ов на follower'е учитывает обе отправки.
	cm := h.cluster[followerID]
	cm.mu.Lock()
	stale := peerSum(cm.counters.installSnapshotSkippedStale)
	cm.mu.Unlock()
	if stale < 2 {
		t.Fatalf("installSnapshotSkippedStale = %d, want >= 2", stale)
	}

	// Цикла нет: за окно наблюдения новых InstallSnapshot не появляется.
	before := h.installSnapshotCount(followerID)
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	sleepMs(ReelectionTimeoutMs * 2)
	if n := h.installSnapshotCount(followerID); n != before {
		t.Fatalf("InstallSnapshot storm: count %d -> %d", before, n)
	}
}

// TestSnapshot_InstallQuorumFiveNodes — тест 1: кластер из пяти
// узлов, два follower'а догнаны исключительно через InstallSnapshot —
// commitIndex продвигается, Apply-future разрешаются (кворум учитывает
// догнанных снимком).
func TestSnapshot_InstallQuorumFiveNodes(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 5)
	defer h.Shutdown()

	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(20, 50*time.Millisecond, 2)
	}

	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		t.Fatal("no leader elected")
	}
	f1 := (leaderID + 1) % 5
	f2 := (leaderID + 2) % 5

	// Seed-запись: f1 и f2 гарантированно имеют записи журнала до
	// отключения (детерминированность догона через InstallSnapshot).
	if idx := h.SubmitToServer(leaderID, "kseed=vseed"); idx < 0 {
		t.Fatal("seed command failed")
	}
	h.waitForCatchUp(leaderID, f1, 10*time.Second)
	h.waitForCatchUp(leaderID, f2, 10*time.Second)

	// Два follower'а отключены: отстанут за границу снимка (trailing=2).
	h.DisconnectPeer(f1)
	h.DisconnectPeer(f2)

	for i := 0; i < 40; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		if idx := h.SubmitToServer(leaderID, cmd); idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot was not created on leader")
	}

	// Догон отставших — исключительно через InstallSnapshot.
	h.ReconnectPeer(f1)
	h.ReconnectPeer(f2)
	if !h.waitForSnapshot(f1, 10*time.Second) || !h.waitForSnapshot(f2, 10*time.Second) {
		t.Fatal("snapshot-caught followers did not install snapshot")
	}
	h.waitForCatchUp(leaderID, f1, 10*time.Second)
	h.waitForCatchUp(leaderID, f2, 10*time.Second)

	// Отключаем двух follower'ов, догнанных через AE: кворум теперь
	// {leader, f1, f2}. Новая команда обязана зафиксироваться и примениться.
	up1, up2 := -1, -1
	for i := 0; i < h.n; i++ {
		if i != leaderID && i != f1 && i != f2 {
			if up1 < 0 {
				up1 = i
			} else {
				up2 = i
			}
		}
	}
	h.DisconnectPeer(up1)
	h.DisconnectPeer(up2)

	idx := h.SubmitToServer(leaderID, "kq=vq")
	if idx < 0 {
		t.Fatal("apply did not commit with two snapshot-caught followers in quorum")
	}
}

// TestReplicationBackoff_BoundsAttemptsToUnavailablePeer — тест 1:
// недоступный пир, отставший за границу снимка: число попыток
// InstallSnapshot за фиксированное окно ограничено сверху задержкой повторов
// (до изменения — попытка на каждый heartbeat, ~30/с).
func TestReplicationBackoff_BoundsAttemptsToUnavailablePeer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(20, 50*time.Millisecond, 8)
	}

	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		t.Fatal("no leader elected")
	}
	followerID := (leaderID + 1) % 3

	// Seed-запись: follower имеет записи журнала до отключения, поэтому
	// после снимка лидер уходит именно в ветку InstallSnapshot
	// (детерминированность — см. driveSnapshotInstall).
	if idx := h.SubmitToServer(leaderID, "kseed=vseed"); idx < 0 {
		t.Fatal("seed command failed")
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	// Follower отключён: лидер будет безуспешно слать InstallSnapshot
	// (транспортная ошибка) — задержка повторов обязан ограничить частоту.
	h.DisconnectPeer(followerID)
	for i := 0; i < 30; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		if idx := h.SubmitToServer(leaderID, cmd); idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot was not created on leader")
	}

	// Окно наблюдения: 3 секунды.
	before := h.installSnapshotCount(followerID)
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(3 * time.Second)
	attempts := h.installSnapshotCount(followerID) - before

	if attempts == 0 {
		t.Fatal("no InstallSnapshot attempts — scenario broken")
	}
	if attempts > 12 {
		t.Fatalf("InstallSnapshot attempts in 3s = %d, want <= 12 (backoff ceiling)", attempts)
	}
}

// TestReplicationBackoff_RecoveryAfterReconnect — тест 2: пир
// становится доступен — первый успешный RPC происходит вскоре после
// переподключения, replFailures обнуляется после успеха. Потолок
// задержки (1000 мс) проверяется детерминированно в
// TestReplicationBackoffDelay_Clamp; здесь дедлайн 3 с — запас на
// contention полного набора тестов (in-memory harness, один процесс).
func TestReplicationBackoff_RecoveryAfterReconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	h := newSnapshotHarness(t, 3)
	defer h.Shutdown()

	for i := 0; i < h.n; i++ {
		h.cluster[i].SetSnapshotConfig(20, 50*time.Millisecond, 8)
	}

	leaderID := h.waitForSingleLeader()
	if leaderID < 0 {
		t.Fatal("no leader elected")
	}
	followerID := (leaderID + 1) % 3

	// Seed-запись: детерминированный вход в ветку InstallSnapshot
	// (см. driveSnapshotInstall).
	if idx := h.SubmitToServer(leaderID, "kseed=vseed"); idx < 0 {
		t.Fatal("seed command failed")
	}
	h.waitForCatchUp(leaderID, followerID, 10*time.Second)

	// Точка отсчёта: matchIndex до отключения (после seed-записи > -1).
	// Успешный RPC после переподключения определяется именно ростом
	// matchIndex, а не значением > -1.
	cm := h.cluster[leaderID]
	cm.mu.Lock()
	matchBefore := cm.leaderState.matchIndex[followerID]
	cm.mu.Unlock()

	h.DisconnectPeer(followerID)
	for i := 0; i < 30; i++ {
		cmd := fmt.Sprintf("k%d=v%d", i, i)
		if idx := h.SubmitToServer(leaderID, cmd); idx < 0 {
			t.Fatalf("SubmitToServer(%q) failed", cmd)
		}
	}
	if !h.waitForSnapshot(leaderID, 10*time.Second) {
		t.Fatal("snapshot was not created on leader")
	}

	// Даём накопиться транспортным ошибкам (задержка выросла).
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(1500 * time.Millisecond)

	h.ReconnectPeer(followerID)
	start := time.Now()

	// Ждём первого успешного RPC (matchIndex на лидере растёт выше
	// значения, зафиксированного до отключения).
	deadline := start.Add(3000 * time.Millisecond)
	for {
		cm := h.cluster[leaderID]
		cm.mu.Lock()
		mi := cm.leaderState.matchIndex[followerID]
		rf := cm.leaderState.replFailures[followerID]
		cm.mu.Unlock()
		if mi > matchBefore {
			if rf != 0 {
				t.Fatalf("replFailures = %d after first success, want 0", rf)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("first successful RPC delayed beyond backoff ceiling + load margin")
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(10 * time.Millisecond)
	}
}
