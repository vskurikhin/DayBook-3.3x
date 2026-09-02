package raft

import "github.com/vskurikhin/raft/pkg/raft/contract"

// Файл aliases.go сохраняет публичный API корневого пакета после переноса
// доменных типов и контрактов в лист-пакет pkg/raft/contract.
// Типы переэкспортируются прозрачными type-алиасами, константы — через
// const-переэкспорты значений. Прозрачные алиасы делают типы компиляционно
// тождественными их объявлениям в contract, поэтому код корня и пакетов
// реализаций может ссылаться на raft.X и contract.X взаимозаменяемо.
//
// Сентинелы ошибок (contract.ErrRaftShutdown и др.) здесь НЕ переэкспортируются:
// var-переэкспорт создал бы вторую личность сентинела, и сравнения
// err == ErrX / errors.Is по указателю разошлись бы между корнем и листом.
// Все вхождения сентинелов в корне квалифицируются contract.Err* напрямую.

// Типы из contract/transport.go.
type (
	// Transport — абстракция сетевого уровня Raft.
	Transport = contract.Transport
	// RPC — входящий RPC-запрос, полученный через Consumer().
	RPC = contract.RPC
	// RPCResponse содержит ответ на RPC-вызов: либо Reply, либо Error.
	RPCResponse = contract.RPCResponse
	// AppendPipeline — интерфейс конвейерной отправки AppendEntries.
	AppendPipeline = contract.AppendPipeline
	// ServerID — тип для идентификатора (ID) сервера в кластере Raft.
	ServerID = contract.ServerID
	// ServerAddress — тип для адреса сервера.
	ServerAddress = contract.ServerAddress
	// TimeoutNowRequest — RPC-запрос немедленного начала выборов.
	TimeoutNowRequest = contract.TimeoutNowRequest
	// TimeoutNowResponse — ответ на TimeoutNowRequest.
	TimeoutNowResponse = contract.TimeoutNowResponse
	// InstallSnapshotRequest — запрос на установку снимка от лидера.
	InstallSnapshotRequest = contract.InstallSnapshotRequest
	// InstallSnapshotResponse — ответ на установку снимка.
	InstallSnapshotResponse = contract.InstallSnapshotResponse
)

// Типы из contract/rpc.go.
type (
	// RPCHeader — общий заголовок для всех RPC-сообщений Raft.
	RPCHeader = contract.RPCHeader
	// WithRPCHeader — интерфейс получения RPCHeader из RPC-сообщения.
	WithRPCHeader = contract.WithRPCHeader
	// RequestVoteArgs — аргументы RPC запроса голоса.
	RequestVoteArgs = contract.RequestVoteArgs
	// RequestVoteReply — ответ на RequestVote RPC.
	RequestVoteReply = contract.RequestVoteReply
	// RequestPreVoteArgs — аргументы RPC предварительного голосования.
	RequestPreVoteArgs = contract.RequestPreVoteArgs
	// RequestPreVoteReply — ответ на RequestPreVote RPC.
	RequestPreVoteReply = contract.RequestPreVoteReply
	// AppendEntriesArgs — аргументы RPC AppendEntries.
	AppendEntriesArgs = contract.AppendEntriesArgs
	// AppendEntriesReply — ответ на AppendEntries RPC.
	AppendEntriesReply = contract.AppendEntriesReply
	// LogType — тип записи журнала.
	LogType = contract.LogType
	// LogEntry — запись журнала репликации Raft.
	LogEntry = contract.LogEntry
)

// Типы из contract/snapshot.go.
type (
	// SnapshotSink — получатель данных снимка.
	SnapshotSink = contract.SnapshotSink
	// SnapshotMeta — метаданные снимка.
	SnapshotMeta = contract.SnapshotMeta
)

// Типы из contract/configuration.go.
type (
	// ServerSuffrage определяет, участвует ли сервер в голосовании.
	ServerSuffrage = contract.ServerSuffrage
	// ConfigServer — один сервер в конфигурации кластера Raft.
	ConfigServer = contract.ConfigServer
	// Configuration — состав кластера Raft.
	Configuration = contract.Configuration
)

// Типы из contract/storage.go и contract/snapshot_store.go.
type (
	// Storage — интерфейс поставщика постоянного хранилища.
	Storage = contract.Storage
	// SnapshotStore — хранилище снимков Raft.
	SnapshotStore = contract.SnapshotStore
)

// Константы из contract (значения переэкспортируются без новых личностей).
const (
	// ProtocolVersion — версия протокола Raft.
	ProtocolVersion = contract.ProtocolVersion
	// TCPRPCTimeout — тайм-аут TCP RPC.
	TCPRPCTimeout = contract.TCPRPCTimeout

	// _defaultTCPRPCTimeout — константный алиас contract.TCPRPCTimeout,
	// сохраняющий историческое внутреннее имя для производственного кода.
	_defaultTCPRPCTimeout = contract.TCPRPCTimeout

	LogCommand       = contract.LogCommand
	LogNoop          = contract.LogNoop
	LogConfiguration = contract.LogConfiguration

	Voter    = contract.Voter
	Nonvoter = contract.Nonvoter
)
