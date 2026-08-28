package raft

import (
	"fmt"
	"io"
	"time"
)

// SetSnapshotConfig обновляет параметры снимков и сигнализирует
// runSnapshots о необходимости проверить, нужно ли создать снимок.
func (cm *ConsensusModule) SetSnapshotConfig(threshold int, interval time.Duration, trailing int) {
	cm.mu.Lock()
	cm.snapshotThreshold = threshold
	cm.snapshotInterval = interval
	cm.trailingLogs = trailing
	cm.mu.Unlock()

	select {
	case cm.snapshotCh <- struct{}{}:
	default:
	}
}

// handleFsmSnapshot обрабатывает запрос на создание снимка от runFSM.
// Вызывается из горутины runFSM при получении reqSnapshotFuture.
// Захватывает фактически применённый индекс и терм, вызывает fsm.Snapshot().
//
// Источник индекса — cmState.fsmAppliedIndex, а не cmState.lastApplied:
// lastApplied означает «диапазон отправлен в очередь машины состояний»
// и продвигается в processLogs до отправки батча, поэтому снимок,
// созданный по нему, мог не содержать результата применения записи с
// этим индексом. fsmAppliedIndex публикует та же горутина runFSM после
// возврата из вызова машины состояний, поэтому опережение невозможно
// по построению, независимо от планировщика.
func (cm *ConsensusModule) handleFsmSnapshot(req *reqSnapshotFuture) {
	startTimeNow := time.Now()
	defer func() { cm.latency.handleFsmSnapshot.observe(time.Since(startTimeNow)) }()

	// Захватываем индекс и терм под cm.mu: cm.cmState.fsmAppliedIndex и
	// cm.cmState.log изменяются из других горутин (handleInstallSnapshot,
	// restoreFromStorage), чтение без блокировки — data race.
	// fsm.Snapshot() вызываем после снятия блокировки — это дорогая операция.
	cm.mu.Lock()
	if cm.cmState.fsmAppliedIndex < 0 {
		cm.mu.Unlock()
		req.respond(ErrNothingNewToSnapshot)
		return
	}
	// Снимок с индексом, не превышающим lastSnapshotIndex, не добавляет
	// информации: compactLogs с тем же аргументом ничего не сжимает,
	// а повторение цикла «создать снимок → сжатие вхолостую» только
	// нагружает хранилище. Защищает и монотонность lastSnapshotIndex.
	if cm.cmState.fsmAppliedIndex <= cm.cmState.lastSnapshotIndex {
		cm.mu.Unlock()
		req.respond(ErrNothingNewToSnapshot)
		return
	}
	index := cm.cmState.fsmAppliedIndex
	term := cm.lookupTermLocked(index)
	if cm.cmState.fsmAppliedIndex < cm.cmState.lastApplied {
		// Снимок создаётся внутри окна, в котором машина состояний
		// отстаёт от диспетчеризации. Ранее индекс брался из lastApplied,
		// и снимок фиксировался с завышенным индексом.
		cm.counters.snapshotIndexBehindDispatched.Add(1)
		cm.traceLockedLogf(
			4, "handleFsmSnapshot: FSM behind dispatch: fsmAppliedIndex=%d lastApplied=%d lastSnapshotIndex=%d",
			cm.cmState.fsmAppliedIndex, cm.cmState.lastApplied, cm.cmState.lastSnapshotIndex,
		)
	}
	cm.mu.Unlock()

	snap, err := cm.fsm.Snapshot()
	if err != nil {
		req.respond(err)
		return
	}
	req.index = index
	req.term = term
	req.snapshot = snap
	req.respond(nil)
}

// Получает снимок от лидера, сохраняет в SnapshotStore,
// восстанавливает FSM и заменяет журнал.
func (cm *ConsensusModule) handleInstallSnapshot(rpc RPC, req *InstallSnapshotRequest) {
	startTimeNow := time.Now()
	defer func() { cm.latency.installSnapshot.observe(time.Since(startTimeNow)) }()

	resp := &InstallSnapshotResponse{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		},
		Success: false,
	}
	var rpcErr error
	defer func() {
		if rpc.Reader != nil {
			// Drain остатка данных снимка с лимитом, чтобы не зависнуть
			// при повреждённом соединении. Лимит maxSnapshotDataSize
			// гарантирует завершение drain даже при некорректном DataSize.
			_, _ = io.CopyN(io.Discard, rpc.Reader, maxSnapshotDataSize)
		}
		rpc.RespChan <- RPCResponse{Reply: resp, Error: rpcErr}
	}()

	cm.mu.Lock()
	proceed := cm.beginInstallSnapshotLocked(req, resp)
	cm.mu.Unlock()
	if !proceed {
		return
	}

	cfg, err := DecodeConfiguration(req.Configuration)
	if err != nil {
		rpcErr = err
		return
	}

	// Валидация DataSize: защита от паники io.Copy при некорректном размере.
	if req.DataSize <= 0 || req.DataSize > maxSnapshotDataSize {
		rpcErr = fmt.Errorf("invalid DataSize %d", req.DataSize)
		return
	}

	// Идемпотентность: если у узла уже есть снимок с LastLogIndex ≥ запрошенного,
	// установка не требуется (no-op).
	cm.mu.Lock()
	stale := req.LastLogIndex <= cm.cmState.lastSnapshotIndex
	if stale {
		cm.counters.installSnapshotSkippedStale = incPeerCount(cm.counters.installSnapshotSkippedStale, req.LeaderID)
	}
	lastSnapshotIndex := cm.cmState.lastSnapshotIndex
	cm.mu.Unlock()
	if stale {
		cm.traceLogf(
			4, "InstallSnapshot skipped as no-op: LastLogIndex=%d <= lastSnapshotIndex=%d (leaderID=%d)",
			req.LastLogIndex, lastSnapshotIndex, req.LeaderID,
		)
		resp.Success = true
		return
	}

	sinkID, err := cm.receiveAndSealSnapshot(rpc, req, cfg)
	if err != nil {
		rpcErr = err
		return
	}

	// Restore через прямой вызов fsm.Restore.
	meta, snapReader, err := cm.snapshotStore.Open(sinkID)
	if err != nil {
		rpcErr = err
		return
	}
	defer func() { _ = snapReader.Close() }()

	if err := cm.fsm.Restore(snapReader); err != nil {
		rpcErr = err
		return
	}

	cm.mu.Lock()
	cm.installSnapshotStateLocked(meta)
	cm.mu.Unlock()

	cm.counters.installSnapshotReceived.Add(1)
	resp.Success = true
}

