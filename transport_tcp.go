package raft

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	// connReceiveBufferSize — размер буфера для чтения входящих RPC (256KB).
	connReceiveBufferSize = 256 * 1024

	// connSendBufferSize — размер буфера для записи исходящих RPC (256KB).
	connSendBufferSize = 256 * 1024

	// defaultMaxPool — максимальное количество соединений в пуле на один
	// целевой адрес. Значение 2 позволяет переиспользовать соединения
	// без избыточного удержания ресурсов.
	defaultMaxPool = 2
)

// Типы RPC для фрейминга (1 байт вместо строки).
const (
	rpcAppendEntries byte = iota
	rpcInstallSnapshot
	rpcRequestPreVote
	rpcRequestVote
	rpcTimeoutNow
)

//nolint:gochecknoinits
func init() {
	gob.Register(AppendEntriesArgs{})
	gob.Register(AppendEntriesReply{})
	gob.Register(RequestVoteArgs{})
	gob.Register(RequestVoteReply{})
	gob.Register(TimeoutNowRequest{})
	gob.Register(TimeoutNowResponse{})
	gob.Register(RequestPreVoteArgs{})
	gob.Register(RequestPreVoteReply{})
	gob.Register(InstallSnapshotRequest{})
	gob.Register(InstallSnapshotResponse{})
}

