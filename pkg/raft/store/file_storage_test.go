package store

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fortytw2/leaktest"

	"github.com/vskurikhin/raft"
)

// gobEncode кодирует value в gob-формат, как это делает raft_cm_storage.go.
// Локальная копия, предназначенная для тестов пакета store.
func gobEncode(t *testing.T, value any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	return buf.Bytes()
}

// gobDecode декодирует data в value, как это делает raft_cm_storage.go.
// Локальная копия, предназначенная для тестов пакета store.
func gobDecode(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := gob.NewDecoder(bytes.NewBuffer(data)).Decode(value); err != nil {
		t.Fatalf("gob decode: %v", err)
	}
}

// TestFileStorage_SetGetRoundtrip проверяет запись и чтение значения.
func TestFileStorage_SetGetRoundtrip(t *testing.T) {
	fs := NewFileStorage(t.TempDir())
	fs.Set("currentTerm", gobEncode(t, 5))

	data, ok := fs.Get("currentTerm")
	if !ok {
		t.Fatal("key currentTerm not found")
	}
	var term int
	gobDecode(t, data, &term)
	if term != 5 {
		t.Fatalf("term = %d, want 5", term)
	}
}

// TestFileStorage_HasData проверяет флаг наличия данных.
func TestFileStorage_HasData(t *testing.T) {
	fs := NewFileStorage(t.TempDir())
	if fs.HasData() {
		t.Fatal("HasData() = true for empty dir, want false")
	}
	fs.Set("k", gobEncode(t, 1))
	if !fs.HasData() {
		t.Fatal("HasData() = false after Set, want true")
	}
}

// TestFileStorage_RestartSimulation проверяет восстановление всех ключей
// при создании нового FileStorage в той же директории.
func TestFileStorage_RestartSimulation(t *testing.T) {
	dir := t.TempDir()

	fs1 := NewFileStorage(dir)
	fs1.Set("currentTerm", gobEncode(t, 3))
	fs1.Set("votedFor", gobEncode(t, 2))
	log := []raft.LogEntry{
		{Index: 1, Term: 1, Data: 10},
		{Index: 2, Term: 1, Data: 20},
		{Index: 3, Term: 2, Data: 30},
	}
	fs1.Set("log", gobEncode(t, log))
	fs1.Set("lastSnapshotIndex", gobEncode(t, 0))
	fs1.Set("lastSnapshotTerm", gobEncode(t, 0))

	fs2 := NewFileStorage(dir)
	if !fs2.HasData() {
		t.Fatal("HasData() = false after restart, want true")
	}

	var term int
	gobDecode(t, mustGet(t, fs2, "currentTerm"), &term)
	if term != 3 {
		t.Fatalf("currentTerm = %d, want 3", term)
	}

	var votedFor int
	gobDecode(t, mustGet(t, fs2, "votedFor"), &votedFor)
	if votedFor != 2 {
		t.Fatalf("votedFor = %d, want 2", votedFor)
	}

	var restoredLog []raft.LogEntry
	gobDecode(t, mustGet(t, fs2, "log"), &restoredLog)
	if len(restoredLog) != len(log) {
		t.Fatalf("log length = %d, want %d", len(restoredLog), len(log))
	}
	for i := range log {
		if restoredLog[i] != log[i] {
			t.Fatalf("log[%d] = %+v, want %+v", i, restoredLog[i], log[i])
		}
	}

	var snapIdx int
	gobDecode(t, mustGet(t, fs2, "lastSnapshotIndex"), &snapIdx)
	if snapIdx != 0 {
		t.Fatalf("lastSnapshotIndex = %d, want 0", snapIdx)
	}

	var snapTerm int
	gobDecode(t, mustGet(t, fs2, "lastSnapshotTerm"), &snapTerm)
	if snapTerm != 0 {
		t.Fatalf("lastSnapshotTerm = %d, want 0", snapTerm)
	}
}

