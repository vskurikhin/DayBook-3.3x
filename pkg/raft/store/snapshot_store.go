package store

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// InmemSnapshot — in-memory реализация SnapshotStore. Test-only:
// не переживает рестарт процесса; для production использовать
// FileSnapshot.
//
// Хранит только последний снимок: незавершённый sink живёт в pending
// и становится видимым (latest) только после успешного Close; Cancel
// очищает pending, не затрагивая latest. Поддерживает один одновременный
// Create — второй Create затирает pending первого, и первый Close не
// продвигает свои данные в latest (защита от гонки двух Create).
type InmemSnapshot struct {
	mu      sync.Mutex
	pending *InmemSnapshotSink
	latest  *InmemSnapshotSink
}

var _ contract.SnapshotStore = (*InmemSnapshot)(nil)

// NewInmemSnapshot создаёт новый InmemSnapshot.
func NewInmemSnapshot() *InmemSnapshot {
	return &InmemSnapshot{}
}

// Create создаёт новый снимок. Снимок не виден в List/Open до Close.
func (s *InmemSnapshot) Create(
	index, term int, configuration contract.Configuration, configIndex int,
) (contract.SnapshotSink, error) {
	id := fmt.Sprintf("snapshot-%d-%d", term, index)
	s.mu.Lock()
	defer s.mu.Unlock()
	sink := &InmemSnapshotSink{
		store: s,
		meta: contract.SnapshotMeta{
			Version:       contract.ProtocolVersion,
			ID:            id,
			Index:         index,
			Term:          term,
			Configuration: configuration,
			ConfigIndex:   configIndex,
		},
	}
	s.pending = sink
	return sink, nil
}

// List возвращает срез с одним *SnapshotMeta или пустой срез, если
// завершённого снимка нет.
func (s *InmemSnapshot) List() ([]*contract.SnapshotMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		return nil, nil
	}
	return []*contract.SnapshotMeta{cloneMeta(&s.latest.meta)}, nil
}

// Open ищет завершённый снимок по ID. Возвращает копию данных.
func (s *InmemSnapshot) Open(id string) (*contract.SnapshotMeta, io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil || s.latest.meta.ID != id {
		return nil, nil, fmt.Errorf("raft: snapshot not found: %s", id)
	}
	meta := cloneMeta(&s.latest.meta)
	data := make([]byte, s.latest.buf.Len())
	copy(data, s.latest.buf.Bytes())
	return meta, io.NopCloser(bytes.NewReader(data)), nil
}

// InmemSnapshotSink — реализация SnapshotSink в памяти.
type InmemSnapshotSink struct {
	store  *InmemSnapshot
	meta   contract.SnapshotMeta
	buf    bytes.Buffer
	mu     sync.Mutex
	closed bool
}

var _ contract.SnapshotSink = (*InmemSnapshotSink)(nil)

// Write пишет данные в буфер снимка, обновляя Size.
func (s *InmemSnapshotSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	s.meta.Size = int64(s.buf.Len())
	return n, err
}

// Close завершает запись снимка и делает его видимым в List/Open.
// Идемпотентен: повторный Close — no-op.
func (s *InmemSnapshotSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	// Продвигаем pending → latest только если этот sink всё ещё pending:
	// защита от «второй Create затёр pending первого».
	if s.store.pending == s {
		s.store.pending = nil
		s.store.latest = s
	}
	return nil
}

// ID возвращает идентификатор снимка.
func (s *InmemSnapshotSink) ID() string {
	return s.meta.ID
}

// Cancel отменяет запись снимка: очищает pending, не затрагивая
// последний завершённый снимок. Идемпотентен.
func (s *InmemSnapshotSink) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.pending == s {
		s.store.pending = nil
	}
	return nil
}

// cloneMeta создаёт копию SnapshotMeta.
func cloneMeta(m *contract.SnapshotMeta) *contract.SnapshotMeta {
	cfg := m.Configuration
	servers := make([]contract.ConfigServer, len(cfg.ConfigServers))
	copy(servers, cfg.ConfigServers)
	return &contract.SnapshotMeta{
		Version:       m.Version,
		ID:            m.ID,
		Index:         m.Index,
		Term:          m.Term,
		Configuration: contract.Configuration{ConfigServers: servers},
		ConfigIndex:   m.ConfigIndex,
		Size:          m.Size,
	}
}
