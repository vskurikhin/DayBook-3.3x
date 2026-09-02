package raft

import (
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// transportPairFactory создаёт пару соединённых транспортов (client, server, cleanup)
// для параметризованных contract-тестов.
type transportPairFactory func(t *testing.T) (Transport, Transport, func())

// newInmemTransportPair — фабрика для InmemTransport.
func newInmemTransportPair(t *testing.T) (Transport, Transport, func()) {
	t.Helper()
	t1, t2, cleanup := newInmemPair(t)
	return t1, t2, cleanup
}

// newTCPTransportPair — фабрика для TCPTransport.
func newTCPTransportPair(t *testing.T) (Transport, Transport, func()) {
	t.Helper()
	timeout := time.Second
	server, err := NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := NewTCPTransport("127.0.0.1:0", timeout, 2)
	if err != nil {
		server.Close()
		t.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))
	return client, server, func() {
		client.Close()
		server.Close()
	}
}

// runTransportHandler запускает обработчик RPC на переданном транспорте.
// Возвращает функцию остановки.
func runTransportHandler(t *testing.T, trans Transport, consume bool) func() {
	t.Helper()
	if !consume {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case rpc, ok := <-trans.Consumer():
				if !ok {
					return
				}
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
	return func() { close(done) }
}

func TestTransportConsumerIsUnbuffered(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			if cap(client.Consumer()) != 0 {
				t.Errorf("client Consumer() cap = %d, want 0", cap(client.Consumer()))
			}
			if cap(server.Consumer()) != 0 {
				t.Errorf("server Consumer() cap = %d, want 0", cap(server.Consumer()))
			}
		})
	}
}

func TestTransportAppendEntriesIdempotent(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			args := AppendEntriesArgs{Term: 1, LeaderID: 0, PrevLogIndex: -1, PrevLogTerm: -1, LeaderCommit: -1}

			for i := 0; i < 5; i++ {
				reply, err := client.AppendEntries(1, args)
				if err != nil {
					t.Fatalf("iteration %d: AppendEntries failed: %v", i, err)
				}
				if !reply.Success {
					t.Fatalf("iteration %d: reply.Success = false", i)
				}
			}
		})
	}
}

func TestTransportRequestVoteIdempotent(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			args := RequestVoteArgs{Term: 2, CandidateID: 0, LastLogIndex: -1, LastLogTerm: -1}

			for i := 0; i < 5; i++ {
				reply, err := client.RequestVote(1, args)
				if err != nil {
					t.Fatalf("iteration %d: RequestVote failed: %v", i, err)
				}
				if !reply.VoteGranted {
					t.Fatalf("iteration %d: reply.VoteGranted = false", i)
				}
			}
		})
	}
}

func TestTransportDisconnectReturnsError(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			args := AppendEntriesArgs{Term: 1, LeaderID: 0}

			// Успешная отправка
			_, err := client.AppendEntries(1, args)
			if err != nil {
				t.Fatalf("first AppendEntries failed: %v", err)
			}

			// Disconnect
			if d, ok := client.(interface{ Disconnect(ServerID) }); ok {
				d.Disconnect(1)
			}

			// После Disconnect — ошибка
			_, err = client.AppendEntries(1, args)
			if err == nil {
				t.Fatal("expected error after Disconnect")
			}
		})
	}
}

func TestTransportCloseReturnsError(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			if c, ok := client.(interface{ Close() }); ok {
				c.Close()
			}

			args := AppendEntriesArgs{Term: 1}
			_, err := client.AppendEntries(1, args)
			if err == nil {
				t.Fatal("expected error after Close")
			}

			_, err = client.RequestVote(1, RequestVoteArgs{Term: 1})
			if err == nil {
				t.Fatal("expected error after Close")
			}
		})
	}
}

func TestTransportStubsReturnNotImplemented(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", func(t *testing.T) (Transport, Transport, func()) {
			t1 := NewInmemTransport("test-0")
			t2 := NewInmemTransport("test-1")
			return t1, t2, func() { t1.Close(); t2.Close() }
		}},
		{"TCPTransport", func(t *testing.T) (Transport, Transport, func()) {
			t1, err := NewTCPTransport("127.0.0.1:0", 100*time.Millisecond, 2)
			if err != nil {
				t.Fatalf("NewTCPTransport: %v", err)
			}
			t2, err := NewTCPTransport("127.0.0.1:0", 100*time.Millisecond, 2)
			if err != nil {
				t1.Close()
				t.Fatalf("NewTCPTransport: %v", err)
			}
			return t1, t2, func() { t1.Close(); t2.Close() }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _, cleanup := tt.fn(t)
			defer cleanup()

			if _, err := client.RequestPreVote(1, RequestPreVoteArgs{}); err == nil || err == contract.ErrNotImplemented {
				t.Fatalf("RequestPreVote: want transport error, got %v", err)
			}
			if _, err := client.TimeoutNow(1, TimeoutNowRequest{}); err == nil || err == contract.ErrNotImplemented {
				t.Fatalf("TimeoutNow: want transport error, got %v", err)
			}
			// InmemTransport реализует InstallSnapshot и возвращает contract.ErrNotReachable
			// (или другую транспортную ошибку), а TCPTransport — contract.ErrNotImplemented.
			if _, err := client.InstallSnapshot(1, InstallSnapshotRequest{}, nil); err == nil {
				t.Fatalf("InstallSnapshot: want error, got nil")
			}
			if _, err := client.AppendEntriesPipeline(1); err != contract.ErrNotImplemented {
				t.Fatalf("AppendEntriesPipeline: want ErrNotImplemented, got %v", err)
			}
		})
	}
}

func TestTransportAppendEntriesWithEntries(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   transportPairFactory
	}{
		{"InmemTransport", newInmemTransportPair},
		{"TCPTransport", newTCPTransportPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, cleanup := tt.fn(t)
			defer cleanup()
			defer runTransportHandler(t, server, true)()

			args := AppendEntriesArgs{
				Term:         1,
				LeaderID:     0,
				PrevLogIndex: -1,
				PrevLogTerm:  -1,
				Entries: []LogEntry{
					{Index: 0, Term: 1, Type: LogCommand, Data: []byte("test")},
				},
				LeaderCommit: -1,
			}

			reply, err := client.AppendEntries(1, args)
			if err != nil {
				t.Fatalf("AppendEntries with entries failed: %v", err)
			}
			if !reply.Success {
				t.Fatal("reply.Success = false")
			}
		})
	}
}

func TestTransportLocalAddr(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	tests := []struct {
		name string
		fn   func(t *testing.T) Transport
	}{
		{"InmemTransport", func(t *testing.T) Transport {
			return NewInmemTransport("test-addr")
		}},
		{"TCPTransport", func(t *testing.T) Transport {
			trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
			if err != nil {
				t.Fatalf("NewTCPTransport: %v", err)
			}
			return trans
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans := tt.fn(t)
			defer func() {
				if c, ok := trans.(interface{ Close() }); ok {
					c.Close()
				}
			}()
			if trans.LocalAddr() == "" {
				t.Fatal("LocalAddr() returned empty")
			}
		})
	}
}
