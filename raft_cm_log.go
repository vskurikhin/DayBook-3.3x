package raft

import (
	"fmt"
	"sort"
	"time"
)

// lastLogIndexAndTerm возвращает кэшированный индекс и терм
// последней записи в журнале. Не вычисляет из len(cm.cmState.log).
// Требует удержания cm.mu.
func (cm *ConsensusModule) lastLogIndexAndTerm() (int, int) {
	return cm.cmState.lastLogIndex, cm.cmState.lastLogTerm
}

// logPositionLocked возвращает позицию в срезе cm.cmState.log для записи с данным Index.
// Использует бинарный поиск. Если запись не найдена, возвращает
// позицию первой записи с Index >= заданного (место для вставки).
// Требует удержания cm.mu.
func (cm *ConsensusModule) logPositionLocked(index int) int {
	return sort.Search(len(cm.cmState.log), func(i int) bool {
		return cm.cmState.log[i].Index >= index
	})
}

// lookupTermLocked возвращает терм для записи с указанным index.
// Использует бинарный поиск: после внедрения снимков позиция
// записи в cm.cmState.log может не совпадать с её индексом.
// Требует удержания cm.mu.
func (cm *ConsensusModule) lookupTermLocked(index int) int {
	lo, hi := 0, len(cm.cmState.log)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case cm.cmState.log[mid].Index == index:
			return cm.cmState.log[mid].Term
		case cm.cmState.log[mid].Index < index:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	// Fallback: запись может быть в снепшоте
	if index == cm.cmState.lastSnapshotIndex {
		return cm.cmState.lastSnapshotTerm
	}
	return -1
}

// rebuildLastLog восстанавливает кэш lastLogIndex/lastLogTerm
// из текущего содержимого cm.cmState.log.
// Вызывается из restoreFromStorage() и других мест, где журнал
// мог измениться без вызова setLastLog.
//
// Инвариант: при пустом срезе журнала последняя
// запись логического журнала — граница снимка, поэтому
// lastLogIndex/lastLogTerm берутся из lastSnapshotIndex/lastSnapshotTerm.
// Для свежего узла без снимка (lastSnapshotIndex == -1) поведение
// совпадает с прежним: (-1, -1).
// Требует удержания cm.mu (Lock).
func (cm *ConsensusModule) rebuildLastLog() {
	if len(cm.cmState.log) > 0 {
		last := cm.cmState.log[len(cm.cmState.log)-1]
		cm.cmState.lastLogIndex = last.Index
		cm.cmState.lastLogTerm = last.Term
		return
	}
	cm.cmState.lastLogIndex = cm.cmState.lastSnapshotIndex
	cm.cmState.lastLogTerm = cm.cmState.lastSnapshotTerm
}

// rebuildTermIndexMap перестраивает карту term→lastIndex из текущего cm.cmState.log.
// Вызывается после любого изменения журнала, которое не является простым
// append в конец: обрезка (AppendEntries conflict), сжатие
// (compactLogs), замена (installSnapshot), загрузка (restoreFromStorage).
// Сложность O(n) по длине журнала. Вызывается редко — только при
// несовпадении термов на ведомом или сжатии.
// Требует удержания cm.mu (Lock).
func (cm *ConsensusModule) rebuildTermIndexMap() {
	cm.cmState.termIndexMap = make(map[int]int)
	for _, entry := range cm.cmState.log {
		cm.cmState.termIndexMap[entry.Term] = entry.Index
	}
}

// setLastLog обновляет кэш lastLogIndex/lastLogTerm.
// Вызывается после любого изменения журнала (добавление записей,
// удаление при конфликте, восстановление из storage).
// Требует удержания cm.mu (Lock).
func (cm *ConsensusModule) setLastLog(index, term int) {
	cm.cmState.lastLogIndex = index
	cm.cmState.lastLogTerm = term
}

// compactLogs удаляет записи из cm.cmState.log с Index < compactIndex.
// Оставляет как минимум trailingLogs записей.
// Вызывается под cm.mu.Lock().
//
// Инвариант сжатия (Compaction Safety): замена backing array
// допустима ТОЛЬКО аллокацией нового среза (make+copy). In-place обрезка вида
// log = log[pos:] с переиспользованием того же массива запрещена: сформированные
// в sendBatch батчи FSM хранят собственные копии записей, но будущий рефакторинг
// на in-place обрезку молча сломал бы гарантию «применяемые записи не зависят
// от мутаций старого массива».
func (cm *ConsensusModule) compactLogs(compactIndex int) {
	if compactIndex <= 0 {
		return
	}
	pos := cm.logPositionLocked(compactIndex)
	if pos <= 0 {
		return
	}
	kept := make([]LogEntry, len(cm.cmState.log)-pos)
	copy(kept, cm.cmState.log[pos:])
	cm.cmState.log = kept
	cm.cmState.logNeedsPersist = true
	if pos > 0 {
		cm.rebuildTermIndexMap()
	}
}

