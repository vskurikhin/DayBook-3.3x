package raft

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"log"
	"time"
)

// persistToStorage сохраняет постоянное состояние CM в cm.storage.
// Кодирование журнала выполняется только при cm.cmState.logNeedsPersist == true
// (т.е. когда лог действительно изменился), что позволяет избежать
// дорогого gob.Encode(cm.cmState.log) на каждом heartbeat или RPC.
//
// Порядок записи ключей важен для крэш-безопасности:
// currentTerm, votedFor, lastSnapshotIndex, lastSnapshotTerm, затем log.
// Снимок-ключи пишутся ДО усечённого лога: окно «лог усечён, а
// lastSnapshotIndex старый/отсутствует» устраняется; обратное окно
// («lastSnapshotIndex новее, лог полный») безопасно — полный лог
// консервативнее, а startup-restore самовосстанавливает рассинхрон
// (restoreFromSnapshotStore).
//
//nolint:gocritic
func (cm *ConsensusModule) persistToStorage() {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		cm.traceLockedLogf(2, "persistToStorage elapsed %s", elapsed)
	}()
	var termData bytes.Buffer
	if err := gob.NewEncoder(&termData).Encode(cm.cmState.currentTerm); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("currentTerm", termData.Bytes())

	var votedData bytes.Buffer
	if err := gob.NewEncoder(&votedData).Encode(cm.cmState.votedFor); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("votedFor", votedData.Bytes())

	var snapIdxData bytes.Buffer
	if err := gob.NewEncoder(&snapIdxData).Encode(cm.cmState.lastSnapshotIndex); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("lastSnapshotIndex", snapIdxData.Bytes())

	var snapTermData bytes.Buffer
	if err := gob.NewEncoder(&snapTermData).Encode(cm.cmState.lastSnapshotTerm); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("lastSnapshotTerm", snapTermData.Bytes())

	if cm.cmState.logNeedsPersist {
		var logData bytes.Buffer
		if err := gob.NewEncoder(&logData).Encode(cm.cmState.log); err != nil {
			log.Fatal(err)
		}
		cm.storage.Set("log", logData.Bytes())
		cm.cmState.logNeedsPersist = false
	}
}

// restoreFromStorage восстанавливает постоянное состояние данного CM
// из хранилища. Должен вызываться в конструкторе до запуска какой-либо
// конкурентной работы.
func (cm *ConsensusModule) restoreFromStorage() {
	if termData, found := cm.storage.Get("currentTerm"); found {
		d := gob.NewDecoder(bytes.NewBuffer(termData))
		if err := d.Decode(&cm.cmState.currentTerm); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("currentTerm not found in storage")
	}
	if votedData, found := cm.storage.Get("votedFor"); found {
		d := gob.NewDecoder(bytes.NewBuffer(votedData))
		if err := d.Decode(&cm.cmState.votedFor); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("votedFor not found in storage")
	}
	if logData, found := cm.storage.Get("log"); found {
		d := gob.NewDecoder(bytes.NewBuffer(logData))
		if err := d.Decode(&cm.cmState.log); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("log not found in storage")
	}
	if err := cm.checkSnapshotKeysConsistency(); err != nil {
		log.Fatal(err)
	}
	cm.rebuildLastLog()
	cm.rebuildTermIndexMap()

	cm.rebuildConfigurations()

	if snapIdxData, found := cm.storage.Get("lastSnapshotIndex"); found {
		d := gob.NewDecoder(bytes.NewBuffer(snapIdxData))
		if err := d.Decode(&cm.cmState.lastSnapshotIndex); err != nil {
			log.Fatal(err)
		}
	}
	if snapTermData, found := cm.storage.Get("lastSnapshotTerm"); found {
		d := gob.NewDecoder(bytes.NewBuffer(snapTermData))
		if err := d.Decode(&cm.cmState.lastSnapshotTerm); err != nil {
			log.Fatal(err)
		}
	}
}

// checkSnapshotKeysConsistency проверяет, что для сжатого журнала
// (первая запись с Index > 0) в storage присутствуют ключи
// lastSnapshotIndex/lastSnapshotTerm. Их отсутствие означало бы тихую
// потерю метаданных снимка при усечённом журнале на диске.
// Для свежего узла (полный лог с первой записью Index == 0) отсутствие
// ключей допустимо (обратная совместимость данных). Выделена в отдельную
// функцию для тестирования fail-fast пути без завершения процесса.
func (cm *ConsensusModule) checkSnapshotKeysConsistency() error {
	if len(cm.cmState.log) == 0 || cm.cmState.log[0].Index == 0 {
		return nil
	}
	firstIndex := cm.cmState.log[0].Index
	if _, found := cm.storage.Get("lastSnapshotIndex"); !found {
		return fmt.Errorf(
			"log compacted (first index %d) but lastSnapshotIndex missing in storage",
			firstIndex,
		)
	}
	if _, found := cm.storage.Get("lastSnapshotTerm"); !found {
		return fmt.Errorf(
			"log compacted (first index %d) but lastSnapshotTerm missing in storage",
			firstIndex,
		)
	}
	return nil
}

