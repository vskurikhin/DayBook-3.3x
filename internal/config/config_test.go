package config

import (
	"bytes"
	"flag"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vskurikhin/raft"
)

func TestParsePeerAddress(t *testing.T) {
	// with rpc:// prefix
	addr := parsePeerAddress("rpc://127.0.0.1:9990")
	if addr.Network() != "tcp" {
		t.Errorf("parsePeerAddress('rpc://...') network = %s, want tcp", addr.Network())
	}
	if addr.String() != "127.0.0.1:9990" {
		t.Errorf("parsePeerAddress('rpc://...') addr = %s, want 127.0.0.1:9990", addr.String())
	}

	// without prefix (backward compatibility)
	addr2 := parsePeerAddress("127.0.0.1:9991")
	if addr2.Network() != "tcp" {
		t.Errorf("parsePeerAddress('host:port') network = %s, want tcp", addr2.Network())
	}
	if addr2.String() != "127.0.0.1:9991" {
		t.Errorf("parsePeerAddress('host:port') addr = %s, want 127.0.0.1:9991", addr2.String())
	}
}

func TestAddrAppendWithPrefix(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 0, "rpc://127.0.0.1:9990")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestAddrAppendWithoutPrefix(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 1, "127.0.0.1:9991")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("peers[1] = %s, want 127.0.0.1:9991", peers[1].String())
	}
}

func TestAddrAppendTrimmed(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = addrAppend(peers, 0, "  rpc://127.0.0.1:9990  ")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestParsePeers(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = parsePeers(peers, "0=127.0.0.1:9990,1=127.0.0.1:9991")
	if len(peers) != 2 {
		t.Fatalf("len = %d, want 2", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
	if peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("peers[1] = %s, want 127.0.0.1:9991", peers[1].String())
	}
}

func TestParsePeersSingle(t *testing.T) {
	peers := make(map[int]net.Addr)
	peers = parsePeers(peers, "0=127.0.0.1:9990")
	if len(peers) != 1 {
		t.Fatalf("len = %d, want 1", len(peers))
	}
	if peers[0].String() != "127.0.0.1:9990" {
		t.Errorf("peers[0] = %s, want 127.0.0.1:9990", peers[0].String())
	}
}

func TestParseFlags(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-rpc-addr", ":9999", "-number", "0", "-peers", "1=127.0.0.1:9991"}

	v := ParseFlags()
	if v.Number != 0 {
		t.Errorf("RequestRate = %d, want 0", v.Number)
	}
	if v.RPCAddress.String() != ":9999" &&
		v.RPCAddress.String() != "0.0.0.0:9999" &&
		v.RPCAddress.String() != "[::]:9999" {
		t.Errorf("Address = %s, want :9999, 0.0.0.0:9999, or [::]:9999", v.RPCAddress.String())
	}
	if len(v.Peers) != 1 {
		t.Errorf("len(Peers) = %d, want 1", len(v.Peers))
	}
	if v.Peers[1].String() != "127.0.0.1:9991" {
		t.Errorf("Peers[1] = %s, want 127.0.0.1:9991", v.Peers[1].String())
	}
}

func TestParseFlagsNoPeers(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-rpc-addr", ":9990", "-number", "1"}

	v := ParseFlags()
	if v.Number != 1 {
		t.Errorf("RequestRate = %d, want 1", v.Number)
	}
	if v.RPCAddress.String() != ":9990" &&
		v.RPCAddress.String() != "0.0.0.0:9990" &&
		v.RPCAddress.String() != "[::]:9990" {
		t.Errorf("Address = %s, want :9990, 0.0.0.0:9990, or [::]:9990", v.RPCAddress.String())
	}
	if len(v.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(v.Peers))
	}
}

func TestParseFlagsDefaultAddr(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "2"}

	v := ParseFlags()
	if v.Number != 2 {
		t.Errorf("RequestRate = %d, want 2", v.Number)
	}
	if v.RPCAddress.String() != ":9990" &&
		v.RPCAddress.String() != "0.0.0.0:9990" &&
		v.RPCAddress.String() != "[::]:9990" {
		t.Errorf("Address = %s, want :9990", v.RPCAddress.String())
	}
}

func TestParseFlagsTraceLogDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.TraceLogLevel != 1 {
		t.Errorf("TraceLogLevel = %d, want default 1", v.TraceLogLevel)
	}
	if v.TraceCMLogFile != "" {
		t.Errorf("TraceCMLogFile = %q, want default empty", v.TraceCMLogFile)
	}
	if v.TraceKVLogFile != "" {
		t.Errorf("TraceKVLogFile = %q, want default empty", v.TraceKVLogFile)
	}
}

// TestParseFlagsNodeDefaults — сводная сверка контракта дефолтов четырёх
// параметров узла. Таблица «флаг → ожидаемое значение» против единого
// источника — raft.TCPRPCTimeout / DefaultMaxPool /
// raft.DefaultSnapshotInterval / raft.DefaultSnapshotThreshold. Защита
// от рассинхрона дефолтов между internal/config и пакетом raft (RISK-023).
func TestParseFlagsNodeDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"tcp-rpc-timeout", v.TCPRPCTimeout, raft.TCPRPCTimeout},
		{"max-pool", v.MaxPool, DefaultMaxPool},
		{"snapshot-interval", v.SnapshotInterval, raft.DefaultSnapshotInterval},
		{"snapshot-threshold", v.SnapshotThreshold, raft.DefaultSnapshotThreshold},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want default %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseFlagsTraceLog(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--trace-log-level", "16",
		"--trace-cm-log-file", "/tmp/raft-trace-cm.log",
		"--trace-kv-log-file", "/tmp/raft-trace-kv.log",
	}

	v := ParseFlags()
	if v.TraceLogLevel != 16 {
		t.Errorf("TraceLogLevel = %d, want 16", v.TraceLogLevel)
	}
	if v.TraceCMLogFile != "/tmp/raft-trace-cm.log" {
		t.Errorf("TraceCMLogFile = %q, want /tmp/raft-trace-cm.log", v.TraceCMLogFile)
	}
	if v.TraceKVLogFile != "/tmp/raft-trace-kv.log" {
		t.Errorf("TraceKVLogFile = %q, want /tmp/raft-trace-kv.log", v.TraceKVLogFile)
	}
}

func TestParseFlagsDataDir(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "0", "--data-dir", "/tmp/raft-data-test"}

	v := ParseFlags()
	if v.Number != 0 {
		t.Errorf("Number = %d, want 0", v.Number)
	}
	if v.DataDir != "/tmp/raft-data-test" {
		t.Errorf("DataDir = %q, want /tmp/raft-data-test", v.DataDir)
	}
}

func TestParseFlagsDataDirDefault(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.DataDir != "" {
		t.Errorf("DataDir = %q, want default empty", v.DataDir)
	}
}

func TestValuesImplements(t *testing.T) {
	v := Values{
		RPCAddress: mustParseAddr(t, "127.0.0.1:9990"),
		Number:     0,
		Peers:      map[int]net.Addr{1: mustParseAddr(t, "127.0.0.1:9991")},
	}
	if v.Number != 0 {
		t.Errorf("RequestRate = %d, want 0", v.Number)
	}
	if v.RPCAddress.String() != "127.0.0.1:9990" {
		t.Errorf("Address = %s, want 127.0.0.1:9990", v.RPCAddress.String())
	}
	if len(v.Peers) != 1 {
		t.Errorf("len(Peers) = %d, want 1", len(v.Peers))
	}
}

func mustParseAddr(t *testing.T, s string) net.Addr {
	t.Helper()
	addr, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		t.Fatalf("parse addr %q: %v", s, err)
	}
	return addr
}

func TestParseFlagsProfilingDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.PprofAddress != "" {
		t.Errorf("PprofAddress = %q, want default empty", v.PprofAddress)
	}
	if v.BlockProfileRate != 0 {
		t.Errorf("BlockProfileRate = %d, want default 0", v.BlockProfileRate)
	}
	if v.MutexProfileFraction != 0 {
		t.Errorf("MutexProfileFraction = %d, want default 0", v.MutexProfileFraction)
	}
}

func TestParseFlagsProfiling(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--pprof-addr", ":6060",
		"--block-profile-rate", "1",
		"--mutex-profile-fraction", "5",
	}

	v := ParseFlags()
	if v.PprofAddress != ":6060" {
		t.Errorf("PprofAddress = %q, want :6060", v.PprofAddress)
	}
	if v.BlockProfileRate != 1 {
		t.Errorf("BlockProfileRate = %d, want 1", v.BlockProfileRate)
	}
	if v.MutexProfileFraction != 5 {
		t.Errorf("MutexProfileFraction = %d, want 5", v.MutexProfileFraction)
	}
}

func TestParseFlagsMaxPoolDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.MaxPool != DefaultMaxPool {
		t.Errorf("MaxPool = %d, want default %d", v.MaxPool, DefaultMaxPool)
	}
	if DefaultMaxPool != 4 {
		t.Errorf("DefaultMaxPool = %d, want 4", DefaultMaxPool)
	}
}

func TestParseFlagsMaxPool(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--max-pool", "8",
	}

	v := ParseFlags()
	if v.MaxPool != 8 {
		t.Errorf("MaxPool = %d, want 8", v.MaxPool)
	}
}

// TestParseFlagsTCPRPCTimeoutDefaults проверяет дефолт флага -tcp-rpc-timeout:
// без флага поле Values.TCPRPCTimeout равно raft.TCPRPCTimeout (191 мс).
// Одновременно это защита от рассинхрона дефолта между internal/config
// и пакетом raft (RISK-023): единый источник — алиас raft.TCPRPCTimeout.
func TestParseFlagsTCPRPCTimeoutDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.TCPRPCTimeout != raft.TCPRPCTimeout {
		t.Errorf("TCPRPCTimeout = %v, want default %v", v.TCPRPCTimeout, raft.TCPRPCTimeout)
	}
	if raft.TCPRPCTimeout != 191*time.Millisecond {
		t.Errorf("raft.TCPRPCTimeout = %v, want 191ms", raft.TCPRPCTimeout)
	}
}

// TestParseFlagsTCPRPCTimeout проверяет, что нестандартное значение
// флага -tcp-rpc-timeout доезжает в поле Values.TCPRPCTimeout.
func TestParseFlagsTCPRPCTimeout(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--tcp-rpc-timeout", "500ms",
	}

	v := ParseFlags()
	if v.TCPRPCTimeout != 500*time.Millisecond {
		t.Errorf("TCPRPCTimeout = %v, want 500ms", v.TCPRPCTimeout)
	}
}

// TestParseFlagsSnapshotDefaults проверяет дефолты флагов снимков: без флага
// поля Values.SnapshotInterval/SnapshotThreshold равны экспортированным
// дефолтам пакета raft. Это защита от рассинхрона дефолтов между
// internal/config и пакетом raft (RISK-023): единый источник —
// raft.DefaultSnapshotInterval/raft.DefaultSnapshotThreshold.
func TestParseFlagsSnapshotDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	if v.SnapshotInterval != raft.DefaultSnapshotInterval {
		t.Errorf("SnapshotInterval = %v, want default %v", v.SnapshotInterval, raft.DefaultSnapshotInterval)
	}
	if v.SnapshotThreshold != raft.DefaultSnapshotThreshold {
		t.Errorf("SnapshotThreshold = %d, want default %d", v.SnapshotThreshold, raft.DefaultSnapshotThreshold)
	}
	if raft.DefaultSnapshotInterval != 3*time.Second {
		t.Errorf("raft.DefaultSnapshotInterval = %v, want 3s", raft.DefaultSnapshotInterval)
	}
	if raft.DefaultSnapshotThreshold != 1024 {
		t.Errorf("raft.DefaultSnapshotThreshold = %d, want 1024", raft.DefaultSnapshotThreshold)
	}
}

// TestParseFlagsSnapshot проверяет, что нестандартные значения флагов
// -snapshot-interval и -snapshot-threshold доезжают в поля Values.
func TestParseFlagsSnapshot(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--snapshot-interval", "10s",
		"--snapshot-threshold", "2048",
	}

	v := ParseFlags()
	if v.SnapshotInterval != 10*time.Second {
		t.Errorf("SnapshotInterval = %v, want 10s", v.SnapshotInterval)
	}
	if v.SnapshotThreshold != 2048 {
		t.Errorf("SnapshotThreshold = %d, want 2048", v.SnapshotThreshold)
	}
}

// TestParseFlagsTimingDefaults проверяет дефолты четырёх временных флагов:
// без флага поля Values равны экспортированным дефолтам пакета raft.
func TestParseFlagsTimingDefaults(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"raft", "-number", "1"}

	v := ParseFlags()
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"heartbeat-timeout", v.HeartbeatTimeout, raft.DefaultHeartbeatTimeout},
		{"ticker-timeout", v.TickerTimeout, raft.DefaultTickerTimeout},
		{"reelection-timeout", v.ReelectionTimeout, raft.DefaultReelectionTimeout},
		{"apply-batch-interval", v.ApplyBatchInterval, raft.DefaultApplyBatchInterval},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want default %v", tc.name, tc.got, tc.want)
		}
	}
}

