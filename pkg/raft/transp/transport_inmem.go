package transp

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// InmemTransport — реализация Transport, работающая в оперативной памяти
// (без TCP, без net/rpc, без gob-сериализации). Предназначена для
// использования в тестах (Harness). Каждый вызов AppendEntries,
// RequestVote и т.д. отправляет RPC через канал целевому узлу.
//
// Потокобезопасность: Connect/Disconnect/IsDisconnected защищены sync.Mutex.
// Consumer() и LocalAddr() не требуют блокировки.
// После Close() все методы возвращают contract.ErrRaftShutdown.
type InmemTransport struct {
	consumerCh chan contract.RPC
	localAddr  contract.ServerAddress
	mu         sync.Mutex
	peers      map[contract.ServerID]*InmemTransport
	heartbeat  func(contract.RPC)
	shutdownCh chan struct{}
	timeout    time.Duration
}

var _ contract.Transport = (*InmemTransport)(nil)

// InmemTransportTimeout — тайм-аут одного RPC внутрипроцессного
// транспорта (500 мс). Корневой тестовый харнесс выводит свои
// бюджеты ожидания из этой величины (_inmemRPCTimeout), поэтому
// изменение значения меняет и бюджеты тестов. Экспортируется,
// чтобы бюджеты тестов корневого пакета ссылались на единую
// точку значения и рассинхрон был исключён компилятором.
const InmemTransportTimeout = 500 * time.Millisecond

// NewInmemTransport создаёт новый InmemTransport с заданным локальным адресом.
// Тайм-аут по умолчанию — 500ms.
func NewInmemTransport(addr contract.ServerAddress) *InmemTransport {
	return &InmemTransport{
		consumerCh: make(chan contract.RPC),
		localAddr:  addr,
		peers:      make(map[contract.ServerID]*InmemTransport),
		shutdownCh: make(chan struct{}),
		timeout:    InmemTransportTimeout,
	}
}

// Consumer возвращает небуферизированный канал входящих RPC.
func (t *InmemTransport) Consumer() <-chan contract.RPC {
	return t.consumerCh
}

// LocalAddr возвращает локальный адрес транспорта.
func (t *InmemTransport) LocalAddr() contract.ServerAddress {
	return t.localAddr
}