// TestFileStorage_CrashSafety_NoTmpLeft проверяет, что после Set
// не остаётся .tmp-файлов.
func TestFileStorage_CrashSafety_NoTmpLeft(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileStorage(dir)
	fs.Set("currentTerm", gobEncode(t, 1))
	fs.Set("votedFor", gobEncode(t, 1))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("unexpected .tmp file left after Set: %s", entry.Name())
		}
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 .dat files, got %d", len(entries))
	}
}

// TestFileStorage_CrashSafety_OrphanTmpIgnored проверяет, что orphan .tmp
// файл игнорируется при загрузке и не помечает хранилище как имеющее данные.
func TestFileStorage_CrashSafety_OrphanTmpIgnored(t *testing.T) {
	dir := t.TempDir()
	orphanPath := filepath.Join(dir, "orphan.dat.tmp")
	f, err := os.Create(orphanPath)
	if err != nil {
		t.Fatalf("create orphan tmp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close orphan tmp: %v", err)
	}

	fs := NewFileStorage(dir)
	if fs.HasData() {
		t.Fatal("HasData() = true with only orphan .tmp, want false")
	}
	if _, ok := fs.Get("orphan"); ok {
		t.Fatal("orphan key loaded from .tmp file, want not found")
	}
}

// TestFileStorage_ConcurrentSet проверяет конкурентную запись разных ключей.
func TestFileStorage_ConcurrentSet(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	fs := NewFileStorage(t.TempDir())

	keys := []string{"currentTerm", "votedFor", "log", "lastSnapshotIndex", "lastSnapshotTerm", "k1", "k2", "k3"}
	const iterations = 100

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				fs.Set(key, gobEncode(t, i))
			}
		}(key)
	}
	wg.Wait()

	for _, key := range keys {
		data, ok := fs.Get(key)
		if !ok {
			t.Fatalf("key %q not found after concurrent Set", key)
		}
		var v int
		gobDecode(t, data, &v)
		if v != iterations-1 {
			t.Fatalf("key %q = %d, want %d", key, v, iterations-1)
		}
	}
}

// TestFileStorage_LargeValue проверяет запись и восстановление большого значения.
func TestFileStorage_LargeValue(t *testing.T) {
	dir := t.TempDir()

	large := make([]byte, 1<<20)
	for i := range large {
		large[i] = byte(i % 251)
	}

	fs1 := NewFileStorage(dir)
	fs1.Set("log", gobEncode(t, large))

	fs2 := NewFileStorage(dir)
	var restored []byte
	gobDecode(t, mustGet(t, fs2, "log"), &restored)
	if len(restored) != len(large) {
		t.Fatalf("restored length = %d, want %d", len(restored), len(large))
	}
	for i := range large {
		if restored[i] != large[i] {
			t.Fatalf("byte %d = %d, want %d", i, restored[i], large[i])
		}
	}
}

// TestFileStorage_GobCompatibility проверяет round-trip для типов,
// используемых raft_cm_storage.go: int и []LogEntry.
func TestFileStorage_GobCompatibility(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileStorage(dir)

	fs.Set("currentTerm", gobEncode(t, 7))
	fs.Set("votedFor", gobEncode(t, -1))
	fs.Set("lastSnapshotIndex", gobEncode(t, 4))
	fs.Set("lastSnapshotTerm", gobEncode(t, 3))
	log := []raft.LogEntry{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: 100},
		{Index: 2, Term: 1, Type: raft.LogNoop, Data: nil},
	}
	fs.Set("log", gobEncode(t, log))

	var term, votedFor, snapIdx, snapTerm int
	gobDecode(t, mustGet(t, fs, "currentTerm"), &term)
	gobDecode(t, mustGet(t, fs, "votedFor"), &votedFor)
	gobDecode(t, mustGet(t, fs, "lastSnapshotIndex"), &snapIdx)
	gobDecode(t, mustGet(t, fs, "lastSnapshotTerm"), &snapTerm)
	if term != 7 || votedFor != -1 || snapIdx != 4 || snapTerm != 3 {
		t.Fatalf("scalars mismatch: term=%d votedFor=%d snapIdx=%d snapTerm=%d", term, votedFor, snapIdx, snapTerm)
	}

	var restoredLog []raft.LogEntry
	gobDecode(t, mustGet(t, fs, "log"), &restoredLog)
	if len(restoredLog) != len(log) {
		t.Fatalf("log length = %d, want %d", len(restoredLog), len(log))
	}
	for i := range log {
		if restoredLog[i] != log[i] {
			t.Fatalf("log[%d] = %+v, want %+v", i, restoredLog[i], log[i])
		}
	}
}

