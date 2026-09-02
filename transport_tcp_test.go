package raft

import (
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// newTCPPair создаёт два соединённых TCPTransport на localhost:0.
// Возвращает (client, server, cleanup). client может отправлять RPC серверу
// и наоборот. cleanup закрывает оба транспорта. maxPool для тестов = 2.
func newTCPPair(t *testing.T, timeout time.Duration) (*TCPTransport, *TCPTransport, func()) {
	t.Helper()
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

// startTCPHandler запускает горутину, которая читает из consumer транспорта
// и отвечает на AppendEntries и RequestVote. Возвращает функцию остановки.
func startTCPHandler(t *testing.T, trans *TCPTransport) func() {
	t.Helper()
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

// startTCPHandlerNoReply запускает горутину, которая читает из consumer,
// но НЕ отправляет ответ (для тестов таймаута).
func startTCPHandlerNoReply(t *testing.T, trans *TCPTransport) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case _, ok := <-trans.Consumer():
				if !ok {
					return
				}
				// Не отправляем ответ — serveConn получит таймаут
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// TestTCPNewTransport проверяет успешное создание TCPTransport.
func TestTCPNewTransport(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport failed: %v", err)
	}
	defer trans.Close()
	if trans.Consumer() == nil {
		t.Fatal("Consumer() returned nil")
	}
	if trans.LocalAddr() == "" {
		t.Fatal("LocalAddr() returned empty")
	}
}

// TestTCPNewTransportZeroTimeout проверяет защитную проверку конструктора
// (AC-2): нулевой тайм-аут заменяется дефолтом _defaultTCPRPCTimeout, чтобы
// транспорт всегда имел действующие дедлайны. Без этой проверки нулевая
// конфигурация дала бы транспорт без дедлайнов (RISK-021).
func TestTCPNewTransportZeroTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 0, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer trans.Close()
	if trans.timeout != _defaultTCPRPCTimeout {
		t.Fatalf("timeout = %v, want default %v", trans.timeout, _defaultTCPRPCTimeout)
	}
	if trans.timeout != TCPRPCTimeout {
		t.Fatalf("timeout = %v, want alias TCPRPCTimeout = %v", trans.timeout, TCPRPCTimeout)
	}
}

// TestTCPNewTransportInvalidAddr проверяет ошибку при неверном адресе.
func TestTCPNewTransportInvalidAddr(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	_, err := NewTCPTransport("invalid", 500*time.Millisecond, 2)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

// TestTCPNewTransportPortInUse проверяет ошибку при занятом порте.
func TestTCPNewTransportPortInUse(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	first, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("first transport: %v", err)
	}
	defer first.Close()

	// Второй транспорт на тот же порт
	second, err := NewTCPTransport(string(first.LocalAddr()), 500*time.Millisecond, 2)
	if err == nil {
		second.Close()
		t.Fatal("expected error for port in use")
	}
}

// TestTCPAppendEntriesSuccess проверяет успешную отправку и получение ответа.
func TestTCPAppendEntriesSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := AppendEntriesArgs{
		Term:         1,
		LeaderID:     0,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		LeaderCommit: -1,
	}
	reply, err := client.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries reply.Success = false, want true")
	}
	if reply.Term != 1 {
		t.Fatalf("AppendEntries reply.Term = %d, want 1", reply.Term)
	}
}

// TestTCPAppendEntriesUnknownPeer проверяет отправку неизвестному peer.
func TestTCPAppendEntriesUnknownPeer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", 100*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	// Не вызываем Connect для peer 1
	args := AppendEntriesArgs{Term: 1}
	_, err = client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

// TestTCPAppendEntriesAfterClose проверяет отправку после закрытия.
func TestTCPAppendEntriesAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	args := AppendEntriesArgs{Term: 1}
	_, err := client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPAppendEntriesDisconnect проверяет отправку после Disconnect.
func TestTCPAppendEntriesDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	// Сначала успешная отправка
	args := AppendEntriesArgs{Term: 1, LeaderID: 0}
	reply, err := client.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("first AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("first AppendEntries: Success = false")
	}

	// Disconnect
	client.Disconnect(1)

	// После Disconnect — отправка не работает
	_, err = client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error after Disconnect")
	}
}

// TestTCPAppendEntriesTimeout проверяет таймаут ответа.
func TestTCPAppendEntriesTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	// Сервер с коротким таймаутом — 50ms
	server, err := NewTCPTransport("127.0.0.1:0", 50*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := NewTCPTransport("127.0.0.1:0", time.Second, 2)
	if err != nil {
		server.Close()
		t.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))
	defer client.Close()
	defer server.Close()
	defer startTCPHandlerNoReply(t, server)()

	args := AppendEntriesArgs{Term: 1}
	_, err = client.AppendEntries(1, args)
	if err != contract.ErrEnqueueTimeout {
		t.Fatalf("want ErrEnqueueTimeout, got %v", err)
	}
}

