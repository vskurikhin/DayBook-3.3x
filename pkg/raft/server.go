package raft

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
)

// Server инкапсулирует raft.ConsensusModule вместе с rpc.Server, который предоставляет
// свои методы в качестве RPC-конечных точек. Он также управляет узлами Raft-сервера.
// Основная цель этого типа — упростить код raft.Server для целей представления.
// raft.ConsensusModule имеет *Server для осуществления связи с узлами,
// и ему не нужно беспокоиться о специфике работы RPC-сервера.
type Server struct {
	mu sync.Mutex

	harness bool

	serverID int
	peerIds  []int

	cm       *ConsensusModule
	storage  Storage
	fsm      FSM
	rpcProxy *RPCProxy

	rpcServer *rpc.Server
	listener  net.Listener

	peerAddresses map[int]net.Addr
	peerClients   map[int]*rpc.Client

	ready <-chan any
	quit  chan any
	wg    sync.WaitGroup
}

// Config — конфигурация для создания нового сервера Raft.
// Содержит идентификатор сервера, список идентификаторов узлов-соседей,
// адрес для RPC и карту адресов узлов-соседей.
type Config struct {
	PeerAddresses map[int]net.Addr
	PeerIds       []int
	RPCAddress    string
	ServerID      int
}

// New создаёт новый сервер Raft с заданной конфигурацией cfg, хранилищем storage,
// каналом уведомления ready и FSM для применения зафиксированных записей журнала.
func New(cfg Config, storage Storage, ready <-chan any, fsm FSM) *Server {
	s := new(Server)
	s.serverID = cfg.ServerID
	s.peerIds = cfg.PeerIds
	s.peerAddresses = cfg.PeerAddresses
	if s.peerAddresses == nil {
		s.peerAddresses = make(map[int]net.Addr, len(cfg.PeerIds))
	}
	s.peerClients = make(map[int]*rpc.Client, len(cfg.PeerIds))
	if os.Getenv("RAFT_TEST_HARNESS") != "" {
		s.harness = true
	}
	s.storage = storage
	s.ready = ready
	s.fsm = fsm
	s.quit = make(chan any)
	return s
}

// NewServer создаёт новый сервер Raft с указанными идентификатором serverID,
// списком идентификаторов узлов-соседей peerIds, хранилищем storage, каналом
// уведомления ready и FSM для применения зафиксированных записей журнала.
func NewServer(serverID int, peerIds []int, storage Storage, ready <-chan any, fsm FSM) *Server {
	return New(Config{
		ServerID: serverID,
		PeerIds:  peerIds,
	}, storage, ready, fsm)
}

func (s *Server) Serve(address string) {
	s.mu.Lock()
	s.cm = NewConsensusModule(s.serverID, s.peerIds, s, s.storage, s.fsm, s.ready)

	// Создаём новый RPC-сервер и регистрируем RPCProxy,
	// который перенаправляет все методы в n.cm (ConsensusModule)
	s.rpcServer = rpc.NewServer()
	s.rpcProxy = NewProxy(s.cm)
	_ = s.rpcServer.RegisterName("ConsensusModule", s.rpcProxy)

	var err error
	s.listener, err = net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[%v] listening at %s", s.serverID, s.listener.Addr())
	s.mu.Unlock()

	s.wg.Go(func() {
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.quit:
					return
				default:
					log.Fatal("accept error:", err)
				}
			}
			s.wg.Go(func() {
				s.rpcServer.ServeConn(conn)
			})
		}
	})
}

// Apply отправляет команду в Raft-кластер и возвращает ApplyFuture.
// future.Error() блокирует до коммита и возвращает ошибку или nil.
// future.Response() возвращает ответ FSM.
// future.Index() возвращает индекс записи в журнале.
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture {
	return s.cm.Apply(cmd, timeout)
}

// DisconnectAll закрывает все клиентские соединения с другими узлами для этого сервера.
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.peerClients {
		if s.peerClients[id] != nil {
			_ = s.peerClients[id].Close()
			s.peerClients[id] = nil
		}
	}
}

