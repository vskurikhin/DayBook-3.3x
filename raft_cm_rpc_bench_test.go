package raft

import (
	"testing"
)

// Размер базового журнала ведомого во всех трёх формах бенчмарка и размер
// батча записей, приписываемых в форме с записями. Значения фиксированы,
// чтобы база «до» и последующие измерения сравнивались при одинаковом
// состоянии приспособления.
const (
	benchAppendInitialLog = 100
	benchAppendBatch      = 16
	// benchAppendData — небольшая полезная нагрузка записей батча.
	// Содержимое данных не влияет на измеряемый путь обработчика:
	// записи типа LogCommand не разбираются и не применяются.
	benchAppendData = "cmd"
)

// newAppendEntriesBenchCM собирает ведомого с журналом из n записей
// (индексы 1..n, терм 1, тип LogCommand) прямой инициализацией структуры,
// без запущенных горутин. Индекс фиксации равен последнему индексу журнала,
// поэтому LeaderCommit, не превосходящий его, не продвигает индекс фиксации,
// и путь processLogs не входит. Приспособление повторяет поля ведомого из
// тестов обработчика и не содержит недостижимых сочетаний.
func newAppendEntriesBenchCM(n int) *ConsensusModule {
	cm := &ConsensusModule{}
	cm.storage = NewMapStorage()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 1
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.commitIndex = n
	cm.cmState.log = make([]LogEntry, n)
	for i := range cm.cmState.log {
		cm.cmState.log[i] = LogEntry{
			Index: i + 1,
			Term:  1,
			Type:  LogCommand,
		}
	}
	cm.cmState.lastLogIndex = n
	cm.cmState.lastLogTerm = 1
	cm.cmState.termIndexMap = map[int]int{1: n}
	return cm
}

// makeAppendEntriesBatch формирует батч из n записей в терме term, начиная
// с индекса startIndex. Используется как подготовка на итерацию в форме
// с записями: батч пересоздаётся каждый раз, чтобы каждая итерация
// выполняла настоящую вставку в журнал.
func makeAppendEntriesBatch(startIndex, n, term int) []LogEntry {
	entries := make([]LogEntry, n)
	for i := range entries {
		entries[i] = LogEntry{
			Index: startIndex + i,
			Term:  term,
			Type:  LogCommand,
			Data:  benchAppendData,
		}
	}
	return entries
}

// BenchmarkCMAppendEntriesHeartbeat измеряет обработчик AppendEntries на
// ведомом в ветке пульса: PrevLogIndex/PrevLogTerm совпадают с концом
// журнала, батч записей пуст, LeaderCommit не продвигает индекс фиксации,
// поэтому путь processLogs не входит. Измеряемый вызов — экспортируемый
// обработчик cm.AppendEntries(args, &reply).
func BenchmarkCMAppendEntriesHeartbeat(b *testing.B) {
	cm := newAppendEntriesBenchCM(benchAppendInitialLog)
	args := AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         cm.cmState.currentTerm,
		LeaderID:     99,
		PrevLogIndex: cm.cmState.lastLogIndex,
		PrevLogTerm:  cm.cmState.lastLogTerm,
		LeaderCommit: cm.cmState.commitIndex,
	}
	var reply AppendEntriesReply

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cm.AppendEntries(args, &reply)
	}
}

// BenchmarkCMAppendEntriesEntries измеряет обработчик AppendEntries на
// ведомом с приписыванием батча записей фиксированного размера
// (benchAppendBatch). Батч пересоздаётся на каждой итерации, а журнал
// возвращается к базовому состоянию вне измеряемого пути, чтобы каждая
// итерация выполняла настоящую вставку и при этом журнал не рос
// неограниченно (иначе перекодирование журнала в постоянном хранилище
// доминировало бы над измеряемым эффектом).
func BenchmarkCMAppendEntriesEntries(b *testing.B) {
	cm := newAppendEntriesBenchCM(benchAppendInitialLog)
	baseLog := cm.cmState.log
	var reply AppendEntriesReply

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Подготовка на итерацию: восстановление согласованного состояния
		// ведомого и пересоздание батча. Не входит в измеряемый путь.
		b.StopTimer()
		cm.cmState.log = baseLog
		cm.cmState.lastLogIndex = benchAppendInitialLog
		cm.cmState.lastLogTerm = 1
		cm.cmState.termIndexMap = map[int]int{1: benchAppendInitialLog}
		args := AppendEntriesArgs{
			RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
			Term:         1,
			LeaderID:     99,
			PrevLogIndex: benchAppendInitialLog,
			PrevLogTerm:  1,
			Entries:      makeAppendEntriesBatch(benchAppendInitialLog+1, benchAppendBatch, 1),
			LeaderCommit: benchAppendInitialLog,
		}
		b.StartTimer()

		_ = cm.AppendEntries(args, &reply)
	}
}

// BenchmarkCMAppendEntriesConflict измеряет обработчик AppendEntries на
// ведомом в ветке конфликтного ответа: PrevLogTerm не совпадает с термом
// записи журнала на PrevLogIndex, поэтому обработчик строит ответ с
// ConflictIndex/ConflictTerm. Журнал и индекс фиксации не меняются.
func BenchmarkCMAppendEntriesConflict(b *testing.B) {
	cm := newAppendEntriesBenchCM(benchAppendInitialLog)
	args := AppendEntriesArgs{
		RPCHeader:    RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: 99},
		Term:         cm.cmState.currentTerm,
		LeaderID:     99,
		PrevLogIndex: cm.cmState.lastLogIndex,
		PrevLogTerm:  0, // не совпадает с термом 1 записи журнала
		LeaderCommit: cm.cmState.commitIndex,
	}
	var reply AppendEntriesReply

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cm.AppendEntries(args, &reply)
	}
}
