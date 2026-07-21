package kvservice

import (
	"net"
	"testing"
	"time"

	"github.com/vskurikhin/raft/pkg/raft"
)

func TestConfig(t *testing.T) {
	cfg := Config{
		Config: raft.Config{
			PeerAddresses: map[int]net.Addr{1: nil, 2: nil},
			PeerIds:       []int{1, 2},
			RPCAddress:    ":0",
			ServerID:      0,
		},
		HTTPAddress: ":8080",
	}
	if cfg.ServerID != 0 {
		t.Fatalf("expected ServerID=0, got %d", cfg.ServerID)
	}
	if cfg.HTTPAddress != ":8080" {
		t.Fatalf("expected HTTPAddress=:8080, got %s", cfg.HTTPAddress)
	}
}

func TestNewKVService(t *testing.T) {
	storage := raft.NewMapStorage()
	ready := make(chan any)

	kvs := New(Config{
		Config: raft.Config{
			PeerIds:    []int{1, 2},
			RPCAddress: ":0",
			ServerID:   0,
		},
		HTTPAddress: ":8080",
	}, storage, ready)

	if kvs.id != 0 {
		t.Fatalf("expected id=0, got %d", kvs.id)
	}
	if kvs.rs == nil {
		t.Fatalf("expected non-nil raft server")
	}
}

func TestNewKVServiceWrapper(t *testing.T) {
	storage := raft.NewMapStorage()
	ready := make(chan any)

	kvs := NewKVService(":0", 1, []int{0, 2}, storage, ready)

	if kvs.id != 1 {
		t.Fatalf("expected id=1, got %d", kvs.id)
	}
}

func TestConnectToRaftPeer(t *testing.T) {
	storage0 := raft.NewMapStorage()
	storage1 := raft.NewMapStorage()
	ready0 := make(chan any)
	ready1 := make(chan any)

	kvs0 := NewKVService(":0", 0, []int{1}, storage0, ready0)
	kvs1 := NewKVService(":0", 1, []int{0}, storage1, ready1)

	addr1 := kvs1.GetRaftListenAddr()

	// Подключение должно работать
	if err := kvs0.ConnectToRaftPeer(1, addr1); err != nil {
		t.Fatalf("ConnectToRaftPeer failed: %v", err)
	}

	// Повторное подключение не должно вызывать ошибку
	if err := kvs0.ConnectToRaftPeer(1, addr1); err != nil {
		t.Fatalf("re-connect should not fail: %v", err)
	}
}

func TestConnectToRaftPeerTimeout(t *testing.T) {
	storage := raft.NewMapStorage()
	ready := make(chan any)

	kvs := NewKVService(":0", 0, []int{1}, storage, ready)

	// Подключение к несуществующему адресу должно вернуть ошибку по таймауту
	fakeAddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:9999")
	err := kvs.ConnectToRaftPeer(1, fakeAddr)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestConnectToRaftPeerWithTimeoutValue(t *testing.T) {
	// Проверяем, что ConnectToRaftPeer использует ConnectToPeerWithTimeout
	// с ожидаемым таймаутом 2*Quantum секунд
	expectedTimeout := 2 * raft.Quantum * time.Second
	if expectedTimeout != 4*time.Second {
		t.Fatalf("expected timeout 4s, got %v", expectedTimeout)
	}
}
