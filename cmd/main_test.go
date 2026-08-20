package main

import (
	"net"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/internal/config"
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
	peer := raft.NewServer(1, []int{}, raft.NewCommitChannelFSM(commitChannel), peerReady)
	peer.Serve(":0")
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
