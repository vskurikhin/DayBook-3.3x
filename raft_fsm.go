package raft

import (
	"io"
)

// FSM — интерфейс машины состояний клиента Raft.
// Все зафиксированные записи журнала применяются к FSM через Apply.
// После применения всех записей FSM может создавать снимки
// через Snapshot и восстанавливаться из них через Restore.
//
// Контракт владения переданной записью: ConsensusModule
// не изменяет запись журнала и её payload после фиксации — FSM получает
// собственную копию структуры LogEntry. При условии, что вызывающий Apply
// не изменяет переданную команду после передачи, payload иммутабелен и его
// удержание после возврата из Apply допустимо (этим пользуется
// CommitChannelFSM). Мутировать payload FSM не должна: его байты разделяются
// между журналом и FSM.
//
// Потоковая модель (фактическое состояние кода):
//   - Apply/ApplyBatch вызываются только из горутины runFSM, сериализованы
//     очередью fsmMutateCh;
//   - Snapshot вызывается из той же горутины runFSM (через fsmSnapshotCh);
//   - Restore вызывается в двух случаях: стартовый путь (restoreFromSnapshotStore
//     из конструктора, до запуска горутин — однопоточен, безопасен) и RPC-путь
//     (InstallSnapshot, из горутины обработки RPC). В текущей реализации
//     RPC-путь Restore может выполняться параллельно с Apply — известный
//     дефект, планируется устранение; полагаться на это поведение не следует.
//
// Контракт жизненного цикла: Apply обязан завершаться —
// не блокироваться бесконечно. CM.Stop() присоединяет горутину runFSM
// через WaitGroup, и бесконечно блокирующий Apply привёл бы к вечному
// зависанию Stop(). В частности, CommitChannelFSM пишет в небуферизованный
// канал — при живом узле канал обязан иметь читателя.
type FSM interface {
	Apply(*LogEntry) any

	// Snapshot возвращает снимок текущего состояния FSM.
	// Должен выполняться быстро — только захват состояния.
	// Дорогая сериализация выполняется в FSMSnapshot.Persist.
	Snapshot() (FSMSnapshot, error)

	// Restore восстанавливает состояние FSM из снимка.
	// Должен сбросить все предыдущее состояние.
	Restore(io.ReadCloser) error
}

// FSMSnapshot — снимок состояния FSM, готовый к персистентности.
// Создаётся в FSM.Snapshot(), сериализуется в SnapshotSink через Persist.
//
// Контракт: Release вызывается всегда, даже если Persist не был вызван.
type FSMSnapshot interface {
	Persist(SnapshotSink) error
	Release()
}

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

// CommitChannelFSM оборачивает chan<- CommitEntry в FSM для
// обратной совместимости с тестовой инфраструктурой.
//
// Это test-only обёртка: она передаёт log.Data в канал и тем самым
// удерживает payload после возврата из Apply, полагаясь на право удержания
// из контракта FSM (payload иммутабелен при условии, что вызывающий Apply
// не изменяет команду после передачи).
type CommitChannelFSM struct {
	commitChan chan<- CommitEntry
}

var _ FSM = (*CommitChannelFSM)(nil)

func NewCommitChannelFSM(commitChan chan<- CommitEntry) *CommitChannelFSM {
	return &CommitChannelFSM{commitChan: commitChan}
}

func (f *CommitChannelFSM) Apply(log *LogEntry) any {
	if log.Type == LogNoop {
		return nil
	}
	f.commitChan <- CommitEntry{
		Data:  log.Data,
		Index: log.Index,
		Term:  log.Term,
	}
	return nil
}

func (f *CommitChannelFSM) Snapshot() (FSMSnapshot, error) {
	return nil, ErrNotImplemented
}

func (f *CommitChannelFSM) Restore(_ io.ReadCloser) error {
	return ErrNotImplemented
}

// BatchingFSM — опциональное расширение FSM для группового применения
// записей журнала. Если FSM клиента реализует этот интерфейс, runFSM
// вызывает ApplyBatch вместо последовательных вызовов Apply.
type BatchingFSM interface {
	FSM

	// ApplyBatch получает срез зафиксированных записей журнала и применяет
	// их к FSM за один вызов. Длина возвращаемого среза должна совпадать
	// с длиной входного — каждый ответ соответствует записи с тем же
	// индексом. Нарушение контракта приводит к ошибке
	// ErrBatchFSMResponseMismatch, которой отвечаются все future батча.
	//
	// Контракт владения и потоковая модель — как у FSM.Apply: переданные
	// *LogEntry — собственные копии, мутировать их и payload не следует;
	// вызов выполняется из горутины runFSM.
	ApplyBatch([]*LogEntry) []any
}
