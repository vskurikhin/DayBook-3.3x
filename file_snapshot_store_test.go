package raft

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// testConfig — конфигурация кластера для тестов FileSnapshotStore.
func testConfig() Configuration {
	return Configuration{
		ConfigServers: []ConfigServer{
			{ID: 1, Address: "node1", Suffrage: Voter},
			{ID: 2, Address: "node2", Suffrage: Voter},
		},
	}
}

// writeSnapshot создаёт и закрывает снимок с данными data, возвращая sink.ID.
func writeSnapshot(t *testing.T, store *FileSnapshotStore, index, term int, data []byte) string {
	t.Helper()
	sink, err := store.Create(index, term, testConfig(), index)
	if err != nil {
		t.Fatalf("Create(%d, %d) failed: %v", index, term, err)
	}
	if _, err := sink.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	return sink.ID()
}

// TestFileSnapshotStore_RoundTrip проверяет полный цикл
// Create → Write → Close → List → Open.
func TestFileSnapshotStore_RoundTrip(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}

	data := []byte("snapshot state data")
	id := writeSnapshot(t, store, 10, 3, data)

	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("List() = %d snapshots, want 1", len(snapshots))
	}
	meta := snapshots[0]
	if meta.ID != id {
		t.Fatalf("ID = %q, want %q", meta.ID, id)
	}
	if meta.Index != 10 {
		t.Fatalf("Index = %d, want 10", meta.Index)
	}
	if meta.Term != 3 {
		t.Fatalf("Term = %d, want 3", meta.Term)
	}
	if meta.Size != int64(len(data)) {
		t.Fatalf("Size = %d, want %d", meta.Size, len(data))
	}
	if len(meta.Configuration.ConfigServers) != 2 {
		t.Fatalf("Configuration servers = %d, want 2", len(meta.Configuration.ConfigServers))
	}

	openMeta, reader, err := store.Open(id)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if openMeta.Index != 10 || openMeta.Term != 3 {
		t.Fatalf("Open meta = (index %d, term %d), want (10, 3)", openMeta.Index, openMeta.Term)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("data = %q, want %q", string(got), string(data))
	}
}

// TestFileSnapshotStore_SurvivesRestart проверяет, что новый экземпляр стора
// на той же директории видит ранее записанные снимки.
func TestFileSnapshotStore_SurvivesRestart(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	dir := t.TempDir()
	store, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	data := []byte("durable data")
	id := writeSnapshot(t, store, 7, 2, data)

	store2, err := NewFileSnapshotStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore (restart) failed: %v", err)
	}
	snapshots, err := store2.List()
	if err != nil {
		t.Fatalf("List after restart failed: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != id {
		t.Fatalf("List after restart = %v, want snapshot %q", snapshots, id)
	}
	_, reader, err := store2.Open(id)
	if err != nil {
		t.Fatalf("Open after restart failed: %v", err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("data after restart = %q, want %q", string(got), string(data))
	}
}

// TestFileSnapshotStore_Cancel проверяет, что Cancel удаляет временную
// директорию и не затрагивает ранее созданные снимки.
func TestFileSnapshotStore_Cancel(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	writeSnapshot(t, store, 5, 1, []byte("keep"))

	sink, err := store.Create(6, 1, testConfig(), 6)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	tmpDir := filepath.Join(store.path, sink.ID()+tmpSuffix)
	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatalf("tmp dir %s should exist: %v", tmpDir, err)
	}
	if _, err := sink.Write([]byte("discard")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := sink.Cancel(); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmp dir %s still exists after Cancel", tmpDir)
	}
	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Index != 5 {
		t.Fatalf("List after Cancel = %v, want previous snapshot (index 5)", snapshots)
	}
}

// TestFileSnapshotStore_InvisibleBeforeClose проверяет, что незакрытый
// снимок не виден в List.
func TestFileSnapshotStore_InvisibleBeforeClose(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	sink, err := store.Create(11, 2, testConfig(), 11)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() { _ = sink.Cancel() }()
	if _, err := sink.Write([]byte("unfinished")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("List before Close = %v, want empty", snapshots)
	}
}

