package raft

import (
	"fmt"
	"time"
)

// applyBatch применяет все LogCommand записи из батча за один вызов
// ApplyBatch переданного BatchingFSM. Non-command записи (noop)
// разрешаются с nil-ответом без вызова FSM. При несовпадении длины
// возвращаемого среза с числом записей все future батча отвечают ошибкой
// ErrBatchFSMResponseMismatch. Паника в этом месте недопустима:
// она упала бы в горутине runFSM и убила бы весь процесс.
func (cm *ConsensusModule) applyBatch(fsm BatchingFSM, batch []*commitTuple) {
	var logs []*LogEntry
	var futures []*logFuture
	var nonCmdFutures []*logFuture
	for _, ct := range batch {
		if ct.log.Type != LogCommand {
			if ct.future != nil {
				nonCmdFutures = append(nonCmdFutures, ct.future)
			}
			continue
		}
		logs = append(logs, ct.log)
		futures = append(futures, ct.future)
	}
	for _, f := range nonCmdFutures {
		f.response = nil
		f.respond(nil)
	}
	if len(logs) == 0 {
		return
	}
	responses := fsm.ApplyBatch(logs)
	if len(responses) != len(logs) {
		// Внешний FSM нарушил контракт BatchingFSM: число ответов не
		// совпадает с числом записей. Нельзя доверять частичному
		// результату, поэтому FSM-ответы не интерпретируются. Отвечаем
		// ошибкой во все future батча (они ещё не отвечены — noop/non-command
		// записи обработаны раньше и в срез futures не входят) и выходим:
		// runFSM продолжает жить, следующие батчи обрабатываются как обычно.
		err := fmt.Errorf("%w: got %d, want %d",
			ErrBatchFSMResponseMismatch, len(responses), len(logs))
		for _, f := range futures {
			if f != nil {
				f.respond(err)
			}
		}
		return
	}
	for i, f := range futures {
		if f != nil {
			f.response = responses[i]
			f.respond(nil)
		}
	}
}

// applySingle применяет каждую запись из батча по отдельности через
// cm.fsm.Apply. Используется для FSM, не реализующих BatchingFSM.
func (cm *ConsensusModule) applySingle(batch []*commitTuple) {
	for _, ct := range batch {
		if ct.log.Type != LogCommand {
			if ct.future != nil {
				ct.future.response = nil
				ct.future.respond(nil)
			}
			continue
		}
		resp := cm.fsm.Apply(ct.log)
		if ct.future != nil {
			ct.future.response = resp
			ct.future.respond(nil)
		}
	}
}

// processLogs собирает зафиксированные записи от lastApplied+1 до commitIndex
// и отправляет их в FSM goroutine. Диапазон делится на под-батчи размером
// не более _maxApplyBatchSize для снижения latency FSM.
// Метод НЕ ТРЕБУЕТ удержания cm.mu при вызове, но сам захватывает её
// внутри для чтения журнала и поиска future в карте inflight.
func (cm *ConsensusModule) processLogs(commitIndex int) {
	startTimeNow := time.Now()
	defer func() { cm.latency.processLogs.observe(time.Since(startTimeNow)) }()
	cm.mu.Lock()
	if commitIndex <= cm.cmState.lastApplied {
		cm.mu.Unlock()
		return
	}
	lastApplied := cm.cmState.lastApplied
	cm.cmState.lastApplied = commitIndex
	cm.persistToStorage()
	cm.mu.Unlock()

	start := lastApplied + 1
	for start <= commitIndex {
		end := start + _maxApplyBatchSize - 1
		if end > commitIndex {
			end = commitIndex
		}
		cm.sendBatch(start, end)
		start = end + 1
	}
}

// runFSM — отдельная горутина, применяющая зафиксированные записи к FSM.
// Если FSM реализует BatchingFSM, использует ApplyBatch для группового
// применения; иначе вызывает Apply для каждой записи по отдельности.
// Type assertion выполняется один раз при старте горутины.
// Завершает работу при закрытии shutdownCh.
//
// После возврата из применения батча публикует отметку фактически
// применённого индекса (publishFsmApplied): это единственная точка
// продвижения cmState.fsmAppliedIndex в штатной работе.
func (cm *ConsensusModule) runFSM() {
	batchingFSM, supportsBatch := cm.fsm.(BatchingFSM)
	for {
		select {
		case batch := <-cm.fsmMutateCh:
			// Замер охватывает только применение батча к FSM:
			// прежний defer на выходе измерял время жизни горутины.
			// Наблюдение — атомарная операция, мьютексов не захватывает.
			start := time.Now()
			if supportsBatch {
				cm.applyBatch(batchingFSM, batch)
			} else {
				cm.applySingle(batch)
			}
			cm.latency.fsmApply.observe(time.Since(start))
			cm.publishFsmApplied(batch)
		case req := <-cm.fsmSnapshotCh:
			cm.handleFsmSnapshot(req)
		case <-cm.shutdownCh:
			return
		}
	}
}

