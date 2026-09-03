package transp

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

const (
	// _connReceiveBufferSize — размер буфера для чтения входящих RPC (256KB).
	_connReceiveBufferSize = 256 * 1024

	// _connSendBufferSize — размер буфера для записи исходящих RPC (256KB).
	_connSendBufferSize = 256 * 1024

	// _defaultMaxPool — максимальное количество соединений в пуле на один
	// целевой адрес. Значение 2 позволяет переиспользовать соединения
	// без избыточного удержания ресурсов.
	_defaultMaxPool = 2
)

// Типы RPC для фрейминга (1 байт вместо строки).
const (
	_rpcAppendEntries byte = iota
	_rpcRequestVote
	_rpcInstallSnapshot
	_rpcTimeoutNow
	_rpcRequestPreVote
)

//nolint:gochecknoinits
func init() {
	// Метки закреплены полным путём импорта корневого пакета
	// github.com/vskurikhin/raft.
	// Метки осознанно сохраняют путь корневого пакета;
	// заменять их на contract.<ИмяТипа> нельзя.
	gob.RegisterName("github.com/vskurikhin/raft.AppendEntriesArgs", contract.AppendEntriesArgs{})
	gob.RegisterName("github.com/vskurikhin/raft.AppendEntriesReply", contract.AppendEntriesReply{})
	gob.RegisterName("github.com/vskurikhin/raft.RequestVoteArgs", contract.RequestVoteArgs{})
	gob.RegisterName("github.com/vskurikhin/raft.RequestVoteReply", contract.RequestVoteReply{})
	gob.RegisterName("github.com/vskurikhin/raft.TimeoutNowRequest", contract.TimeoutNowRequest{})
	gob.RegisterName("github.com/vskurikhin/raft.TimeoutNowResponse", contract.TimeoutNowResponse{})
	gob.RegisterName("github.com/vskurikhin/raft.RequestPreVoteArgs", contract.RequestPreVoteArgs{})
	gob.RegisterName("github.com/vskurikhin/raft.RequestPreVoteReply", contract.RequestPreVoteReply{})
	gob.RegisterName("github.com/vskurikhin/raft.InstallSnapshotRequest", contract.InstallSnapshotRequest{})
	gob.RegisterName("github.com/vskurikhin/raft.InstallSnapshotResponse", contract.InstallSnapshotResponse{})
}

// tcpConn — обёртка над net.Conn с буферизированным writer и gob-кодеками.
// Используется как для исходящих соединений (пул), так и для входящих
// (handleConn).
// В исходящем случае буфер чтения не используется
// — декодирование идёт напрямую из conn.
type tcpConn struct {
	target contract.ServerAddress
	conn   net.Conn
	w      *bufio.Writer
	dec    *gob.Decoder
	enc    *gob.Encoder
}

// Release закрывает соединение и освобождает ресурсы.
func (t *tcpConn) Release() error {
	return t.conn.Close()
}

// TCPTransport — реализация Transport, использующая TCP и encoding/gob.
// Содержит пул исходящих соединений (LIFO, maxPool на адрес) и
// переиспользует входящие соединения для множества RPC (цикл handleConn).
//
// Потокобезопасность: все методы потокобезопасны.
// После Close() все методы возвращают транспортную ошибку.
type TCPTransport struct {
	consumerCh chan contract.RPC
	localAddr  contract.ServerAddress
	listener   net.Listener
	timeout    time.Duration

	mu    sync.Mutex
	peers map[contract.ServerID]string

	connPool     map[contract.ServerAddress][]*tcpConn
	connPoolLock sync.Mutex
	maxPool      int

	heartbeatFn     func(contract.RPC)
	heartbeatFnLock sync.Mutex

	activeConns     map[net.Conn]struct{}
	activeConnsLock sync.Mutex

	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

var _ contract.Transport = (*TCPTransport)(nil)

// NewTCPTransport создаёт новый TCPTransport, слушающий на указанном адресе.
// Принимает адрес для прослушивания, таймаут и максимальный размер пула
// соединений (0 = _defaultMaxPool). Запускает acceptLoop в отдельной горутине.
func NewTCPTransport(addr string, timeout time.Duration, maxPool int) (*TCPTransport, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("raft: failed to listen on %s: %w", addr, err)
	}
	if maxPool <= 0 {
		maxPool = _defaultMaxPool
	}
	// Защитная проверка: нулевой тайм-аут (например, из нулевой конфигурации
	// или неявного нуля в литерале) заменяется дефолтом, чтобы транспорт всегда
	// имел действующие дедлайны соединения, отправки и чтения. Это защитная
	// проверка, а не молчаливое исправление: недокументированная возможность
	// «0 = без дедлайнов» изымается из контракта конструктора.
	if timeout <= 0 {
		timeout = contract.TCPRPCTimeout
	}
	t := &TCPTransport{
		consumerCh:  make(chan contract.RPC),
		localAddr:   contract.ServerAddress(listener.Addr().String()),
		listener:    listener,
		timeout:     timeout,
		peers:       make(map[contract.ServerID]string),
		connPool:    make(map[contract.ServerAddress][]*tcpConn),
		maxPool:     maxPool,
		activeConns: make(map[net.Conn]struct{}),
		shutdownCh:  make(chan struct{}),
	}
	t.wg.Add(1)
	go t.acceptLoop()
	return t, nil
}

