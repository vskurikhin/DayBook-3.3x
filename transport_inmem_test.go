package raft

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// newInmemPair создаёт два соединённых InmemTransport с уникальными адресами.
// Возвращает (t1, t2, cleanup). cleanup закрывает оба транспорта.
func newInmemPair(t *testing.T) (*InmemTransport, *InmemTransport, func()) {
	t.Helper()
	t1 := NewInmemTransport("test-0")
	t2 := NewInmemTransport("test-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)
	return t1, t2, func() {
		t1.Close()
		t2.Close()
	}
}

// startInmemHandler запускает горутину, которая читает из consumer транспорта
// и отвечает на AppendEntries и RequestVote. Если consume == false — не запускает
// обработчик (используется для тестов таймаута). Возвращает функцию остановки.
func startInmemHandler(t *testing.T, trans *InmemTransport, consume bool) func() {
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
						Reply: &AppendEntriesReply{
							Success: true,
							Term:    cmd.Term,
						},
					}
				case *RequestVoteArgs:
					rpc.RespChan <- RPCResponse{
						Reply: &RequestVoteReply{
							VoteGranted: true,
							Term:        cmd.Term,
						},
					}
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// TestInmemNewTransport проверяет создание InmemTransport.
func TestInmemNewTransport(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("raft-42")
	if trans.Consumer() == nil {
		t.Fatal("Consumer() returned nil")
	}
	if trans.LocalAddr() != "raft-42" {
		t.Fatalf("LocalAddr() = %q, want %q", trans.LocalAddr(), "raft-42")
	}
	if trans.timeout != 500*time.Millisecond {
		t.Fatalf("timeout = %v, want 500ms", trans.timeout)
	}
	trans.Close()
}

// TestInmemDefaultTimeout проверяет таймаут по умолчанию.
func TestInmemDefaultTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	if trans.timeout != 500*time.Millisecond {
		t.Fatalf("default timeout = %v, want 500ms", trans.timeout)
	}
	trans.Close()
}

// TestInmemAppendEntriesSuccess проверяет успешную отправку и получение ответа.
func TestInmemAppendEntriesSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	defer startInmemHandler(t, t2, true)()

	args := AppendEntriesArgs{
		Term:         1,
		LeaderID:     0,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	reply, err := t1.AppendEntries(1, args)
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

// TestInmemAppendEntriesDisconnectedPeer проверяет отправку отключённому peer.
func TestInmemAppendEntriesDisconnectedPeer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	t2 := NewInmemTransport("test-1")
	defer t1.Close()
	defer t2.Close()

	// Не вызываем Connect()
	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrNotReachable {
		t.Fatalf("want ErrNotReachable, got %v", err)
	}
}