// restoreFromSnapshotStore восстанавливает FSM из последнего снимка
// SnapshotStore и синхронизирует lastApplied/fsmAppliedIndex/commitIndex/
// lastLog* с метаданными снимка. Должен вызываться в конструкторе до запуска
// какой-либо конкурентной работы (однопоточный старт, cm.mu не требуется,
// как у restoreFromStorage). Все ошибки и нарушения инвариантов
// возвращаются вызывающему; решение о фатальности принимает вызывающий код.
func (cm *ConsensusModule) restoreFromSnapshotStore() error {
	if isNilInterface(cm.snapshotStore) {
		return nil
	}
	snapshots, err := cm.snapshotStore.List()
	if err != nil {
		return fmt.Errorf("snapshot store List: %w", err)
	}

	snapshotChanged := false
	if len(snapshots) > 0 {
		latest := snapshots[0] // List упорядочен от новых к старым
		if latest == nil || latest.ID == "" {
			return fmt.Errorf("snapshot store returned invalid snapshot meta")
		}
		if latest.Index < cm.cmState.lastSnapshotIndex {
			return fmt.Errorf(
				"snapshot store is behind storage: snapshot index %d < lastSnapshotIndex %d",
				latest.Index, cm.cmState.lastSnapshotIndex,
			)
		}
		meta, reader, err := cm.snapshotStore.Open(latest.ID)
		if err != nil {
			return fmt.Errorf("open snapshot %s: %w", latest.ID, err)
		}
		defer func() { _ = reader.Close() }()
		if err := cm.fsm.Restore(reader); err != nil {
			return fmt.Errorf("fsm restore from snapshot %s: %w", latest.ID, err)
		}
		// Восстановление конфигурации кластера из метаданных снимка
		// снимок берёт configurations.committed в takeSnapshot,
		// поэтому конфигурация снимка зафиксирована по определению. Применяется,
		// только если новее конфигурации, восстановленной из суффикса лога;
		// пустая конфигурация (легаси/тестовые снимки) игнорируется.
		if meta.ConfigIndex > cm.cmState.configurations.latestIndex &&
			len(meta.Configuration.ConfigServers) > 0 {
			cm.cmState.configurations.committed = meta.Configuration
			cm.cmState.configurations.committedIndex = meta.ConfigIndex
			cm.cmState.configurations.latest = meta.Configuration
			cm.cmState.configurations.latestIndex = meta.ConfigIndex
		}
		if meta.Index != cm.cmState.lastSnapshotIndex {
			// Окно крэша между Close снимка и persistToStorage
			// снимок в постоянном хранилище новее диска —
			// самовосстановление метаданных из стора с повторным персистом.
			cm.cmState.lastSnapshotIndex = meta.Index
			cm.cmState.lastSnapshotTerm = meta.Term
			snapshotChanged = true
		}
		cm.cmState.lastApplied = meta.Index
		// fsm.Restore выше заместил состояние машины состояний целиком:
		// отметка фактически применённого индекса равна индексу снимка.
		// Без этого присваивания первый запрос снимка после перезапуска
		// вернул бы ErrNothingNewToSnapshot навсегда (отметка осталась бы
		// равной -1), и журнал рос бы неограниченно.
		cm.cmState.fsmAppliedIndex = meta.Index
		cm.cmState.commitIndex = meta.Index
		if cm.cmState.lastLogIndex < meta.Index {
			// Пустой или короткий журнал (trailing=0): синхронизация
			// lastLogIndex/lastLogTerm со снимком — симметрично
			// handleInstallSnapshot. Иначе rebuildLastLog() оставил бы
			// lastLogIndex = -1 при lastApplied > 0 (нарушение инварианта
			// lastApplied <= lastLogIndex, новая запись получила бы
			// индекс, уже покрытый снимком).
			cm.cmState.lastLogIndex = meta.Index
			cm.cmState.lastLogTerm = meta.Term
		}
	} else if cm.cmState.lastSnapshotIndex >= 0 {
		// Журнал на диске усечён, а снимок, обосновывающий усечение,
		// отсутствует: продолжение — молчаливая потеря данных.
		return fmt.Errorf(
			"log compacted up to snapshot index %d but snapshot store is empty",
			cm.cmState.lastSnapshotIndex,
		)
	}

	// Инварианты непрерывности лога (после восстановления).
	if err := cm.checkSnapshotLogContinuity(); err != nil {
		return err
	}

	if snapshotChanged {
		// logNeedsPersist уже false (сброшен после restoreFromStorage),
		// поэтому log-ключ не переписывается — только дешёвые ключи.
		cm.persistToStorage()
	}
	return nil
}

// checkSnapshotLogContinuity проверяет инварианты непрерывности лога
// относительно снимка. Выделена в отдельную функцию для тестирования
// fail-fast путей без завершения процесса.
func (cm *ConsensusModule) checkSnapshotLogContinuity() error {
	if len(cm.cmState.log) > 0 && cm.cmState.log[0].Index > cm.cmState.lastSnapshotIndex+1 {
		return fmt.Errorf(
			"gap between snapshot (index %d) and first log entry (index %d)",
			cm.cmState.lastSnapshotIndex, cm.cmState.log[0].Index,
		)
	}
	if cm.cmState.lastLogIndex < cm.cmState.lastSnapshotIndex {
		// Безусловная проверка, включая пустой журнал: guard выше при
		// len(log) == 0 молча пропускается. После синхронизации
		// lastLogIndex со снимком — defense-in-depth против регрессий.
		return fmt.Errorf(
			"lastLogIndex %d behind lastSnapshotIndex %d",
			cm.cmState.lastLogIndex, cm.cmState.lastSnapshotIndex,
		)
	}
	return nil
}
