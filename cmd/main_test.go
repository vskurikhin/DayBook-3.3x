package main

import (
	"net"
	"os"
	"testing"
	"time"

	"vskurikhin/raft/internal/config"
	"vskurikhin/raft/pkg/raft"
)

func TestRunWithEmptyPeers(t *testing.T) {
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	values := config.Values{
		Number:  0,
		Address: addr,
		Peers:   map[int]net.Addr{},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWith(values)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("runWith returned unexpectedly: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestRunWithPeerConnect(t *testing.T) {
	// Start a peer server for runWith to connect to.
	peerReady := make(chan any)
	commitChannel := make(chan raft.CommitEntry)
	storage := raft.NewMapStorage()
	peer := raft.NewServer(1, []int{}, storage, peerReady, commitChannel)
	peer.Serve(":0")
	close(peerReady)
	t.Cleanup(func() { peer.DisconnectAll() })

	peerAddr := peer.GetListenAddr()

	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	values := config.Values{
		Number:  0,
		Address: addr,
		Peers:   map[int]net.Addr{1: peerAddr},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWith(values)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("runWith returned: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestRun(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-addr", ":0", "-number", "0"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- run()
	}()

	select {
	case err := <-errCh:
		t.Fatalf("run returned: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestMainFunction(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	// main() calls log.Fatal on error, which exits. We redirect log output
	// to discard and verify it doesn't exit immediately.
	os.Args = []string{"raft", "-addr", ":0", "-number", "0"}

	done := make(chan any)
	go func() {
		main()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("main returned unexpectedly")
	case <-time.After(500 * time.Millisecond):
	}
}