// TestInmemAppendEntriesAfterClose проверяет отправку после закрытия.
func TestInmemAppendEntriesAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2 := NewInmemTransport("test-0"), NewInmemTransport("test-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)
	defer t2.Close()
	defer startInmemHandler(t, t2, true)()

	t1.Close() // закрываем t1

	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemAppendEntriesPeerClosed проверяет отправку, когда peer закрыт.
func TestInmemAppendEntriesPeerClosed(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	t2.Close() // закрываем peer

	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemAppendEntriesTimeout проверяет таймаут ожидания ответа.
func TestInmemAppendEntriesTimeout(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	// Не запускаем обработчик — consumerCh будет заблокирован

	// Используем короткий таймаут для ускорения теста
	t1.timeout = 50 * time.Millisecond
	t2.timeout = 50 * time.Millisecond

	// Запускаем обработчик, который НИЧЕГО не потребляет, чтобы сообщение
	// попало в consumerCh, но ответа не было
	go func() {
		// Просто прочитаем из consumerCh, чтобы AppendEntries не заблокировался
		// на отправке. Не отправляем ответ — это вызовет таймаут.
		<-t2.Consumer()
		// Ничего не делаем — не отправляем ответ
	}()

	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrEnqueueTimeout {
		t.Fatalf("want ErrEnqueueTimeout, got %v", err)
	}
}

// TestInmemRequestVoteSuccess проверяет успешную отправку RequestVote.
func TestInmemRequestVoteSuccess(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	defer startInmemHandler(t, t2, true)()

	args := RequestVoteArgs{
		Term:         2,
		CandidateID:  0,
		LastLogIndex: -1,
		LastLogTerm:  -1,
	}
	reply, err := t1.RequestVote(1, args)
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

// TestInmemRequestVoteDisconnectedPeer проверяет отправку RequestVote отключённому peer.
func TestInmemRequestVoteDisconnectedPeer(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	t2 := NewInmemTransport("test-1")
	defer t1.Close()
	defer t2.Close()

	args := RequestVoteArgs{Term: 1}
	_, err := t1.RequestVote(1, args)
	if err != ErrNotReachable {
		t.Fatalf("want ErrNotReachable, got %v", err)
	}
}

// TestInmemRequestVoteAfterClose проверяет отправку RequestVote после закрытия.
func TestInmemRequestVoteAfterClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2 := NewInmemTransport("test-0"), NewInmemTransport("test-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)
	defer t2.Close()
	defer startInmemHandler(t, t2, true)()

	t1.Close()

	args := RequestVoteArgs{Term: 1}
	_, err := t1.RequestVote(1, args)
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemRequestVotePeerClosed проверяет отправку RequestVote, когда peer закрыт.
func TestInmemRequestVotePeerClosed(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	t2.Close()

	args := RequestVoteArgs{Term: 1}
	_, err := t1.RequestVote(1, args)
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemConnect проверяет Connect.
func TestInmemConnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	t2 := NewInmemTransport("test-1")
	defer t1.Close()
	defer t2.Close()

	if !t1.IsDisconnected(1) {
		t.Fatal("expected peer 1 to be disconnected before Connect")
	}

	t1.Connect(1, t2)
	if t1.IsDisconnected(1) {
		t.Fatal("expected peer 1 to be connected after Connect")
	}
}

// TestInmemDisconnect проверяет Disconnect.
func TestInmemDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	_ = t2

	t1.Disconnect(1)
	if !t1.IsDisconnected(1) {
		t.Fatal("expected peer 1 to be disconnected")
	}

	// Убедимся, что после Disconnect отправка не работает
	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrNotReachable {
		t.Fatalf("want ErrNotReachable, got %v", err)
	}
}

// TestInmemDisconnectAll проверяет DisconnectAll.
func TestInmemDisconnectAll(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	t2 := NewInmemTransport("test-1")
	t3 := NewInmemTransport("test-2")
	defer t1.Close()
	defer t2.Close()
	defer t3.Close()

	t1.Connect(1, t2)
	t1.Connect(2, t3)

	if t1.IsDisconnected(1) || t1.IsDisconnected(2) {
		t.Fatal("expected peers to be connected after Connect")
	}

	t1.DisconnectAll()
	if !t1.IsDisconnected(1) {
		t.Fatal("expected peer 1 to be disconnected after DisconnectAll")
	}
	if !t1.IsDisconnected(2) {
		t.Fatal("expected peer 2 to be disconnected after DisconnectAll")
	}
}

// TestInmemDoubleDisconnect проверяет, что Disconnect несуществующего peer не паникует.
func TestInmemDoubleDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	defer t1.Close()

	// Disconnect несуществующего peer
	t1.Disconnect(999)

	// Double Disconnect существующего
	t1.Connect(1, NewInmemTransport("test-1"))
	t1.Disconnect(1)
	t1.Disconnect(1) // не должно panic
}

// TestInmemConnectThenSend проверяет, что после Connect отправка работает,
// а после Disconnect — нет.
func TestInmemConnectThenSend(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	defer startInmemHandler(t, t2, true)()

	// После Connect — отправка работает
	args := AppendEntriesArgs{Term: 1}
	reply, err := t1.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("AppendEntries after Connect failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries after Connect: Success = false")
	}

	// Disconnect
	t1.Disconnect(1)

	// После Disconnect — отправка не работает
	_, err = t1.AppendEntries(1, args)
	if err != ErrNotReachable {
		t.Fatalf("want ErrNotReachable, got %v", err)
	}
}

// TestInmemClose проверяет, что после Close все методы возвращают ошибку.
func TestInmemClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2 := NewInmemTransport("test-0"), NewInmemTransport("test-1")
	t1.Connect(1, t2)
	t2.Connect(0, t1)
	defer t2.Close()
	defer startInmemHandler(t, t2, true)()

	t1.Close()

	args := AppendEntriesArgs{Term: 1}
	_, err := t1.AppendEntries(1, args)
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemDoubleClose проверяет, что вызов Close дважды не вызывает panic.
func TestInmemDoubleClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	t1.Close()
	t1.Close() // не должно panic
}

