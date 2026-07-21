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

	s := New(Config{
		ServerID:   1,
		PeerIds:    []int{2, 3},
		RPCAddress: ":0",
	}, storage, ready, commitChan)

	if s.serverID != 1 {
		t.Fatalf("expected serverID=1, got %d", s.serverID)
	}
	if len(s.peerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(s.peerIds))
	}
	if s.peerAddresses == nil {
		t.Fatalf("peerAddresses should not be nil")
	}
}

func TestNewServerWrapper(t *testing.T) {
	storage := NewMapStorage()
	commitChan := make(chan CommitEntry, 10)
	ready := make(chan any)

	s := NewServer(2, []int{1, 3}, storage, ready, commitChan)

	if s.serverID != 2 {
		t.Fatalf("expected serverID=2, got %d", s.serverID)
	}
	if len(s.peerIds) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(s.peerIds))
	}
}

func TestConnectToPeerWithTimeout(t *testing.T) {
	storage0 := NewMapStorage()
	storage1 := NewMapStorage()
	commitChan0 := make(chan CommitEntry, 10)
	commitChan1 := make(chan CommitEntry, 10)
	ready0 := make(chan any)
	ready1 := make(chan any)

	s0 := NewServer(0, []int{1}, storage0, ready0, commitChan0)
	s1 := NewServer(1, []int{0}, storage1, ready1, commitChan1)

	s0.Serve(":0")
	s1.Serve(":0")

	addr1 := s1.GetListenAddr()

	// Подключение с таймаутом должно работать
	if err := s0.ConnectToPeerWithTimeout(1, addr1, 2*time.Second); err != nil {
		t.Fatalf("ConnectToPeerWithTimeout failed: %v", err)
	}

	// Повторное подключение не должно вызывать ошибку
	if err := s0.ConnectToPeerWithTimeout(1, addr1, 2*time.Second); err != nil {
		t.Fatalf("re-connect should not fail: %v", err)
	}
}

func TestConnectToPeerWithTimeoutInvalid(t *testing.T) {
	storage := NewMapStorage()
	commitChan := make(chan CommitEntry, 10)
	ready := make(chan any)

	s := NewServer(0, []int{1}, storage, ready, commitChan)

	// Подключение к несуществующему адресу должно вернуть ошибку
	fakeAddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:9999")
	err := s.ConnectToPeerWithTimeout(1, fakeAddr, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestPeerClient(t *testing.T) {
	storage0 := NewMapStorage()
	storage1 := NewMapStorage()
	commitChan0 := make(chan CommitEntry, 10)
	commitChan1 := make(chan CommitEntry, 10)
	ready0 := make(chan any)
	ready1 := make(chan any)

	s0 := NewServer(0, []int{1}, storage0, ready0, commitChan0)
	s1 := NewServer(1, []int{0}, storage1, ready1, commitChan1)

	s0.Serve(":0")
	s1.Serve(":0")

	// До подключения PeerClient должен вернуть nil
	if c := s0.PeerClient(1); c != nil {
		t.Fatalf("expected nil client before connect")
	}

	addr1 := s1.GetListenAddr()
	if err := s0.ConnectToPeer(1, addr1); err != nil {
		t.Fatalf("ConnectToPeer failed: %v", err)
	}

	// После подключения PeerClient должен вернуть не nil
	if c := s0.PeerClient(1); c == nil {
		t.Fatalf("expected non-nil client after connect")
	}
}

func TestClosePeerClient(t *testing.T) {
	storage0 := NewMapStorage()
	storage1 := NewMapStorage()
	commitChan0 := make(chan CommitEntry, 10)
	commitChan1 := make(chan CommitEntry, 10)
	ready0 := make(chan any)
	ready1 := make(chan any)

	s0 := NewServer(0, []int{1}, storage0, ready0, commitChan0)
	s1 := NewServer(1, []int{0}, storage1, ready1, commitChan1)

	s0.Serve(":0")
	s1.Serve(":0")

	addr1 := s1.GetListenAddr()
	if err := s0.ConnectToPeer(1, addr1); err != nil {
		t.Fatalf("ConnectToPeer failed: %v", err)
	}

	s0.ClosePeerClient(1)

	// После закрытия PeerClient должен вернуть nil
	if c := s0.PeerClient(1); c != nil {
		t.Fatalf("expected nil client after close")
	}
}

func TestCallReconnect(t *testing.T) {
	storage0 := NewMapStorage()
	storage1 := NewMapStorage()
	commitChan0 := make(chan CommitEntry, 10)
	commitChan1 := make(chan CommitEntry, 10)
	ready0 := make(chan any)
	ready1 := make(chan any)

	s0 := NewServer(0, []int{1}, storage0, ready0, commitChan0)
	s1 := NewServer(1, []int{0}, storage1, ready1, commitChan1)

	s0.Serve(":0")
	s1.Serve(":0")

	addr1 := s1.GetListenAddr()
	// Используем ConnectToPeerWithTimeout, чтобы установить peerAddresses
	if err := s0.ConnectToPeerWithTimeout(1, addr1, 2*time.Second); err != nil {
		t.Fatalf("ConnectToPeer failed: %v", err)
	}

	// Закрываем клиент, затем вызываем Call — должна сработать попытка переподключения
	s0.ClosePeerClient(1)

	var reply RequestVoteReply
	err := s0.Call(1, "ConsensusModule.RequestVote", RequestVoteArgs{
		Term:        1,
		CandidateID: 0,
	}, &reply)

	if err != nil {
		t.Fatalf("Call with reconnect should succeed: %v", err)
	}
}