// mustGet возвращает значение ключа и завершает тест при его отсутствии.
func mustGet(t *testing.T, fs *FileStorage, key string) []byte {
	t.Helper()
	data, ok := fs.Get(key)
	if !ok {
		t.Fatalf("key %q not found", key)
	}
	return data
}

// TestFileStorage_SetSkipsUnchangedValue проверяет, что повторная запись того
// же значения не выполняет ввода-вывода, а смена значения — выполняет.
func TestFileStorage_SetSkipsUnchangedValue(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	fs := NewFileStorage(t.TempDir())
	value := gobEncode(t, 42)
	for i := 0; i < 10; i++ {
		fs.Set("currentTerm", value)
	}
	if got := fs.WriteCount(); got != 1 {
		t.Fatalf("WriteCount = %d after 10 identical Set, want 1", got)
	}

	fs2 := NewFileStorage(t.TempDir())
	v1 := gobEncode(t, 1)
	v2 := gobEncode(t, 2)
	for _, value := range [][]byte{v1, v1, v2, v2, v1} {
		fs2.Set("currentTerm", value)
	}
	if got := fs2.WriteCount(); got != 3 {
		t.Fatalf("WriteCount = %d for sequence v1,v1,v2,v2,v1, want 3", got)
	}
}

// TestFileStorage_SetWritesChangedValue проверяет, что изменение каждого из
// пяти ключей по отдельности выполняет запись именно этого ключа и что после
// открытия каталога новым экземпляром значения прочитаны верно.
func TestFileStorage_SetWritesChangedValue(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	dir := t.TempDir()
	fs := NewFileStorage(dir)

	initial := map[string][]byte{
		"currentTerm":       gobEncode(t, 1),
		"votedFor":          gobEncode(t, -1),
		"lastSnapshotIndex": gobEncode(t, 0),
		"lastSnapshotTerm":  gobEncode(t, 0),
		"log":               gobEncode(t, []raft.LogEntry{{Index: 0, Term: 1}}),
	}
	keys := []string{"currentTerm", "votedFor", "lastSnapshotIndex", "lastSnapshotTerm", "log"}
	for _, key := range keys {
		fs.Set(key, initial[key])
	}
	if got := fs.WriteCount(); got != len(keys) {
		t.Fatalf("WriteCount = %d after initial fill, want %d", got, len(keys))
	}

	changed := map[string][]byte{
		"currentTerm":       gobEncode(t, 7),
		"votedFor":          gobEncode(t, 2),
		"lastSnapshotIndex": gobEncode(t, 5),
		"lastSnapshotTerm":  gobEncode(t, 3),
		"log":               gobEncode(t, []raft.LogEntry{{Index: 6, Term: 3}}),
	}
	expected := make(map[string][]byte, len(keys))
	for key, value := range initial {
		expected[key] = value
	}

	for i, key := range keys {
		before := fs.WriteCount()
		fs.Set(key, changed[key])
		if got := fs.WriteCount() - before; got != 1 {
			t.Fatalf("key %q: %d writes on change, want 1", key, got)
		}
		expected[key] = changed[key]

		// Остальные ключи на диске не тронуты: их значения соответствуют
		// последнему записанному состоянию.
		restarted := NewFileStorage(dir)
		for _, other := range keys {
			if !bytes.Equal(mustGet(t, restarted, other), expected[other]) {
				t.Fatalf("after change %d of key %q: key %q on disk differs from expected", i, key, other)
			}
		}

		// Повторная запись того же значения ввода-вывода не выполняет.
		before = fs.WriteCount()
		fs.Set(key, changed[key])
		if got := fs.WriteCount() - before; got != 0 {
			t.Fatalf("key %q: %d writes on repeated identical Set, want 0", key, got)
		}
	}
}