// tcpRPCRequest — gob-структура RPC-запроса.
type tcpRPCRequest struct {
	Type byte
	Args any
}

// Consumer возвращает небуферизированный канал входящих RPC.
func (t *TCPTransport) Consumer() <-chan contract.RPC {
	return t.consumerCh
}

// LocalAddr возвращает адрес, на котором слушает транспорт.
func (t *TCPTransport) LocalAddr() contract.ServerAddress {
	return t.localAddr
}

// SetHeartbeatHandler устанавливает обработчик heartbeat fast-path.
func (t *TCPTransport) SetHeartbeatHandler(fn func(contract.RPC)) {
	t.heartbeatFnLock.Lock()
	defer t.heartbeatFnLock.Unlock()
	t.heartbeatFn = fn
}

// AppendEntries отправляет AppendEntries указанному узлу через пул соединений.
//
//nolint:gocritic
func (t *TCPTransport) AppendEntries(
	peerID contract.ServerID, args contract.AppendEntriesArgs,
) (contract.AppendEntriesReply, error) {
	var reply contract.AppendEntriesReply
	err := t.genericRPC(peerID, _rpcAppendEntries, &args, &reply)
	return reply, err
}

// RequestVote отправляет RequestVote указанному узлу через пул соединений.
func (t *TCPTransport) RequestVote(
	peerID contract.ServerID, args contract.RequestVoteArgs,
) (contract.RequestVoteReply, error) {
	var reply contract.RequestVoteReply
	err := t.genericRPC(peerID, _rpcRequestVote, &args, &reply)
	return reply, err
}

// RequestPreVote отправляет PreVote RPC узлу peerID через пул соединений.
func (t *TCPTransport) RequestPreVote(
	peerID contract.ServerID, args contract.RequestPreVoteArgs,
) (contract.RequestPreVoteReply, error) {
	var reply contract.RequestPreVoteReply
	err := t.genericRPC(peerID, _rpcRequestPreVote, &args, &reply)
	return reply, err
}

// TimeoutNow отправляет TimeoutNowRequest указанному узлу через пул соединений.
func (t *TCPTransport) TimeoutNow(
	peerID contract.ServerID, args contract.TimeoutNowRequest,
) (contract.TimeoutNowResponse, error) {
	var reply contract.TimeoutNowResponse
	err := t.genericRPC(peerID, _rpcTimeoutNow, &args, &reply)
	return reply, err
}

// InstallSnapshot отправляет snapshot узлу peerID. Соединение НЕ возвращается в пул.
//
//nolint:gocritic
func (t *TCPTransport) InstallSnapshot(
	peerID contract.ServerID, args contract.InstallSnapshotRequest, data io.Reader,
) (contract.InstallSnapshotResponse, error) {
	var reply contract.InstallSnapshotResponse
	target, err := t.lookupPeer(peerID)
	if err != nil {
		return reply, err
	}
	conn, err := t.getConn(target)
	if err != nil {
		return reply, err
	}
	defer func() { _ = conn.Release() }()

	if t.timeout > 0 {
		timeout := t.timeout * time.Duration(args.DataSize/int64(_connSendBufferSize))
		if timeout < t.timeout {
			timeout = t.timeout
		}
		_ = conn.conn.SetDeadline(time.Now().Add(timeout))
	}

	if err := t.sendRPC(conn, _rpcInstallSnapshot, &args); err != nil {
		return reply, err
	}

	if _, err := io.Copy(conn.w, data); err != nil {
		return reply, err
	}

	if err := conn.w.Flush(); err != nil {
		return reply, err
	}

	_, err = t.decodeResponse(conn, &reply)
	return reply, err
}

