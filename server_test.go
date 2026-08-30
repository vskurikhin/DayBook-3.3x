package raft

import (
	"net"
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	cfg := Config{
		PeerAddresses: map[int]net.Addr{1: nil, 2: nil},
		PeerIds:       []int{1, 2},
		RPCAddress:    ":0",
		ServerID:      0,
	}
	if cfg.ServerID != 0 {
		t.Fatalf("expected ServerID=0, got %d", cfg.ServerID)
	}
	if len(cfg.PeerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg.PeerIds))
	}
}

func TestNewServer(t *testing.T) {
	storage := NewMapStorage()
	commitChan := make(chan CommitEntry, 10)
	ready := make(chan any)

	s := New(&Config{
		Fsm:        NewCommitChannelFSM(commitChan),
		PeerIds:    []int{2, 3},
		RPCAddress: ":0",
		ServerID:   1,
		Storage:    storage,
	}, ready)

	if s.serverID != 1 {
		t.Fatalf("expected serverID=1, got %d", s.serverID)
	}
	if len(s.peerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(s.peerIds))
	}
}

func TestNewServerWrapper(t *testing.T) {
	commitChan := make(chan CommitEntry, 10)
	ready := make(chan any)

	s := NewServer(2, []int{1, 3}, NewCommitChannelFSM(commitChan), ready)

	if s.serverID != 2 {
		t.Fatalf("expected serverID=2, got %d", s.serverID)
	}
	if len(s.peerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(s.peerIds))
	}
}

// TestServeMaxPool проверяет, что значение MaxPool из конфигурации доезжает
// до TCP-транспорта, а ноль заменяется значением по умолчанию транспорта.
func TestServeMaxPool(t *testing.T) {
	tests := []struct {
		name    string
		maxPool int
		want    int
	}{
		{name: "explicit", maxPool: 7, want: 7},
		{name: "zero uses transport default", maxPool: 0, want: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commitChan := make(chan CommitEntry)
			readerDone := make(chan any)
			go func() {
				defer close(readerDone)
				for range commitChan {
				}
			}()

			ready := make(chan any)
			s := New(&Config{
				Fsm:           NewCommitChannelFSM(commitChan),
				MaxPool:       test.maxPool,
				PeerIds:       []int{},
				RPCAddress:    "127.0.0.1:0",
				ServerID:      1,
				Storage:       NewMapStorage(),
				TCPRPCTimeout: TCPRPCTimeout,
			}, ready)
			s.Serve("127.0.0.1:0")
			close(ready)
			t.Cleanup(func() {
				s.Shutdown()
				close(commitChan)
				<-readerDone
			})

			if s.transport.maxPool != test.want {
				t.Fatalf("transport.maxPool = %d, want %d", s.transport.maxPool, test.want)
			}
		})
	}
}

// TestDefaultTCPRPCTimeoutBarrier закрепляет контракт дефолтов тайм-аута
// (AC-1): алиас TCPRPCTimeout несёт то же значение, что и каноническое
// внутреннее имя defaultTCPRPCTimeout (191 мс), а производная константа
// проверки кворума остаётся ровно двукратной (инвариант 2×RPC на уровне
// дефолтов, 382 мс).
func TestDefaultTCPRPCTimeoutBarrier(t *testing.T) {
	if TCPRPCTimeout != defaultTCPRPCTimeout {
		t.Fatalf("TCPRPCTimeout = %v, want %v", TCPRPCTimeout, defaultTCPRPCTimeout)
	}
	if defaultTCPRPCTimeout != 191*time.Millisecond {
		t.Fatalf("defaultTCPRPCTimeout = %v, want 191ms", defaultTCPRPCTimeout)
	}
	if defaultCheckQuorumTimeout != 382*time.Millisecond {
		t.Fatalf("defaultCheckQuorumTimeout = %v, want 382ms", defaultCheckQuorumTimeout)
	}
	if defaultCheckQuorumTimeout != 2*defaultTCPRPCTimeout {
		t.Fatalf("defaultCheckQuorumTimeout = %v, want 2*defaultTCPRPCTimeout = %v",
			defaultCheckQuorumTimeout, 2*defaultTCPRPCTimeout)
	}
}

// TestServeTCPRPCTimeout проверяет, что значение TCPRPCTimeout из конфигурации
// доезжает до TCP-транспорта (мёртвый маршрут оживает, AC-3), а нулевая
// конфигурация получает дефолт транспорта — те же эффективные 191 мс,
// что и до починки маршрута (RISK-021 закрыт).
func TestServeTCPRPCTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "explicit", timeout: 500 * time.Millisecond, want: 500 * time.Millisecond},
		{name: "zero uses transport default", timeout: 0, want: TCPRPCTimeout},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commitChan := make(chan CommitEntry)
			readerDone := make(chan any)
			go func() {
				defer close(readerDone)
				for range commitChan {
				}
			}()

			ready := make(chan any)
			s := New(&Config{
				Fsm:           NewCommitChannelFSM(commitChan),
				PeerIds:       []int{},
				RPCAddress:    "127.0.0.1:0",
				ServerID:      1,
				Storage:       NewMapStorage(),
				TCPRPCTimeout: test.timeout,
			}, ready)
			s.Serve("127.0.0.1:0")
			close(ready)
			t.Cleanup(func() {
				s.Shutdown()
				close(commitChan)
				<-readerDone
			})

			if s.transport.timeout != test.want {
				t.Fatalf("transport.timeout = %v, want %v", s.transport.timeout, test.want)
			}
		})
	}
}