// beginInstallSnapshotLocked снимает терм для ответа, отклоняет запрос
// с устаревшим термом и обновляет отметки контакта с лидером. Возвращает
// false, если снимок принимать не нужно.
// Требует удержания cm.mu.
func (cm *ConsensusModule) beginInstallSnapshotLocked(
	req *InstallSnapshotRequest, resp *InstallSnapshotResponse,
) (proceed bool) {
	// Term снимается под cm.mu: чтение cm.cmState.currentTerm
	// вне критической секции — data race.
	resp.Term = cm.cmState.currentTerm
	if req.Term < cm.cmState.currentTerm {
		return false
	}
	if req.Term > cm.cmState.currentTerm {
		cm.becomeFollowerLocked(req.Term)
		resp.Term = req.Term
	}
	cm.cmState.electionResetEvent = time.Now()
	cm.cmState.leaderLastContact = time.Now()
	cm.cmState.leaderID = req.LeaderID
	return true
}

// installSnapshotStateLocked заменяет состояние узла метаданными
// принятого снимка: водяные знаки журнала и снимка, отметки применения
// и фиксации, уплотнение журнала и запись постоянного состояния.
// Требует удержания cm.mu.
func (cm *ConsensusModule) installSnapshotStateLocked(meta *SnapshotMeta) {
	cm.cmState.lastLogIndex = meta.Index
	cm.cmState.lastLogTerm = meta.Term
	cm.cmState.lastSnapshotIndex = meta.Index
	cm.cmState.lastSnapshotTerm = meta.Term
	cm.cmState.lastApplied = meta.Index
	// fsm.Restore замещает состояние машины состояний целиком, поэтому
	// отметка фактически применённого индекса выставляется безусловно —
	// в том числе если она была больше meta.Index.
	cm.cmState.fsmAppliedIndex = meta.Index
	cm.cmState.commitIndex = meta.Index
	// Сжимаем журнал: заменяем срез (make+copy), без изменения на месте.
	pos := cm.logPositionLocked(meta.Index)
	if pos < len(cm.cmState.log) && cm.cmState.log[pos].Index == meta.Index {
		kept := make([]LogEntry, len(cm.cmState.log)-pos-1)
		copy(kept, cm.cmState.log[pos+1:])
		cm.cmState.log = kept
	} else {
		cm.cmState.log = nil
	}
	cm.rebuildLastLog()
	cm.rebuildTermIndexMap()
	// Проверка непрерывности границы снимка и журнала (защита от регрессий).
	// При нарушении — только счётчик и трассировка, без паники и изменения Success.
	if err := cm.checkSnapshotLogContinuity(); err != nil {
		cm.counters.snapshotLogBoundaryViolation.Add(1)
		cm.traceLockedLogf(
			0, "handleInstallSnapshot: snapshot/log boundary violation: %v (lastLogIndex=%d, lastSnapshotIndex=%d)",
			err, cm.cmState.lastLogIndex, cm.cmState.lastSnapshotIndex,
		)
	}
	cm.cmState.logNeedsPersist = true
	cm.persistToStorage()
}