// AppendEntriesPipeline не реализован.
func (t *TCPTransport) AppendEntriesPipeline(_ contract.ServerID) (contract.AppendPipeline, error) {
	return nil, contract.ErrNotImplemented
}

// Connect сохраняет адрес для указанного соседа. Если адрес изменился —
// сбрасывает пул соединений для старого адреса.
func (t *TCPTransport) Connect(peerID contract.ServerID, addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if oldAddr, ok := t.peers[peerID]; ok && oldAddr != addr {
		t.removeConnPoolForTarget(contract.ServerAddress(oldAddr))
	}

	t.peers[peerID] = addr
}

// Disconnect удаляет адрес указанного соседа.
func (t *TCPTransport) Disconnect(peerID contract.ServerID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, peerID)
}

// DisconnectAll удаляет адреса всех соседей.
func (t *TCPTransport) DisconnectAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = make(map[contract.ServerID]string)
}

// Close завершает работу транспорта: закрывает shutdownCh, слушатель,
// удаляет всех соседей, закрывает все соединения в пуле и активные входящие
// соединения, дожидается завершения горутин через wg.Wait(). Идемпотентен.
func (t *TCPTransport) Close() {
	select {
	case <-t.shutdownCh:
		return
	default:
		close(t.shutdownCh)
	}
	_ = t.listener.Close()
	t.DisconnectAll()
	t.CloseStreams()
	t.closeActiveConns()
	t.wg.Wait()
}

// CloseStreams закрывает все соединения в пуле и удаляет их.
// Используется при реконфигурации кластера, когда адреса соседей
// могли измениться.
func (t *TCPTransport) CloseStreams() {
	t.connPoolLock.Lock()
	defer t.connPoolLock.Unlock()
	for key, conns := range t.connPool {
		for _, conn := range conns {
			_ = conn.Release()
		}
		delete(t.connPool, key)
	}
}

// IsShutdown проверяет, закрыт ли транспорт.
func (t *TCPTransport) IsShutdown() bool {
	select {
	case <-t.shutdownCh:
		return true
	default:
		return false
	}
}

// closeActiveConns закрывает все активные входящие соединения.
func (t *TCPTransport) closeActiveConns() {
	t.activeConnsLock.Lock()
	defer t.activeConnsLock.Unlock()
	for conn := range t.activeConns {
		_ = conn.Close()
	}
}

// genericRPC отправляет RPC любого типа, используя пул соединений.
func (t *TCPTransport) genericRPC(peerID contract.ServerID, rpcType byte, args, reply any) error {
	target, err := t.lookupPeer(peerID)
	if err != nil {
		return err
	}
	conn, err := t.getConn(target)
	if err != nil {
		return err
	}
	if t.timeout > 0 {
		_ = conn.conn.SetDeadline(time.Now().Add(t.timeout))
	}
	if err := t.sendRPC(conn, rpcType, args); err != nil {
		return err
	}
	canReturn, err := t.decodeResponse(conn, reply)
	if canReturn {
		t.returnConn(conn)
	}
	return err
}

// lookupPeer возвращает адрес соседа по его ID.
func (t *TCPTransport) lookupPeer(peerID contract.ServerID) (contract.ServerAddress, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	addr, ok := t.peers[peerID]
	if !ok {
		return "", fmt.Errorf("raft: unknown peer %d", peerID)
	}
	return contract.ServerAddress(addr), nil
}

// getConn возвращает соединение к указанному адресу из пула или создаёт новое.
func (t *TCPTransport) getConn(target contract.ServerAddress) (*tcpConn, error) {
	if conn := t.getPooledConn(target); conn != nil {
		return conn, nil
	}
	c, err := net.DialTimeout("tcp", string(target), t.timeout)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriterSize(c, _connSendBufferSize)
	return &tcpConn{
		target: target,
		conn:   c,
		w:      w,
		dec:    gob.NewDecoder(c),
		enc:    gob.NewEncoder(w),
	}, nil
}

