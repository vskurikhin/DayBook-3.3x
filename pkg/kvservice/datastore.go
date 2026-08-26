package kvservice

import (
	"bytes"
	"encoding/gob"
	"sync"
)

// DataStore — простое потокобезопасное хранилище «ключ-значение»,
// используемое в качестве внутреннего хранилища данных для kvservice.
type DataStore struct {
	mu   sync.Mutex
	data map[string]string
}

func NewDataStore() *DataStore {
	return &DataStore{
		data: make(map[string]string),
	}
}

// Snapshot возвращает сериализованное состояние DataStore.
func (ds *DataStore) Snapshot() ([]byte, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(ds.data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore восстанавливает состояние DataStore из сериализованных данных.
func (ds *DataStore) Restore(data []byte) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.data = make(map[string]string)
	return gob.NewDecoder(bytes.NewBuffer(data)).Decode(&ds.data)
}

// Get получает значение по ключу из хранилища.
// Возвращает (v, true), если ключ найден, либо ("", false) в противном случае.
func (ds *DataStore) Get(key string) (string, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	value, ok := ds.data[key]
	return value, ok
}

// Put присваивает datastore[key] = value.
// Возвращает (v, true), если ключ уже существовал в хранилище и его прежнее
// значение было равно v, либо ("", false) в противном случае.
func (ds *DataStore) Put(key, value string) (string, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	v, ok := ds.data[key]
	ds.data[key] = value
	return v, ok
}

// CAS выполняет атомарную операцию compare-and-swap:
//
// Если ключ существует и его текущее значение совпадает с compare,
// записывается новое значение value, иначе никаких изменений не производится.
//
// Возвращаются предыдущее значение ключа и признак того,
// существовал ли ключ в хранилище.
func (ds *DataStore) CAS(key, compare, value string) (string, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	prevValue, ok := ds.data[key]
	if ok && prevValue == compare {
		ds.data[key] = value
	}
	return prevValue, ok
}
