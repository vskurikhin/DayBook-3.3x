package transp

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// uniformTCPTimeouts возвращает TCPTimeouts, у которого все четыре поля
// равны d. Используется в тестах, где прежний единый тайм-аут (установка
// соединения, дедлайн RPC, снимок, ответ) заменяется равномерным
// заполнением структуры — семантически эквивалентно прежнему значению.
func uniformTCPTimeouts(d time.Duration) TCPTimeouts {
	return TCPTimeouts{
		ConnectionTimeout:      d,
		GenericRPCTimeout:      d,
		InstallSnapshotTimeout: d,
		ResponseTimeout:        d,
	}
}

// newTCPPair создаёт два соединённых TCPTransport на localhost:0.
// Возвращает (client, server, cleanup). client может отправлять RPC серверу
// и наоборот. cleanup закрывает оба транспорта. maxPool для тестов = 2.
func newTCPPair(t *testing.T, timeouts TCPTimeouts) (*TCPTransport, *TCPTransport, func()) {
	t.Helper()
	server, err := NewTCPTransport("127.0.0.1:0", timeouts, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := NewTCPTransport("127.0.0.1:0", timeouts, 2)
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
				case *contract.AppendEntriesArgs:
					rpc.RespChan <- contract.RPCResponse{
						Reply: &contract.AppendEntriesReply{Success: true, Term: cmd.Term},
					}
				case *contract.RequestVoteArgs:
					rpc.RespChan <- contract.RPCResponse{
						Reply: &contract.RequestVoteReply{VoteGranted: true, Term: cmd.Term},
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
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
	if trans.connectionTimeout != 500*time.Millisecond {
		t.Fatalf("timeout = %v, want 500ms", trans.connectionTimeout)
	}
}

// TestTCPNewTransportMaxPool проверяет, что явное значение maxPool
// сохраняется, а ноль заменяется дефолтом транспорта _defaultMaxPool.
func TestTCPNewTransportMaxPool(t *testing.T) {
	tests := []struct {
		name    string
		maxPool int
		want    int
	}{
		{name: "explicit", maxPool: 7, want: 7},
		{name: "zero uses transport default", maxPool: 0, want: _defaultMaxPool},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
			trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), test.maxPool)
			if err != nil {
				t.Fatalf("NewTCPTransport: %v", err)
			}
			defer trans.Close()
			if trans.maxPool != test.want {
				t.Fatalf("transport.maxPool = %d, want %d", trans.maxPool, test.want)
			}
		})
	}
}

// TestTCPNewTransportZeroTimeout проверяет контракт нулевой структуры
// TCPTimeouts{} (подтверждён владельцем): каждое нулевое
// поле заменяется константой, поэтому нулевая структура даёт все дефолты
// 165/200/310/200 мс (установка соединения, обычный RPC, снимок, ответ).
// Без этой защитной проверки нулевая конфигурация дала бы транспорт
// без действующих дедлайнов.
func TestTCPNewTransportZeroTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", TCPTimeouts{}, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer trans.Close()
	if trans.connectionTimeout != contract.ConnectionTCPRPCTimeout {
		t.Fatalf("connectionTimeout = %v, want default %v", trans.connectionTimeout, contract.ConnectionTCPRPCTimeout)
	}
	if trans.genericRPCTimeout != contract.TCPRPCTimeout {
		t.Fatalf("genericRPCTimeout = %v, want default %v", trans.genericRPCTimeout, contract.TCPRPCTimeout)
	}
	if trans.installSnapshotTimeout != contract.InstallSnapshotTimeout {
		t.Fatalf("installSnapshotTimeout = %v, want default %v", trans.installSnapshotTimeout, contract.InstallSnapshotTimeout)
	}
	if trans.responseTimeout != contract.TCPRPCTimeout {
		t.Fatalf("responseTimeout = %v, want default %v", trans.responseTimeout, contract.TCPRPCTimeout)
	}
}

