package contract

import "time"

// ProtocolVersion — версия протокола Raft, используемая данной реализацией.
// Значение 3. Совместимость с версиями 0-2 не поддерживается.
const ProtocolVersion = 3

// TCPRPCTimeout — тайм-аут TCP RPC (не зависит от Quantum), используется
// в production-транспорте. Исходное значение 85ms получено из 382/2 при
// Quantum=3. Вынесено в независимую константу, чтобы изменение Quantum
// для ускорения тестов не влияло на production-развёртывание.
// Единый источник значения: корень и пакеты реализаций переэкспортируют
// её через алиасы.
const TCPRPCTimeout = 191 * time.Millisecond

// RPCHeader — общий заголовок для всех RPC-сообщений Raft.
// Встраивается во все структуры аргументов и ответов RPC.
// Содержит версию протокола и идентификатор узла-отправителя.
type RPCHeader struct {
	ProtocolVersion int
	ServerID        int
}

// WithRPCHeader — интерфейс, позволяющий получить RPCHeader из RPC-сообщения.
type WithRPCHeader interface {
	GetRPCHeader() RPCHeader
}

// RequestVoteArgs См. рисунок 2 в статье.
type RequestVoteArgs struct {
	RPCHeader

	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
	// LeadershipTransfer — true, если запрос голоса вызван TimeoutNow.
	// Позволяет bypass проверку "есть лидер" в RequestVote handler.
	LeadershipTransfer bool
}

func (r *RequestVoteArgs) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

type RequestVoteReply struct {
	RPCHeader

	Term        int
	VoteGranted bool
}

func (r *RequestVoteReply) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

// RequestPreVoteArgs — аргументы RPC предварительного голосования (§4 Pre-Vote).
// PreVote НЕ увеличивает currentTerm, НЕ меняет votedFor, НЕ персистится.
// Получатель проверяет только актуальность лога (log-safety) и наличие активного лидера.
type RequestPreVoteArgs struct {
	RPCHeader

	Term         int
	LastLogIndex int
	LastLogTerm  int
}

func (r *RequestPreVoteArgs) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

// RequestPreVoteReply — ответ на RequestPreVote RPC.
type RequestPreVoteReply struct {
	RPCHeader

	Term        int
	VoteGranted bool
}

func (r *RequestPreVoteReply) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

// AppendEntriesArgs См. рисунок 2 в статье.
type AppendEntriesArgs struct {
	RPCHeader

	Term     int
	LeaderID int

	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

func (r *AppendEntriesArgs) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

type AppendEntriesReply struct {
	RPCHeader

	Term    int
	Success bool

	// Faster conflict resolution optimization (described near the end of section
	// 5.3 in the paper.)
	ConflictIndex int
	ConflictTerm  int
}

func (r *AppendEntriesReply) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

// LogType определяет тип записи журнала.
type LogType int

const (
	LogCommand LogType = iota
	LogNoop
	LogConfiguration
)

// LogEntry — запись журнала репликации Raft.
type LogEntry struct {
	Index int
	Term  int
	Type  LogType
	Data  any
}