// TestParseFlagsTiming проверяет, что нестандартные значения временных
// флагов доезжают в поля Values.
func TestParseFlagsTiming(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{
		"raft", "-number", "1",
		"--heartbeat-timeout", "40ms",
		"--ticker-timeout", "30ms",
		"--reelection-timeout", "500ms",
		"--apply-batch-interval", "60ms",
	}

	v := ParseFlags()
	if v.HeartbeatTimeout != 40*time.Millisecond {
		t.Errorf("HeartbeatTimeout = %v, want 40ms", v.HeartbeatTimeout)
	}
	if v.TickerTimeout != 30*time.Millisecond {
		t.Errorf("TickerTimeout = %v, want 30ms", v.TickerTimeout)
	}
	if v.ReelectionTimeout != 500*time.Millisecond {
		t.Errorf("ReelectionTimeout = %v, want 500ms", v.ReelectionTimeout)
	}
	if v.ApplyBatchInterval != 60*time.Millisecond {
		t.Errorf("ApplyBatchInterval = %v, want 60ms", v.ApplyBatchInterval)
	}
}

// TestValidateTimingFlags_Delegation проверяет, что обёртка делегирует
// проверку raft.ValidateTiming: валидная четвёрка даёт nil, нарушение
// даёт ошибку с именем параметра.
func TestValidateTimingFlags_Delegation(t *testing.T) {
	if err := validateTimingFlags(33*time.Millisecond, 21*time.Millisecond, 381*time.Millisecond, 50*time.Millisecond); err != nil {
		t.Errorf("validateTimingFlags(defaults) = %v, want nil", err)
	}

	err := validateTimingFlags(200*time.Millisecond, 21*time.Millisecond, 381*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("validateTimingFlags = nil, want error for heartbeat 200ms")
	}
	if !strings.Contains(err.Error(), "heartbeat-timeout") || !strings.Contains(err.Error(), "95") {
		t.Errorf("error %q must mention heartbeat-timeout and 95", err)
	}
}

// TestParseFlagsTimingFailFast проверяет, что невалидная комбинация
// временных флагов отклоняется с сообщением о параметре, значении и
// требовании (проверяется на уровне обёртки, т.к. log.Fatalf завершает
// процесс).
func TestParseFlagsTimingFailFast(t *testing.T) {
	err := validateTimingFlags(33*time.Millisecond, 21*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("validateTimingFlags = nil, want error for C1 violation")
	}
	for _, want := range []string{"reelection-timeout", "heartbeat-timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestParseFlagsTimingHelp проверяет, что подсказки временных флагов
// содержат предупреждения о межпараметрических связях и фиксированном
// check-quorum. Проверка по строкам подсказок, которые выводятся в -help.
func TestParseFlagsTimingHelp(t *testing.T) {
	fs := flag.NewFlagSet("raft", flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.Duration("heartbeat-timeout", raft.DefaultHeartbeatTimeout,
		"Leader heartbeat interval (default 33ms). Upper bound derives from the "+
			"fixed 382ms check-quorum timeout: beyond 95ms a leader steps down "+
			"on a single lost packet")
	fs.Duration("ticker-timeout", raft.DefaultTickerTimeout,
		"Election timer polling tick (default 21ms); must be at most "+
			"reelection-timeout/10")
	fs.Duration("reelection-timeout", raft.DefaultReelectionTimeout,
		"Base of the randomized election timeout, actual timeout is in "+
			"[reelection, 2*reelection) (default 381ms); must be at least 10x "+
			"heartbeat-timeout; also gates the pre-vote suppression window "+
			"[reelection, 2*reelection); check-quorum stays fixed at 382ms and "+
			"does not scale with this flag")
	fs.Duration("apply-batch-interval", raft.DefaultApplyBatchInterval,
		"Leader-side safety-net apply batching interval (default 50ms); must "+
			"not exceed reelection-timeout")
	_ = fs.Parse([]string{"-h"})

	help := buf.String()
	for _, want := range []string{
		"382", "95ms", "check-quorum", "reelection-timeout/10", "10x heartbeat-timeout", "reelection-timeout",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help output does not contain %q", want)
		}
	}
}
