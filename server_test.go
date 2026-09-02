package raft

import (
	"io"
	"net"
	"testing"
	"time"
)

var _ TransportManager = (*TCPTransport)(nil)

func TestConfig(t *testing.T) {
	cfg := Config{
		PeerAddresses: map[int]net.Addr{1: nil, 2: nil},
		PeerIds:       []int{1, 2},
		ServerID:      0,
	}
	if cfg.ServerID != 0 {
		t.Fatalf("expected ServerID=0, got %d", cfg.ServerID)
	}
	if len(cfg.PeerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg.PeerIds))
	}
}

// stubTransportManager — минимальная тестовая заглушка
// TransportManager для TestNewServer: без слушателя и горутин.
// Методы не вызываются — тест проверяет только присваивание полей
// в New; нулевые возвращаемые значения достаточны.
type stubTransportManager struct{}

func (stubTransportManager) Consumer() <-chan RPC { return nil }

func (stubTransportManager) AppendEntries(ServerID, AppendEntriesArgs) (AppendEntriesReply, error) {
	return AppendEntriesReply{}, nil
}

func (stubTransportManager) RequestVote(ServerID, RequestVoteArgs) (RequestVoteReply, error) {
	return RequestVoteReply{}, nil
}

func (stubTransportManager) RequestPreVote(ServerID, RequestPreVoteArgs) (RequestPreVoteReply, error) {
	return RequestPreVoteReply{}, nil
}

func (stubTransportManager) TimeoutNow(ServerID, TimeoutNowRequest) (TimeoutNowResponse, error) {
	return TimeoutNowResponse{}, nil
}

func (stubTransportManager) InstallSnapshot(ServerID, InstallSnapshotRequest, io.Reader) (InstallSnapshotResponse, error) {
	return InstallSnapshotResponse{}, nil
}

func (stubTransportManager) AppendEntriesPipeline(ServerID) (AppendPipeline, error) {
	return nil, nil
}

func (stubTransportManager) SetHeartbeatHandler(func(RPC)) {}
func (stubTransportManager) LocalAddr() ServerAddress      { return "" }
func (stubTransportManager) Connect(ServerID, string)      {}
func (stubTransportManager) Disconnect(ServerID)           {}
func (stubTransportManager) DisconnectAll()                {}
func (stubTransportManager) Close()                        {}

func TestNewServer(t *testing.T) {
	storage := NewMapStorage()
	commitChan := make(chan CommitEntry, 10)
	ready := make(chan any)

	s := New(&Config{
		Fsm:       NewCommitChannelFSM(commitChan),
		PeerIds:   []int{2, 3},
		ServerID:  1,
		Storage:   storage,
		Transport: stubTransportManager{},
	}, ready)

	if s.serverID != 1 {
		t.Fatalf("expected serverID=1, got %d", s.serverID)
	}
	if len(s.peerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(s.peerIds))
	}
}

// TestDefaultTCPRPCTimeoutBarrier закрепляет контракт дефолтов тайм-аута
// (AC-1): алиас TCPRPCTimeout несёт то же значение, что и каноническое
// внутреннее имя _defaultTCPRPCTimeout (191 мс), а производная константа
// проверки кворума остаётся ровно двукратной (инвариант 2×RPC на уровне
// дефолтов, 382 мс).
func TestDefaultTCPRPCTimeoutBarrier(t *testing.T) {
	if TCPRPCTimeout != _defaultTCPRPCTimeout {
		t.Fatalf("TCPRPCTimeout = %v, want %v", TCPRPCTimeout, _defaultTCPRPCTimeout)
	}
	if _defaultTCPRPCTimeout != 191*time.Millisecond {
		t.Fatalf("_defaultTCPRPCTimeout = %v, want 191ms", _defaultTCPRPCTimeout)
	}
	if _defaultCheckQuorumTimeout != 382*time.Millisecond {
		t.Fatalf("_defaultCheckQuorumTimeout = %v, want 382ms", _defaultCheckQuorumTimeout)
	}
	if _defaultCheckQuorumTimeout != 2*_defaultTCPRPCTimeout {
		t.Fatalf("_defaultCheckQuorumTimeout = %v, want 2*_defaultTCPRPCTimeout = %v",
			_defaultCheckQuorumTimeout, 2*_defaultTCPRPCTimeout)
	}
}

