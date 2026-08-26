package raft

import (
	"io"
	"testing"
	"time"
)

// NoOpFSM — пустая реализация FSM для тестов.
type NoOpFSM struct{}

func (f NoOpFSM) Apply(_ *LogEntry) any {
	return nil
}

func (f NoOpFSM) Snapshot() (FSMSnapshot, error) {
	return nil, ErrNotImplemented
}

func (f NoOpFSM) Restore(_ io.ReadCloser) error {
	return ErrNotImplemented
}

// tcpHarness — минимальный тестовый стенд для TCP-кластера Raft.
// Использует TCPTransport вместо InmemTransport.
type tcpHarness struct {
	t          *testing.T
	cluster    []*ConsensusModule
	transports []*TCPTransport
	storage    []*MapStorage
	alive      []bool
	ready      chan any
	n          int
}

// newTCPHarness создаёт кластер из n TCP-узлов, соединённых друг с другом.
func newTCPHarness(t *testing.T, n int) *tcpHarness {
	t.Helper()

	transports := make([]*TCPTransport, n)
	storage := make([]*MapStorage, n)
	cluster := make([]*ConsensusModule, n)
	alive := make([]bool, n)
	ready := make(chan any)

	// Создаём транспорты (каждый на своём порту).
	for i := 0; i < n; i++ {
		trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
		if err != nil {
			t.Fatalf("NewTCPTransport(%d): %v", i, err)
		}
		transports[i] = trans
	}

	// Извлекаем адреса и соединяем все узлы друг с другом.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				transports[i].Connect(ServerID(j), string(transports[j].LocalAddr()))
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

		storage[i] = NewMapStorage()
		cluster[i] = NewConsensusModule(
			i, peerIds, transports[i],
			storage[i], NoOpFSM{}, ready,
		)
		alive[i] = true
	}
	close(ready)

	return &tcpHarness{
		t:          t,
		cluster:    cluster,
		transports: transports,
		storage:    storage,
		alive:      alive,
		ready:      ready,
		n:          n,
	}
}

// Close останавливает все живые узлы кластера и закрывает транспорты.
func (h *tcpHarness) Close() {
	for i := 0; i < h.n; i++ {
		if h.alive[i] {
			h.cluster[i].Stop()
			h.transports[i].Close()
			h.alive[i] = false
		}
	}
}

// stopPeer останавливает указанный узел.
func (h *tcpHarness) stopPeer(id int) {
	if h.alive[id] {
		h.cluster[id].Stop()
		h.transports[id].Close()
		h.alive[id] = false
	}
}

// getLeaderState возвращает ID узла, который считает себя лидером,
// или -1 если такого нет.
func (h *tcpHarness) getLeaderState() int {
	for i := 0; i < h.n; i++ {
		_, _, isLeader := h.cluster[i].Report()
		if isLeader {
			return i
		}
	}
	return -1
}

// checkSingleLeader проверяет, что ровно один лидер выбран.
func (h *tcpHarness) checkSingleLeader() int {
	h.t.Helper()
	leaders := make(map[int]int)
	for i := 0; i < h.n; i++ {
		id, term, isLeader := h.cluster[i].Report()
		if isLeader {
			leaders[i] = int(term)
			_ = id
		}
	}
	if len(leaders) != 1 {
		h.t.Fatalf("expected exactly one leader, got %d: %v", len(leaders), leaders)
	}
	for id, term := range leaders {
		if term == 0 {
			h.t.Fatalf("leader %d has term 0", id)
		}
		return id
	}
	return -1
}

// TestTCPRaftElectionBasic проверяет выборы лидера в 3-узловом TCP-кластере.
func TestTCPRaftElectionBasic(t *testing.T) {
	h := newTCPHarness(t, 3)
	defer h.Close()

	// Ждём выборы с таймаутом.
	timeout := time.After(10 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			if h.getLeaderState() != -1 {
				close(done)
				return
			}
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-timeout:
		t.Fatal("timed out waiting for leader election")
	case <-done:
	}

	h.checkSingleLeader()
}

// TestTCPRaftReelectionAfterCrash проверяет, что после остановки лидера
// новый лидер избирается среди оставшихся узлов.
func TestTCPRaftReelectionAfterCrash(t *testing.T) {
	h := newTCPHarness(t, 3)
	defer h.Close()

	// Ждём первого лидера.
	timeout := time.After(10 * time.Second)
	leader := -1
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for initial leader")
		default:
		}
		if l := h.getLeaderState(); l != -1 {
			leader = l
			break
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(100 * time.Millisecond)
	}

	// Останавливаем лидера.
	h.stopPeer(leader)

	// Ждём выборов нового лидера.
	timeout = time.After(10 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for re-election after crash")
		default:
		}
		if l := h.getLeaderState(); l != -1 && l != leader {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(100 * time.Millisecond)
	}
}
