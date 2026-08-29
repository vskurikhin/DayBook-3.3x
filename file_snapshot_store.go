package raft

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// snapshotsSubdir — поддиректория dataDir, в которой FileSnapshotStore
	// хранит снимки. Не конфликтует с FileStorage.loadAll: тот читает
	// только *.dat-файлы в корне каталога.
	snapshotsSubdir = "snapshots"

	// metaFileName — имя файла метаданных внутри директории снимка.
	metaFileName = "meta.dat"

	// stateFileName — имя файла данных FSM внутри директории снимка.
	stateFileName = "state.bin"

	// tmpSuffix — суффикс временной директории незавершённого снимка.
	tmpSuffix = ".tmp"
)

// fileSnapshotMeta — метаданные снимка на диске: SnapshotMeta
// с контрольной суммой CRC32 данных FSM.
type fileSnapshotMeta struct {
	SnapshotMeta

	CRC []byte
}

// FileSnapshotStore — файловая реализация SnapshotStore с сохранением на диск.
// Снимки хранятся в <base>/snapshots/<term>-<index>-<msec>/;
// запись атомарна (временная директория + rename + fsync), целостность
// данных проверяется CRC32. Переживает рестарт процесса.
type FileSnapshotStore struct {
	path   string
	retain int
}

var _ SnapshotStore = (*FileSnapshotStore)(nil)

// NewFileSnapshotStore создаёт FileSnapshotStore в поддиректории
// snapshots каталога base. retain >= 1 — сколько снимков сохранять;
// при создании нового снимка более старые удаляются.
func NewFileSnapshotStore(base string, retain int) (*FileSnapshotStore, error) {
	if retain < 1 {
		return nil, fmt.Errorf("raft: must retain at least one snapshot")
	}
	path := filepath.Join(base, snapshotsSubdir)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("raft: snapshot path not accessible: %v", err)
	}
	store := &FileSnapshotStore{
		path:   path,
		retain: retain,
	}
	if err := store.testPermissions(); err != nil {
		return nil, fmt.Errorf("raft: permissions test failed: %v", err)
	}
	return store, nil
}

// testPermissions проверяет, что в каталог снимков можно писать.
func (f *FileSnapshotStore) testPermissions() error {
	path := filepath.Join(f.path, ".permissions-test")
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}

// snapshotName генерирует имя директории снимка: <term>-<index>-<msec>.
func snapshotName(term, index int, msec int64) string {
	return fmt.Sprintf("%d-%d-%d", term, index, msec)
}

// Create создаёт новый снимок с заданными метаданными.
// Снимок не отображается в List/Open, пока не будет успешно вызван Close.
//
// Временная директория создаётся через os.Mkdir (неидемпотентная операция).
// Если одновременно запускается несколько Create для одной пары (term, index)
// (в пределах миллисекунды), один из вызовов получит os.IsExist.
// Проигравший вызывающий обрабатывает эту ошибку как обычную ошибку создания снимка.
func (f *FileSnapshotStore) Create(index, term int, configuration Configuration, configIndex int) (SnapshotSink, error) {
	return f.createAt(index, term, configIndex, configuration, time.Now())
}

// createAt — Create с инъекцией времени для детерминированных тестов
// коллизии имени директории.
func (f *FileSnapshotStore) createAt(
	index, term, configIndex int, configuration Configuration, now time.Time,
) (SnapshotSink, error) {
	name := snapshotName(term, index, now.UnixNano()/int64(time.Millisecond))
	path := filepath.Join(f.path, name+tmpSuffix)

	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("raft: failed to make snapshot directory %s: %w", path, err)
	}

	sink := &FileSnapshotSink{
		store:     f,
		dir:       path,
		parentDir: f.path,
		meta: fileSnapshotMeta{
			SnapshotMeta: SnapshotMeta{
				Version:       ProtocolVersion,
				ID:            name,
				Index:         index,
				Term:          term,
				Configuration: configuration,
				ConfigIndex:   configIndex,
			},
		},
	}

	statePath := filepath.Join(path, stateFileName)
	fh, err := os.Create(statePath)
	if err != nil {
		_ = os.RemoveAll(path)
		return nil, fmt.Errorf("raft: failed to create state file: %v", err)
	}
	sink.stateFile = fh
	sink.stateHash = crc32.NewIEEE()
	sink.buffered = bufio.NewWriter(io.MultiWriter(sink.stateFile, sink.stateHash))
	return sink, nil
}