// Shutdown закрывает сервер и ожидает его корректного завершения работы.
func (s *Server) Shutdown() {
	s.cm.Stop()
	close(s.quit)
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *Server) GetListenAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listener.Addr()
}

func (s *Server) ConnectToPeer(peerID int, addr net.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peerClients[peerID] == nil {
		client, err := rpc.Dial(addr.Network(), addr.String())
		if err != nil {
			return err
		}
		s.peerClients[peerID] = client
	}
	return nil
}

// ConnectToPeerWithTimeout подключает данный сервер к удалённому узлу peerID
// по адресу addr с заданным таймаутом timeout. Если соединение уже существует,
// метод завершается без ошибки. При недоступности узла возвращает ошибку.
func (s *Server) ConnectToPeerWithTimeout(peerID int, addr net.Addr, timeout time.Duration) error {
	fmtErrorf := func() error { return fmt.Errorf("peer %v already connected", peerID) }
	s.mu.Lock()
	if s.peerClients[peerID] != nil {
		s.mu.Unlock()
		s.cm.dLogf(fmtErrorf().Error())
		return nil
	}
	s.mu.Unlock()
	client, err := net.DialTimeout("tcp", addr.String(), timeout)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.peerClients[peerID] == nil {
		rpcClient := rpc.NewClient(client)
		s.peerClients[peerID] = rpcClient
		s.peerAddresses[peerID] = addr
	} else {
		s.mu.Unlock()
		return fmtErrorf()
	}
	s.mu.Unlock()
	return nil
}

// DisconnectPeer отключает этот сервер от удаленного узла, идентифицированного по peerId.
func (s *Server) DisconnectPeer(peerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peerClients[peerID] != nil {
		err := s.peerClients[peerID].Close()
		s.peerClients[peerID] = nil
		return err
	}
	return nil
}

// Call выполняет удалённый RPC-вызов метода serviceMethod на узле id.
// При отсутствии соединения или его разрыве автоматически выполняет
// повторное подключение. В тестовом режиме (harness) повторное
// подключение не выполняется.
func (s *Server) Call(id int, serviceMethod string, args, reply any) (err error) {
	fmtErrorf := func() error { return fmt.Errorf("call client %d after it's closed", id) }
	const onlyTwo = 2
	for i := 0; i < onlyTwo; i++ {
		peer := s.PeerClient(id)
		// Если этот метод вызывается после завершения работы (когда вызывается client.Close),
		// он вернет ошибку.
		if peer == nil {
			if s.harness {
				return fmtErrorf()
			}
			s.mu.Lock()
			addr := s.peerAddresses[id]
			if addr == nil {
				s.mu.Unlock()
				return fmtErrorf()
			}
			client, err := net.DialTimeout("tcp", addr.String(), HeartbeatTimeoutMs/onlyTwo*time.Millisecond)
			if err != nil {
				s.mu.Unlock()
				s.cm.dLogf("[%v] failed to connect to peer %v: %v", s.serverID, id, err)
				continue
			}
			peer = rpc.NewClient(client)
			s.peerClients[id] = peer
			s.mu.Unlock()
			s.cm.dLogf("reconnected to peer %v", id)
		}
		err = s.rpcProxy.Call(peer, serviceMethod, args, reply)
		if err != nil {
			continue
		}
		return nil
	}
	if err != nil && !s.harness {
		s.ClosePeerClient(id)
	}
	return err
}

// PeerClient возвращает RPC-клиент для узла с идентификатором id.
// Потокобезопасна. Возвращает nil, если клиент не подключён.
func (s *Server) PeerClient(id int) *rpc.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer := s.peerClients[id]
	return peer
}

// ClosePeerClient закрывает RPC-клиент для узла с идентификатором id
// и удаляет его из карты клиентов. Потокобезопасна.
func (s *Server) ClosePeerClient(id int) {
	s.mu.Lock()
	peer := s.peerClients[id]
	s.peerClients[id] = nil
	s.mu.Unlock()
	if peer != nil {
		_ = peer.Close()
	}
}

