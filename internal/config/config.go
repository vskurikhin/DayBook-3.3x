// Package config Разбор аргументов командной строки
package config

import (
	"flag"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	PrefixHTTP = "http://"
	PrefixRPC  = "rpc://"
)

// DefaultMaxPool — значение по умолчанию для количества соединений в пуле
// на один адрес соседа у узла raftkv. Значение по умолчанию транспорта
// в пакете raft другое и равно 2.
const DefaultMaxPool = 4

type Values struct {
	HTTPAddress net.Addr
	RPCAddress  net.Addr
	Number      int
	Peers       map[int]net.Addr

	// TraceLogLevel — порог отладочных сообщений трассировки; передаётся
	// в raft.TraceConfig.Level и kvservice.TraceConfig.Level.
	TraceLogLevel int
	// TraceCMLogFile — путь к файлу трассировки ConsensusModule; пустая строка — stderr.
	TraceCMLogFile string
	// TraceKVLogFile — путь к файлу трассировки Key-Value; пустая строка — stderr.
	TraceKVLogFile string
	// DataDir — директория для persistent-хранилища узла;
	// пустая строка — вычисляется путь по умолчанию в cmd/main.go.
	DataDir string
	// MaxPool — максимальное количество соединений в пуле на один адрес
	// соседа; ноль заменяется на DefaultMaxPool при сборке конфигурации узла.
	MaxPool int

	// PprofAddress — адрес отдельного HTTP-сервера профилирования;
	// пустая строка выключает профилирование.
	PprofAddress string
	// BlockProfileRate — доля учитываемых событий блокировки;
	// ноль оставляет профилирование блокировок выключенным.
	BlockProfileRate int
	// MutexProfileFraction — доля учитываемых событий состязания за мьютекс;
	// ноль оставляет профилирование мьютексов выключенным.
	MutexProfileFraction int
}

func ParseFlags() Values {
	fs := flag.NewFlagSet("raft", flag.ContinueOnError)
	httpAddressFlag := fs.String("http-addr", ":8880", "HTTP server listen address")
	rpcAddressFlag := fs.String("rpc-addr", ":9990", "RPC server listen address")
	numberFlag := fs.Int("number", -1, "")
	peersFlag := fs.String("peers", "", "Comma-separated list of peers servers (id=host:port)")
	traceLogLevelFlag := fs.Int("trace-log-level", 1, "Trace log level for the raft and kvservice packages")
	traceCMLogFileFlag := fs.String("trace-cm-log-file", "", "Trace consensus module log file path (empty = stderr)")
	traceKVLogFileFlag := fs.String("trace-kv-log-file", "", "Trace key-value database log file path (empty = stderr)")
	dataDirFlag := fs.String("data-dir", "", "Directory for persistent storage")
	maxPoolFlag := fs.Int("max-pool", DefaultMaxPool, "Max connections pooled per peer address (0 = transport default)")
	pprofAddressFlag := fs.String("pprof-addr", "", "Profiling HTTP server listen address (empty = disabled)")
	blockProfileRateFlag := fs.Int("block-profile-rate", 0, "Block profile rate (0 = disabled)")
	mutexProfileFractionFlag := fs.Int("mutex-profile-fraction", 0, "Mutex profile fraction (0 = disabled)")

	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.") {
			continue
		}
		args = append(args, arg)
	}
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}

	httpAddress := parseHTTPAddress(*httpAddressFlag)
	rpcAddress := parsePeerAddress(*rpcAddressFlag)

	peers := make(map[int]net.Addr)
	if *peersFlag != "" {
		peers = parsePeers(peers, *peersFlag)
	}

	return Values{
		HTTPAddress:    httpAddress,
		RPCAddress:     rpcAddress,
		Number:         *numberFlag,
		Peers:          peers,
		TraceLogLevel:  *traceLogLevelFlag,
		TraceCMLogFile: *traceCMLogFileFlag,
		TraceKVLogFile: *traceKVLogFileFlag,
		DataDir:        *dataDirFlag,
		MaxPool:        *maxPoolFlag,

		PprofAddress:         *pprofAddressFlag,
		BlockProfileRate:     *blockProfileRateFlag,
		MutexProfileFraction: *mutexProfileFractionFlag,
	}
}

func parsePeers(peers map[int]net.Addr, raw string) map[int]net.Addr {
	for elem := range strings.SplitSeq(raw, ",") {
		keyValue := strings.Split(elem, "=")
		if len(keyValue) != 2 {
			log.Fatalf("invalid peer server address: %s", raw)
		}
		num, err := strconv.Atoi(keyValue[0])
		if err != nil {
			log.Fatalf("invalid peer server address: %s", raw)
		}
		addr := keyValue[1]
		if addr == "" {
			log.Fatalf("invalid peer server address: %s", raw)
		}
		peers = addrAppend(peers, num, addr)
	}
	return peers
}

func addrAppend(peers map[int]net.Addr, num int, addr string) map[int]net.Addr {
	addr = strings.TrimSpace(addr)

	// Check if address has a scheme prefix

	if strings.HasPrefix(addr, PrefixRPC) {
		if _, err := url.Parse(addr); err != nil {
			log.Fatalf("invalid peer address: %s", addr)
		}
		peers[num] = parsePeerAddress(addr)
		return peers
	}

	// No scheme — assume http:// (backward compatibility)
	withScheme := PrefixRPC + addr
	if _, err := url.Parse(withScheme); err != nil {
		log.Fatalf("invalid peer address: %s", addr)
	}
	peers[num] = parsePeerAddress(withScheme)
	return peers
}

// parsePeerAddress извлекает чистый host:port из адреса со схемой.
// Например, "rpc://example.com:9999" -> "example.com:9999".
// Возвращает [net.Addr].
func parsePeerAddress(addr string) net.Addr {
	trimmed := strings.TrimPrefix(addr, PrefixRPC)
	// Преобразуем строку в net.Addr
	result, err := net.ResolveTCPAddr("tcp", trimmed)
	if err != nil {
		log.Fatalf("invalid peers address: %s", addr)
	}
	return result
}

func parseHTTPAddress(addr string) net.Addr {
	var result net.Addr
	trimmed, _ := strings.CutPrefix(addr, PrefixHTTP)
	// Преобразуем строку в net.Addr
	result, err := net.ResolveTCPAddr("tcp", trimmed)
	if err != nil {
		log.Fatalf("invalid peers address: %s", addr)
	}
	return result
}