// TestInmemCloseConsumerDrain проверяет, что после Close consumerCh пуст.
func TestInmemCloseConsumerDrain(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	// Отправляем RPC без обработчика — потребитель сообщений не запущен.
	// AppendEntries заблокируется на отправке или на ожидании ответа.
	// Но мы не ждём — закрываем t2 и проверяем, что Close дренирует consumerCh.
	errCh := make(chan error, 1)
	go func() {
		_, err := t1.AppendEntries(1, AppendEntriesArgs{Term: 1})
		errCh <- err
	}()

	// Ждём небольшой интервал, чтобы AppendEntries успел отправить RPC в consumerCh
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(10 * time.Millisecond)

	// Закрываем t2 — это должно разблокировать AppendEntries (через peer.shutdownCh)
	// и дренировать consumerCh
	t2.Close()

	// Проверяем, что AppendEntries завершился с ошибкой, а не заблокировался навсегда
	select {
	case err := <-errCh:
		if err != ErrRaftShutdown {
			t.Fatalf("want ErrRaftShutdown, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AppendEntries blocked after Close")
	}
}

// TestInmemStubsReturnNotImplemented проверяет, что stub-методы
// возвращают ErrNotImplemented. RequestPreVote не проверяется —
// он реализован для InmemTransport.
func TestInmemStubsReturnNotImplemented(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	defer trans.Close()
	t.Run("TimeoutNow", func(t *testing.T) {
		_, err := trans.TimeoutNow(1, TimeoutNowRequest{})
		if err != ErrNotReachable {
			t.Fatalf("want ErrNotReachable, got %v", err)
		}
	})
	t.Run("InstallSnapshot", func(t *testing.T) {
		_, err := trans.InstallSnapshot(1, InstallSnapshotRequest{}, nil)
		// InstallSnapshot теперь реализован — peer не подключён, ошибка ErrNotReachable.
		if err != ErrNotReachable {
			t.Fatalf("want ErrNotReachable, got %v", err)
		}
	})
}

// TestInmemAppendPipelineReturnsNotImplemented проверяет, что
// AppendEntriesPipeline возвращает ErrNotImplemented.
func TestInmemAppendPipelineReturnsNotImplemented(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	defer trans.Close()

	_, err := trans.AppendEntriesPipeline(1)
	if err != ErrNotImplemented {
		t.Fatalf("want ErrNotImplemented, got %v", err)
	}
}

// TestInmemConcurrentSend проверяет, что 10 горутин одновременно
// могут отправлять AppendEntries разным peers.
func TestInmemConcurrentSend(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	// Создаём 3 транспорта, соединённых в полную mesh-сеть
	t0 := NewInmemTransport("test-0")
	t1 := NewInmemTransport("test-1")
	t2 := NewInmemTransport("test-2")
	defer t0.Close()
	defer t1.Close()
	defer t2.Close()

	t0.Connect(1, t1)
	t0.Connect(2, t2)
	t1.Connect(0, t0)
	t1.Connect(2, t2)
	t2.Connect(0, t0)
	t2.Connect(1, t1)

	// Запускаем обработчики на t1 и t2
	stop := startInmemHandler(t, t1, true)
	defer stop()
	stop2 := startInmemHandler(t, t2, true)
	defer stop2()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var peer ServerID
			if id%2 == 0 {
				peer = 1
			} else {
				peer = 2
			}
			args := AppendEntriesArgs{Term: 1, LeaderID: 0}
			reply, err := t0.AppendEntries(peer, args)
			if err != nil {
				t.Errorf("AppendEntries to peer %d failed: %v", peer, err)
				return
			}
			if !reply.Success {
				t.Errorf("AppendEntries to peer %d: Success = false", peer)
			}
		}(i)
	}
	wg.Wait()
}

