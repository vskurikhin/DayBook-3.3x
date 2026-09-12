// Package config Разбор аргументов командной строки
package config

import (
	"errors"
	"flag"
	"fmt"
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
	// применяется raft.DefaultReelectionTimeout (430 мс). База должна быть
	// строго больше окна проверки кворума 400 мс.
	ReelectionTimeout time.Duration
	// SnapshotInterval — интервал проверки необходимости снимка.
	// Ноль — защитное значение: применяется дефолт конструктора.
	SnapshotInterval time.Duration
	// SnapshotThreshold — минимальное количество записей после последнего
	// снимка, при котором создаётся новый снимок.
	// Ноль — защитное значение: применяется дефолт конструктора.
	SnapshotThreshold int
	// TCPConnectTimeout — тайм-аут установки TCP-соединения к соседям.
	// Ноль — защитное значение: применяется дефолт транспорта
	// (raft.ConnectionTCPRPCTimeout, 165 мс). Связь значения с окном
	// проверки кворума — в подсказке флага.
	TCPConnectTimeout time.Duration
	// TCPRPCTimeout — тайм-аут TCP RPC к соседям. Ноль — защитное значение:
	// применяется дефолт транспорта (raft.TCPRPCTimeout, 200 мс). Окно
	// проверки кворума фиксировано 400 мс (2 × raft.TCPRPCTimeout) и флагом
	// не масштабируется; сумма connect + rpc ограничена 400 мс.
	TCPRPCTimeout time.Duration
	// InstallSnapshotTimeout — базовое значение для расчёта лимита времени передачи снимка.
	// Если установлено 0, применяется дефолтное значение транспорта
	// (raft.InstallSnapshotTimeout, 310 мс) как защитное значение.
	InstallSnapshotTimeout time.Duration
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
	snapshotIntervalFlag, snapshotThresholdFlag := addSnapshotFlags(fs)
	tcpConnectTimeoutFlag, tcpRPCTimeoutFlag, installSnapshotTimeoutFlag := addTransportFlags(fs)
	traceCMLogFileFlag, traceKVLogFileFlag, traceLogLevelFlag := addTraceFlags(fs)

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

	// Ранний отказ при недопустимых значениях флагов узла: сообщение
	// содержит имя флага, фактическое значение и требования к допустимому диапазону.
	// Верхняя граница тайм‑аута не задаётся.
	if err := validateTransportTimingFlags(*tcpConnectTimeoutFlag, *tcpRPCTimeoutFlag, *installSnapshotTimeoutFlag); err != nil {
		log.Fatalf("invalid TCP timeout flags: %v", err)
	}
	if err := validateElectionQuorumInvariant(*reelectionTimeoutFlag); err != nil {
		log.Fatalf("invalid election/quorum timing: %v", err)
	}

	httpAddress := parseHTTPAddress(*httpAddressFlag)
	rpcAddress := parsePeerAddress(*rpcAddressFlag)

	peers := make(map[int]net.Addr)
	if *peersFlag != "" {
		peers = parsePeers(peers, *peersFlag)
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
		ApplyBatchInterval:     *applyBatchIntervalFlag,
		DataDir:                *dataDirFlag,
		HTTPAddress:            httpAddress,
		HeartbeatTimeout:       *heartbeatTimeoutFlag,
		MaxPool:                *maxPoolFlag,
		Number:                 *numberFlag,
		Peers:                  peers,
		RPCAddress:             rpcAddress,
		ReelectionTimeout:      *reelectionTimeoutFlag,
		SnapshotInterval:       *snapshotIntervalFlag,
		SnapshotThreshold:      *snapshotThresholdFlag,
		TCPConnectTimeout:      *tcpConnectTimeoutFlag,
		TCPRPCTimeout:          *tcpRPCTimeoutFlag,
		InstallSnapshotTimeout: *installSnapshotTimeoutFlag,
		TickerTimeout:          *tickerTimeoutFlag,
		TraceCMLogFile:         *traceCMLogFileFlag,
		TraceKVLogFile:         *traceKVLogFileFlag,
		TraceLogLevel:          *traceLogLevelFlag,

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

// validateTransportTimingFlags проверяет три флага тайм-аутов
// TCP-транспорта: индивидуальные границы (положительное значение,
// целое число миллисекунд) и жёсткие межпараметрические инварианты
// (connect + rpc ≤ 2 × raft.TCPRPCTimeout). Функция чистая, без
// побочных эффектов; все нарушения собираются в одну ошибку
// (errors.Join).
func validateTransportTimingFlags(connect, rpc, snapshot time.Duration) error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(connect > 0, "-tcp-connect-timeout must be greater than 0, got %v", connect)
	check(connect%time.Millisecond == 0, "-tcp-connect-timeout must be a whole number of milliseconds, got %v", connect)
	check(rpc > 0, "-tcp-rpc-timeout must be greater than 0, got %v", rpc)
	check(rpc%time.Millisecond == 0, "-tcp-rpc-timeout must be a whole number of milliseconds, got %v", rpc)
	check(snapshot > 0, "-install-snapshot-timeout must be greater than 0, got %v", snapshot)
	check(snapshot%time.Millisecond == 0, "-install-snapshot-timeout must be a whole number of milliseconds, got %v", snapshot)
	check(connect <= rpc,
		"-tcp-connect-timeout must not exceed -tcp-rpc-timeout"+
			" (connection establishment plus the RPC must fit within 2x -tcp-rpc-timeout):"+
			" got %v vs -tcp-rpc-timeout %v",
		connect, rpc)
	check(snapshot >= rpc,
		"-install-snapshot-timeout must be at least -tcp-rpc-timeout: got %v vs -tcp-rpc-timeout %v",
		snapshot, rpc)
	check(connect+rpc <= 2*raft.TCPRPCTimeout,
		"-tcp-connect-timeout plus -tcp-rpc-timeout must not exceed 2x raft.TCPRPCTimeout"+
			" (the quorum check window is fixed by the compile-time constant and is not scaled by -tcp-rpc-timeout):"+
			" got %v + %v",
		connect, rpc)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// validateElectionQuorumInvariant проверяет инвариант: база
// тайм-аута выборов строго больше фиксированного окна проверки
// кворума 2 × raft.TCPRPCTimeout. Окно — константа компиляции и
// флагом -tcp-rpc-timeout не масштабируется, поэтому инвариант
// сравнивается с константой, а не с удвоенным флагом. Функция
// чистая, без побочных эффектов.
func validateElectionQuorumInvariant(reelection time.Duration) error {
	if reelection > 2*raft.TCPRPCTimeout {
		return nil
	}
	return fmt.Errorf("-reelection-timeout must be greater than 2x raft.TCPRPCTimeout"+
		" (the quorum check window is fixed by the compile-time constant and is not scaled by -tcp-rpc-timeout):"+
		" got %v vs %v",
		reelection, 2*raft.TCPRPCTimeout)
}

func addSnapshotFlags(fs *flag.FlagSet) (snapshotIntervalFlag *time.Duration, snapshotThresholdFlag *int) {
	return fs.Duration(
			"snapshot-interval", raft.DefaultSnapshotInterval,
			"Interval between snapshot checks (default 3s)",
		), fs.Int(
			"snapshot-threshold", raft.DefaultSnapshotThreshold,
			"Log entries since last snapshot to trigger a new one (default 1024)",
		)
}

// addTimingFlags регистрирует четыре временных флага узла и возвращает
// их указатели. Вынесено из ParseFlags для сокращения функции.
func addTimingFlags(fs *flag.FlagSet) (applyBatch, heartbeat, reelection, ticker *time.Duration) {
	return fs.Duration(
			"apply-batch-interval", raft.DefaultApplyBatchInterval,
			"Leader-side safety-net apply batching interval (default 50ms); must "+
				"not exceed reelection-timeout",
		), fs.Duration(
			"heartbeat-timeout", raft.DefaultHeartbeatTimeout,
			"Leader heartbeat interval (default 33ms). Upper bound derives from the "+
				"fixed 400ms check-quorum timeout: beyond 100ms a leader steps down "+
				"on a single lost packet; the 100ms bound is reachable only with "+
				"reelection-timeout >= 10x heartbeat-timeout (with the default "+
				"430ms the effective ceiling is 43ms)",
		), fs.Duration(
			"reelection-timeout", raft.DefaultReelectionTimeout,
			"Base of the randomized election timeout, actual timeout is in "+
				"[reelection, 2*reelection) (default 430ms); must be at least 10x "+
				"heartbeat-timeout; also gates the pre-vote suppression window "+
				"[reelection, 2*reelection); must be greater than 2x "+
				"raft.TCPRPCTimeout (the fixed quorum check window; not scaled "+
				"by -tcp-rpc-timeout)",
		), fs.Duration(
			"ticker-timeout", raft.DefaultTickerTimeout,
			"Election timer polling tick (default 20ms); must be at most "+
				"reelection-timeout/10",
		)
}

// addTransportFlags регистрирует три флага тайм-аутов TCP-транспорта и
// возвращает их указатели.
func addTransportFlags(fs *flag.FlagSet) (connect, rpc, snapshot *time.Duration) {
	return fs.Duration(
			"tcp-connect-timeout", raft.ConnectionTCPRPCTimeout,
			"Timeout for establishing a TCP connection (DialTimeout); must not "+
				"exceed -tcp-rpc-timeout (connection establishment plus "+
				"the RPC must fit within 2x -tcp-rpc-timeout) and together with "+
				"it must not exceed the fixed quorum check window of 400ms = 2x "+
				"raft.TCPRPCTimeout (the window is a constant and is not "+
				"scaled by this flag) (default 165ms)",
		), fs.Duration(
			"tcp-rpc-timeout", raft.TCPRPCTimeout,
			"Timeout for one RPC exchange with a peer; the quorum check window "+
				"is fixed at 400ms (2x raft.TCPRPCTimeout, a compile-time "+
				"constant) and does NOT scale with this flag; the effective RPC "+
				"window on a new connection is connect + rpc; the sum is capped "+
				"by connect + rpc <= 400, so this flag is bounded above by "+
				"400 - connect (with the default connect of 165ms, rpc <= 235ms) "+
				"(default 200ms)",
		), fs.Duration(
			"install-snapshot-timeout", raft.InstallSnapshotTimeout,
			"Base of the snapshot transfer deadline; the final deadline scales "+
				"with the data volume (blocks of 256KiB); must be at least "+
				"-tcp-rpc-timeout; the flag controls both sides of the "+
				"transfer - the sender deadline and the receiver response window "+
				"(max with responseTimeout), including small snapshots below "+
				"256KiB (default 310ms)",
		)
}

func addTraceFlags(fs *flag.FlagSet) (traceCMLogFileFlag, traceKVLogFileFlag *string, traceLogLevelFlag *int) {
	return fs.String("trace-cm-log-file", "", "Trace consensus module log file path (empty = stderr)"),
		fs.String("trace-kv-log-file", "", "Trace key-value database log file path (empty = stderr)"),
		fs.Int("trace-log-level", 1, "Trace log level for the raft and kvservice packages")
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
