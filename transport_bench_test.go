package raft

import (
	"testing"
	"time"
)

// setupInmemBenchmark создаёт пару InmemTransport с обработчиком для бенчмарков.
func setupInmemBenchmark(b *testing.B) (*InmemTransport, func()) {
	b.Helper()
	t1 := NewInmemTransport("bench-0")
	t2 := NewInmemTransport("bench-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)

	done := make(chan struct{})
	go func() {
		for {
			select {
			case rpc := <-t2.Consumer():
				switch cmd := rpc.Command.(type) {
				case *AppendEntriesArgs:
					rpc.RespChan <- RPCResponse{
						Reply: &AppendEntriesReply{Success: true, Term: cmd.Term},
					}
				case *RequestVoteArgs:
					rpc.RespChan <- RPCResponse{
						Reply: &RequestVoteReply{VoteGranted: true, Term: cmd.Term},
					}
				}
			case <-done:
				return
			}
		}
	}()

	return t1, func() {
		close(done)
		t1.Close()
		t2.Close()
	}
}

// setupTCPBenchmark создаёт пару TCPTransport с обработчиком для бенчмарков.
func setupTCPBenchmark(b *testing.B) (*TCPTransport, func()) {
	b.Helper()
	timeout := time.Second
	server, err := NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		b.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		server.Close()
		b.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))

	done := make(chan struct{})
	go func() {
		for {
			select {
			case rpc := <-server.Consumer():
				switch cmd := rpc.Command.(type) {
				case *AppendEntriesArgs:
					rpc.RespChan <- RPCResponse{
						Reply: &AppendEntriesReply{Success: true, Term: cmd.Term},
					}
				case *RequestVoteArgs:
					rpc.RespChan <- RPCResponse{
						Reply: &RequestVoteReply{VoteGranted: true, Term: cmd.Term},
					}
				}
			case <-done:
				return
			}
		}
	}()

	return client, func() {
		close(done)
		client.Close()
		server.Close()
	}
}

func BenchmarkInmemAppendEntries(b *testing.B) {
	trans, cleanup := setupInmemBenchmark(b)
	defer cleanup()

	args := AppendEntriesArgs{Term: 1, LeaderID: 0, PrevLogIndex: -1, PrevLogTerm: -1, LeaderCommit: -1}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := trans.AppendEntries(1, args)
		if err != nil {
			b.Fatalf("AppendEntries failed: %v", err)
		}
	}
}

func BenchmarkInmemAppendEntriesParallel(b *testing.B) {
	trans, cleanup := setupInmemBenchmark(b)
	defer cleanup()

	args := AppendEntriesArgs{Term: 1, LeaderID: 0, PrevLogIndex: -1, PrevLogTerm: -1, LeaderCommit: -1}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := trans.AppendEntries(1, args)
			if err != nil {
				b.Fatalf("AppendEntries failed: %v", err)
			}
		}
	})
}

func BenchmarkTCPAppendEntries(b *testing.B) {
	trans, cleanup := setupTCPBenchmark(b)
	defer cleanup()

	args := AppendEntriesArgs{Term: 1, LeaderID: 0, PrevLogIndex: -1, PrevLogTerm: -1, LeaderCommit: -1}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := trans.AppendEntries(1, args)
		if err != nil {
			b.Fatalf("AppendEntries failed: %v", err)
		}
	}
}

func BenchmarkTCPAppendEntriesParallel(b *testing.B) {
	trans, cleanup := setupTCPBenchmark(b)
	defer cleanup()

	args := AppendEntriesArgs{Term: 1, LeaderID: 0, PrevLogIndex: -1, PrevLogTerm: -1, LeaderCommit: -1}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := trans.AppendEntries(1, args)
			if err != nil {
				b.Fatalf("AppendEntries failed: %v", err)
			}
		}
	})
}