// AppendEntries отправляет AppendEntries RPC узлу peerID.
// Блокируется до получения ответа или тайм-аута. Тайм-аут — t.timeout (500ms).
// Возвращает contract.ErrNotReachable, contract.ErrRaftShutdown или contract.ErrEnqueueTimeout.
//
//nolint:gocritic
func (t *InmemTransport) AppendEntries(
	peerID contract.ServerID, args contract.AppendEntriesArgs,
) (contract.AppendEntriesReply, error) {
	var zero contract.AppendEntriesReply
	select {
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan contract.RPCResponse, 1)
	select {
	case peer.consumerCh <- contract.RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		// Таймаут защищает от вечной блокировки при остановленном получателе.
		return zero, contract.ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*contract.AppendEntriesReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
}

// RequestVote отправляет RequestVote RPC узлу peerID.
// Семантика ошибок и таймаут аналогичны AppendEntries.
func (t *InmemTransport) RequestVote(
	peerID contract.ServerID, args contract.RequestVoteArgs,
) (contract.RequestVoteReply, error) {
	var zero contract.RequestVoteReply
	select {
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan contract.RPCResponse, 1)
	select {
	case peer.consumerCh <- contract.RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		// Таймаут защищает от вечной блокировки при остановленном получателе.
		return zero, contract.ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*contract.RequestVoteReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
}

// RequestPreVote отправляет PreVote RPC узлу peerID.
// Семантика ошибок и тайм-аут аналогичны RequestVote.
func (t *InmemTransport) RequestPreVote(
	peerID contract.ServerID, args contract.RequestPreVoteArgs,
) (contract.RequestPreVoteReply, error) {
	var zero contract.RequestPreVoteReply
	select {
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan contract.RPCResponse, 1)
	select {
	case peer.consumerCh <- contract.RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		// Ограничение блокировки отправителя:
		// при остановленном получателе с ещё открытым транспортом
		// enqueue не должен блокироваться навсегда — join в Stop()
		// обязан оставаться конечным.
		return zero, contract.ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*contract.RequestPreVoteReply)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
}

// TimeoutNow отправляет TimeoutNowRequest узлу peerID.
// Реализация аналогична RequestVote: создаёт RPC, отправляет в consumerCh,
// ожидает ответ с таймаутом t.timeout.
func (t *InmemTransport) TimeoutNow(
	peerID contract.ServerID, args contract.TimeoutNowRequest,
) (contract.TimeoutNowResponse, error) {
	var zero contract.TimeoutNowResponse
	select {
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan contract.RPCResponse, 1)
	select {
	case peer.consumerCh <- contract.RPC{Command: &args, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		// Ограничение блокировки отправителя:
		// при остановленном получателе с ещё открытым транспортом
		// enqueue не должен блокироваться навсегда — join в Stop()
		// обязан оставаться конечным.
		return zero, contract.ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*contract.TimeoutNowResponse)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
}

// InstallSnapshot отправляет InstallSnapshot RPC узлу peerID.
// В отличие от AppendEntries/RequestVote, данные снимка передаются
// через поле RPC.Reader, а RespChan — через поле RPC.RespChan.
//
//nolint:gocritic
func (t *InmemTransport) InstallSnapshot(
	peerID contract.ServerID, req contract.InstallSnapshotRequest, data io.Reader,
) (contract.InstallSnapshotResponse, error) {
	var zero contract.InstallSnapshotResponse
	select {
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	default:
	}
	peer, err := t.getPeer(peerID)
	if err != nil {
		return zero, err
	}
	respCh := make(chan contract.RPCResponse, 1)
	select {
	case peer.consumerCh <- contract.RPC{Command: &req, Reader: data, RespChan: respCh}:
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return zero, resp.Error
		}
		reply, ok := resp.Reply.(*contract.InstallSnapshotResponse)
		if !ok {
			return zero, fmt.Errorf("raft: unexpected reply type %T", resp.Reply)
		}
		return *reply, nil
	case <-peer.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-t.shutdownCh:
		return zero, contract.ErrRaftShutdown
	case <-time.After(t.timeout):
		return zero, contract.ErrEnqueueTimeout
	}
}

// AppendEntriesPipeline возвращает конвейер. Не реализован.
func (t *InmemTransport) AppendEntriesPipeline(_ contract.ServerID) (contract.AppendPipeline, error) {
	return nil, contract.ErrNotImplemented
}

// SetHeartbeatHandler сохраняет обработчик heartbeat-сообщений.
func (t *InmemTransport) SetHeartbeatHandler(h func(contract.RPC)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.heartbeat = h
}

// DisconnectAll отключает все соединения указанного транспорта.
func (t *InmemTransport) DisconnectAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = make(map[contract.ServerID]*InmemTransport)
}

// Connect добавляет peer в карту peers. Используется Harness для
// установки логического соединения между двумя узлами.
func (t *InmemTransport) Connect(peerID contract.ServerID, transport *InmemTransport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[peerID] = transport
}

// Disconnect удаляет peer из карты peers, разрывая логическое соединение.
func (t *InmemTransport) Disconnect(peerID contract.ServerID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, peerID)
}

// IsDisconnected проверяет, отключён ли указанный peer.
func (t *InmemTransport) IsDisconnected(peerID contract.ServerID) bool {
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
// После Close все вызовы AppendEntries/RequestVote вернут contract.ErrRaftShutdown.
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
	t.peers = make(map[contract.ServerID]*InmemTransport)
	t.mu.Unlock()
}

// getPeer возвращает транспорт соседа по его ID. Потокобезопасна.
func (t *InmemTransport) getPeer(peerID contract.ServerID) (*InmemTransport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	peer, ok := t.peers[peerID]
	if !ok {
		return nil, contract.ErrNotReachable
	}
	return peer, nil
}
