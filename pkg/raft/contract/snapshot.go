package contract

import "io"

// SnapshotSink — получатель данных снимка.
// FSM пишет сериализованное состояние через Write, завершает Close.
// При ошибке вызывает Cancel.
type SnapshotSink interface {
	io.WriteCloser
	ID() string
	Cancel() error
}

// SnapshotMeta — метаданные снимка.
type SnapshotMeta struct {
	Version       int
	ID            string
	Index         int
	Term          int
	Configuration Configuration
	ConfigIndex   int
	Size          int64
}
