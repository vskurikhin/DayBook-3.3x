package raft

import (
	"log"
	"net"
	"sync"
	"time"
)

// Server — тонкая обёртка над TCPTransport и ConsensusModule для
// обратной совместимости с cmd/main.go и pkg/kvservice.
// В тестах (Harness) не используется — вместо него применяется
// InmemTransport напрямую.
type Server struct {
	mu sync.Mutex

	fsm           FSM
	snapshotStore SnapshotStore
	storage       Storage

	cm        *ConsensusModule
	transport *TCPTransport

	maxPool  int
	peerIds  []int
	serverID int

	ready <-chan any
	quit  chan any

	tcpRPCTimeout     time.Duration
	snapshotInterval  time.Duration
	snapshotThreshold int
}

// Config — конфигурация для создания нового сервера Raft.
type Config struct {
	PeerAddresses map[int]net.Addr
	PeerIds       []int
	RPCAddress    string
	ServerID      int

	Fsm FSM

	// MaxPool — максимальное количество соединений в пуле на один целевой
	// адрес (0 = _defaultMaxPool).
	MaxPool int

	// SnapshotInterval — интервал проверки необходимости снимка
	// (0 = дефолт конструктора).
	SnapshotInterval time.Duration

	// SnapshotThreshold — минимальное количество записей после последнего
	// снимка, при котором создаётся новый снимок (0 = дефолт конструктора).
	SnapshotThreshold int

	// SnapshotStore — хранилище снимков. Если nil, снимки отключены.
	SnapshotStore SnapshotStore

	Storage Storage

	TCPRPCTimeout time.Duration
}

// New создаёт новый сервер Raft с заданной конфигурацией cfg, хранилищем storage,
// каналом уведомления ready и FSM для применения зафиксированных записей журнала.
func New(cfg *Config, ready <-chan any) *Server {
	s := &Server{
		fsm:               cfg.Fsm,
		maxPool:           cfg.MaxPool,
		peerIds:           cfg.PeerIds,
		quit:              make(chan any),
		ready:             ready,
		serverID:          cfg.ServerID,
		snapshotInterval:  cfg.SnapshotInterval,
		snapshotStore:     cfg.SnapshotStore,
		snapshotThreshold: cfg.SnapshotThreshold,
		storage:           cfg.Storage,
		tcpRPCTimeout:     cfg.TCPRPCTimeout,
	}
	return s
}

// NewServer создаёт новый сервер Raft с указанными идентификатором serverID,
// списком идентификаторов узлов-соседей peerIds, хранилищем storage, каналом
// уведомления ready и FSM для применения зафиксированных записей журнала.
func NewServer(serverID int, peerIds []int, fsm FSM, ready <-chan any) *Server {
	return New(&Config{
		ServerID:      serverID,
		Fsm:           fsm,
		PeerIds:       peerIds,
		Storage:       NewMapStorage(),
		TCPRPCTimeout: _defaultTCPRPCTimeout,
	}, ready)
}

// Serve запускает TCP-транспорт на указанном адресе и создаёт ConsensusModule.
func (s *Server) Serve(address string) {
	transport, err := NewTCPTransport(address, s.tcpRPCTimeout, s.maxPool)
	if err != nil {
		log.Fatalf("raft: failed to create TCPTransport: %v", err)
	}
	s.mu.Lock()
	s.transport = transport
	s.mu.Unlock()

	s.cm = NewConsensusModule(s.serverID, s.peerIds, transport, s.storage, s.fsm, s.ready, s.snapshotStore)

	// Применение параметров снимков из конфигурации сразу после создания
	// CM и до закрытия ready — CM ещё не участвует в выборах. Выполняется
	// только при включённых снимках (snapshotStore != nil); нулевые и
	// отрицательные значения заменяются дефолтами конструктора. Сеттер —
	// единая точка записи параметров снимков; сигнал snapshotCh безопасен
	// (shouldSnapshot — фильтр) и лишь ускоряет применение нового интервала.
	if s.snapshotStore != nil {
		interval := s.snapshotInterval
		if interval <= 0 {
			interval = DefaultSnapshotInterval
		}
		threshold := s.snapshotThreshold
		if threshold <= 0 {
			threshold = DefaultSnapshotThreshold
		}
		s.cm.SetSnapshotConfig(threshold, interval, _defaultTrailingLogs)
	}
}

// Apply отправляет команду в Raft-кластер и возвращает ApplyFuture.
//
// Обязательство отправителя: объект команды cmd сохраняется по ссылке;
// вызывающий не должен мутировать переданную команду после вызова Apply.
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture {
	return s.cm.Apply(cmd, timeout)
}

// ConnectToPeerWithTimeout сохраняет адрес соседа в TCPTransport.
// В новой архитектуре транспорт подключается лениво (при первом RPC).
// Метод сохранён для обратной совместимости с kvservice.
func (s *Server) ConnectToPeerWithTimeout(peerID int, addr net.Addr, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != nil {
		s.transport.Connect(ServerID(peerID), addr.String())
	}
	return nil
}

// ConnectToPeer сохраняет адрес соседа. Аналогично ConnectToPeerWithTimeout.
func (s *Server) ConnectToPeer(peerID int, addr net.Addr) error {
	return s.ConnectToPeerWithTimeout(peerID, addr, 0)
}

// DisconnectPeer отключает от указанного соседа.
func (s *Server) DisconnectPeer(peerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != nil {
		s.transport.Disconnect(ServerID(peerID))
	}
	return nil
}

// DisconnectAll закрывает все соединения с соседями.
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != nil {
		s.transport.DisconnectAll()
	}
}

// GetListenAddr возвращает адрес, на котором слушает TCPTransport.
func (s *Server) GetListenAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport == nil {
		return nil
	}
	addr, err := net.ResolveTCPAddr("tcp", string(s.transport.LocalAddr()))
	if err != nil {
		return nil
	}
	return addr
}

// IsLeader проверяет, является ли сервер лидером.
func (s *Server) IsLeader() bool {
	_, _, isLeader := s.cm.Report()
	return isLeader
}

// VerifyLeader проверяет, что сервер всё ещё является лидером
// (ReadIndex, Raft §8). Возвращает Future, которая завершится с
// ошибкой, если лидерство утеряно.
func (s *Server) VerifyLeader() Future {
	return s.cm.VerifyLeader()
}

// Shutdown останавливает сервер: cm.Stop() (присоединяет все горутины
// CM — join после), затем transport.Close(). Контракт порядка
// transport.Close() выполняется до или одновременно с join
// горутин CM; фактически — сразу после Stop(), поэтому отправители RPC
// не блокируются навсегда на остановленном получателе с открытым
// транспортом.
func (s *Server) Shutdown() {
	s.cm.Stop()
	s.mu.Lock()
	if s.transport != nil {
		s.transport.Close()
	}
	s.mu.Unlock()
	close(s.quit)
}