// tcpConn — обёртка над net.Conn с буферизированным writer и gob-кодеками.
// Используется как для исходящих соединений (пул), так и для входящих
// (handleConn). В исходящем случае буфер чтения не используется — декодирование
// идёт напрямую из conn.
type tcpConn struct {
	target ServerAddress
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
	consumerCh chan RPC
	localAddr  ServerAddress
	listener   net.Listener
	timeout    time.Duration

	mu    sync.Mutex
	peers map[ServerID]string

	connPool     map[ServerAddress][]*tcpConn
	connPoolLock sync.Mutex
	maxPool      int

	heartbeatFn     func(RPC)
	heartbeatFnLock sync.Mutex

	activeConns     map[net.Conn]struct{}
	activeConnsLock sync.Mutex

	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

var _ Transport = (*TCPTransport)(nil)

// NewTCPTransport создаёт новый TCPTransport, слушающий на указанном адресе.
// Принимает адрес для прослушивания, таймаут и максимальный размер пула
// соединений (0 = defaultMaxPool). Запускает acceptLoop в отдельной горутине.
func NewTCPTransport(addr string, timeout time.Duration, maxPool int) (*TCPTransport, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("raft: failed to listen on %s: %w", addr, err)
	}
	if maxPool <= 0 {
		maxPool = defaultMaxPool
	}
	t := &TCPTransport{
		consumerCh:  make(chan RPC),
		localAddr:   ServerAddress(listener.Addr().String()),
		listener:    listener,
		timeout:     timeout,
		peers:       make(map[ServerID]string),
		connPool:    make(map[ServerAddress][]*tcpConn),
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
func (t *TCPTransport) Consumer() <-chan RPC {
	return t.consumerCh
}

// LocalAddr возвращает адрес, на котором слушает транспорт.
func (t *TCPTransport) LocalAddr() ServerAddress {
	return t.localAddr
}

// SetHeartbeatHandler устанавливает обработчик heartbeat fast-path.
func (t *TCPTransport) SetHeartbeatHandler(fn func(RPC)) {
	t.heartbeatFnLock.Lock()
	defer t.heartbeatFnLock.Unlock()
	t.heartbeatFn = fn
}

// AppendEntries отправляет AppendEntries указанному узлу через пул соединений.
//
//nolint:gocritic
func (t *TCPTransport) AppendEntries(peerID ServerID, args AppendEntriesArgs) (AppendEntriesReply, error) {
	var reply AppendEntriesReply
	err := t.genericRPC(peerID, rpcAppendEntries, &args, &reply)
	return reply, err
}

// RequestVote отправляет RequestVote указанному узлу через пул соединений.
func (t *TCPTransport) RequestVote(peerID ServerID, args RequestVoteArgs) (RequestVoteReply, error) {
	var reply RequestVoteReply
	err := t.genericRPC(peerID, rpcRequestVote, &args, &reply)
	return reply, err
}

// RequestPreVote отправляет PreVote RPC узлу peerID через пул соединений.
func (t *TCPTransport) RequestPreVote(peerID ServerID, args RequestPreVoteArgs) (RequestPreVoteReply, error) {
	var reply RequestPreVoteReply
	err := t.genericRPC(peerID, rpcRequestPreVote, &args, &reply)
	return reply, err
}

// TimeoutNow отправляет TimeoutNowRequest указанному узлу через пул соединений.
func (t *TCPTransport) TimeoutNow(peerID ServerID, args TimeoutNowRequest) (TimeoutNowResponse, error) {
	var reply TimeoutNowResponse
	err := t.genericRPC(peerID, rpcTimeoutNow, &args, &reply)
	return reply, err
}

// InstallSnapshot отправляет snapshot узлу peerID. Соединение НЕ возвращается в пул.
//
//nolint:gocritic
func (t *TCPTransport) InstallSnapshot(
	peerID ServerID, args InstallSnapshotRequest, data io.Reader,
) (InstallSnapshotResponse, error) {
	var reply InstallSnapshotResponse
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
		timeout := t.timeout * time.Duration(args.DataSize/int64(connSendBufferSize))
		if timeout < t.timeout {
			timeout = t.timeout
		}
		_ = conn.conn.SetDeadline(time.Now().Add(timeout))
	}

	if err := t.sendRPC(conn, rpcInstallSnapshot, &args); err != nil {
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
func (t *TCPTransport) AppendEntriesPipeline(_ ServerID) (AppendPipeline, error) {
	return nil, ErrNotImplemented
}

// Connect сохраняет адрес для указанного соседа. Если адрес изменился —
// сбрасывает пул соединений для старого адреса.
func (t *TCPTransport) Connect(peerID ServerID, addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if oldAddr, ok := t.peers[peerID]; ok && oldAddr != addr {
		t.removeConnPoolForTarget(ServerAddress(oldAddr))
	}

	t.peers[peerID] = addr
}

// Disconnect удаляет адрес указанного соседа.
func (t *TCPTransport) Disconnect(peerID ServerID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, peerID)
}

// DisconnectAll удаляет адреса всех соседей.
func (t *TCPTransport) DisconnectAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = make(map[ServerID]string)
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
func (t *TCPTransport) genericRPC(peerID ServerID, rpcType byte, args, reply any) error {
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
func (t *TCPTransport) lookupPeer(peerID ServerID) (ServerAddress, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	addr, ok := t.peers[peerID]
	if !ok {
		return "", fmt.Errorf("raft: unknown peer %d", peerID)
	}
	return ServerAddress(addr), nil
}

// getConn возвращает соединение к указанному адресу из пула или создаёт новое.
func (t *TCPTransport) getConn(target ServerAddress) (*tcpConn, error) {
	if conn := t.getPooledConn(target); conn != nil {
		return conn, nil
	}
	c, err := net.DialTimeout("tcp", string(target), t.timeout)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriterSize(c, connSendBufferSize)
	return &tcpConn{
		target: target,
		conn:   c,
		w:      w,
		dec:    gob.NewDecoder(c),
		enc:    gob.NewEncoder(w),
	}, nil
}

// getPooledConn извлекает соединение из пула (LIFO).
func (t *TCPTransport) getPooledConn(target ServerAddress) *tcpConn {
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
		case ErrRaftShutdown.Error():
			return true, ErrRaftShutdown
		case ErrEnqueueTimeout.Error():
			return true, ErrEnqueueTimeout
		default:
			return true, fmt.Errorf("raft: RPC error from peer: %s", rpcError)
		}
	}
	return true, nil
}

// removeConnPoolForTarget удаляет и закрывает все соединения к указанному адресу.
func (t *TCPTransport) removeConnPoolForTarget(target ServerAddress) {
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

	r := bufio.NewReaderSize(conn, connReceiveBufferSize)
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

	respCh := make(chan RPCResponse, 1)

	cmd := req.Args
	switch v := cmd.(type) {
	case AppendEntriesArgs:
		cmd = &v
	case RequestVoteArgs:
		cmd = &v
	case TimeoutNowRequest:
		cmd = &v
	case RequestPreVoteArgs:
		cmd = &v
	case InstallSnapshotRequest:
		cmd = &v
	}

	// Для InstallSnapshot читаем данные снимка напрямую из TCP-соединения,
	// минуя bufio.Reader, который используется gob.Decoder. После ошибок
	// декодирования gob внутреннее состояние bufio (b.r/b.w) может быть
	// повреждено, поэтому разделяем источники данных.
	// conn.Read безопасен — handleConn не вызывает conn.Read до следующей
	// итерации цикла (flush + decode).
	var snapReader io.ReadCloser
	if req, ok := cmd.(*InstallSnapshotRequest); ok && req.DataSize > 0 {
		snapReader = io.NopCloser(io.LimitReader(r, req.DataSize))
	}

	// Heartbeat fast-path: если это heartbeat и установлен обработчик,
	// вызываем его напрямую, минуя consumerCh.
	if req.Type == rpcAppendEntries {
		if args, ok := cmd.(*AppendEntriesArgs); ok {
			if args.Term != 0 && args.LeaderCommit == 0 && len(args.Entries) == 0 {
				t.heartbeatFnLock.Lock()
				fn := t.heartbeatFn
				t.heartbeatFnLock.Unlock()
				if fn != nil {
					fn(RPC{Command: cmd, RespChan: respCh})
					goto RESP
				}
			}
		}
	}

	select {
	case t.consumerCh <- RPC{Command: cmd, Reader: snapReader, RespChan: respCh}:
	case <-t.shutdownCh:
		return ErrRaftShutdown
	}

RESP:
	var respErr string
	var respReply any
	respTimeout := t.timeout
	if req, ok := cmd.(*InstallSnapshotRequest); ok && req.DataSize > 0 {
		scaled := t.timeout * time.Duration(req.DataSize/int64(connSendBufferSize))
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
		return ErrRaftShutdown
	case <-time.After(respTimeout):
		respErr = ErrEnqueueTimeout.Error()
	}

	if err := enc.Encode(respErr); err != nil {
		return err
	}
	if respReply == nil {
		respReply = &AppendEntriesReply{}
	}
	if err := enc.Encode(respReply); err != nil {
		return err
	}
	return nil
}