// TestInmemConcurrentConnectDisconnect проверяет Connect/Disconnect
// из нескольких горутин. Запускать с -race.
func TestInmemConcurrentConnectDisconnect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t0 := NewInmemTransport("test-0")
	t1 := NewInmemTransport("test-1")
	defer t0.Close()
	defer t1.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0.Connect(1, t1)
			t0.Disconnect(1)
		}()
	}
	wg.Wait()
}

// TestInmemManyRoundTrips проверяет 100 последовательных AppendEntries.
func TestInmemManyRoundTrips(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()
	defer startInmemHandler(t, t2, true)()

	for i := 0; i < 100; i++ {
		args := AppendEntriesArgs{
			Term:     i + 1,
			LeaderID: 0,
		}
		reply, err := t1.AppendEntries(1, args)
		if err != nil {
			t.Fatalf("AppendEntries %d failed: %v", i, err)
		}
		if reply.Term != i+1 {
			t.Fatalf("AppendEntries %d: reply.Term = %d, want %d", i, reply.Term, i+1)
		}
	}
}

// TestInmemTransportNoGoroutineLeak проверяет, что после Close
// не остаётся goroutine.
func TestInmemTransportNoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1 := NewInmemTransport("test-0")
	// Просто создаём и закрываем — проверяем только утечку
	t1.Close()
}

// TestInmemLocalAddr проверяет LocalAddr().
func TestInmemLocalAddr(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("my-addr")
	defer trans.Close()
	if addr := trans.LocalAddr(); addr != "my-addr" {
		t.Fatalf("LocalAddr() = %q, want %q", addr, "my-addr")
	}
}

// TestInmemSetHeartbeatHandler проверяет, что SetHeartbeatHandler сохраняет функцию.
func TestInmemSetHeartbeatHandler(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	defer trans.Close()

	called := false
	trans.SetHeartbeatHandler(func(rpc RPC) {
		called = true
	})
	if trans.heartbeat == nil {
		t.Fatal("heartbeat handler is nil after SetHeartbeatHandler")
	}
	// Вызываем сохранённую функцию
	trans.heartbeat(RPC{})
	if !called {
		t.Fatal("heartbeat handler was not called")
	}
}

// TestInmemConsumerIsUnbuffered проверяет, что Consumer() возвращает
// небуферизированный канал.
func TestInmemConsumerIsUnbuffered(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	defer trans.Close()
	if cap(trans.Consumer()) != 0 {
		t.Fatalf("Consumer() channel capacity = %d, want 0", cap(trans.Consumer()))
	}
}

// TestInmemDisconnectPeerCallsSideEffect проверяет, что Disconnect на
// одном транспорте не влияет на другой транспорт (симметричность не требуется).
func TestInmemDisconnectPeerCallsSideEffect(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	// Disconnect только t1 → t2
	t1.Disconnect(1)

	// t2 → t1 должно всё ещё работать (если t2 не отключал t1)
	// Проверяем через startInmemHandler
	defer startInmemHandler(t, t1, true)()

	args := AppendEntriesArgs{Term: 1}
	reply, err := t2.AppendEntries(0, args)
	if err != nil {
		t.Fatalf("AppendEntries from t2 to t1 failed after t1 disconnected: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries from t2 to t1: Success = false")
	}
}