// TestFileStorage_CloneOnSet проверяет, что кэш хранит собственную копию
// значения: мутация буфера вызывающего после Set не влияет ни на диск, ни на
// решение о пропуске следующей записи.
func TestFileStorage_CloneOnSet(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	dir := t.TempDir()
	fs := NewFileStorage(dir)

	original := []byte{1, 2, 3}
	fs.Set("log", original)
	if got := fs.WriteCount(); got != 1 {
		t.Fatalf("WriteCount = %d after first Set, want 1", got)
	}

	// Вызывающий мутирует свой буфер на месте.
	original[0] = 9
	mutated := []byte{9, 2, 3}

	if got := mustGet(t, NewFileStorage(dir), "log"); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("disk value = %v after caller mutation, want [1 2 3]", got)
	}

	// Значение, совпадающее с мутированным буфером, отличается от лежащего на
	// диске, поэтому запись обязана быть выполнена.
	fs.Set("log", mutated)
	if got := fs.WriteCount(); got != 2 {
		t.Fatalf("WriteCount = %d after Set of mutated value, want 2 (cache aliased caller buffer)", got)
	}
	if got := mustGet(t, NewFileStorage(dir), "log"); !bytes.Equal(got, mutated) {
		t.Fatalf("disk value = %v, want %v", got, mutated)
	}
}

// TestFileStorage_CloneOnGet проверяет, что Get возвращает защитную копию:
// мутация полученного среза не портит ни кэш, ни диск.
func TestFileStorage_CloneOnGet(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	dir := t.TempDir()
	fs := NewFileStorage(dir)

	original := []byte{1, 2, 3}
	fs.Set("log", original)

	got, ok := fs.Get("log")
	if !ok {
		t.Fatal("key log not found")
	}
	got[0] = 9

	if again := mustGet(t, fs, "log"); !bytes.Equal(again, []byte{1, 2, 3}) {
		t.Fatalf("cached value = %v after reader mutation, want [1 2 3]", again)
	}
	if onDisk := mustGet(t, NewFileStorage(dir), "log"); !bytes.Equal(onDisk, []byte{1, 2, 3}) {
		t.Fatalf("disk value = %v after reader mutation, want [1 2 3]", onDisk)
	}

	before := fs.WriteCount()
	fs.Set("log", []byte{9, 2, 3})
	if got := fs.WriteCount() - before; got != 1 {
		t.Fatalf("%d writes for value differing from cached, want 1 (cache spoiled by reader)", got)
	}
}

// TestFileStorage_HasDataAfterSkippedSet проверяет, что пропуск записи не
// меняет семантику HasData.
func TestFileStorage_HasDataAfterSkippedSet(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	dir := t.TempDir()
	fs := NewFileStorage(dir)
	value := gobEncode(t, 3)

	fs.Set("currentTerm", value)
	if !fs.HasData() {
		t.Fatal("HasData() = false after first Set, want true")
	}
	fs.Set("currentTerm", value)
	if got := fs.WriteCount(); got != 1 {
		t.Fatalf("WriteCount = %d after repeated identical Set, want 1", got)
	}
	if !fs.HasData() {
		t.Fatal("HasData() = false after skipped Set, want true")
	}

	// Значение поднято с диска новым экземпляром: пропуск записи должен
	// сохранить HasData, выставленный загрузкой каталога.
	restarted := NewFileStorage(dir)
	if !restarted.HasData() {
		t.Fatal("HasData() = false after load from disk, want true")
	}
	restarted.Set("currentTerm", value)
	if got := restarted.WriteCount(); got != 0 {
		t.Fatalf("WriteCount = %d after Set of value already on disk, want 0", got)
	}
	if !restarted.HasData() {
		t.Fatal("HasData() = false after skipped Set on restarted storage, want true")
	}
}
