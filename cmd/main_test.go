package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/internal/config"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// TestRunWithEmptyPeers запускает узел без соседей через runWith и
// останавливает его как следствие:
// - после теста не остаётся живых узловых горутин (leaktest).
// - порядок cleanup (LIFO): stop → проверка leaktest.
func TestRunWithEmptyPeers(t *testing.T) {
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	values := newTestValues(t)
	values.Peers = map[int]net.Addr{}

	stop, err := runWith(&values)
	if err != nil {
		t.Fatalf("runWith returned unexpectedly: %v", err)
	}
	t.Cleanup(stop)
}

// TestRunWithNonDefaultNodeFlags запускает узел с нестандартными значениями
// всех четырёх параметров узла: тайм-аут TCP RPC, размер пула, интервал
// и порог снимков. Проверка значений внутри узла — поведенческая
// (старт/стоп без ошибок, leaktest чист); маршрут значений теперь прямой —
// тайм-аут и пул передаются аргументами NewTCPTransport в runWith, а
// подстановка дефолтов транспорта покрыта тестами transport_tcp_test.go;
// параметры снимков доказываются юнит-тестами server_test.go.
func TestRunWithNonDefaultNodeFlags(t *testing.T) {
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	values := newTestValues(t)
	values.Peers = map[int]net.Addr{}
	values.TCPRPCTimeout = 500 * time.Millisecond
	values.MaxPool = 8
	values.SnapshotInterval = time.Second
	values.SnapshotThreshold = 32

	stop, err := runWith(&values)
	if err != nil {
		t.Fatalf("runWith with non-default node flags returned unexpectedly: %v", err)
	}
	t.Cleanup(stop)
}

// TestRunWithPeerConnect запускает узел, подключённый к серверу-соседу.
func TestRunWithPeerConnect(t *testing.T) {
	t.Cleanup(leaktest.CheckTimeout(t, raft.LeaktestBudget))

	peerReady := make(chan any)
	commitChannel := make(chan raft.CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitChannel {
		}
	}()
	peerTransport, err := raft.NewTCPTransport(":0", 0, 0)
	if err != nil {
		t.Fatalf("raft.NewTCPTransport: %v", err)
	}
	peer := raft.New(&raft.Config{
		Fsm:       raft.NewCommitChannelFSM(commitChannel),
		PeerIds:   []int{},
		ServerID:  1,
		Storage:   store.NewMapStorage(),
		Transport: peerTransport,
	}, peerReady)
	peer.Serve()
	close(peerReady)
	t.Cleanup(func() {
		peer.Shutdown()
		close(commitChannel)
		<-readerDone
	})

	values := newTestValues(t)
	values.Peers = map[int]net.Addr{1: peer.GetListenAddr()}

	stop, err := runWith(&values)
	if err != nil {
		t.Fatalf("runWith returned: %v", err)
	}
	t.Cleanup(stop)
}

// newTestValues собирает конфигурацию узла для тестов:
// слушатели на :0 (без фиксированных портов), хранилище — во
// временном каталоге теста.
func newTestValues(t *testing.T) config.Values {
	t.Helper()
	httpAddr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	rpcAddr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	return config.Values{
		Number:      0,
		HTTPAddress: httpAddr,
		RPCAddress:  rpcAddr,
		DataDir:     t.TempDir(),
	}
}

// TestStartPprofDisabledByDefault проверяет, что без адреса профилирования
// не создаётся ни сервера, ни накладных расходов среды выполнения.
func TestStartPprofDisabledByDefault(t *testing.T) {
	values := newTestValues(t)
	values.BlockProfileRate = 1
	values.MutexProfileFraction = 1

	stop := startPprof(&values)
	if stop == nil {
		t.Fatal("startPprof returned nil stop function")
	}
	stop()

	// SetMutexProfileFraction с отрицательным аргументом только возвращает
	// текущее значение, не изменяя его.
	if fraction := runtime.SetMutexProfileFraction(-1); fraction != 0 {
		t.Errorf("MutexProfileFraction = %d, want 0", fraction)
	}
}

// TestPprofMuxEndpoints проверяет доступность профилей на отдельном
// мультиплексоре.
func TestPprofMuxEndpoints(t *testing.T) {
	srv := httptest.NewServer(pprofMux())
	defer srv.Close()

	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/block",
		"/debug/pprof/mutex",
		"/debug/pprof/profile?seconds=1",
	}
	for _, path := range paths {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}
}

// TestStartPprofServesProfiles проверяет, что при заданном адресе поднимается
// отдельный сервер профилирования и он завершается функцией остановки.
func TestStartPprofServesProfiles(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	values := newTestValues(t)
	values.PprofAddress = addr
	stop := startPprof(&values)
	defer stop()

	url := "http://" + addr + "/debug/pprof/block"
	var resp *http.Response
	for range 50 {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp != nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", url, resp.StatusCode)
		}
	}
}