// TestDefaultSnapshotBarrier закрепляет экспортированные дефолты снимков
// (AC-1): DefaultSnapshotInterval == 3 с и DefaultSnapshotThreshold == 1024.
// Контрольная проверка отсутствия неэкспортированных имён выполняется
// командным grep (см. отчёт задачи).
func TestDefaultSnapshotBarrier(t *testing.T) {
	if DefaultSnapshotInterval != 3*time.Second {
		t.Fatalf("DefaultSnapshotInterval = %v, want 3s", DefaultSnapshotInterval)
	}
	if DefaultSnapshotThreshold != 1024 {
		t.Fatalf("DefaultSnapshotThreshold = %d, want 1024", DefaultSnapshotThreshold)
	}
}

// TestServeSnapshotConfig проверяет маршрут параметров снимков (AC-2):
// значения SnapshotInterval/SnapshotThreshold из конфигурации доезжают
// до ConsensusModule через сеттер в Serve, а trailingLogs не изменяется
// (остаётся дефолтом конструктора).
func TestServeSnapshotConfig(t *testing.T) {
	commitChan := make(chan CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitChan {
		}
	}()

	ready := make(chan any)
	transport, err := NewTCPTransport("127.0.0.1:0", 0, 0)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	s := New(&Config{
		Fsm:               NewCommitChannelFSM(commitChan),
		PeerIds:           []int{},
		ServerID:          1,
		SnapshotInterval:  5 * time.Second,
		SnapshotStore:     NewInmemSnapshotStore(),
		SnapshotThreshold: 100,
		Storage:           NewMapStorage(),
		Transport:         transport,
	}, ready)
	s.Serve()
	close(ready)
	t.Cleanup(func() {
		s.Shutdown()
		close(commitChan)
		<-readerDone
	})

	if s.cm.snapshotInterval != 5*time.Second {
		t.Fatalf("cm.snapshotInterval = %v, want 5s", s.cm.snapshotInterval)
	}
	if s.cm.snapshotThreshold != 100 {
		t.Fatalf("cm.snapshotThreshold = %d, want 100", s.cm.snapshotThreshold)
	}
	if s.cm.trailingLogs != _defaultTrailingLogs {
		t.Fatalf("cm.trailingLogs = %d, want %d", s.cm.trailingLogs, _defaultTrailingLogs)
	}
}

// TestServeSnapshotConfigZeros проверяет контракт нулевых значений (AC-3):
// нулевые поля снимков конфигурации заменяются дефолтами конструктора.
func TestServeSnapshotConfigZeros(t *testing.T) {
	commitChan := make(chan CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitChan {
		}
	}()

	ready := make(chan any)
	transport, err := NewTCPTransport("127.0.0.1:0", 0, 0)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	s := New(&Config{
		Fsm:           NewCommitChannelFSM(commitChan),
		PeerIds:       []int{},
		ServerID:      1,
		SnapshotStore: NewInmemSnapshotStore(),
		Storage:       NewMapStorage(),
		Transport:     transport,
	}, ready)
	s.Serve()
	close(ready)
	t.Cleanup(func() {
		s.Shutdown()
		close(commitChan)
		<-readerDone
	})

	if s.cm.snapshotInterval != DefaultSnapshotInterval {
		t.Fatalf("cm.snapshotInterval = %v, want %v", s.cm.snapshotInterval, DefaultSnapshotInterval)
	}
	if s.cm.snapshotThreshold != DefaultSnapshotThreshold {
		t.Fatalf("cm.snapshotThreshold = %d, want %d", s.cm.snapshotThreshold, DefaultSnapshotThreshold)
	}
}

// TestServeSnapshotConfigDisabled проверяет, что при выключенных снимках
// (SnapshotStore == nil) сеттер не вызывается (AC-4): поля CM остаются
// нулевыми, как и до введения маршрута.
func TestServeSnapshotConfigDisabled(t *testing.T) {
	commitChan := make(chan CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitChan {
		}
	}()

	ready := make(chan any)
	transport, err := NewTCPTransport("127.0.0.1:0", 0, 0)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	s := New(&Config{
		Fsm:               NewCommitChannelFSM(commitChan),
		PeerIds:           []int{},
		ServerID:          1,
		SnapshotInterval:  5 * time.Second,
		SnapshotThreshold: 100,
		Storage:           NewMapStorage(),
		Transport:         transport,
	}, ready)
	s.Serve()
	close(ready)
	t.Cleanup(func() {
		s.Shutdown()
		close(commitChan)
		<-readerDone
	})

	if s.cm.snapshotStore != nil {
		t.Fatalf("cm.snapshotStore = %v, want nil", s.cm.snapshotStore)
	}
	if s.cm.snapshotInterval != 0 {
		t.Fatalf("cm.snapshotInterval = %v, want 0 (setter not called)", s.cm.snapshotInterval)
	}
	if s.cm.snapshotThreshold != 0 {
		t.Fatalf("cm.snapshotThreshold = %d, want 0 (setter not called)", s.cm.snapshotThreshold)
	}
}
