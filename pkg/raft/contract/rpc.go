package contract

import "time"

// ProtocolVersion — версия протокола Raft, используемая данной реализацией.
// Значение 3. Совместимость с версиями 0-2 не поддерживается.
const ProtocolVersion = 3

// TCPRPCTimeout — тайм-аут TCP RPC, используется в production-транспорте.
// Окно одного RPC-обмена запрос/ответ для обычных RPC. Производные: окно
// проверки кворума = 2 × 200 мс = 400, потолок пульса = ⌊400/4⌋ = 100.
// Вынесено в независимую константу, чтобы настройка таймеров для тестового
// ускорения не влияла на production-развёртывание. Единый источник значения:
// корневой пакет и пакеты реализаций переэкспортируют её через алиасы.
const TCPRPCTimeout = 200 * time.Millisecond

// ConnectionTCPRPCTimeout — значение по умолчанию для тайм‑аута установки TCP‑соединения
// (используется в net.DialTimeout внутри getConn). Историческое значение RPC‑тайм‑аута;
// в локальных и LAN‑сетях рукопожатие укладывается в этот лимит с запасом.
const ConnectionTCPRPCTimeout = 165 * time.Millisecond

// InstallSnapshotTimeout — опорная длительность для расчёта предельного времени передачи снимка.
// Итоговое время масштабируется объёмом данных: значение × ⌊DataSize / 256 KiB⌋,
// с ограничением снизу исходным значением. Окно ожидания ответа на получателе
// использует ту же базу — max с responseTimeout — поэтому флаг управляет обеими
// сторонами передачи снимка, включая малые снимки (< 256 КиБ). Учитывает запас
// на один блок снимка (256 КиБ) и накладные расходы дисковой подсистемы на отправителе.
const InstallSnapshotTimeout = 310 * time.Millisecond

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
