package raft

import (
	"fmt"
	"io"
	"testing"

	"github.com/vskurikhin/raft/pkg/raft/store"
)

// benchNoopFSM — тривиальная машина состояний для измерений apply-пути.
type benchNoopFSM struct{}

func (benchNoopFSM) Apply(*LogEntry) any { return nil }

func (benchNoopFSM) Snapshot() (FSMSnapshot, error) { return nil, nil }

func (benchNoopFSM) Restore(io.ReadCloser) error { return nil }

// newBenchCM собирает ConsensusModule с постоянным хранилищем в каталоге dir
// прямой инициализацией структуры, без запуска горутин.
func newBenchCM(dir string) *ConsensusModule {
	cm := &ConsensusModule{storage: store.NewFileStorage(dir)}
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	return cm
}

// BenchmarkPersistToStorageUnchanged измеряет сохранение состояния, при
// котором журнал не менялся: пишутся только дешёвые ключи.
func BenchmarkPersistToStorageUnchanged(b *testing.B) {
	cm := newBenchCM(b.TempDir())
	cm.cmState.log = benchLogEntries(100, benchValueSizeLog)
	cm.cmState.logNeedsPersist = false
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.persistToStorage()
	}
}

// BenchmarkPersistToStorageLogChanged измеряет сохранение состояния, при
// котором журнал меняется на каждой итерации и переписывается целиком.
func BenchmarkPersistToStorageLogChanged(b *testing.B) {
	cm := newBenchCM(b.TempDir())
	cm.cmState.log = benchLogEntries(100, benchValueSizeLog)
	last := len(cm.cmState.log) - 1
	b.SetBytes(int64(benchValueSizeLog))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.cmState.log[last].Term = i + 1
		cm.cmState.logNeedsPersist = true
		cm.persistToStorage()
	}
}

// BenchmarkProcessLogsFileStorage измеряет продвижение применённых записей
// на постоянном хранилище с тривиальной машиной состояний.
func BenchmarkProcessLogsFileStorage(b *testing.B) {
	cm := newBenchCM(b.TempDir())
	cm.fsm = benchNoopFSM{}
	cm.fsmMutateCh = make(chan []*commitTuple, 1024)
	cm.shutdownCh = make(chan struct{})
	cm.leaderState.inflight = make(map[int]*logFuture)
	cm.cmState.log = benchLogEntries(b.N, b.N*8)
	cm.cmState.lastLogIndex = b.N - 1
	cm.cmState.lastLogTerm = 1
	cm.cmState.lastApplied = -1
	cm.cmState.logNeedsPersist = false

	// Батчи забирает отдельный потребитель: без него отправка в fsmMutateCh
	// заблокировалась бы при заполнении буфера.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			select {
			case <-cm.fsmMutateCh:
			case <-cm.shutdownCh:
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.processLogs(i)
	}
	b.StopTimer()
	close(cm.shutdownCh)
	<-consumerDone
}

// benchValueSizeLog — размер, сопоставимый с журналом работающего узла.
// Локальная копия, предназначенная для тестов пакета raft.
const benchValueSizeLog = 97 * 1024

// benchLogEntries формирует журнал, кодированный размер которого близок
// к totalBytes: полезная нагрузка распределена по entries записям.
// Локальная копия, предназначенная для тестов пакета raft.
func benchLogEntries(entries, totalBytes int) []LogEntry {
	payload := totalBytes / entries
	log := make([]LogEntry, 0, entries)
	for i := 0; i < entries; i++ {
		log = append(log, LogEntry{
			Index: i,
			Term:  1,
			Type:  LogCommand,
			Data:  fmt.Sprintf("%0*d", payload, i),
		})
	}
	return log
}