// IsLeader проверяет, считает ли сервер s себя лидером в кластере Raft.
func (s *Server) IsLeader() bool {
	_, _, isLeader := s.cm.Report()
	return isLeader
}

// Proxy предоставляет доступ к RPC-прокси, используемому данным сервером.
// Используется только в тестах для моделирования отказов.
func (s *Server) Proxy() *RPCProxy {
	return s.rpcProxy
}

// RPCProxy — прокси-сервер, прозрачно перенаправляющий RPC-вызовы
// ConsensusModule. Он принимает RPC-запросы, адресованные CM, при
// необходимости модифицирует их и затем передаёт самому CM.
//
// Полезен для следующих целей:
//   - моделирования потери RPC-вызовов;
//   - моделирования небольшой задержки передачи RPC;
//   - моделирования ненадёжных соединений путём значительной задержки
//     некоторых сообщений и отбрасывания других, если установлена
//     переменная окружения RAFT_UNRELIABLE_RPC.
type RPCProxy struct {
	mu sync.Mutex
	cm *ConsensusModule

	// numCallsBeforeDrop используется для управления отбрасыванием
	// RPC-вызовов:
	//   -1: не отбрасывать ни одного вызова;
	//    0: отбрасывать все вызовы;
	//   >0: начать отбрасывать вызовы после выполнения указанного
	//       количества вызовов.
	numCallsBeforeDrop int
}

func NewProxy(cm *ConsensusModule) *RPCProxy {
	return &RPCProxy{
		cm:                 cm,
		numCallsBeforeDrop: -1,
	}
}

func (rpp *RPCProxy) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	if os.Getenv("RAFT_UNRELIABLE_RPC") != "" {
		dice := rand.Intn(10)
		switch dice {
		case 9:
			rpp.cm.dLogf("drop RequestVote")
			return fmt.Errorf("RPC failed")
		case 8:
			rpp.cm.dLogf("delay RequestVote")
			time.Sleep(75 * time.Millisecond)
		}
	} else {
		time.Sleep(time.Duration(1+rand.Intn(5)) * time.Millisecond)
	}
	return rpp.cm.RequestVote(args, reply)
}

func (rpp *RPCProxy) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	if os.Getenv("RAFT_UNRELIABLE_RPC") != "" {
		dice := rand.Intn(10)
		switch dice {
		case 9:
			rpp.cm.dLogf("drop AppendEntries")
			return fmt.Errorf("RPC failed")
		case 8:
			rpp.cm.dLogf("delay AppendEntries")
			time.Sleep(75 * time.Millisecond)
		}
	} else {
		time.Sleep(time.Duration(1+rand.Intn(5)) * time.Millisecond)
	}
	return rpp.cm.AppendEntries(args, reply)
}

func (rpp *RPCProxy) Call(peer *rpc.Client, method string, args, reply any) error {
	rpp.mu.Lock()
	if rpp.numCallsBeforeDrop == 0 {
		rpp.mu.Unlock()
		rpp.cm.dLogf("drop Call %s: %v", method, args)
		return fmt.Errorf("RPC failed")
	}
	if rpp.numCallsBeforeDrop > 0 {
		rpp.numCallsBeforeDrop--
	}
	rpp.mu.Unlock()
	return peer.Call(method, args, reply)
}

// DropCallsAfterN настраивает прокси так, чтобы он начал отбрасывать
// RPC-вызовы после выполнения следующих n вызовов.
func (rpp *RPCProxy) DropCallsAfterN(n int) {
	rpp.mu.Lock()
	defer rpp.mu.Unlock()

	rpp.numCallsBeforeDrop = n
}

func (rpp *RPCProxy) DontDropCalls() {
	rpp.mu.Lock()
	defer rpp.mu.Unlock()

	rpp.numCallsBeforeDrop = -1
}
