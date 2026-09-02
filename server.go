package raft

import (
	"net"
	"sync"
	"time"
)

// TransportManager — транспорт с управлением соединениями и
// завершением: расширяет Transport адресной книгой соседей и
// закрытием. Требуется Server для подключения/отключения соседей
// и остановки. Реализация — TCPTransport (pkg/raft/transp).
type TransportManager interface {
	Transport
	Connect(peerID ServerID, addr string)
	Disconnect(peerID ServerID)
	DisconnectAll()
	Close()
}

// Server — тонкая обёртка над транспортом и ConsensusModule для
// обратной совместимости с cmd/main.go и pkg/kvservice.
// В тестах (Harness) не используется — вместо него применяется
// внутрипроцессный транспорт напрямую.
type Server struct {
	mu sync.Mutex

	fsm           FSM
	snapshotStore SnapshotStore
	storage       Storage

	cm        *ConsensusModule
	transport TransportManager

	peerIds  []int
	serverID int

	ready <-chan any
	quit  chan any

	snapshotInterval  time.Duration
	snapshotThreshold int
}

// Config — конфигурация для создания нового сервера Raft.
type Config struct {
	PeerAddresses map[int]net.Addr
	PeerIds       []int
	ServerID      int

	Fsm FSM

	// SnapshotInterval — интервал проверки необходимости снимка
	// (0 = дефолт конструктора).
	SnapshotInterval time.Duration

	// SnapshotThreshold — минимальное количество записей после последнего
	// снимка, при котором создаётся новый снимок (0 = дефолт конструктора).
	SnapshotThreshold int

	// SnapshotStore — хранилище снимков. Если nil, снимки отключены.
	SnapshotStore SnapshotStore

	Storage Storage

	// Transport — транспорт с адресной книгой соседей и закрытием,
	// создаётся вызывающим и передаётся в Server.
	Transport TransportManager
}

// New создаёт новый сервер Raft с заданной конфигурацией cfg, хранилищем storage,
// каналом уведомления ready и FSM для применения зафиксированных записей журнала.
func New(cfg *Config, ready <-chan any) *Server {
	if isNilInterface(cfg.Transport) {
		panic("raft: Config.Transport is nil or typed nil: the transport must be created and passed by the caller")
	}
	s := &Server{
		fsm:               cfg.Fsm,
		peerIds:           cfg.PeerIds,
		quit:              make(chan any),
		ready:             ready,
		serverID:          cfg.ServerID,
		snapshotInterval:  cfg.SnapshotInterval,
		snapshotStore:     cfg.SnapshotStore,
		snapshotThreshold: cfg.SnapshotThreshold,
		storage:           cfg.Storage,
		transport:         cfg.Transport,
	}
	return s
}

// Serve создаёт ConsensusModule поверх переданного транспорта.
func (s *Server) Serve() {
	s.cm = NewConsensusModule(s.serverID, s.peerIds, s.transport, s.storage, s.fsm, s.ready, s.snapshotStore)

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

// ConnectToPeerWithTimeout сохраняет адрес соседа в транспорте.
// В новой архитектуре транспорт подключается лениво (при первом RPC).
// Метод сохранён для обратной совместимости с kvservice.
func (s *Server) ConnectToPeerWithTimeout(peerID int, addr net.Addr, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transport.Connect(ServerID(peerID), addr.String())
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
	s.transport.Disconnect(ServerID(peerID))
	return nil
}

// DisconnectAll закрывает все соединения с соседями.
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transport.DisconnectAll()
}

// GetListenAddr возвращает адрес, на котором слушает транспорт.
func (s *Server) GetListenAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.transport.Close()
	s.mu.Unlock()
	close(s.quit)
}