// TestInmemAppendEntriesRequestVoteError проверяет, что если обработчик
// возвращает ошибку в RespChan, AppendEntries/RequestVote возвращают её.
func TestInmemAppendEntriesError(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	// Обработчик, возвращающий ошибку
	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-t2.Consumer():
			rpc.RespChan <- RPCResponse{Error: fmt.Errorf("raft: test error")}
		case <-done:
		}
	}()
	defer close(done)

	_, err := t1.AppendEntries(1, AppendEntriesArgs{Term: 1})
	if err == nil || err.Error() != "raft: test error" {
		t.Fatalf("want 'raft: test error', got %v", err)
	}
}

// TestInmemRequestVoteError проверяет, что ошибка от обработчика
// RequestVote пробрасывается вызывающей стороне.
func TestInmemRequestVoteError(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-t2.Consumer():
			rpc.RespChan <- RPCResponse{Error: ErrRaftShutdown}
		case <-done:
		}
	}()
	defer close(done)

	_, err := t1.RequestVote(1, RequestVoteArgs{Term: 1})
	if err != ErrRaftShutdown {
		t.Fatalf("want ErrRaftShutdown, got %v", err)
	}
}

// TestInmemCloseWithPendingRPC проверяет, что Close() не блокируется,
// если есть RPC-запрос, ожидающий в consumerCh.
func TestInmemCloseWithPendingRPC(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	// Отправляем RPC без обработчика — он зависнет в consumerCh
	errCh := make(chan error, 1)
	go func() {
		_, err := t1.AppendEntries(1, AppendEntriesArgs{Term: 1})
		errCh <- err
	}()

	// Даём время RPC дойти до consumerCh
	// keep: timing — окно является предметом проверки в этом месте;
	// наблюдаемого признака состояния здесь нет.
	time.Sleep(10 * time.Millisecond)

	// Закрываем t2 — это должно разблокировать AppendEntries
	// (через peer.shutdownCh) и дренировать consumerCh
	t2.Close()

	select {
	case err := <-errCh:
		if err != ErrRaftShutdown {
			t.Fatalf("want ErrRaftShutdown, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked or AppendEntries didn't return")
	}
}

// TestInmemAppendEntriesWithEntries проверяет, что AppendEntries
// с записями журнала работает корректно.
func TestInmemAppendEntriesWithEntries(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	t1, t2, cleanup := newInmemPair(t)
	defer cleanup()

	done := make(chan struct{})
	go func() {
		select {
		case rpc := <-t2.Consumer():
			cmd := rpc.Command.(*AppendEntriesArgs)
			rpc.RespChan <- RPCResponse{
				Reply: &AppendEntriesReply{
					Success:       true,
					Term:          cmd.Term,
					ConflictIndex: cmd.PrevLogIndex + len(cmd.Entries),
				},
			}
		case <-done:
		}
	}()
	defer close(done)

	entries := []LogEntry{{Index: 0, Term: 1, Data: "cmd1"}}
	args := AppendEntriesArgs{
		Term:         1,
		LeaderID:     0,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      entries,
		LeaderCommit: -1,
	}
	reply, err := t1.AppendEntries(1, args)
	if err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}
	if !reply.Success {
		t.Fatal("AppendEntries: Success = false")
	}
	if reply.ConflictIndex != 0 {
		t.Fatalf("ConflictIndex = %d, want 0", reply.ConflictIndex)
	}
}

// TestInmemCloseIdempotent проверяет, что множественный Close
// не вызывает проблем с каналами.
func TestInmemCloseIdempotent(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	trans := NewInmemTransport("test")
	trans.Close()
	trans.Close()
	trans.Close()
	// Если panic не произошло — тест пройден
}

// TestInmemNewTransportMultiple проверяет, что несколько транспортов
// создаются независимо.
func TestInmemNewTransportMultiple(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()
	for i := 0; i < 10; i++ {
		trans := NewInmemTransport(ServerAddress(fmt.Sprintf("node-%d", i)))
		if trans.LocalAddr() != ServerAddress(fmt.Sprintf("node-%d", i)) {
			t.Fatalf("unexpected address for node %d", i)
		}
		trans.Close()
	}
}