// TestFileSnapshotStore_DetectsCorruption проверяет, что порча state.bin
// детектируется CRC32 при Open.
func TestFileSnapshotStore_DetectsCorruption(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}
	id := writeSnapshot(t, store, 12, 2, []byte("state data"))

	statePath := filepath.Join(store.path, id, stateFileName)
	fh, err := os.OpenFile(statePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("cannot open state file: %v", err)
	}
	if _, err := fh.WriteAt([]byte{'X'}, 0); err != nil {
		_ = fh.Close()
		t.Fatalf("cannot corrupt state file: %v", err)
	}
	if err := fh.Close(); err != nil {
		t.Fatalf("cannot close state file: %v", err)
	}

	if _, _, err := store.Open(id); err == nil {
		t.Fatal("Open of corrupted snapshot: want error, got nil")
	}
}

// TestFileSnapshotStore_Retain проверяет, что после создания снимков
// сверх retain старейшие удаляются.
func TestFileSnapshotStore_Retain(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}

	id1 := writeSnapshot(t, store, 1, 1, []byte("one"))
	id2 := writeSnapshot(t, store, 2, 1, []byte("two"))
	id3 := writeSnapshot(t, store, 3, 1, []byte("three"))

	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("List() = %d snapshots, want 2", len(snapshots))
	}
	if snapshots[0].ID != id3 || snapshots[1].ID != id2 {
		t.Fatalf("List IDs = %v, want [%s %s]", snapshotIDs(snapshots), id3, id2)
	}
	if _, err := os.Stat(filepath.Join(store.path, id1)); !os.IsNotExist(err) {
		t.Fatalf("oldest snapshot %s still exists", id1)
	}
}

// TestFileSnapshotStore_InvalidRetain проверяет валидацию retain в конструкторе.
func TestFileSnapshotStore_InvalidRetain(t *testing.T) {
	if _, err := NewFileSnapshotStore(t.TempDir(), 0); err == nil {
		t.Fatal("NewFileSnapshotStore with retain=0: want error, got nil")
	}
	if _, err := NewFileSnapshotStore(t.TempDir(), -1); err == nil {
		t.Fatal("NewFileSnapshotStore with retain=-1: want error, got nil")
	}
}

// TestFileSnapshotStore_ListSortedByMeta проверяет сортировку List
// по (Term, Index) в порядке убывания, а не по имени директории.
func TestFileSnapshotStore_ListSortedByMeta(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 5)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}

	writeSnapshot(t, store, 5, 2, []byte("a"))
	writeSnapshot(t, store, 9, 1, []byte("b"))
	writeSnapshot(t, store, 7, 3, []byte("c"))

	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 3 {
		t.Fatalf("List() = %d snapshots, want 3", len(snapshots))
	}
	if snapshots[0].Term != 3 || snapshots[0].Index != 7 {
		t.Fatalf("newest = (%d, %d), want (3, 7)", snapshots[0].Term, snapshots[0].Index)
	}
	if snapshots[1].Term != 2 || snapshots[1].Index != 5 {
		t.Fatalf("second = (%d, %d), want (2, 5)", snapshots[1].Term, snapshots[1].Index)
	}
	if snapshots[2].Term != 1 || snapshots[2].Index != 9 {
		t.Fatalf("third = (%d, %d), want (1, 9)", snapshots[2].Term, snapshots[2].Index)
	}
}

// TestFileSnapshotStore_CreateNameCollision — детерминированный тест коллизии
// имени временной директории (ревью MEDIUM-1): существующая <name>.tmp
// заставляет Create завершиться ошибкой os.IsExist вместо молчаливого
// переиспользования каталога.
func TestFileSnapshotStore_CreateNameCollision(t *testing.T) {
	defer leaktest.CheckTimeout(t, LeaktestBudget)()

	store, err := NewFileSnapshotStore(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("NewFileSnapshotStore failed: %v", err)
	}

	fixed := time.UnixMilli(1700000000000)
	name := snapshotName(4, 42, fixed.UnixNano()/int64(time.Millisecond))
	collisionPath := filepath.Join(store.path, name+tmpSuffix)
	if err := os.Mkdir(collisionPath, 0o700); err != nil {
		t.Fatalf("cannot pre-create collision dir: %v", err)
	}

	_, err = store.createAt(42, 4, 42, testConfig(), fixed)
	if err == nil {
		t.Fatal("Create with colliding tmp dir: want error, got nil")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Create collision error = %v, want fs.ErrExist", err)
	}
}

// snapshotIDs возвращает ID снимков для сообщений об ошибках.
func snapshotIDs(snapshots []*SnapshotMeta) []string {
	ids := make([]string, len(snapshots))
	for i, s := range snapshots {
		ids[i] = s.ID
	}
	return ids
}
