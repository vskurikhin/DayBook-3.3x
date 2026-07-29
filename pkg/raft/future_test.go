package raft

import (
	"bytes"
	"encoding/gob"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// --- deferError ---

func TestDeferErrorInit(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))
	if d.errCh == nil {
		t.Fatal("errCh is nil after init")
	}
}

func TestDeferErrorRespond(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))
	testErr := errors.New("test error")
	go d.respond(testErr)
	if err := d.Error(); err != testErr {
		t.Fatalf("got %v, want %v", err, testErr)
	}
}

func TestDeferErrorRespondNil(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))
	go d.respond(nil)
	if err := d.Error(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestDeferErrorShutdown(t *testing.T) {
	shutdownCh := make(chan struct{})
	d := &deferError{}
	d.init(shutdownCh)
	close(shutdownCh)
	if err := d.Error(); err != ErrRaftShutdown {
		t.Fatalf("got %v, want ErrRaftShutdown", err)
	}
}

func TestDeferErrorMultipleRespond(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))
	d.respond(errors.New("first"))
	d.respond(errors.New("second"))
	if err := d.Error(); err == nil || err.Error() != "first" {
		t.Fatalf("got %v, want 'first'", err)
	}
}

func TestDeferErrorMultipleErrorCall(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))
	testErr := errors.New("persistent")
	go d.respond(testErr)
	err1 := d.Error()
	err2 := d.Error()
	if err1 != testErr || err2 != testErr {
		t.Fatalf("got %v and %v, want %v", err1, err2, testErr)
	}
}

func TestDeferErrorConcurrent(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		d.respond(nil)
	}()
	go func() {
		defer wg.Done()
		if err := d.Error(); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	}()
	wg.Wait()
}

func TestDeferErrorConcurrentCalls(t *testing.T) {
	shutdownCh := make(chan struct{})
	d := &deferError{}
	d.init(shutdownCh)
	close(shutdownCh)

	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = d.Error()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != ErrRaftShutdown {
			t.Errorf("errs[%d] = %v, want ErrRaftShutdown", i, err)
		}
	}
}

// --- logFuture ---

func TestLogFutureIndex(t *testing.T) {
	f := &logFuture{}
	f.log.Index = 42
	if f.Index() != 42 {
		t.Fatalf("got %d, want 42", f.Index())
	}
}

func TestLogFutureResponse(t *testing.T) {
	f := &logFuture{}
	f.response = "hello"
	if f.Response() != "hello" {
		t.Fatalf("got %v, want 'hello'", f.Response())
	}
}

func TestLogFutureImplementsApplyFuture(t *testing.T) {
	var _ ApplyFuture = (*logFuture)(nil)
}

// --- errorFuture ---

func TestErrorFutureNotLeader(t *testing.T) {
	f := errorFuture{ErrNotLeader}
	if f.Error() != ErrNotLeader {
		t.Fatalf("got %v, want ErrNotLeader", f.Error())
	}
}

func TestErrorFutureIndex(t *testing.T) {
	f := errorFuture{ErrNotLeader}
	if f.Index() != 0 {
		t.Fatalf("got %d, want 0", f.Index())
	}
}

func TestErrorFutureResponse(t *testing.T) {
	f := errorFuture{ErrNotLeader}
	if f.Response() != nil {
		t.Fatalf("got %v, want nil", f.Response())
	}
}

// --- Integration tests with Raft ---

// CaptureFSM сохраняет все закоммиченные записи и возвращает Index как Response.
type CaptureFSM struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (f *CaptureFSM) Apply(entry *LogEntry) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, *entry)
	return entry.Index
}

func (f *CaptureFSM) Entries() []LogEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]LogEntry{}, f.entries...)
}

// captureCommitFSM отправляет данные и в CaptureFSM, и в CommitChannelFSM.
type captureCommitFSM struct {
	*CaptureFSM
	commitChanFSM *CommitChannelFSM
}

func (f *captureCommitFSM) Apply(entry *LogEntry) any {
	resp := f.CaptureFSM.Apply(entry)
	f.commitChanFSM.Apply(entry)
	return resp
}

// copyBytes returns a copy of the given byte slice.
func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// testServerWithFSM creates a single-node ConsensusModule with the given FSM.
func testServerWithFSM(t testing.TB, fsm FSM) *ConsensusModule {
	t.Helper()
	storage := NewMapStorage()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(0); err != nil {
		t.Fatal(err)
	}
	storage.Set("currentTerm", copyBytes(buf.Bytes()))
	buf.Reset()
	if err := gob.NewEncoder(&buf).Encode(-1); err != nil {
		t.Fatal(err)
	}
	storage.Set("votedFor", copyBytes(buf.Bytes()))
	var logBuf bytes.Buffer
	if err := gob.NewEncoder(&logBuf).Encode([]LogEntry{}); err != nil {
		t.Fatal(err)
	}
	storage.Set("log", copyBytes(logBuf.Bytes()))

	ready := make(chan any)
	close(ready)

	cm := NewConsensusModule(0, []int{}, nil, storage, fsm, ready)
	return cm
}