// receiveAndSealSnapshot принимает тело снимка от лидера в приёмник
// хранилища снимков и запечатывает его, возвращая идентификатор готового
// снимка. На каждом ошибочном пути приёмник отменяется, а ошибка
// возвращается вызывающему для ответа RPC.
// Блокировку cm.mu не захватывает и не снимает.
func (cm *ConsensusModule) receiveAndSealSnapshot(
	rpc RPC, req *InstallSnapshotRequest, cfg Configuration,
) (sinkID string, err error) {
	sink, err := cm.snapshotStore.Create(req.LastLogIndex, req.LastLogTerm, cfg, req.ConfigIndex)
	if err != nil {
		return "", err
	}
	if sink == nil {
		return "", fmt.Errorf("snapshotStore.Create returned nil sink without error")
	}

	if rpc.Reader == nil {
		_ = sink.Cancel()
		return "", fmt.Errorf("InstallSnapshot with nil Reader")
	}
	n, err := io.Copy(sink, rpc.Reader)
	if err != nil {
		_ = sink.Cancel()
		return "", err
	}
	if req.DataSize > 0 && n != req.DataSize {
		_ = sink.Cancel()
		return "", fmt.Errorf("snapshot size mismatch: got %d, expected %d", n, req.DataSize)
	}

	if err := sink.Close(); err != nil {
		return "", err
	}

	// Валидация ID снепшота перед Open.
	// Некорректный ID (пустая строка) может вызвать панику
	// в реализации SnapshotStore.
	sinkID = sink.ID()
	if sinkID == "" {
		_ = sink.Cancel()
		return "", fmt.Errorf("sink.ID() is empty")
	}
	return sinkID, nil
}

// runSnapshots — фоновая горутина управления снимками.
// Запускается только если cm.snapshotStore != nil.
// Проверяет необходимость снимка по таймеру или при сигнале из snapshotCh.
// Завершается при закрытии shutdownCh.
func (cm *ConsensusModule) runSnapshots() {
	for {
		cm.mu.Lock()
		interval := cm.snapshotInterval
		cm.mu.Unlock()

		select {
		case <-cm.snapshotCh:
			if cm.shouldSnapshot() {
				if err := cm.takeSnapshot(); err != nil {
					// Ошибки создания снимка не должны быть тихими
					// ретрай обеспечивает следующий цикл.
					cm.traceLogf(0, "runSnapshots: takeSnapshot failed: %v", err)
				}
			}
		case <-time.After(interval):
			if cm.shouldSnapshot() {
				if err := cm.takeSnapshot(); err != nil {
					cm.traceLogf(0, "runSnapshots: takeSnapshot failed: %v", err)
				}
			}
		case <-cm.shutdownCh:
			return
		}
	}
}

// takeSnapshot создаёт снимок и сжимает журнал.
// Вызывается только из runSnapshots.
func (cm *ConsensusModule) takeSnapshot() error {
	startTimeNow := time.Now()
	defer func() { cm.latency.takeSnapshot.observe(time.Since(startTimeNow)) }()
	snapReq := &reqSnapshotFuture{}
	snapReq.init(cm.shutdownCh)

	select {
	case cm.fsmSnapshotCh <- snapReq:
	case <-cm.shutdownCh:
		return ErrRaftShutdown
	case <-time.After(defaultTakeSnapshotTimeout):
		cm.mu.Lock()
		applied := cm.cmState.fsmAppliedIndex
		dispatched := cm.cmState.lastApplied
		lastSnapshotIndex := cm.cmState.lastSnapshotIndex
		cm.mu.Unlock()
		return fmt.Errorf(
			"takeSnapshot: timeout sending to fsmSnapshotCh "+
				"(fsmAppliedIndex=%d, lastApplied=%d, lastSnapshotIndex=%d, pending batches=%d)",
			applied, dispatched, lastSnapshotIndex, len(cm.fsmMutateCh),
		)
	}

	if err := snapReq.Error(); err != nil {
		if err == ErrNothingNewToSnapshot {
			return nil
		}
		return err
	}
	if snapReq.snapshot == nil {
		return fmt.Errorf("takeSnapshot: snapshot is nil after successful FSM response")
	}
	defer snapReq.snapshot.Release()

	cm.mu.Lock()
	committed := cm.cmState.configurations.committed
	committedIndex := cm.cmState.configurations.committedIndex
	cm.mu.Unlock()

	sink, err := cm.snapshotStore.Create(snapReq.index, snapReq.term, committed, committedIndex)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	if sink == nil {
		return fmt.Errorf("snapshotStore.Create returned nil sink without error")
	}

	if err := snapReq.snapshot.Persist(sink); err != nil {
		_ = sink.Cancel()
		return fmt.Errorf("failed to persist snapshot: %w", err)
	}

	if err := sink.Close(); err != nil {
		return fmt.Errorf("failed to close snapshot: %w", err)
	}

	cm.mu.Lock()
	cm.cmState.lastSnapshotIndex = snapReq.index
	cm.cmState.lastSnapshotTerm = snapReq.term
	cm.compactLogs(snapReq.index - cm.trailingLogs)
	cm.persistToStorage()
	cm.mu.Unlock()

	return nil
}

// shouldSnapshot проверяет, нужно ли создать снимок.
// Снимок нужен, если с момента последнего снимка накопилось
// больше snapshotThreshold записей.
func (cm *ConsensusModule) shouldSnapshot() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.lastLogIndex < 0 {
		return false
	}
	if cm.snapshotStore == nil {
		return false
	}
	delta := cm.cmState.lastLogIndex - cm.cmState.lastSnapshotIndex
	return delta >= cm.snapshotThreshold
}