// publishFsmApplied продвигает отметку фактически применённого индекса до
// индекса последней записи батча. Вызывается только из runFSM, строго после
// возврата из applyBatch/applySingle: удерживать cm.mu во время вызова
// машины состояний запрещено.
//
// Индекс последнего элемента — максимальный в батче: sendBatch заполняет
// батч строго по возрастанию idx (см. цикл в sendBatch).
//
// Продвижение монотонно (max): запоздалый батч со старыми индексами не
// должен откатывать отметку назад после установки снимка.
// Продвижение выполняется независимо от результата применения, включая
// путь ErrBatchFSMResponseMismatch: записи уже переданы машине состояний,
// и вызов вернул управление. Иначе отметка замерла бы навсегда и снимки
// перестали бы создаваться.
func (cm *ConsensusModule) publishFsmApplied(batch []*commitTuple) {
	if len(batch) == 0 {
		return
	}
	batchMax := batch[len(batch)-1].log.Index
	cm.mu.Lock()
	if batchMax > cm.cmState.fsmAppliedIndex {
		cm.cmState.fsmAppliedIndex = batchMax
	}
	cm.mu.Unlock()
}

// sendBatch собирает под-батч записей журнала от start до end включительно
// и отправляет в fsmMutateCh. Поиск future в inflight выполняется за O(1)
// через map[int]*logFuture. Блокировка cm.mu удерживается только на время
// поиска и чтения журнала, отправка — без блокировки.
//
// Записи добавляются в батч строго по возрастанию индекса — на этом
// свойстве основано вычисление максимума батча за O(1) в publishFsmApplied.
func (cm *ConsensusModule) sendBatch(start, end int) {
	startTimeNow := time.Now()
	defer func() { cm.latency.sendBatch.observe(time.Since(startTimeNow)) }()
	cm.mu.Lock()
	batch := make([]*commitTuple, 0, end-start+1)
	for idx := start; idx <= end; idx++ {
		future := cm.leaderState.inflight[idx]
		delete(cm.leaderState.inflight, idx)
		if idx > cm.cmState.lastLogIndex {
			cm.counters.sendBatchEntrySkipped.Add(1)
			continue
		}
		pos := cm.logPositionLocked(idx)
		if pos >= len(cm.cmState.log) {
			cm.traceLockedLogf(
				_traceLevelKeyEvents,
				"sendBatch: logPositionLocked(%d) returned %d, len(log)=%d",
				idx, pos, len(cm.cmState.log),
			)
			cm.counters.sendBatchEntrySkipped.Add(1)
			continue
		}
		// Защита от применения чужой записи: для idx ниже первого индекса
		// журнала бинарный поиск возвращает позицию 0, но запись log[0]
		// не соответствует idx и не должна применяться к FSM.
		if cm.cmState.log[pos].Index != idx {
			cm.traceLockedLogf(
				_traceLevelKeyEvents,
				"sendBatch: no log entry at index %d (entry=%d), skipping",
				idx, cm.cmState.log[pos].Index,
			)
			cm.counters.sendBatchEntrySkipped.Add(1)
			continue
		}
		// Копия значения записи журнала под cm.mu обязательна:
		// после cm.mu.Unlock() элемент батча не должен ссылаться в
		// cm.cmState.log. Единственный in-place писатель существующих слотов —
		// конфликтная перезапись в AppendEntries (raft_cm_rpc.go:149); без
		// копии горутина runFSM читала бы запись, которую может перезаписать
		// следующий AppendEntries, пока батч ждёт в fsmMutateCh.
		entry := cm.cmState.log[pos]
		batch = append(batch, &commitTuple{log: &entry, future: future})
	}
	cm.mu.Unlock()
	if len(batch) > 0 {
		// Отправка с веткой shutdownCh: после выхода
		// runFSM по shutdownCh канал fsmMutateCh не дренируется, и при
		// заполненном буфере (1024) простая отправка блокировалась бы
		// навсегда — wg.Wait() в Stop() не завершился бы.
		select {
		case cm.fsmMutateCh <- batch:
		case <-cm.shutdownCh:
			return
		}
	}
}