// List возвращает метаданные всех доступных снимков, упорядоченные
// от новых к старым (по Term, затем Index, затем ID). Повреждённые
// снимки пропускаются с логированием.
func (f *FileSnapshotStore) List() ([]*SnapshotMeta, error) {
	snapshots, err := f.getSnapshots()
	if err != nil {
		return nil, err
	}
	var metas []*SnapshotMeta
	for _, meta := range snapshots {
		metas = append(metas, &meta.SnapshotMeta)
	}
	return metas, nil
}

// Open открывает снимок по ID, проверяет CRC32 данных и возвращает
// метаданные с reader. Вызывающий обязан закрыть reader.
func (f *FileSnapshotStore) Open(id string) (*SnapshotMeta, io.ReadCloser, error) {
	meta, err := f.readMeta(id)
	if err != nil {
		return nil, nil, fmt.Errorf("raft: snapshot %s is corrupt: %v", id, err)
	}

	statePath := filepath.Join(f.path, id, stateFileName)
	fh, err := os.Open(statePath)
	if err != nil {
		return nil, nil, fmt.Errorf("raft: failed to open snapshot state %s: %v", id, err)
	}

	hash := crc32.NewIEEE()
	if _, err := io.Copy(hash, fh); err != nil {
		_ = fh.Close()
		return nil, nil, fmt.Errorf("raft: failed to read snapshot state %s: %v", id, err)
	}
	if !bytes.Equal(meta.CRC, hash.Sum(nil)) {
		_ = fh.Close()
		return nil, nil, fmt.Errorf("raft: snapshot %s CRC mismatch", id)
	}
	if _, err := fh.Seek(0, 0); err != nil {
		_ = fh.Close()
		return nil, nil, fmt.Errorf("raft: failed to seek snapshot state %s: %v", id, err)
	}

	return &meta.SnapshotMeta, &bufferedReadCloser{Reader: bufio.NewReader(fh), fh: fh}, nil
}

// getSnapshots возвращает все известные снимки, отсортированные
// от новых к старым.
func (f *FileSnapshotStore) getSnapshots() ([]*fileSnapshotMeta, error) {
	entries, err := os.ReadDir(f.path)
	if err != nil {
		return nil, fmt.Errorf("raft: failed to scan snapshot directory: %v", err)
	}

	var snapshots []*fileSnapshotMeta
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasSuffix(entry.Name(), tmpSuffix) {
			continue
		}
		meta, err := f.readMeta(entry.Name())
		if err != nil {
			log.Printf("raft: skipping corrupt snapshot %s: %v", entry.Name(), err)
			continue
		}
		snapshots = append(snapshots, meta)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Term != snapshots[j].Term {
			return snapshots[i].Term > snapshots[j].Term
		}
		if snapshots[i].Index != snapshots[j].Index {
			return snapshots[i].Index > snapshots[j].Index
		}
		return snapshots[i].ID > snapshots[j].ID
	})
	return snapshots, nil
}

