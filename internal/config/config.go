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
	"time"

	"github.com/vskurikhin/raft"
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

	// ApplyBatchInterval — интервал батча применения записей к FSM.
	// Ноль — защитное значение: применяется raft.DefaultApplyBatchInterval.
	ApplyBatchInterval time.Duration
	// DataDir — директория для persistent-хранилища узла;
	// пустая строка — вычисляется путь по умолчанию в cmd/main.go.
	DataDir string
	// HeartbeatTimeout — период пульса лидера. Ноль — защитное значение:
	// применяется raft.DefaultHeartbeatTimeout.
	HeartbeatTimeout time.Duration
	// MaxPool — максимальное количество соединений в пуле на один адрес
	// соседа; ноль заменяется на DefaultMaxPool при сборке конфигурации узла.
	MaxPool int
	// ReelectionTimeout — база тайм-аута выборов. Ноль — защитное значение:
	// применяется raft.DefaultReelectionTimeout.
	ReelectionTimeout time.Duration
	// SnapshotInterval — интервал проверки необходимости снимка.
	// Ноль — защитное значение: применяется дефолт конструктора.
	SnapshotInterval time.Duration
	// SnapshotThreshold — минимальное количество записей после последнего
	// снимка, при котором создаётся новый снимок.
	// Ноль — защитное значение: применяется дефолт конструктора.
	SnapshotThreshold int
	// TCPRPCTimeout — тайм-аут TCP RPC к соседям. Ноль — защитное значение:
	// применяется дефолт транспорта (raft.TCPRPCTimeout, 191 мс). Связь
	// значения с проверкой кворума лидера — в подсказке флага.
	TCPRPCTimeout time.Duration
	// TickerTimeout — такт тикера выборов. Ноль — защитное значение:
	// применяется raft.DefaultTickerTimeout.
	TickerTimeout time.Duration
	// TraceCMLogFile — путь к файлу трассировки ConsensusModule; пустая строка — stderr.
	TraceCMLogFile string
	// TraceKVLogFile — путь к файлу трассировки Key-Value; пустая строка — stderr.
	TraceKVLogFile string
	// TraceLogLevel — порог отладочных сообщений трассировки; передаётся
	// в raft.TraceConfig.Level и kvservice.TraceConfig.Level.
	TraceLogLevel int

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
	applyBatchIntervalFlag, heartbeatTimeoutFlag, reelectionTimeoutFlag, tickerTimeoutFlag := addTimingFlags(fs)
	blockProfileRateFlag := fs.Int("block-profile-rate", 0, "Block profile rate (0 = disabled)")
	dataDirFlag := fs.String("data-dir", "", "Directory for persistent storage")
	httpAddressFlag := fs.String("http-addr", ":8880", "HTTP server listen address")
	maxPoolFlag := fs.Int("max-pool", DefaultMaxPool, "Max connections pooled per peer address (0 = transport default)")
	mutexProfileFractionFlag := fs.Int("mutex-profile-fraction", 0, "Mutex profile fraction (0 = disabled)")
	numberFlag := fs.Int("number", -1, "")
	peersFlag := fs.String("peers", "", "Comma-separated list of peers servers (id=host:port)")
	pprofAddressFlag := fs.String("pprof-addr", "", "Profiling HTTP server listen address (empty = disabled)")
	rpcAddressFlag := fs.String("rpc-addr", ":9990", "RPC server listen address")
	snapshotIntervalFlag := fs.Duration(
		"snapshot-interval", raft.DefaultSnapshotInterval,
		"Interval between snapshot checks (default 3s)",
	)
	snapshotThresholdFlag := fs.Int(
		"snapshot-threshold", raft.DefaultSnapshotThreshold,
		"Log entries since last snapshot to trigger a new one (default 1024)",
	)
	tcpRPCTimeoutFlag := fs.Duration(
		"tcp-rpc-timeout", raft.TCPRPCTimeout,
		"Timeout for TCP RPC calls to peers; the leader check-quorum timeout "+
			"stays fixed at 382ms and does NOT scale with this flag (default 191ms)",
	)
	traceCMLogFileFlag := fs.String("trace-cm-log-file", "", "Trace consensus module log file path (empty = stderr)")
	traceKVLogFileFlag := fs.String("trace-kv-log-file", "", "Trace key-value database log file path (empty = stderr)")
	traceLogLevelFlag := fs.Int("trace-log-level", 1, "Trace log level for the raft and kvservice packages")

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

	// Ранняя отказка для недопустимых значений флагов узла: сообщение
	// содержит имя флага, фактическое значение и требование. Верхняя
	// граница тайм-аута не вводится.
	if *tcpRPCTimeoutFlag <= 0 {
		log.Fatalf("-tcp-rpc-timeout must be greater than 0, got %v", *tcpRPCTimeoutFlag)
	}
	if *maxPoolFlag < 0 {
		log.Fatalf("-max-pool must not be negative, got %d", *maxPoolFlag)
	}
	if *snapshotIntervalFlag <= 0 {
		log.Fatalf("-snapshot-interval must be greater than 0, got %v", *snapshotIntervalFlag)
	}
	if *snapshotThresholdFlag < 1 {
		log.Fatalf("-snapshot-threshold must be at least 1, got %d", *snapshotThresholdFlag)
	}

	checkTimingFlags(heartbeatTimeoutFlag, tickerTimeoutFlag, reelectionTimeoutFlag, applyBatchIntervalFlag)

	return Values{
		ApplyBatchInterval: *applyBatchIntervalFlag,
		DataDir:            *dataDirFlag,
		HTTPAddress:        httpAddress,
		HeartbeatTimeout:   *heartbeatTimeoutFlag,
		MaxPool:            *maxPoolFlag,
		Number:             *numberFlag,
		Peers:              peers,
		RPCAddress:         rpcAddress,
		ReelectionTimeout:  *reelectionTimeoutFlag,
		SnapshotInterval:   *snapshotIntervalFlag,
		SnapshotThreshold:  *snapshotThresholdFlag,
		TCPRPCTimeout:      *tcpRPCTimeoutFlag,
		TickerTimeout:      *tickerTimeoutFlag,
		TraceCMLogFile:     *traceCMLogFileFlag,
		TraceKVLogFile:     *traceKVLogFileFlag,
		TraceLogLevel:      *traceLogLevelFlag,

		PprofAddress:         *pprofAddressFlag,
		BlockProfileRate:     *blockProfileRateFlag,
		MutexProfileFraction: *mutexProfileFractionFlag,
	}
}

