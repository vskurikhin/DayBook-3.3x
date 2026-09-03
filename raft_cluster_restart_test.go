package raft

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
	"github.com/vskurikhin/raft/pkg/raft/transp"
)

// clusterNode — файловый стенд одного узла для теста полного рестарта
// кластера: FileStorage + FileSnapshot в отдельной директории.
type clusterNode struct {
	id        int
	dir       string
	cm        *ConsensusModule
	fsm       *snapshotTestFSM
	storage   *store.FileStorage
	store     *store.FileSnapshot
	transport *transp.InmemTransport
	peerIds   []int
}

// newClusterNode создаёт узел с реальными файловыми хранилищами.
func newClusterNode(t *testing.T, id, n int, baseDir string) *clusterNode {
	t.Helper()
	peerIds := make([]int, 0, n-1)
	for p := 0; p < n; p++ {
		if p != id {
			peerIds = append(peerIds, p)
		}
	}
	node := &clusterNode{
		id:      id,
		dir:     filepath.Join(baseDir, fmt.Sprintf("node-%d", id)),
		peerIds: peerIds,
	}
	node.start(t)
	return node
}

// start создаёт хранилища, транспорт, CM и готовит канал ready.
func (n *clusterNode) start(t *testing.T) {
	t.Helper()
	n.storage = store.NewFileStorage(n.dir)
	stor, err := store.NewFileSnapshot(n.dir, 2)
	if err != nil {
		t.Fatalf("node %d: NewFileSnapshot failed: %v", n.id, err)
	}
	n.store = stor
	n.fsm = newSnapshotTestFSM()
	n.transport = transp.NewInmemTransport(ServerAddress(fmt.Sprintf("raft-cluster-%d", n.id)))
	ready := make(chan any)
	n.cm = NewConsensusModule(n.id, n.peerIds, n.transport, n.storage, n.fsm, ready, stor)
	close(ready)
}

// stop останавливает узел.
func (n *clusterNode) stop() {
	n.cm.Stop()
	n.transport.Close()
}

// connect соединяет транспорт узла с транспортом другого.
func (n *clusterNode) connect(other *clusterNode) {
	n.transport.Connect(ServerID(other.id), other.transport)
	other.transport.Connect(ServerID(n.id), n.transport)
}

// isLeader возвращает true, если узел — лидер.
func (n *clusterNode) isLeader() bool {
	n.cm.mu.Lock()
	defer n.cm.mu.Unlock()
	return n.cm.cmState.state == Leader
}

// waitForClusterLeader ждёт появления лидера среди узлов и возвращает его.
func waitForClusterLeader(t *testing.T, nodes []*clusterNode) *clusterNode {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.isLeader() {
				return n
			}
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no leader elected")
	return nil
}

// applyClusterCommand применяет команду через текущего лидера. Кластер
// может переизбрать лидера во время фиксации (таймеры выборов в
// многоузловом стенде), поэтому при потере лидерства команда повторяется
// через нового лидера — как это делает реальный клиент.
func applyClusterCommand(t *testing.T, nodes []*clusterNode, cmd string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		leader := waitForClusterLeader(t, nodes)
		future := leader.cm.Apply(cmd, 0)
		if err := future.Error(); err == nil {
			return
		}
		// poll-интервал condition-wait (не фиксированная пауза).
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Apply %q failed after retries", cmd)
}

// TestSnapshot_ClusterFullRestart — регрессионный тест:
// после снимков и сжатия журналов полный рестарт ВСЕХ узлов
// кластера обязан восстановить FSM каждого узла из его снимка на диске,
// а кластер — продолжить принимать записи.
func TestSnapshot_ClusterFullRestart(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	const (
		numNodes      = 3
		numCommands   = 40
		snapThreshold = 8
	)

	baseDir := t.TempDir()
	nodes := make([]*clusterNode, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = newClusterNode(t, i, numNodes, baseDir)
	}
	// Полная связность транспортов.
	for i := 0; i < numNodes; i++ {
		for j := i + 1; j < numNodes; j++ {
			nodes[i].connect(nodes[j])
		}
	}

	waitForClusterLeader(t, nodes)
	for i := 0; i < numNodes; i++ {
		nodes[i].cm.SetSnapshotConfig(snapThreshold, 2, 50*time.Millisecond)
	}

	for i := 0; i < numCommands; i++ {
		applyClusterCommand(t, nodes, fmt.Sprintf("k%d=v%d", i, i))
	}

	// Ждём снимок и сжатие на КАЖДОМ узле.
	for _, n := range nodes {
		waitForSnapshotAndCompaction(t, n.cm, numCommands)
		waitForSnapshotQuiescence(t, n.cm, n.storage, 10*time.Second)
	}

	// Совокупное состояние до рестарта: все узлы сошлись на всех ключах.
	deadline := time.Now().Add(10 * time.Second)
	for _, n := range nodes {
		for time.Now().Before(deadline) {
			allPresent := true
			for i := 0; i < numCommands; i++ {
				key := fmt.Sprintf("k%d", i)
				want := fmt.Sprintf("v%d", i)
				if n.fsm.getState(key) != want {
					allPresent = false
					break
				}
			}
			if allPresent {
				break
			}
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(20 * time.Millisecond)
		}
		for i := 0; i < numCommands; i++ {
			key := fmt.Sprintf("k%d", i)
			want := fmt.Sprintf("v%d", i)
			if v := n.fsm.getState(key); v != want {
				t.Fatalf("before restart: node %d %s = %q, want %q", n.id, key, v, want)
			}
		}
	}

	// Полный рестарт всех узлов.
	for _, n := range nodes {
		n.stop()
	}
	for _, n := range nodes {
		n.start(t)
	}
	for i := 0; i < numNodes; i++ {
		for j := i + 1; j < numNodes; j++ {
			nodes[i].connect(nodes[j])
		}
	}
	for _, n := range nodes {
		// defer (а не t.Cleanup): leaktest.CheckTimeout отрабатывает до
		// t.Cleanup, и узлы должны быть остановлены до его проверки.
		defer n.stop()
	}

	waitForClusterLeader(t, nodes)

	// Кластер продолжает принимать записи.
	applyClusterCommand(t, nodes, "k40=v40")

	// Каждый узел восстановил состояние до линии сжатия (k0),
	// доиграл суффикс (k39) и догнал свежую запись (k40).
	deadline = time.Now().Add(10 * time.Second)
	for _, n := range nodes {
		for time.Now().Before(deadline) {
			if n.fsm.getState("k0") == "v0" &&
				n.fsm.getState("k39") == "v39" &&
				n.fsm.getState("k40") == "v40" {
				break
			}
			// poll-интервал condition-wait (не фиксированная пауза).
			time.Sleep(20 * time.Millisecond)
		}
		if v := n.fsm.getState("k0"); v != "v0" {
			t.Fatalf("node %d: k0 = %q after restart, want v0", n.id, v)
		}
		if v := n.fsm.getState("k39"); v != "v39" {
			t.Fatalf("node %d: k39 = %q after restart, want v39", n.id, v)
		}
		if v := n.fsm.getState("k40"); v != "v40" {
			t.Fatalf("node %d: k40 = %q after restart, want v40", n.id, v)
		}
		n.cm.mu.Lock()
		snapIdx := n.cm.cmState.lastSnapshotIndex
		n.cm.mu.Unlock()
		if snapIdx < 0 {
			t.Fatalf("node %d: lastSnapshotIndex = %d, want >= 0", n.id, snapIdx)
		}
	}
}