// getPooledConn извлекает соединение из пула (LIFO).
func (t *TCPTransport) getPooledConn(target contract.ServerAddress) *tcpConn {
	t.connPoolLock.Lock()
	defer t.connPoolLock.Unlock()

	conns := t.connPool[target]
	if len(conns) == 0 {
		return nil
	}
	var conn *tcpConn
	lastIdx := len(conns) - 1
	conn, conns[lastIdx] = conns[lastIdx], nil
	t.connPool[target] = conns[:lastIdx]
	return conn
}

// returnConn возвращает соединение в пул, если транспорт не закрыт
// и количество соединений в пуле для этого адреса < maxPool.
// Иначе закрывает соединение.
func (t *TCPTransport) returnConn(conn *tcpConn) {
	t.connPoolLock.Lock()
	defer t.connPoolLock.Unlock()

	key := conn.target
	conns := t.connPool[key]

	if !t.IsShutdown() && len(conns) < t.maxPool {
		t.connPool[key] = append(conns, conn)
	} else {
		_ = conn.Release()
	}
}

// sendRPC кодирует и отправляет RPC-запрос через соединение.
func (t *TCPTransport) sendRPC(conn *tcpConn, rpcType byte, args any) error {
	req := tcpRPCRequest{Type: rpcType, Args: args}
	if err := conn.enc.Encode(req); err != nil {
		_ = conn.Release()
		return fmt.Errorf("raft: failed to send RPC: %w", err)
	}
	if err := conn.w.Flush(); err != nil {
		_ = conn.Release()
		return fmt.Errorf("raft: failed to flush RPC: %w", err)
	}
	return nil
}

// decodeResponse декодирует ответ из соединения. Возвращает canReturn=true,
// если соединение можно вернуть в пул. При ошибке декодирования вызывает
// conn.Release() и возвращает canReturn=false.
func (t *TCPTransport) decodeResponse(conn *tcpConn, reply any) (bool, error) {
	var rpcError string
	if err := conn.dec.Decode(&rpcError); err != nil {
		_ = conn.Release()
		return false, fmt.Errorf("raft: failed to decode error: %w", err)
	}
	if err := conn.dec.Decode(reply); err != nil {
		_ = conn.Release()
		return false, fmt.Errorf("raft: failed to decode response: %w", err)
	}
	if rpcError != "" {
		switch rpcError {
		case contract.ErrRaftShutdown.Error():
			return true, contract.ErrRaftShutdown
		case contract.ErrEnqueueTimeout.Error():
			return true, contract.ErrEnqueueTimeout
		default:
			return true, fmt.Errorf("raft: RPC error from peer: %s", rpcError)
		}
	}
	return true, nil
}

// removeConnPoolForTarget удаляет и закрывает все соединения к указанному адресу.
func (t *TCPTransport) removeConnPoolForTarget(target contract.ServerAddress) {
	t.connPoolLock.Lock()
	defer t.connPoolLock.Unlock()
	if conns, ok := t.connPool[target]; ok {
		for _, conn := range conns {
			_ = conn.Release()
		}
		delete(t.connPool, target)
	}
}

// acceptLoop принимает входящие TCP-соединения и запускает handleConn для каждого.
func (t *TCPTransport) acceptLoop() {
	defer t.wg.Done()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.shutdownCh:
				return
			default:
				log.Printf("raft: accept error: %v", err)
				continue
			}
		}
		t.wg.Add(1)
		t.trackConn(conn)
		go t.handleConn(conn)
	}
}

// trackConn добавляет соединение в список активных для закрытия при Close.
func (t *TCPTransport) trackConn(conn net.Conn) {
	t.activeConnsLock.Lock()
	defer t.activeConnsLock.Unlock()
	t.activeConns[conn] = struct{}{}
}

// untrackConn удаляет соединение из списка активных.
func (t *TCPTransport) untrackConn(conn net.Conn) {
	t.activeConnsLock.Lock()
	defer t.activeConnsLock.Unlock()
	delete(t.activeConns, conn)
}