// validateTimingFlags — тонкая тестируемая обёртка над
// raft.ValidateTiming: собирает временные параметры в TimerConfig и
// делегирует проверку пакету raft (владельцу инвариантов). Возвращает
// ошибку со всеми нарушениями сразу.
func validateTimingFlags(heartbeat, ticker, reelection, applyBatch time.Duration) error {
	return raft.ValidateTiming(raft.TimerConfig{
		ApplyBatch: applyBatch,
		Heartbeat:  heartbeat,
		Reelection: reelection,
		Ticker:     ticker,
	})
}

// checkTimingFlags выполняет раннюю отказку для недопустимых значений
// временных флагов узла: индивидуальные границы и межпараметрические
// соотношения проверяются выделенной тестируемой функцией, сообщение
// содержит имя параметра, фактическое значение и требование.
func checkTimingFlags(heartbeat, ticker, reelection, applyBatch *time.Duration) {
	if err := validateTimingFlags(*heartbeat, *ticker, *reelection, *applyBatch); err != nil {
		log.Fatalf("invalid Raft timing flags: %v", err)
	}
}

// addTimingFlags регистрирует четыре временных флага узла и возвращает
// их указатели. Вынесено из ParseFlags для сокращения функции.
func addTimingFlags(fs *flag.FlagSet) (applyBatch, heartbeat, reelection, ticker *time.Duration) {
	return fs.Duration(
			"heartbeat-timeout", raft.DefaultHeartbeatTimeout,
			"Leader heartbeat interval (default 33ms). Upper bound derives from the "+
				"fixed 382ms check-quorum timeout: beyond 95ms a leader steps down "+
				"on a single lost packet",
		), fs.Duration(
			"ticker-timeout", raft.DefaultTickerTimeout,
			"Election timer polling tick (default 21ms); must be at most "+
				"reelection-timeout/10",
		), fs.Duration(
			"reelection-timeout", raft.DefaultReelectionTimeout,
			"Base of the randomized election timeout, actual timeout is in "+
				"[reelection, 2*reelection) (default 381ms); must be at least 10x "+
				"heartbeat-timeout; also gates the pre-vote suppression window "+
				"[reelection, 2*reelection); check-quorum stays fixed at 382ms and "+
				"does not scale with this flag",
		), fs.Duration(
			"apply-batch-interval", raft.DefaultApplyBatchInterval,
			"Leader-side safety-net apply batching interval (default 50ms); must "+
				"not exceed reelection-timeout",
		)
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
