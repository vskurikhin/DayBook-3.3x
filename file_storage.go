package raft

import (
	"encoding/gob"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileStorage — file-backed реализация интерфейса Storage. Каждый ключ
// хранится в отдельном файле <key>.dat в заданной директории.
// Запись атомарна (temp -> fsync -> rename),
// что обеспечивает crash-safety на уровне отдельного ключа.
//
// Предпосылка: ключи, передаваемые в Set, должны быть filesystem-safe.
type FileStorage struct {
	mu      sync.Mutex
	dir     string
	data    map[string][]byte
	hasData bool
}

// NewFileStorage создаёт FileStorage в указанной директории, создавая её
// при необходимости и загружая существующие .dat-файлы в in-memory кэш.
func NewFileStorage(dir string) *FileStorage {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("FileStorage: cannot create dir %s: %v", dir, err)
	}
	fs := &FileStorage{
		dir:  dir,
		data: make(map[string][]byte),
	}
	fs.loadAll()
	return fs
}

// Set атомарно сохраняет value для key на диск и обновляет in-memory кэш.
// При ошибке ФС процесс завершается через log.Fatalf, поэтому блокировка здесь
// не снимается на путях ошибки — это не имеет значения,
// так как процесс всё равно завершается.
func (fs *FileStorage) Set(key string, value []byte) {
	fs.mu.Lock()

	path := filepath.Join(fs.dir, key+".dat")
	tmpPath := path + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		log.Fatalf("FileStorage.Set: cannot create tmp file %s: %v", tmpPath, err)
	}
	if err := gob.NewEncoder(f).Encode(value); err != nil {
		_ = f.Close()
		log.Fatalf("FileStorage.Set: gob encode %s: %v", key, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		log.Fatalf("FileStorage.Set: sync %s: %v", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("FileStorage.Set: close %s: %v", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Fatalf("FileStorage.Set: rename %s -> %s: %v", tmpPath, path, err)
	}

	// fsync родительской директории, чтобы rename пережил крэш ОС.
	if err := syncDir(fs.dir); err != nil {
		log.Fatalf("FileStorage.Set: sync dir %s: %v", fs.dir, err)
	}

	fs.data[key] = value
	fs.hasData = true
	fs.mu.Unlock()
}

// Get возвращает значение key из in-memory кэша.
func (fs *FileStorage) Get(key string) ([]byte, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	v, ok := fs.data[key]
	return v, ok
}

// HasData возвращает true, если в хранилище есть хотя бы один .dat-файл
// либо был выполнен хотя бы один Set.
func (fs *FileStorage) HasData() bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.hasData
}

// loadAll читает все .dat-файлы из директории в in-memory кэш. Вызывается
// один раз в конструкторе. Не-дат-файлы (включая .tmp) и повреждённые
// файлы пропускаются.
func (fs *FileStorage) loadAll() {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".dat") {
			continue
		}
		key := strings.TrimSuffix(name, ".dat")
		f, err := os.Open(filepath.Join(fs.dir, name))
		if err != nil {
			continue
		}
		var val []byte
		if err := gob.NewDecoder(f).Decode(&val); err != nil {
			_ = f.Close()
			continue
		}
		_ = f.Close()
		fs.data[key] = val
		fs.hasData = true
	}
}
