package raft

import (
	"net"
	"testing"
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
