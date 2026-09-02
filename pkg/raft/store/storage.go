// Package store — реализации хранилищ журнала и снимков протокола Raft.
//
// Пакет содержит реализации интерфейсов хранилища (Storage) и хранилища
// снимков (SnapshotStore): MapStorage и FileStorage для журнала,
// InmemSnapshot и FileSnapshot для снимков. Реализации предназначены
// для использования ядром протокола и тестами.
//
// Пакет является листовым: он импортирует только стандартную библиотеку
// и лист-пакет контрактов (contract); от корневого пакета и пакетов
// реализаций он не зависит. Направление зависимостей — от ядра и этого
// пакета к контрактам, что разрывает импорт-циклы между ними.
package store

import (
	"slices"
	"sync"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// MapStorage — простая реализация интерфейса Storage, использующая память;
// предназначена для тестирования.
type MapStorage struct {
	mu sync.Mutex
	m  map[string][]byte
}

var _ contract.Storage = (*MapStorage)(nil)

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
	return slices.Clone(v), found
}

func (ms *MapStorage) Set(key string, value []byte) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.m[key] = slices.Clone(value)
}

func (ms *MapStorage) HasData() bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(ms.m) > 0
}
