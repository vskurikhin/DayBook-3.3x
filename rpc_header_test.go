package raft

import (
	"io"
	"testing"

	"github.com/fortytw2/leaktest"
)

// NoopFSM — FSM, ничего не делающая в Apply.
type NoopFSM struct{}

func (NoopFSM) Apply(*LogEntry) any { return nil }
func (NoopFSM) Snapshot() (FSMSnapshot, error) {
	return nil, ErrNotImplemented
}

func (NoopFSM) Restore(_ io.ReadCloser) error {
	return ErrNotImplemented
}

// testWithHeader — вспомогательный тип для теста checkRPCHeader.
type testWithHeader struct {
	RPCHeader
}

func (r *testWithHeader) GetRPCHeader() RPCHeader {
	return r.RPCHeader
}

// --- RPCHeader struct ---

func TestRPCHeaderStruct(t *testing.T) {
	h := RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 42}
	if h.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", h.ProtocolVersion, ProtocolVersion)
	}
	if h.ServerID != 42 {
		t.Fatalf("got %d, want 42", h.ServerID)
	}
}

func TestRPCHeaderDefaultZero(t *testing.T) {
	h := RPCHeader{}
	if h.ProtocolVersion != 0 {
		t.Fatalf("got %d, want 0", h.ProtocolVersion)
	}
}

func TestRequestVoteArgsGetRPCHeader(t *testing.T) {
	args := RequestVoteArgs{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
	}
	h := args.GetRPCHeader()
	if h.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", h.ProtocolVersion, ProtocolVersion)
	}
	if h.ServerID != 1 {
		t.Fatalf("got %d, want 1", h.ServerID)
	}
}

func TestAppendEntriesArgsGetRPCHeader(t *testing.T) {
	args := AppendEntriesArgs{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 2},
	}
	h := args.GetRPCHeader()
	if h.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", h.ProtocolVersion, ProtocolVersion)
	}
	if h.ServerID != 2 {
		t.Fatalf("got %d, want 2", h.ServerID)
	}
}

func TestRequestVoteReplyGetRPCHeader(t *testing.T) {
	reply := RequestVoteReply{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 3},
	}
	h := reply.GetRPCHeader()
	if h.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", h.ProtocolVersion, ProtocolVersion)
	}
	if h.ServerID != 3 {
		t.Fatalf("got %d, want 3", h.ServerID)
	}
}

func TestAppendEntriesReplyGetRPCHeader(t *testing.T) {
	reply := AppendEntriesReply{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 4},
	}
	h := reply.GetRPCHeader()
	if h.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", h.ProtocolVersion, ProtocolVersion)
	}
	if h.ServerID != 4 {
		t.Fatalf("got %d, want 4", h.ServerID)
	}
}

// --- checkRPCHeader ---

func TestCheckRPCHeaderValid(t *testing.T) {
	rpc := &testWithHeader{RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion}}
	if err := checkRPCHeader(rpc); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestCheckRPCHeaderInvalidVersion(t *testing.T) {
	rpc := &testWithHeader{RPCHeader: RPCHeader{ProtocolVersion: 2}}
	if err := checkRPCHeader(rpc); err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestCheckRPCHeaderInvalidVersionZero(t *testing.T) {
	rpc := &testWithHeader{RPCHeader: RPCHeader{ProtocolVersion: 0}}
	if err := checkRPCHeader(rpc); err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestCheckRPCHeaderInvalidVersion4(t *testing.T) {
	rpc := &testWithHeader{RPCHeader: RPCHeader{ProtocolVersion: 4}}
	if err := checkRPCHeader(rpc); err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

// --- Compile-time interface checks ---

func TestRequestVoteArgsImplementsWithHeader(t *testing.T) {
	var _ WithRPCHeader = (*RequestVoteArgs)(nil)
}

func TestAppendEntriesArgsImplementsWithHeader(t *testing.T) {
	var _ WithRPCHeader = (*AppendEntriesArgs)(nil)
}

// --- Integration: reply.RPCHeader ---

func TestRequestVoteReplyRPCHeaderFilled(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()
	// remove: разогрев не требуется — RequestVote обрабатывается
	// синхронно и не зависит от того, стартовали ли фоновые горутины.

	var reply RequestVoteReply
	err := cm.RequestVote(RequestVoteArgs{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
		Term:      1,
	}, &reply)
	if err != nil {
		t.Fatalf("RequestVote failed: %v", err)
	}
	if reply.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", reply.ProtocolVersion, ProtocolVersion)
	}
	if reply.ServerID != 0 {
		t.Fatalf("got %d, want 0", reply.ServerID)
	}
}

func TestAppendEntriesReplyRPCHeaderFilled(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()
	// remove: разогрев не требуется — AppendEntries обрабатывается
	// синхронно (см. TestRequestVoteReplyRPCHeaderFilled).

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 1},
		Term:      1,
		LeaderID:  1,
	}, &reply)
	if err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}
	if reply.ProtocolVersion != ProtocolVersion {
		t.Fatalf("got %d, want %d", reply.ProtocolVersion, ProtocolVersion)
	}
	if reply.ServerID != 0 {
		t.Fatalf("got %d, want 0", reply.ServerID)
	}
}

// --- Integration: reject wrong protocol version ---

func TestAppendEntriesRejectsWrongProtocolVersion(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader: RPCHeader{ProtocolVersion: 2},
		Term:      1,
		LeaderID:  1,
	}, &reply)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestRequestVoteRejectsWrongProtocolVersion(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()

	var reply RequestVoteReply
	err := cm.RequestVote(RequestVoteArgs{
		RPCHeader: RPCHeader{ProtocolVersion: 2},
		Term:      1,
	}, &reply)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestAppendEntriesRejectsProtocolVersionZero(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader: RPCHeader{ProtocolVersion: 0},
		Term:      1,
		LeaderID:  1,
	}, &reply)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestRequestVoteRejectsProtocolVersionZero(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()

	var reply RequestVoteReply
	err := cm.RequestVote(RequestVoteArgs{
		RPCHeader: RPCHeader{ProtocolVersion: 0},
		Term:      1,
	}, &reply)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

func TestAppendEntriesRejectsProtocolVersionFuture(t *testing.T) {
	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()

	var reply AppendEntriesReply
	err := cm.AppendEntries(AppendEntriesArgs{
		RPCHeader: RPCHeader{ProtocolVersion: 4},
		Term:      1,
		LeaderID:  1,
	}, &reply)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrUnsupportedProtocol {
		t.Fatalf("got %v, want ErrUnsupportedProtocol", err)
	}
}

// --- Leaktest ---

func TestRPCHeaderNoGoroutineLeak(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	cm := testServerWithFSM(t, &NoopFSM{})
	defer cm.Stop()
	// keep: timing — предмет теста. Окно даёт фоновым горутинам CM
	// фактически стартовать, иначе leaktest проверял бы отсутствие
	// ещё не запущенных горутин и был бы ложно-зелёным.
	sleepMs(50)
}
