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

	serverId int
	peerIds  []int

	cm       *ConsensusModule
	storage  Storage
	rpcProxy *RPCProxy

	rpcServer *rpc.Server
	listener  net.Listener

	commitChan  chan<- CommitEntry
	peerClients map[int]*rpc.Client

	ready <-chan any
	quit  chan any
	wg    sync.WaitGroup
}

func NewServer(serverId int, peerIds []int, storage Storage, ready <-chan any, commitChan chan<- CommitEntry) *Server {
	s := new(Server)
	s.serverId = serverId
	s.peerIds = peerIds
	s.peerClients = make(map[int]*rpc.Client)
	s.storage = storage
	s.ready = ready
	s.commitChan = commitChan
	s.quit = make(chan any)
	return s
}

func (s *Server) Serve(address string) {
	s.mu.Lock()
	s.cm = NewConsensusModule(s.serverId, s.peerIds, s, s.storage, s.ready, s.commitChan)

	// Создаём новый RPC-сервер и регистрируем RPCProxy,
	// который перенаправляет все методы в n.cm (ConsensusModule)
	s.rpcServer = rpc.NewServer()
	s.rpcProxy = NewProxy(s.cm)
	s.rpcServer.RegisterName("ConsensusModule", s.rpcProxy)

	var err error
	s.listener, err = net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[%v] listening at %s", s.serverId, s.listener.Addr())
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

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
			s.wg.Add(1)
			go func() {
				s.rpcServer.ServeConn(conn)
				s.wg.Done()
			}()
		}
	}()
}

// Submit вызывает метод Submit базового экземпляра CM; описание см. в
// документации к этому методу.
func (s *Server) Submit(cmd any) int {
	return s.cm.Submit(cmd)
}

// DisconnectAll закрывает все клиентские соединения с другими узлами для этого сервера.
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.peerClients {
		if s.peerClients[id] != nil {
			s.peerClients[id].Close()
			s.peerClients[id] = nil
		}
	}
}

// Shutdown закрывает сервер и ожидает его корректного завершения работы.
func (s *Server) Shutdown() {
	s.cm.Stop()
	close(s.quit)
	s.listener.Close()
	s.wg.Wait()
}

func (s *Server) GetListenAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listener.Addr()
}

func (s *Server) ConnectToPeer(peerId int, addr net.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peerClients[peerId] == nil {
		client, err := rpc.Dial(addr.Network(), addr.String())
		if err != nil {
			return err
		}
		s.peerClients[peerId] = client
	}
	return nil
}

// DisconnectPeer отключает этот сервер от удаленного узла, идентифицированного по peerId.
func (s *Server) DisconnectPeer(peerId int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peerClients[peerId] != nil {
		err := s.peerClients[peerId].Close()
		s.peerClients[peerId] = nil
		return err
	}
	return nil
}

func (s *Server) Call(id int, serviceMethod string, args any, reply any) error {
	s.mu.Lock()
	peer := s.peerClients[id]
	s.mu.Unlock()

	// Если этот метод вызывается после завершения работы (когда вызывается client.Close),
	// он вернет ошибку.
	if peer == nil {
		return fmt.Errorf("call client %d after it's closed", id)
	} else {
		return s.rpcProxy.Call(peer, serviceMethod, args, reply)
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
	if len(os.Getenv("RAFT_UNRELIABLE_RPC")) > 0 {
		dice := rand.Intn(10)
		if dice == 9 {
			rpp.cm.dLogf("drop RequestVote")
			return fmt.Errorf("RPC failed")
		} else if dice == 8 {
			rpp.cm.dLogf("delay RequestVote")
			time.Sleep(75 * time.Millisecond)
		}
	} else {
		time.Sleep(time.Duration(1+rand.Intn(5)) * time.Millisecond)
	}
	return rpp.cm.RequestVote(args, reply)
}

func (rpp *RPCProxy) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	if len(os.Getenv("RAFT_UNRELIABLE_RPC")) > 0 {
		dice := rand.Intn(10)
		if dice == 9 {
			rpp.cm.dLogf("drop AppendEntries")
			return fmt.Errorf("RPC failed")
		} else if dice == 8 {
			rpp.cm.dLogf("delay AppendEntries")
			time.Sleep(75 * time.Millisecond)
		}
	} else {
		time.Sleep(time.Duration(1+rand.Intn(5)) * time.Millisecond)
	}
	return rpp.cm.AppendEntries(args, reply)
}

func (rpp *RPCProxy) Call(peer *rpc.Client, method string, args any, reply any) error {
	rpp.mu.Lock()
	if rpp.numCallsBeforeDrop == 0 {
		rpp.mu.Unlock()
		rpp.cm.dLogf("drop Call %s: %v", method, args)
		return fmt.Errorf("RPC failed")
	} else {
		if rpp.numCallsBeforeDrop > 0 {
			rpp.numCallsBeforeDrop--
		}
		rpp.mu.Unlock()
		return peer.Call(method, args, reply)
	}
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