// readMeta читает и декодирует meta.dat снимка id.
func (f *FileSnapshotStore) readMeta(id string) (*fileSnapshotMeta, error) {
	fh, err := os.Open(filepath.Join(f.path, id, metaFileName))
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	var meta fileSnapshotMeta
	if err := gob.NewDecoder(fh).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// removeOldSnapshots удаляет снимки сверх retain (самые старые).
// Ошибки удаления отдельных директорий логируются, чтобы не ломать
// успешно завершившийся Close.
func (f *FileSnapshotStore) removeOldSnapshots() error {
	snapshots, err := f.getSnapshots()
	if err != nil {
		return err
	}
	for i := f.retain; i < len(snapshots); i++ {
		path := filepath.Join(f.path, snapshots[i].ID)
		if err := os.RemoveAll(path); err != nil {
			log.Printf("raft: failed to reap old snapshot %s: %v", snapshots[i].ID, err)
		}
	}
	return nil
}

// bufferedReadCloser — буферизованный reader, закрывающий файл состояния.
type bufferedReadCloser struct {
	*bufio.Reader

	fh *os.File
}

// Close закрывает файл состояния снимка.
func (b *bufferedReadCloser) Close() error {
	return b.fh.Close()
}

// FileSnapshotSink — реализация SnapshotSink, пишущая данные FSM
// во временную директорию снимка.
type FileSnapshotSink struct {
	store     *FileSnapshotStore
	dir       string
	parentDir string
	meta      fileSnapshotMeta

	stateFile *os.File
	stateHash hash.Hash32
	buffered  *bufio.Writer

	mu     sync.Mutex
	closed bool
}

var _ SnapshotSink = (*FileSnapshotSink)(nil)

// Write пишет данные FSM в state.bin.
func (s *FileSnapshotSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("raft: snapshot sink is closed")
	}
	return s.buffered.Write(p)
}

// Close завершает запись снимка: flush, fsync и закрытие state.bin,
// запись meta.dat с CRC32, атомарный rename временной директории в
// финальное имя, fsync родительской директории и удаление снимков
// сверх retain. Идемпотентен.
func (s *FileSnapshotSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	if err := s.buffered.Flush(); err != nil {
		_ = s.stateFile.Close()
		return fmt.Errorf("raft: failed to flush snapshot state: %v", err)
	}
	if err := s.stateFile.Sync(); err != nil {
		_ = s.stateFile.Close()
		return fmt.Errorf("raft: failed to sync snapshot state: %v", err)
	}
	if err := s.stateFile.Close(); err != nil {
		return fmt.Errorf("raft: failed to close snapshot state: %v", err)
	}

	fi, err := os.Stat(filepath.Join(s.dir, stateFileName))
	if err != nil {
		return fmt.Errorf("raft: failed to stat snapshot state: %v", err)
	}
	s.meta.Size = fi.Size()
	s.meta.CRC = s.stateHash.Sum(nil)

	if err := s.writeMeta(); err != nil {
		return fmt.Errorf("raft: failed to write snapshot meta: %v", err)
	}

	finalPath := filepath.Join(s.parentDir, s.meta.ID)
	if err := os.Rename(s.dir, finalPath); err != nil {
		return fmt.Errorf("raft: failed to move snapshot into place: %v", err)
	}
	// fsync родительской директории, чтобы rename пережил крэш ОС.
	if err := syncDir(s.parentDir); err != nil {
		return fmt.Errorf("raft: failed to sync snapshot directory: %v", err)
	}

	s.closed = true

	// безопасно по POSIX-семантике unlink;
	// не-POSIX файловые системы не поддерживаются.
	if err := s.store.removeOldSnapshots(); err != nil {
		log.Printf("raft: failed to reap old snapshots: %v", err)
	}
	return nil
}

// Cancel отменяет запись снимка: удаляет временную директорию целиком,
// не затрагивая ранее созданные снимки. Идемпотентен.
func (s *FileSnapshotSink) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	_ = s.stateFile.Close()
	if err := os.RemoveAll(s.dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("raft: failed to cancel snapshot: %v", err)
	}
	s.closed = true
	return nil
}

// ID возвращает идентификатор снимка.
func (s *FileSnapshotSink) ID() string {
	return s.meta.ID
}

// writeMeta записывает meta.dat с CRC32 и синхронизирует файл с диском.
func (s *FileSnapshotSink) writeMeta() error {
	fh, err := os.Create(filepath.Join(s.dir, metaFileName))
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()
	if err := gob.NewEncoder(fh).Encode(&s.meta); err != nil {
		return err
	}
	return fh.Sync()
}

// syncDir открывает каталог и синхронизирует его с диском, чтобы
// переименования/удаления внутри пережили крэш ОС.
func syncDir(path string) error {
	dh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dh.Close() }()
	return dh.Sync()
}
