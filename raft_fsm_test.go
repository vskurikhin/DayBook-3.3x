package raft

import (
	"bytes"
	"encoding/gob"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/contract"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// snapshotTestFSM — тестовая FSM с поддержкой снимков.
// Хранит map[string]string, сериализует через gob.
type snapshotTestFSM struct {
	mu    sync.Mutex
	state map[string]string
}

func newSnapshotTestFSM() *snapshotTestFSM {
	return &snapshotTestFSM{
		state: make(map[string]string),
	}
}

func (f *snapshotTestFSM) Apply(log *LogEntry) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd, ok := log.Data.(string)
	if !ok {
		return nil
	}
	parts := strings.SplitN(cmd, "=", 2)
	if len(parts) == 2 {
		f.state[parts[0]] = parts[1]
	}
	return nil
}

func (f *snapshotTestFSM) Snapshot() (FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(f.state); err != nil {
		return nil, err
	}
	return &testSnapshot{data: buf.Bytes()}, nil
}

func (f *snapshotTestFSM) Restore(r io.ReadCloser) error {
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = make(map[string]string)
	return gob.NewDecoder(bytes.NewBuffer(data)).Decode(&f.state)
}

func (f *snapshotTestFSM) getState(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state[key]
}

// testSnapshot — реализация FSMSnapshot для snapshotTestFSM.
type testSnapshot struct {
	data []byte
}

func (s *testSnapshot) Persist(sink SnapshotSink) error {
	_, err := sink.Write(s.data)
	return err
}

func (s *testSnapshot) Release() {}

// TestSnapshotTestFSM_Basic проверяет round-trip Snapshot/Restore.
// Создаёт FSM, применяет команды, делает снимок, восстанавливает
// в новую FSM и проверяет состояние.
func TestSnapshotTestFSM_Basic(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := newSnapshotTestFSM()
	fsm.Apply(&LogEntry{Data: "a=1"})
	fsm.Apply(&LogEntry{Data: "b=2"})
	fsm.Apply(&LogEntry{Data: "c=3"})

	// Snapshot + Persist.
	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	defer snap.Release()

	stor := store.NewInmemSnapshot()
	sink, err := stor.Create(10, 1, 0, Configuration{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}
	_ = sink.Close()

	// Создаём новую FSM и восстанавливаем.
	fsm2 := newSnapshotTestFSM()
	_, reader, err := stor.Open(sink.ID())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := fsm2.Restore(reader); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Проверяем состояние.
	if v := fsm2.getState("a"); v != "1" {
		t.Fatalf("a = %q, want 1", v)
	}
	if v := fsm2.getState("b"); v != "2" {
		t.Fatalf("b = %q, want 2", v)
	}
	if v := fsm2.getState("c"); v != "3" {
		t.Fatalf("c = %q, want 3", v)
	}
}

// TestSnapshotTestFSM_Empty проверяет снимок пустой FSM.
func TestSnapshotTestFSM_Empty(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	fsm := newSnapshotTestFSM()
	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	defer snap.Release()

	stor := store.NewInmemSnapshot()
	sink, err := stor.Create(1, 1, 0, Configuration{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}
	_ = sink.Close()

	fsm2 := newSnapshotTestFSM()
	_, reader, err := stor.Open(sink.ID())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := fsm2.Restore(reader); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
}

// TestCommitChannelFSM_SnapshotStub проверяет, что заглушки
// CommitChannelFSM.Snapshot/Restore возвращают contract.ErrNotImplemented.
func TestCommitChannelFSM_SnapshotStub(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	ch := make(chan CommitEntry, 1)
	fsm := NewCommitChannelFSM(ch)
	close(ch)

	if _, err := fsm.Snapshot(); err != contract.ErrNotImplemented {
		t.Fatalf("Snapshot: want ErrNotImplemented, got %v", err)
	}
	if err := fsm.Restore(nil); err != contract.ErrNotImplemented {
		t.Fatalf("Restore: want ErrNotImplemented, got %v", err)
	}
}