// TestTCPRequestVoteSuccess проверяет успешную отправку RequestVote.
func TestTCPRequestVoteSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := RequestVoteArgs{
		Term:         2,
		CandidateID:  0,
		LastLogIndex: -1,
		LastLogTerm:  -1,
	}
	reply, err := client.RequestVote(1, args)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	if !reply.VoteGranted {
		t.Fatal("RequestVote reply.VoteGranted = false, want true")
	}
	if reply.Term != 2 {
		t.Fatalf("RequestVote reply.Term = %d, want 2", reply.Term)
	}
}

// TestTCPRequestVoteUnknownPeer проверяет отправку RequestVote неизвестному peer.
func TestTCPRequestVoteUnknownPeer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", 100*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	_, err = client.RequestVote(1, RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

// TestTCPRequestVoteAfterClose проверяет отправку RequestVote после закрытия.
func TestTCPRequestVoteAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	_, err := client.RequestVote(1, RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPConnectionReset проверяет, что после разрыва соединения
// следующий вызов AppendEntries возвращает ошибку.
func TestTCPConnectionReset(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	// Успешная отправка
	args := AppendEntriesArgs{Term: 1, LeaderID: 0}
	reply, err := client.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("first AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("first AppendEntries: Success = false")
	}

	// Закрываем сервер — разрываем соединение
	server.Close()

	// Следующая отправка должна вернуть ошибку
	_, err = client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error after connection reset")
	}
}

// TestTCPReconnectAfterDrop проверяет, что Disconnect + Connect
// позволяет установить новое соединение и успешно отправить RPC.
func TestTCPReconnectAfterDrop(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := AppendEntriesArgs{Term: 1, LeaderID: 0}

	// Первый вызов успешен
	reply, err := client.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("first AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("first AppendEntries: Success = false")
	}

	// Disconnect — удаляем соединение и адрес
	client.Disconnect(1)

	// Повторно подключаемся к тому же адресу
	client.Connect(1, string(server.LocalAddr()))

	// Второй вызов должен установить новое соединение и успешно выполниться
	reply, err = client.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("AppendEntries after reconnect failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries after reconnect: Success = false")
	}
}

// TestTCPConcurrentSends проверяет множественные параллельные отправки.
func TestTCPConcurrentSends(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, time.Second)
	defer cleanup()
	defer startTCPHandler(t, server)()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(term int) {
			defer wg.Done()
			args := AppendEntriesArgs{Term: term, LeaderID: 0}
			reply, err := client.AppendEntries(1, args)
			if err != nil {
				t.Errorf("AppendEntries failed: %v", err)
				return
			}
			if !reply.Success {
				t.Errorf("AppendEntries: Success = false")
			}
		}(i + 1)
	}
	wg.Wait()
}

// TestTCPDoubleClose проверяет, что вызов Close дважды не вызывает panic.
func TestTCPDoubleClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	trans.Close()
	trans.Close() // не должно panic
}

// TestTCPCloseWaitsForGoroutines проверяет, что после Close
// не остаётся работающих горутин.
func TestTCPCloseWaitsForGoroutines(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	trans.Close()
}

// TestTCPCloseStopsConsumer проверяет, что после Close
// AppendEntries и RequestVote возвращают ошибку.
func TestTCPCloseStopsConsumer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	// AppendEntries после Close
	_, err := client.AppendEntries(1, AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}

	// RequestVote после Close
	_, err = client.RequestVote(1, RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPGobRegistration проверяет, что RPC-типы зарегистрированы в gob.
func TestTCPGobRegistration(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	// Проверяем, что init() зарегистрировал типы — создаём транспорт,
	// отправляем RPC с разными типами, убеждаемся что gob не паникует.
	client, server, cleanup := newTCPPair(t, time.Second)
	defer cleanup()
	defer startTCPHandler(t, server)()

	t.Run("AppendEntries", func(t *testing.T) {
		args := AppendEntriesArgs{
			Term:         1,
			LeaderID:     0,
			PrevLogIndex: -1,
			PrevLogTerm:  -1,
			Entries: []LogEntry{
				{Index: 0, Term: 1, Type: LogCommand, Data: "test"},
			},
			LeaderCommit: -1,
		}
		reply, err := client.AppendEntries(1, args)
		if err != nil {
			t.Fatalf("AppendEntries with entries failed: %v", err)
		}
		if !reply.Success {
			t.Fatal("AppendEntries: Success = false")
		}
	})

	t.Run("RequestVote", func(t *testing.T) {
		args := RequestVoteArgs{
			Term:         5,
			CandidateID:  0,
			LastLogIndex: 10,
			LastLogTerm:  3,
		}
		reply, err := client.RequestVote(1, args)
		if err != nil {
			t.Fatalf("RequestVote failed: %v", err)
		}
		if !reply.VoteGranted {
			t.Fatal("RequestVote: VoteGranted = false")
		}
	})
}

// TestTCPAppendEntriesError проверяет, что если сервер отвечает с ошибкой,
// клиент получает и распознаёт её.
func TestTCPAppendEntriesError(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, 500*time.Millisecond)
	defer cleanup()

	// Обработчик, возвращающий ошибку
	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-server.Consumer():
			rpc.RespChan <- RPCResponse{Error: contract.ErrRaftShutdown}
		case <-done:
		}
	}()
	defer close(done)

	_, err := client.AppendEntries(1, AppendEntriesArgs{Term: 1})
	if err != contract.ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestTCPAppendEntriesMultiple проверяет несколько последовательных вызовов.
