package raft

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// InmemTransport — реализация Transport, работающая в оперативной памяти
// (без TCP, без net/rpc, без gob-сериализации). Предназначена для
// использования в тестах (Harness). Каждый вызов AppendEntries,
// RequestVote и т.д. отправляет RPC через канал целевому узлу.
//
// Потокобезопасность: Connect/Disconnect/IsDisconnected защищены sync.Mutex.
// Consumer() и LocalAddr() не требуют блокировки.
// После Close() все методы возвращают ErrRaftShutdown.
type InmemTransport struct {
	consumerCh chan RPC
	localAddr  ServerAddress
	mu         sync.Mutex
	peers      map[ServerID]*InmemTransport
	heartbeat  func(RPC)
	shutdownCh chan struct{}
	timeout    time.Duration
}

var _ Transport = (*InmemTransport)(nil)

// NewInmemTransport создаёт новый InmemTransport с заданным локальным адресом.
// Тайм-аут по умолчанию — 500ms.
func NewInmemTransport(addr ServerAddress) *InmemTransport {
	return &InmemTransport{
		consumerCh: make(chan RPC),
		localAddr:  addr,
		peers:      make(map[ServerID]*InmemTransport),
		shutdownCh: make(chan struct{}),
		timeout:    500 * time.Millisecond,
	}
}

// Consumer возвращает небуферизированный канал входящих RPC.
func (t *InmemTransport) Consumer() <-chan RPC {
	return t.consumerCh
}

// LocalAddr возвращает локальный адрес транспорта.
func (t *InmemTransport) LocalAddr() ServerAddress {
	return t.localAddr
}

// AppendEntries отправляет AppendEntries RPC узлу peerID.
// Блокируется до получения ответа или тайм-аута. Тайм-аут — t.timeout (500ms).
// Возвращает ErrNotReachable, ErrRaftShutdown или ErrEnqueueTimeout.
//
//nolint:gocritic
func (t *InmemTransport) AppendEntries(peerID ServerID, args AppendEntriesArgs) (AppendEntriesReply, error) {
	var zero AppendEntriesReply
	select {
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan RPCResponse, 1)
	select {
	case peer.consumerCh <- RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		// Таймаут защищает от вечной блокировки при остановленном получателе.
		return zero, ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*AppendEntriesReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
}

// RequestVote отправляет RequestVote RPC узлу peerID.
// Семантика ошибок и таймаут аналогичны AppendEntries.
func (t *InmemTransport) RequestVote(peerID ServerID, args RequestVoteArgs) (RequestVoteReply, error) {
	var zero RequestVoteReply
	select {
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan RPCResponse, 1)
	select {
	case peer.consumerCh <- RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		// Таймаут защищает от вечной блокировки при остановленном получателе.
		return zero, ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*RequestVoteReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
}

// RequestPreVote отправляет PreVote RPC узлу peerID.
// Семантика ошибок и тайм-аут аналогичны RequestVote.
func (t *InmemTransport) RequestPreVote(peerID ServerID, args RequestPreVoteArgs) (RequestPreVoteReply, error) {
	var zero RequestPreVoteReply
	select {
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan RPCResponse, 1)
	select {
	case peer.consumerCh <- RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		// Ограничение блокировки отправителя:
		// при остановленном получателе с ещё открытым транспортом
		// enqueue не должен блокироваться навсегда — join в Stop()
		// обязан оставаться конечным.
		return zero, ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*RequestPreVoteReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
}

// TimeoutNow отправляет TimeoutNowRequest узлу peerID.
// Реализация аналогична RequestVote: создаёт RPC, отправляет в consumerCh,
// ожидает ответ с таймаутом t.timeout.
func (t *InmemTransport) TimeoutNow(peerID ServerID, args TimeoutNowRequest) (TimeoutNowResponse, error) {
	var zero TimeoutNowResponse
	select {
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan RPCResponse, 1)
	select {
	case peer.consumerCh <- RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		// Ограничение блокировки отправителя:
		// при остановленном получателе с ещё открытым транспортом
		// enqueue не должен блокироваться навсегда — join в Stop()
		// обязан оставаться конечным.
		return zero, ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*TimeoutNowResponse)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
}

// InstallSnapshot отправляет InstallSnapshot RPC узлу peerID.
// В отличие от AppendEntries/RequestVote, данные снимка передаются
// через поле RPC.Reader, а RespChan — через поле RPC.RespChan.
//
//nolint:gocritic
func (t *InmemTransport) InstallSnapshot(
	peerID ServerID, req InstallSnapshotRequest, data io.Reader,
) (InstallSnapshotResponse, error) {
	var zero InstallSnapshotResponse
	select {
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan RPCResponse, 1)
	select {
	case peer.consumerCh <- RPC{Command: &req, Reader: data, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*InstallSnapshotResponse)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, ErrEnqueueTimeout
	}
}

// AppendEntriesPipeline возвращает конвейер. Не реализован.
func (t *InmemTransport) AppendEntriesPipeline(_ ServerID) (AppendPipeline, error) {
	return nil, ErrNotImplemented
}

// SetHeartbeatHandler сохраняет обработчик heartbeat-сообщений.
func (t *InmemTransport) SetHeartbeatHandler(h func(RPC)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.heartbeat = h
}

// Connect добавляет peer в карту peers. Используется Harness для
// установки логического соединения между двумя узлами.
func (t *InmemTransport) Connect(peerID ServerID, transport *InmemTransport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[peerID] = transport
}

// Disconnect удаляет peer из карты peers, разрывая логическое соединение.
func (t *InmemTransport) Disconnect(peerID ServerID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, peerID)
}

// IsDisconnected проверяет, отключён ли указанный peer.
func (t *InmemTransport) IsDisconnected(peerID ServerID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.peers[peerID]; !ok {
		return true
	}
	return false
}

// Close завершает работу транспорта: закрывает shutdownCh, дренирует consumerCh
// и очищает карту peers.
//
// После Close все вызовы AppendEntries/RequestVote вернут ErrRaftShutdown.
// consumerCh не закрывается — runRPCReader выходит по shutdownCh.
// Close идемпотентен: повторный вызов — no-op.
//
// Дренирование consumerCh гарантирует, что отправители не заблокируются
// навсегда: после закрытия shutdownCh они не слают новые запросы,
// а уже отправленные будут прочитаны и отброшены.
func (t *InmemTransport) Close() {
	select {
	case <-t.shutdownCh:
	default:
		close(t.shutdownCh)
	}
	// Дренируем consumerCh: после закрытия shutdownCh отправители
	// не будут слать новые запросы, остаётся только обработать уже
	// отправленные, чтобы они не заблокировали отправителя навсегда.
	for {
		select {
		case _, ok := <-t.consumerCh:
			if !ok {
				return
			}
		default:
			goto done
		}
	}
done:
	t.mu.Lock()
	t.peers = make(map[ServerID]*InmemTransport)
	t.mu.Unlock()
}

// getPeer возвращает транспорт соседа по его ID. Потокобезопасна.
func (t *InmemTransport) getPeer(peerID ServerID) (*InmemTransport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	peer, ok := t.peers[peerID]
	if !ok {
		return nil, ErrNotReachable
	}
	return peer, nil
}