// TestTCPNewTransportInvalidAddr проверяет ошибку при неверном адресе.
func TestTCPNewTransportInvalidAddr(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	_, err := NewTCPTransport("invalid", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

// TestTCPNewTransportPortInUse проверяет ошибку при занятом порте.
func TestTCPNewTransportPortInUse(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	first, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("first transport: %v", err)
	}
	defer first.Close()

	// Второй транспорт на тот же порт
	second, err := NewTCPTransport(string(first.LocalAddr()), uniformTCPTimeouts(500*time.Millisecond), 2)
	if err == nil {
		second.Close()
		t.Fatal("expected error for port in use")
	}
}

// TestTCPAppendEntriesSuccess проверяет успешную отправку и получение ответа.
func TestTCPAppendEntriesSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := contract.AppendEntriesArgs{
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(100*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	// Не вызываем Connect для peer 1
	args := contract.AppendEntriesArgs{Term: 1}
	_, err = client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

// TestTCPAppendEntriesAfterClose проверяет отправку после закрытия.
func TestTCPAppendEntriesAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	args := contract.AppendEntriesArgs{Term: 1}
	_, err := client.AppendEntries(1, args)
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPAppendEntriesDisconnect проверяет отправку после Disconnect.
func TestTCPAppendEntriesDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	// Сначала успешная отправка
	args := contract.AppendEntriesArgs{Term: 1, LeaderID: 0}
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
// «Коротким» делается ResponseTimeout СЕРВЕРА: ошибку ErrEnqueueTimeout
// формирует сервер в handleCommand по time.After(respTimeout), ожидая
// ответа потребителя; строка ошибки передаётся по проводу и
// восстанавливается клиентом в маркерную ошибку ErrEnqueueTimeout
// (decodeResponse). Остальные поля сервера и все поля клиента —
// равномерно по 1 с, чтобы сработал именно серверный дедлайн ответа.
func TestTCPAppendEntriesTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	server, err := NewTCPTransport("127.0.0.1:0", TCPTimeouts{
		ConnectionTimeout:      time.Second,
		GenericRPCTimeout:      time.Second,
		InstallSnapshotTimeout: time.Second,
		ResponseTimeout:        50 * time.Millisecond,
	}, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	client, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(time.Second), 2)
	if err != nil {
		server.Close()
		t.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))
	defer client.Close()
	defer server.Close()
	defer startTCPHandlerNoReply(t, server)()

	args := contract.AppendEntriesArgs{Term: 1}
	_, err = client.AppendEntries(1, args)
	if err != contract.ErrEnqueueTimeout {
		t.Fatalf("want ErrEnqueueTimeout, got %v", err)
	}
}

// TestTCPInstallSnapshotSlowConsumer проверяет устранение гонки bufio.Reader
// при истечении окна ожидания ответа для RPC со снимком.
//
// Механика: сервер отдаёт потребителю rpc.Reader = LimitReader(r, DataSize)
// поверх общего bufio.Reader соединения и ждёт ответа в течение окна
// max(responseTimeout, installSnapshotTimeout) × ⌊DataSize/256 КиБ⌋
// (на умолчаниях TCPTimeouts{} — max(200, 310) × 1 = 310 мс). Медленный
// потребитель (чтение по 16 КиБ с паузой 20 мс: 300 КиБ / 16 КиБ × 20 мс
// ≈ 380 мс) не успевает дочитать данные снимка за окно; сервер кодирует
// маркер ErrEnqueueTimeout, выполняет Flush и закрывает соединение —
// продолжение декодирования из того же bufio.Reader исключено, гонки нет.
//
// Клиенту задаётся uniformTCPTimeouts(time.Second): дедлайн клиента (1 с)
// взводится позже серверного окна (310 мс), поэтому истекает именно окно
// СЕРВЕРА, а не собственный i/o timeout клиента. Проверка elapsed ≥ 300 мс
// доказывает, что путь истечения серверного окна пройден.
//
// Ошибка потребителя не проверяется: потребитель может успеть дочитать
// буфер до закрытия соединения (недетерминизм), поэтому требование ошибки
// чтения было бы флейком.
func TestTCPInstallSnapshotSlowConsumer(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	// Сервер — умолчания вехи (165/200/310/200): окно снимка 310 мс.
	server, err := NewTCPTransport("127.0.0.1:0", TCPTimeouts{}, 2)
	if err != nil {
		t.Fatalf("NewTCPTransport(server): %v", err)
	}
	// Клиент — равномерно 1 с: дедлайн клиента позже серверного окна.
	client, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(time.Second), 2)
	if err != nil {
		server.Close()
		t.Fatalf("NewTCPTransport(client): %v", err)
	}
	client.Connect(1, string(server.LocalAddr()))
	server.Connect(0, string(client.LocalAddr()))
	defer client.Close()
	defer server.Close()

	// Медленный потребитель: читает rpc.Reader порциями 16 КиБ с паузой
	// 20 мс — не успевает за окно 310 мс. Ответ не отправляет: серверное
	// окно истекает само, после закрытия соединения чтение завершается
	// ошибкой.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		rpc, ok := <-server.Consumer()
		if !ok {
			return
		}
		buf := make([]byte, 16*1024)
		for {
			if _, err := rpc.Reader.Read(buf); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	// Защитная проверка: если тест завершился раньше потребителя (например,
	// из-за ошибки ожидания), дожидаемся его завершения после закрытия
	// транспортов — соединение уже закрыто, чтение разблокировано.
	t.Cleanup(func() {
		select {
		case <-consumerDone:
		case <-time.After(3 * time.Second):
			t.Error("медленный потребитель не завершился после закрытия соединения")
		}
	})

	const dataSize = 300 * 1024
	args := contract.InstallSnapshotRequest{
		Term:         1,
		LeaderID:     0,
		LastLogIndex: 100,
		LastLogTerm:  1,
		DataSize:     dataSize,
	}
	data := make([]byte, dataSize)
	start := time.Now()
	_, err = client.InstallSnapshot(1, args, bytes.NewReader(data))
	elapsed := time.Since(start)

	if err != contract.ErrEnqueueTimeout {
		t.Fatalf("want ErrEnqueueTimeout, got %v", err)
	}
	// Путь истечения серверного окна: elapsed ≥ 300 мс (окно 310 мс
	// с допуском на планирование).
	if elapsed < 300*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 300ms (server snapshot window)", elapsed)
	}
	<-consumerDone
}

