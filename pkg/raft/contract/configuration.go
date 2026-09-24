package contract

// ServerSuffrage определяет, участвует ли сервер в голосовании.
type ServerSuffrage int

const (
	Voter ServerSuffrage = iota + 1
	Nonvoter
)

// ConfigServer — один сервер в конфигурации кластера Raft.
type ConfigServer struct {
	ID       ServerID
	Address  ServerAddress
	Suffrage ServerSuffrage
}

// Configuration — состав кластера Raft.
type Configuration struct {
	ConfigServers []ConfigServer
}
