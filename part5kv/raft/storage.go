package raft

import "sync"

// Storage — интерфейс, реализуемый поставщиками постоянного хранилища.
type Storage interface {
	Set(key string, value []byte)

	Get(key string) ([]byte, bool)

	// HasData возвращает true тогда и только тогда, когда в данном хранилище
	// был выполнен хотя бы один вызов Set.
	HasData() bool
}

// MapStorage — простая реализация интерфейса Storage, использующая память;
// предназначена для тестирования.
type MapStorage struct {
	mu sync.Mutex
	m  map[string][]byte
}

func NewMapStorage() *MapStorage {
	m := make(map[string][]byte)
	return &MapStorage{
		m: m,
	}
}

func (ms *MapStorage) Get(key string) ([]byte, bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	v, found := ms.m[key]
	return v, found
}

func (ms *MapStorage) Set(key string, value []byte) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.m[key] = value
}

func (ms *MapStorage) HasData() bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(ms.m) > 0
}
