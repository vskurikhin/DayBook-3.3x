package contract

import "io"

// SnapshotStore — хранилище снимков Raft.
// Отвечает за создание, хранение и предоставление доступа к снимкам.
//
// Контракт:
//   - новый снимок становится видимым в List/Open только после успешного
//     Close sink'а;
//   - Cancel обязан сохранять предыдущие снимки нетронутыми и удалять все
//     артефакты незавершённого снимка;
//   - List возвращает снимки, упорядоченные от новых к старым;
//   - Create может вызываться конкурентно; одновременные sink'и независимы.
//     FileSnapshot поддерживает независимые sink'и полностью;
//     InmemSnapshot — один одновременный sink (test-only).
type SnapshotStore interface {
	// Create создаёт новый снимок с указанными индексом, термом,
	// конфигурацией и индексом конфигурации. Возвращает SnapshotSink
	// для записи данных снимка.
	Create(index, term int, configuration Configuration, configIndex int) (SnapshotSink, error)

	// List возвращает список доступных снимков.
	List() ([]*SnapshotMeta, error)

	// Open открывает снимок по ID. Возвращает метаданные и io.ReadCloser
	// для чтения данных снимка. Вызывающий обязан закрыть reader.
	Open(id string) (*SnapshotMeta, io.ReadCloser, error)
}