// testFSMHarness создаёт одноузловой Harness с CaptureFSM для тестирования
// ApplyFuture.Response().
func testFSMHarness(t *testing.T) (*Harness, *CaptureFSM) {
	t.Helper()
	n := 1
	ns := make([]*Server, n)
	storage := make([]*MapStorage, n)
	commitChans := make([]chan CommitEntry, n)
	commits := make([][]CommitEntry, n)
	connected := make([]bool, n)
	alive := make([]bool, n)
	ready := make(chan any)

	capture := &CaptureFSM{}

	storage[0] = NewMapStorage()
	commitChans[0] = make(chan CommitEntry)
	fsm := &captureCommitFSM{
		CaptureFSM:    capture,
		commitChanFSM: NewCommitChannelFSM(commitChans[0]),
	}
	ns[0] = New(Config{
		ServerID: 0,
	}, storage[0], ready, fsm)
	ns[0].Serve(":0")
	alive[0] = true
	connected[0] = true
	close(ready)

	h := &Harness{
		cluster:     ns,
		storage:     storage,
		commitChans: commitChans,
		commits:     commits,
		connected:   connected,
		alive:       alive,
		n:           n,
		t:           t,
	}
	go h.collectCommits(0)
	return h, capture
}

// waitForLeader polls the CM state until it becomes Leader or timeout elapses.
func waitForLeader(t testing.TB, cm *ConsensusModule, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		cm.mu.Lock()
		isLeader := cm.state == Leader
		cm.mu.Unlock()
		if isLeader {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for leader election")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSingleNodeApply(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()
	capture := &CaptureFSM{}
	cm := testServerWithFSM(t, capture)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	future := cm.Apply("cmd1", 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if future.Index() < 0 {
		t.Fatalf("got Index=%d, want >= 0", future.Index())
	}
}

func TestSingleNodeApplyOrder(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()
	capture := &CaptureFSM{}
	cm := testServerWithFSM(t, capture)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	f1 := cm.Apply("cmd1", 0)
	f2 := cm.Apply("cmd2", 0)
	f3 := cm.Apply("cmd3", 0)

	// Дожидаемся завершения всех Apply, чтобы индексы были назначены.
	for _, f := range []ApplyFuture{f1, f2, f3} {
		if err := f.Error(); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
	}

	i1 := f1.Index()
	i2 := f2.Index()
	i3 := f3.Index()

	if !(i1 < i2 && i2 < i3) {
		t.Fatalf("expected i1 < i2 < i3, got %d, %d, %d", i1, i2, i3)
	}
}

func TestSingleNodeApplyResponse(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()
	h, capture := testFSMHarness(t)
	defer h.Shutdown()
	waitForLeader(t, h.cluster[0].cm, 800*time.Millisecond)

	future := h.cluster[0].Apply("cmd1", 0)
	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	resp := future.Response().(int)
	if resp != future.Index() {
		t.Fatalf("got Response=%d, want Index=%d", resp, future.Index())
	}
	_ = capture
}

func TestApplyOnFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*Quantum*time.Millisecond)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderId, _ := h.CheckSingleLeader()
	followerId := (leaderId + 1) % 3

	future := h.cluster[followerId].Apply(42, 0)
	if err := future.Error(); err != ErrNotLeader {
		t.Fatalf("got %v, want ErrNotLeader", err)
	}
}

func TestApplyAfterShutdown(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*Quantum*time.Millisecond)()
	capture := &CaptureFSM{}
	cm := testServerWithFSM(t, capture)
	time.Sleep(50 * time.Millisecond)

	cm.Stop()
	time.Sleep(10 * time.Millisecond)

	future := cm.Apply("cmd", 0)
	if err := future.Error(); err != ErrRaftShutdown {
		t.Fatalf("got %v, want ErrRaftShutdown", err)
	}
}

func TestMultipleErrorCall(t *testing.T) {
	d := &deferError{}
	d.init(make(chan struct{}))

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = d.Error()
		}(i)
	}

	time.Sleep(10 * time.Millisecond)
	d.respond(nil)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("errs[%d] = %v, want nil", i, err)
		}
	}
}

func TestFSMApplyInOrder(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()
	capture := &CaptureFSM{}
	cm := testServerWithFSM(t, capture)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)

	for i := 0; i < 5; i++ {
		future := cm.Apply(i, 0)
		if err := future.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
	}

	capture.mu.Lock()
	entries := make([]int, len(capture.entries))
	for i, e := range capture.entries {
		entries[i] = e.Command.(int)
	}
	capture.mu.Unlock()

	for i := 0; i < 5; i++ {
		if entries[i] != i {
			t.Fatalf("entries[%d] = %d, want %d", i, entries[i], i)
		}
	}
}

func TestFutureNoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()

	capture := &CaptureFSM{}
	cm := testServerWithFSM(t, capture)
	defer cm.Stop()

	waitForLeader(t, cm, 800*time.Millisecond)
	future := cm.Apply("leaktest", 0)
	future.Error()
}

func TestFutureErrorNoLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*Quantum*time.Millisecond)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	leaderId, _ := h.CheckSingleLeader()
	followerId := (leaderId + 1) % 3

	future := h.cluster[followerId].Apply("leaktest", 0)
	future.Error()
}

func TestBatchApplyOrderAndIndices(t *testing.T) {
	defer leaktest.CheckTimeout(t, 500*Quantum*time.Millisecond)()
	h, capture := testFSMHarness(t)
	defer h.Shutdown()
	waitForLeader(t, h.cluster[0].cm, 800*time.Millisecond)

	values := []int{42, 55, 81}
	futures := make([]ApplyFuture, len(values))
	for i, v := range values {
		futures[i] = h.cluster[0].Apply(v, 0)
	}

	for i, f := range futures {
		if err := f.Error(); err != nil {
			t.Fatalf("Apply %d failed: %v", i, err)
		}
		if f.Index() < 0 {
			t.Fatalf("Index %d < 0", f.Index())
		}
		if i > 0 && f.Index() <= futures[i-1].Index() {
			t.Fatalf("indices not increasing: %d <= %d", f.Index(), futures[i-1].Index())
		}
	}

	capture.mu.Lock()
	entries := make([]int, 0, len(capture.entries))
	for _, e := range capture.entries {
		if e.Type == LogCommand {
			entries = append(entries, e.Command.(int))
		}
	}
	capture.mu.Unlock()

	for i, v := range values {
		if entries[i] != v {
			t.Fatalf("entries[%d] = %d, want %d", i, entries[i], v)
		}
	}
}

func TestApplyInflightOnLeaderCrash(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*Quantum*time.Millisecond)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	origLeaderId, _ := h.CheckSingleLeader()

	for i := 0; i < 3; i++ {
		if i != origLeaderId {
			h.DisconnectPeer(i)
		}
	}
	sleepMs(50)

	future := h.cluster[origLeaderId].Apply(42, 0)
	// Даём runLeaderLoop время применить future до CrashPeer,
	// чтобы future оказалась в inflight перед остановкой.
	sleepMs(50)
	h.CrashPeer(origLeaderId)

	if err := future.Error(); err != ErrLeadershipLost && err != ErrRaftShutdown {
		t.Fatalf("got %v, want ErrLeadershipLost or ErrRaftShutdown", err)
	}
}

func TestApplyWithFollowerCrash(t *testing.T) {
	defer leaktest.CheckTimeout(t, 300*Quantum*time.Millisecond)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()
	followerId := (lid + 1) % 3

	future := h.cluster[lid].Apply(42, 0)
	h.CrashPeer(followerId)

	if err := future.Error(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	sleepMs(50)
	h.CheckCommittedN(42, 2)
}

func TestApplyNoQuorumThenNewLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, 100*Quantum*time.Millisecond)()
	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid, _ := h.CheckSingleLeader()

	h.DisconnectPeer((lid + 1) % 3)
	h.DisconnectPeer((lid + 2) % 3)
	sleepMs(50)

	future := h.cluster[lid].Apply(42, 0)
	sleepMs(50)

	// Force the leader to step down by sending AppendEntries with a higher term.
	// This simulates a new leader being elected (in a higher term) without needing
	// the disconnected followers to actually win an election.
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        (lid + 1) % 3,
		},
		Term:         100,
		LeaderID:     (lid + 1) % 3,
		PrevLogIndex: -1,
		PrevLogTerm:  -1,
		Entries:      []LogEntry{},
		LeaderCommit: -1,
	}
	if err := h.cluster[lid].cm.AppendEntries(args, &AppendEntriesReply{}); err != nil {
		t.Fatal(err)
	}
	sleepMs(100)

	if err := future.Error(); err != ErrLeadershipLost {
		t.Fatalf("got %v, want ErrLeadershipLost", err)
	}
}

// --- Benchmarks ---

func waitForLeaderB(t testing.TB, cm *ConsensusModule) {
	t.Helper()
	deadline := time.After(800 * time.Millisecond)
	for {
		cm.mu.Lock()
		isLeader := cm.state == Leader
		cm.mu.Unlock()
		if isLeader {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for leader election")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func BenchmarkApply(b *testing.B) {
	capture := &CaptureFSM{}
	cm := testServerWithFSM(b, capture)
	defer cm.Stop()

	waitForLeaderB(b, cm)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		future := cm.Apply(i, 0)
		future.Error()
	}
}

func BenchmarkBatchApply(b *testing.B) {
	capture := &CaptureFSM{}
	cm := testServerWithFSM(b, capture)
	defer cm.Stop()

	waitForLeaderB(b, cm)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			future := cm.Apply("cmd", 0)
			future.Error()
		}
	})
}