func TestTCPAppendEntriesMultiple(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, time.Second)
	defer cleanup()
	defer startTCPHandler(t, server)()

	for i := 0; i < 10; i++ {
		args := AppendEntriesArgs{Term: i + 1, LeaderID: 0}
		reply, err := client.AppendEntries(1, args)
		if err != nil {
			t.Fatalf("AppendEntries %d failed: %v", i, err)
		}
		if reply.Term != i+1 {
			t.Fatalf("AppendEntries %d: reply.Term = %d, want %d", i, reply.Term, i+1)
		}
	}
}

// TestTCPLocalAddr проверяет LocalAddr().
func TestTCPLocalAddr(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer trans.Close()
	addr := trans.LocalAddr()
	if addr == "" {
		t.Fatal("LocalAddr() returned empty")
	}
}

// TestTCPStubsReturnNotImplemented проверяет, что нереализованные методы возвращают
// contract.ErrNotImplemented, а реализованные (RequestPreVote, TimeoutNow) — транспортную ошибку.
func TestTCPStubsReturnNotImplemented(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", 100*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer trans.Close()

	t.Run("RequestPreVote", func(t *testing.T) {
		_, err := trans.RequestPreVote(1, RequestPreVoteArgs{})
		if err == nil || err == contract.ErrNotImplemented {
			t.Fatalf("want transport error, got %v", err)
		}
	})
	t.Run("TimeoutNow", func(t *testing.T) {
		_, err := trans.TimeoutNow(1, TimeoutNowRequest{})
		if err == nil || err == contract.ErrNotImplemented {
			t.Fatalf("want transport error, got %v", err)
		}
	})
	t.Run("InstallSnapshot", func(t *testing.T) {
		_, err := trans.InstallSnapshot(1, InstallSnapshotRequest{}, nil)
		if err == nil {
			t.Fatalf("InstallSnapshot: want error, got nil")
		}
	})
	t.Run("AppendEntriesPipeline", func(t *testing.T) {
		_, err := trans.AppendEntriesPipeline(1)
		if err != contract.ErrNotImplemented {
			t.Fatalf("want ErrNotImplemented, got %v", err)
		}
	})
}

// TestTCPDisconnectAll проверяет DisconnectAll.
func TestTCPDisconnectAll(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	// Подключаем два соседа
	server1, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport server1: %v", err)
	}
	server1.Close()

	server2, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport server2: %v", err)
	}
	server2.Close()

	client.Connect(1, string(server1.LocalAddr()))
	client.Connect(2, string(server2.LocalAddr()))

	// DisconnectAll
	client.DisconnectAll()

	// Все соседи должны быть недоступны
	_, err = client.AppendEntries(1, AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after DisconnectAll for peer 1")
	}
	_, err = client.AppendEntries(2, AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after DisconnectAll for peer 2")
	}
}

// TestTCPTransportNoGoroutineLeak проверяет, что после Close
// не остаётся goroutine.
func TestTCPTransportNoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	// Создаём транспорт с активным acceptLoop, используем
	trans, err := NewTCPTransport("127.0.0.1:0", 500*time.Millisecond, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	trans.Close()
}

// TestRPCFramingBytes фиксирует байты фрейминга TCP RPC: значения
// используются как поле Type запроса, кодируются gob и уходят
// в сеть, поэтому смена любого значения — изменение проводного
// формата.
func TestRPCFramingBytes(t *testing.T) {
	tests := []struct {
		name string
		got  byte
		want byte
	}{
		{"rpcAppendEntries", _rpcAppendEntries, 0},
		{"rpcRequestVote", _rpcRequestVote, 1},
		{"rpcInstallSnapshot", _rpcInstallSnapshot, 2},
		{"rpcTimeoutNow", _rpcTimeoutNow, 3},
		{"rpcRequestPreVote", _rpcRequestPreVote, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}