// dispatchLogs присваивает индексы и термы записям, добавляет их в журнал
// лидера (cm.cmState.log), помещает future в очередь inflight и инициирует
// репликацию через commitmentTracker.
//
// Вызывается только из runLeaderLoop после проверки, что сервер всё ещё
// является лидером. После возврата runLeaderLoop вызывает leaderSendAEs
// для немедленной репликации.
//
// Предполагается, что будущие ответы будут разрешены после фиксации
// в processLogs, либо при потере лидерства в runLeaderLoop.
func (cm *ConsensusModule) dispatchLogs(applyLogs []*logFuture) {
	cm.mu.Lock()
	cm.dispatchLogsUnsafe(applyLogs)

	// Защита от nil cm.cmState.log после dispatchLogsUnsafe. В нормальном состоянии
	// append гарантирует non-nil срез, но defensive check предотвращает
	// панику в commitmentTracker.commit при нарушении инварианта.
	if cm.cmState.log == nil {
		for _, f := range applyLogs {
			f.respond(ErrLeadershipLost)
		}
		cm.mu.Unlock()
		return
	}

	// Продвигаем commitIndex через commitmentTracker.
	// Для single-node кластера это приводит к немедленной фиксации.
	// Для multi-node кластера учитываются matchIndex всех peer-ов.
	cm.leaderState.commitmentTracker.commit(cm.cmState.lastLogIndex, cm.lookupTermLocked)
	savedCommitIndex := cm.cmState.commitIndex
	newCommitIdx := cm.leaderState.commitmentTracker.getCommitIndex()
	if newCommitIdx > cm.cmState.commitIndex {
		cm.traceLockedLogf(2, "leader sets commitIndex := %d", newCommitIdx)
		cm.cmState.commitIndex = newCommitIdx
	}
	newCommitIndex := cm.cmState.commitIndex
	cm.mu.Unlock()

	if newCommitIndex > savedCommitIndex {
		cm.processLogs(newCommitIndex)
	}
}

// dispatchLogsUnsafe — внутренняя версия dispatchLogs без захвата блокировки.
// Вызывающий должен удерживать cm.mu.
func (cm *ConsensusModule) dispatchLogsUnsafe(applyLogs []*logFuture) {
	term := cm.cmState.currentTerm
	lastIndex := cm.cmState.lastLogIndex
	now := time.Now()
	for _, f := range applyLogs {
		f.dispatch = now
		lastIndex++
		f.log.Index = lastIndex
		f.log.Term = term
		cm.cmState.termIndexMap[term] = lastIndex
		cm.cmState.log = append(cm.cmState.log, f.log)
	}
	cm.setLastLog(lastIndex, term)
	cm.cmState.logNeedsPersist = true
	cm.persistToStorage()
	cm.leaderState.matchIndex[cm.id] = lastIndex

	for _, f := range applyLogs {
		cm.leaderState.inflight[f.log.Index] = f
	}
}

// processConfigurationLogEntry обрабатывает LogConfiguration запись
// на follower при репликации.
func (cm *ConsensusModule) processConfigurationLogEntry(entry *LogEntry) {
	entryData, ok := entry.Data.([]byte)
	if !ok {
		return
	}
	cfg, err := DecodeConfiguration(entryData)
	if err != nil {
		return
	}
	cm.cmState.configurations.committed = cm.cmState.configurations.latest
	cm.cmState.configurations.committedIndex = cm.cmState.configurations.latestIndex
	cm.cmState.configurations.latest = cfg
	cm.cmState.configurations.latestIndex = entry.Index
}

// processLogConflict вызывается при конфликте логов (перезаписи) в
// AppendEntries. Если конфликт затрагивает latest конфигурацию,
// откатываем её до committed.
func (cm *ConsensusModule) processLogConflict(conflictIndex int) {
	if conflictIndex <= cm.cmState.configurations.latestIndex && conflictIndex > 0 {
		cm.cmState.configurations.latest = cm.cmState.configurations.committed
		cm.cmState.configurations.latestIndex = cm.cmState.configurations.committedIndex
	}
}

// rebuildConfigurations восстанавливает конфигурации из журнала после
// загрузки из хранилища. Сканирует лог в поиске LogConfiguration записей.
func (cm *ConsensusModule) rebuildConfigurations() {
	cm.setInitialConfiguration()
	for i, entry := range cm.cmState.log {
		if entry.Type != LogConfiguration {
			continue
		}
		entryData, ok := entry.Data.([]byte)
		if !ok {
			continue
		}
		cfg, err := DecodeConfiguration(entryData)
		if err != nil {
			continue
		}
		cm.cmState.configurations.committed = cm.cmState.configurations.latest
		cm.cmState.configurations.committedIndex = cm.cmState.configurations.latestIndex
		cm.cmState.configurations.latest = cfg
		cm.cmState.configurations.latestIndex = i
	}
}

// setInitialConfiguration создаёт начальную конфигурацию на основе peerIds.
// Все серверы из peerIds становятся голосующими.
func (cm *ConsensusModule) setInitialConfiguration() {
	servers := make([]ConfigServer, 0, len(cm.peerIds)+1)
	servers = append(servers, ConfigServer{
		ID:       ServerID(cm.id),
		Address:  ServerAddress(fmt.Sprintf("%d", cm.id)),
		Suffrage: Voter,
	})
	for _, peerID := range cm.peerIds {
		servers = append(servers, ConfigServer{
			ID:       ServerID(peerID),
			Address:  ServerAddress(fmt.Sprintf("%d", peerID)),
			Suffrage: Voter,
		})
	}
	cfg := Configuration{ConfigServers: servers}
	cm.cmState.configurations = configurations{
		committed:      cfg,
		committedIndex: 0,
		latest:         cfg,
		latestIndex:    0,
	}
}