// TestTCPRequestVoteSuccess проверяет успешную отправку RequestVote.
func TestTCPRequestVoteSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := contract.RequestVoteArgs{
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(100*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	_, err = client.RequestVote(1, contract.RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

// TestTCPRequestVoteAfterClose проверяет отправку RequestVote после закрытия.
func TestTCPRequestVoteAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	_, err := client.RequestVote(1, contract.RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPConnectionReset проверяет, что после разрыва соединения
// следующий вызов AppendEntries возвращает ошибку.
func TestTCPConnectionReset(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	// Успешная отправка
	args := contract.AppendEntriesArgs{Term: 1, LeaderID: 0}
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	args := contract.AppendEntriesArgs{Term: 1, LeaderID: 0}

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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(time.Second))
	defer cleanup()
	defer startTCPHandler(t, server)()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(term int) {
			defer wg.Done()
			args := contract.AppendEntriesArgs{Term: term, LeaderID: 0}
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	trans.Close()
	trans.Close() // не должно panic
}

// TestTCPCloseWaitsForGoroutines проверяет, что после Close
// не остаётся работающих горутин.
func TestTCPCloseWaitsForGoroutines(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	trans.Close()
}

// TestTCPCloseStopsConsumer проверяет, что после Close
// AppendEntries и RequestVote возвращают ошибку.
func TestTCPCloseStopsConsumer(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()
	defer startTCPHandler(t, server)()

	client.Close()

	// AppendEntries после Close
	_, err := client.AppendEntries(1, contract.AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}

	// RequestVote после Close
	_, err = client.RequestVote(1, contract.RequestVoteArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

// TestTCPGobRegistration проверяет, что RPC-типы зарегистрированы в gob.
func TestTCPGobRegistration(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	// Проверяем, что init() зарегистрировал типы — создаём транспорт,
	// отправляем RPC с разными типами, убеждаемся что gob не паникует.
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(time.Second))
	defer cleanup()
	defer startTCPHandler(t, server)()

	t.Run("AppendEntries", func(t *testing.T) {
		args := contract.AppendEntriesArgs{
			Term:         1,
			LeaderID:     0,
			PrevLogIndex: -1,
			PrevLogTerm:  -1,
			Entries: []raft.LogEntry{
				{Index: 0, Term: 1, Type: raft.LogCommand, Data: "test"},
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
		args := contract.RequestVoteArgs{
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(500*time.Millisecond))
	defer cleanup()

	// Обработчик, возвращающий ошибку
	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-server.Consumer():
			rpc.RespChan <- contract.RPCResponse{Error: contract.ErrRaftShutdown}
		case <-done:
		}
	}()
	defer close(done)

	_, err := client.AppendEntries(1, contract.AppendEntriesArgs{Term: 1})
	if err != contract.ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestTCPAppendEntriesMultiple проверяет несколько последовательных вызовов.
func TestTCPAppendEntriesMultiple(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, server, cleanup := newTCPPair(t, uniformTCPTimeouts(time.Second))
	defer cleanup()
	defer startTCPHandler(t, server)()

	for i := 0; i < 10; i++ {
		args := contract.AppendEntriesArgs{Term: i + 1, LeaderID: 0}
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(100*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer trans.Close()

	t.Run("RequestPreVote", func(t *testing.T) {
		_, err := trans.RequestPreVote(1, contract.RequestPreVoteArgs{})
		if err == nil || err == contract.ErrNotImplemented {
			t.Fatalf("want transport error, got %v", err)
		}
	})
	t.Run("TimeoutNow", func(t *testing.T) {
		_, err := trans.TimeoutNow(1, contract.TimeoutNowRequest{})
		if err == nil || err == contract.ErrNotImplemented {
			t.Fatalf("want transport error, got %v", err)
		}
	})
	t.Run("InstallSnapshot", func(t *testing.T) {
		_, err := trans.InstallSnapshot(1, contract.InstallSnapshotRequest{}, nil)
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
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	client, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	defer client.Close()

	// Подключаем два соседа
	server1, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport server1: %v", err)
	}
	server1.Close()

	server2, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("NewTCPTransport server2: %v", err)
	}
	server2.Close()

	client.Connect(1, string(server1.LocalAddr()))
	client.Connect(2, string(server2.LocalAddr()))

	// DisconnectAll
	client.DisconnectAll()

	// Все соседи должны быть недоступны
	_, err = client.AppendEntries(1, contract.AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after DisconnectAll for peer 1")
	}
	_, err = client.AppendEntries(2, contract.AppendEntriesArgs{Term: 1})
	if err == nil {
		t.Fatal("expected error after DisconnectAll for peer 2")
	}
}

// TestTCPTransportNoGoroutineLeak проверяет, что после Close
// не остаётся goroutine.
func TestTCPTransportNoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()
	// Создаём транспорт с активным acceptLoop, используем
	trans, err := NewTCPTransport("127.0.0.1:0", uniformTCPTimeouts(500*time.Millisecond), 2)
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