// handleConn обрабатывает входящее соединение в цикле, пока не возникнет
// ошибка или соединение не будет закрыто. Переиспользует одно TCP-соединение
// для множества RPC.
func (t *TCPTransport) handleConn(conn net.Conn) {
	defer t.wg.Done()
	defer t.untrackConn(conn)
	defer func() { _ = conn.Close() }()

	r := bufio.NewReaderSize(conn, _connReceiveBufferSize)
	w := bufio.NewWriter(conn)
	dec := gob.NewDecoder(r)
	enc := gob.NewEncoder(w)

	for {
		select {
		case <-t.shutdownCh:
			return
		default:
		}

		if err := t.handleCommand(r, conn, dec, enc); err != nil {
			if err != io.EOF {
				log.Printf("raft: handle conn error: %v", err)
			}
			return
		}
		if err := w.Flush(); err != nil {
			log.Printf("raft: flush error: %v", err)
			return
		}
	}
}

// handleCommand декодирует и отправляет один RPC-запрос из входящего потока.
// Возвращает ошибку, если декодирование не удалось — вызывающий закрывает соединение.
// r — bufio.Reader, из которого был создан dec.
// conn — TCP-соединение, используется для чтения данных снимка напрямую
// (минуя bufio), чтобы избежать повреждения внутреннего состояния bufio
// после ошибок gob-декодирования.
func (t *TCPTransport) handleCommand(r *bufio.Reader, _ net.Conn, dec *gob.Decoder, enc *gob.Encoder) error {
	var req tcpRPCRequest
	if err := dec.Decode(&req); err != nil {
		return err
	}

	respCh := make(chan contract.RPCResponse, 1)

	cmd := req.Args
	switch v := cmd.(type) {
	case contract.AppendEntriesArgs:
		cmd = &v
	case contract.RequestVoteArgs:
		cmd = &v
	case contract.TimeoutNowRequest:
		cmd = &v
	case contract.RequestPreVoteArgs:
		cmd = &v
	case contract.InstallSnapshotRequest:
		cmd = &v
	}

	// Данные снимка не входят в gob-команду — они идут следом в том же потоке.
	// Поэтому для InstallSnapshot потребитель получает отдельный io.Reader
	// поверх r, ограниченный DataSize.
	snapReq, isSnapReq := cmd.(*contract.InstallSnapshotRequest)
	hasSnapshotData := isSnapReq && snapReq.DataSize > 0
	var snapReader io.ReadCloser
	if hasSnapshotData {
		snapReader = io.NopCloser(io.LimitReader(r, snapReq.DataSize))
	}

	// Heartbeat fast-path: если это heartbeat и установлен обработчик,
	// вызываем его напрямую, минуя consumerCh.
	if req.Type == _rpcAppendEntries {
		if args, ok := cmd.(*contract.AppendEntriesArgs); ok {
			if args.Term != 0 && args.LeaderCommit == 0 && len(args.Entries) == 0 {
				t.heartbeatFnLock.Lock()
				fn := t.heartbeatFn
				t.heartbeatFnLock.Unlock()
				if fn != nil {
					fn(contract.RPC{Command: cmd, RespChan: respCh})
					goto RESP
				}
			}
		}
	}

	select {
	case t.consumerCh <- contract.RPC{Command: cmd, Reader: snapReader, RespChan: respCh}:
	case <-t.shutdownCh:
		return contract.ErrRaftShutdown
	}

RESP:
	var respErr string
	var respReply any
	respTimeout := t.timeout
	if hasSnapshotData {
		scaled := t.timeout * time.Duration(snapReq.DataSize/int64(_connSendBufferSize))
		if scaled > respTimeout {
			respTimeout = scaled
		}
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			respErr = resp.Error.Error()
		}
		respReply = resp.Reply
	case <-t.shutdownCh:
		return contract.ErrRaftShutdown
	case <-time.After(respTimeout):
		respErr = contract.ErrEnqueueTimeout.Error()
	}

	if err := enc.Encode(respErr); err != nil {
		return err
	}
	if respReply == nil {
		respReply = &contract.AppendEntriesReply{}
	}
	if err := enc.Encode(respReply); err != nil {
		return err
	}
	return nil
}
