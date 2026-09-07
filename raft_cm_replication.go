package raft

import (
	"fmt"
	"time"
)

// replicationPlan — решение предотправочной фазы репликации на соседа.
type replicationPlan int

const (
	// replicationSkip — активна задержка повторов, попытка пропускается.
	replicationSkip replicationPlan = iota
	// replicationSnapshot — prev попадает в дыру между снимком и журналом.
	replicationSnapshot
	// replicationAppend — отправлять AppendEntries.
	replicationAppend
)

// nextIndexArgsEntries формирует аргументы для AppendEntries и сопутствующие данные
// для отправки конкретному последователю (peerID).
//
// Метод возвращает:
//   - текущее значение nextIndex для узла;
//   - готовый AppendEntriesArgs (включая prevLogIndex/prevLogTerm, записи лога, commitIndex);
//   - срез записей LogEntry, которые нужно отправить;
//   - флаг snapshotNeeded: true, если перед отправкой реплики требуется передать снимок состояния.
//
// Важные детали реализации:
//   - prevLogIndex и prevLogTerm вычисляются на основе nextIndex: prevLogIndex = nextIndex - 1,
//     а prevLogTerm берётся из локального лога. Если терм для prevLogIndex неизвестен,
//     это признак того, что лог не покрывает нужную позицию — в таком случае требуется снимок.
//   - Срез entries формируется как хвост лога, начиная с позиции nextIndex.
//     Добавлена защитная проверка диапазона: если позиция выходит за границы лога,
//     фиксируется аномалия, а entries возвращается пустым.
//   - Все обращения к состоянию модуля выполняются под блокировкой cm.mu.
func (cm *ConsensusModule) nextIndexArgsEntries(peerID, savedCurrentTerm int) (int, AppendEntriesArgs, []LogEntry, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	ni := cm.leaderState.nextIndex[peerID]
	prevLogIndex := ni - 1
	prevLogTerm := -1
	if 0 <= prevLogIndex && prevLogIndex <= cm.cmState.lastLogIndex {
		prevLogTerm = cm.lookupTermLocked(prevLogIndex)
	}

	// Если терм для prevLogIndex неизвестен — нужен снимок: иначе ведомый отвергнет AE.
	snapshotNeeded := prevLogIndex >= 0 && prevLogTerm == -1
	var entries []LogEntry
	if ni <= cm.cmState.lastLogIndex {
		pos := cm.logPositionLocked(ni)

		// Defensive check: если позиция вне диапазона — логируем аномалию и возвращаем пустой срез.
		if pos < len(cm.cmState.log) {
			entries = append([]LogEntry{}, cm.cmState.log[pos:]...)
		} else {
			cm.traceLockedLogf(1, "nextIndexArgsEntries: logPositionLocked(%d) out of range (len=%d)", ni, len(cm.cmState.log))
			entries = nil
		}
	} else {
		entries = nil
	}
	return ni, AppendEntriesArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		},
		Term:         savedCurrentTerm,
		LeaderID:     cm.id,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: cm.cmState.commitIndex,
	}, entries, snapshotNeeded
}

// planReplication выбирает действие репликации на соседа: пропустить попытку,
// отправить снимок или отправить AppendEntries.
//
// Задержка повторов: вычисляется на каждого соседа от числа подряд идущих
// транспортных ошибок; при активной задержке попытка пропускается. Пропуск —
// сравнение времени с моментом последней попытки, а не пауза: пауза удержала бы
// флаг активной горутины и задерживала бы восстановление вернувшегося узла.
//
// Предикат «снимок или AppendEntries» — по фактической обслуживаемости запроса:
// first — первый индекс, для которого лидер может сообщить терм (граница снимка
// при пустом срезе, первая запись среза иначе); prev = nextIndex - 1. Снимок
// отправляется только когда prev попадает в дыру между снимком и первым
// индексом журнала — единственный случай, когда lookupTermLocked(prev) == -1.
//
// Функция только читает состояние: изменять состояние ей запрещено, в частности
// фиксировать момент попытки — иначе задержка повторов обновлялась бы каждым
// пульсом и сосед перестал бы получать RPC.
//
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) planReplication(peerID, savedCurrentTerm int) replicationPlan {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	// Пропуск действует только для текущего лидерства: запоздавшие горутины
	// прошлых термов не влияют на состояние нового лидера.
	if cm.replicationBackoffActiveLocked(peerID, savedCurrentTerm) {
		return replicationSkip
	}
	first := cm.cmState.lastSnapshotIndex + 1
	if len(cm.cmState.log) > 0 {
		first = cm.cmState.log[0].Index
	}
	prev := cm.leaderState.nextIndex[peerID] - 1
	if !isNilInterface(cm.snapshotStore) &&
		prev >= 0 && prev != cm.cmState.lastSnapshotIndex && prev < first {
		return replicationSnapshot
	}
	return replicationAppend
}

// recordAttemptIfLeader фиксирует момент реальной попытки отправки RPC соседу,
// если узел всё ещё лидер терма savedCurrentTerm: запоздавшие горутины прошлых
// термов состояние не трогают.
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) recordAttemptIfLeader(peerID, savedCurrentTerm int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state == Leader && cm.cmState.currentTerm == savedCurrentTerm {
		cm.recordAttemptLocked(peerID)
	}
}

// incReplFailuresIfLeader учитывает транспортную ошибку по соседу, если узел
// всё ещё лидер терма savedCurrentTerm. Задержка повторов включается только
// транспортными ошибками; логический отказ AppendEntries её не вызывает.
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) incReplFailuresIfLeader(peerID, savedCurrentTerm int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state == Leader && cm.cmState.currentTerm == savedCurrentTerm {
		cm.incReplFailuresLocked(peerID)
	}
}

// leaderSendAEsToPeer отправляет AppendEntries указанному соседу и
// обрабатывает ответ. Выполняется в отдельной горутине.
//
// dispatchEpoch — эпоха верификации, снятая вызывающим под cm.mu
// в момент запуска горутины: ответы этого AE являются голосами только
// для verify-запросов с epoch <= dispatchEpoch (AE отправлен не раньше
// постановки запроса, Raft §8).
//
// ownsInflight — признак владения флагом inflightAE[peerID]: истина, если
// флаг захвачен вызывающим через CompareAndSwap(false, true). Все точки
// запуска захватывают флаг только через CompareAndSwap и передают признак
// владения; прямой вызов без захвата запрещён конструктивно. Единый протокол
// захвата гарантирует инвариант «на одного соседа не более одной живой
// горутины репликации при любом пути вызова».
//
// При любом завершении (успех, ошибка, step down) горутина, владеющая
// флагом, сбрасывает inflightAE[peerID] через defer, разрешая последующий
// вызов для этого соседа. Горутина без владения чужой флаг не снимает:
// иначе она обнулила бы флаг параллельной горутины, запущенной другим путём.
func (cm *ConsensusModule) leaderSendAEsToPeer(peerID, savedCurrentTerm int, dispatchEpoch uint64, ownsInflight bool) {
	// Решение предотправочной фазы снимается до объявления defer: defer
	// должен знать, завершилась ли горутина веткой replicationSkip, чтобы не
	// выполнять перерассылку при активной задержке повторов.
	plan := cm.planReplication(peerID, savedCurrentTerm)
	defer func() {
		cm.mu.Lock()
		if ownsInflight && cm.leaderState.inflightAE != nil && cm.leaderState.inflightAE[peerID] != nil {
			cm.leaderState.inflightAE[peerID].Store(false)
			// Немедленная перерассылка при неудовлетворённом verify-запросе:
			// только владелец флага и только вне ветки задержки повторов.
			if plan != replicationSkip {
				cm.redispatchVerifyIfPendingLocked(peerID, savedCurrentTerm, dispatchEpoch)
			}
		}
		cm.mu.Unlock()
	}()

	switch plan {
	case replicationSkip:
		return
	case replicationSnapshot:
		cm.leaderSendSnapshot(peerID, savedCurrentTerm)
		return
	case replicationAppend:
		// Продолжение метода: подготовка запроса и отправка.
	}

	ni, args, entries, snapshotNeeded := cm.nextIndexArgsEntries(peerID, savedCurrentTerm)
	// Если терм для PrevLogIndex неизвестен, отправить снимок: AppendEntries
	// будет гарантированно отвергнут ведомым. Проверка повторная: состояние
	// могло измениться после освобождения блокировки предотправочной фазы.
	if snapshotNeeded && !isNilInterface(cm.snapshotStore) {
		cm.leaderSendSnapshot(peerID, savedCurrentTerm)
		return
	}
	cm.traceLogf(8, "sending AppendEntries to %v: ni=%d, args=%+v", peerID, ni, args)
	cm.recordAttemptIfLeader(peerID, savedCurrentTerm)
	cm.countAEOutbound(peerID)

	reply, err := cm.transport.AppendEntries(ServerID(peerID), args)
	if err != nil {
		cm.incReplFailuresIfLeader(peerID, savedCurrentTerm)
		return
	}
	cm.handleAEReply(peerID, savedCurrentTerm, ni, len(entries), reply, dispatchEpoch)
}

// redispatchVerifyIfPendingLocked выполняет немедленную перерассылку
// AppendEntries соседу после завершения горутины репликации, если остался
// verify-запрос, поставленный позже момента отправки завершившегося AE.
//
// Завершившаяся горутина только что сняла флаг inflightAE[peerID]
// (Store(false)) в той же критической секции — флаг свободен. Если узел всё
// ещё лидер того же терма и в pendingVerify есть запрос с epoch > dispatchEpoch,
// ответ завершившегося AE такому запросу голосом не является (AE отправлен до
// постановки запроса), и запрос ждал бы ближайшего пульса. Перерассылка
// отправляет свежий AE с новой эпохой, покрывающей все текущие pendingVerify.
//
// Ограниченность (отсутствие бесконечной перерассылки) — две независимые
// границы. Первая — по частоте клиентских запросов: verifyEpoch монотонно
// возрастает и инкрементируется только в цикле лидера под cm.mu при постановке
// нового verify. newDispatchEpoch снимается под той же блокировкой и потому не
// меньше эпохи любого verify, находящегося в pendingVerify в этот момент:
// ответ перерассланного AE засчитывается всем текущим запросам, и повторная
// перерассылка для них не требуется — новая возможна только при поступлении
// нового verify. Вторая — минимальный интервал на соседа: перерассылки
// отстоят не менее чем на verifyRedispatchMinInterval, поэтому их частота
// ≤ 1000/интервал независимо от темпа запросов. Запрос, заставший окно
// занятым, дожидается либо следующей перерассылки, либо пульса — не дольше
// пульса (интервал строго меньше HeartbeatTimeoutMs). Перерассылка выполняется
// только владельцем флага (инвариант «на одного соседа не более одной живой
// горутины репликации»), поэтому каскад горутин невозможен. Ветка
// replicationSkip не перерассылает: AE всё равно не был бы отправлен, а смена
// горутин без прогресса была бы бесполезной.
//
// Требует удержания cm.mu; блокировку не берёт и не снимает.
func (cm *ConsensusModule) redispatchVerifyIfPendingLocked(peerID, savedCurrentTerm int, dispatchEpoch uint64) {
	// nil-проверки защищают от изменения конфигурации: сосед мог быть
	// исключён из состава кластера, пока выполнялась горутина репликации.
	if cm.leaderState.inflightAE == nil || cm.leaderState.inflightAE[peerID] == nil {
		return
	}
	// Условие перерассылки — все три одновременно: узел всё ещё лидер того же
	// терма и есть verify, поставленный позже отправки завершившегося AE.
	if cm.cmState.state != Leader || cm.cmState.currentTerm != savedCurrentTerm {
		return
	}
	if !hasPendingVerifyAfterEpoch(cm.leaderState.pendingVerify, dispatchEpoch) {
		return
	}
	// Окно троттлинга: не более одной перерассылки соседу за минимальный
	// интервал. Метка ставится только при фактической перерассылке, поэтому
	// неуспешный CAS (флаг занят другим путём) окно не закрывает — чужая
	// отправка без фактической рассылки не должна подавлять перерассылку.
	now := time.Now()
	if !now.After(cm.leaderState.nextVerifyRedispatchAt[peerID]) {
		cm.counters.verifyRedispatchSuppressed = incPeerCount(cm.counters.verifyRedispatchSuppressed, peerID)
		return
	}
	// Свежая эпоха под той же блокировкой: не меньше эпохи любого текущего
	// запроса, поэтому ответ перерассланного AE покрывает все pendingVerify.
	newDispatchEpoch := cm.leaderState.verifyEpoch
	if !cm.leaderState.inflightAE[peerID].CompareAndSwap(false, true) {
		return
	}
	cm.goSpawnLocked(func() {
		cm.leaderSendAEsToPeer(peerID, savedCurrentTerm, newDispatchEpoch, true)
	})
	// Метка окна и счётчик перерассылок — только при фактическом запуске
	// горутины: неуспешный CAS выше вернулся бы до этой точки.
	cm.leaderState.nextVerifyRedispatchAt[peerID] = now.Add(cm.verifyRedispatchMinInterval)
	cm.counters.verifyRedispatched = incPeerCount(cm.counters.verifyRedispatched, peerID)
}

// hasPendingVerifyAfterEpoch сообщает, есть ли в очереди verify-запрос с
// эпохой позже dispatchEpoch — поставленный после отправки AppendEntries,
// ответ которого такому запросу голосом не является.
func hasPendingVerifyAfterEpoch(pending []*verifyFuture, dispatchEpoch uint64) bool {
	for _, vf := range pending {
		if vf.epoch > dispatchEpoch {
			return true
		}
	}
	return false
}

// countAEOutbound фиксирует факт отправки AppendEntries соседу: инкремент
// счётчика исходящих AE по соседу. Счётчик контролирует частоту исходящих AE
// (перерассылка не должна превращаться в пульсацию); чтение — из горутины
// stats под cm.mu.
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) countAEOutbound(peerID int) {
	cm.mu.Lock()
	cm.counters.aeSentPerPeer = incPeerCount(cm.counters.aeSentPerPeer, peerID)
	cm.mu.Unlock()
}

// handleAEReply разбирает ответ AppendEntries от соседа: сбрасывает задержку
// повторов, обрабатывает больший терм, отсеивает запоздавшие ответы и
// применяет результат к состоянию репликации.
//
// ni и sentEntries сняты до отправки RPC; dispatchEpoch — эпоха верификации
// момента отправки. Все три передаются по значению и внутри не перечитываются.
//
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) handleAEReply(
	peerID, savedCurrentTerm, ni, sentEntries int, reply AppendEntriesReply, dispatchEpoch uint64,
) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.recordPeerReplyLocked(peerID, savedCurrentTerm)
	if reply.Term > cm.cmState.currentTerm {
		cm.traceLockedLogf(8, "term out of date in heartbeat reply")
		cm.becomeFollowerLocked(reply.Term)
		return
	}
	// Запоздавший ответ вытесненного лидерства состояние репликации не меняет:
	// терм отправки сверяется и с термом ответа, и с текущим термом лидера.
	// Без сверки с текущим термом успех прежнего лидерства применился бы к
	// свежим картам репликации нового лидерства, не сбрасывая при этом
	// счётчик транспортных ошибок (recordPeerReplyLocked сверяет текущий терм).
	if cm.cmState.state != Leader || savedCurrentTerm != reply.Term ||
		savedCurrentTerm != cm.cmState.currentTerm {
		return
	}
	if !reply.Success {
		cm.handleFailedAEReplyLocked(peerID, reply)
		return
	}
	cm.applyAESuccessLocked(peerID, ni, sentEntries)
	cm.countVerifyVotesLocked(peerID, dispatchEpoch)
}

// applyAESuccessLocked применяет успешный ответ AppendEntries к состоянию
// репликации соседа: nextIndex, matchIndex и продвижение индекса фиксации.
// ni и sentEntries сняты до отправки RPC и передаются по значению: nextIndex
// вычисляется от фактически отправленного диапазона, а не от текущего
// значения карты.
//
// Продвижение matchIndex и nextIndex — только вперёд: поздний или
// дублирующий ответ более старой отправки с меньшим ni+sentEntries не
// уменьшает значения. Это устраняет расхождение leaderState с фактическим
// состоянием соседа и лишние повторные отправки записей, которые сосед уже
// имеет. Продвижение индекса фиксации от отката защищено независимо от этой
// правки: commitmentTracker.setMatch игнорирует убывающие значения.
// Требует удержания cm.mu; блокировку не берёт и не снимает.
func (cm *ConsensusModule) applyAESuccessLocked(peerID, ni, sentEntries int) {
	newNextIndex := ni + sentEntries
	if newNextIndex <= cm.leaderState.nextIndex[peerID] {
		return
	}
	cm.leaderState.nextIndex[peerID] = newNextIndex
	cm.leaderState.matchIndex[peerID] = cm.leaderState.nextIndex[peerID] - 1

	cm.leaderState.commitmentTracker.setMatch(peerID, cm.leaderState.matchIndex[peerID], cm.lookupTermLocked)
	cm.traceLockedLogf(
		8, "AppendEntries reply from %d success: nextIndex := %v, matchIndex := %v; commitIndex := %d",
		peerID, cm.leaderState.nextIndex, cm.leaderState.matchIndex, cm.leaderState.commitmentTracker.getCommitIndex(),
	)
}

// countVerifyVotesLocked засчитывает подтверждение соседа peerID запросам
// верификации лидерства раунда не позже dispatchEpoch и снимает из очереди
// завершённые. dispatchEpoch снят в момент отправки AppendEntries: голос
// действителен только для запросов, поставленных не позже отправки.
// Требует удержания cm.mu; блокировку не берёт и не снимает.
func (cm *ConsensusModule) countVerifyVotesLocked(peerID int, dispatchEpoch uint64) {
	if len(cm.leaderState.pendingVerify) == 0 || cm.cmState.state != Leader {
		return
	}
	for _, vf := range cm.leaderState.pendingVerify {
		// Голос учитывается только от узла с правом голоса текущей конфигурации
		// и только для ответов на AppendEntries, отправленных не раньше запроса.
		if hasVote(cm.cmState.configurations.latest, peerID) && vf.epoch <= dispatchEpoch {
			vf.vote(peerID, true)
		}
	}
	var remaining []*verifyFuture
	for _, vf := range cm.leaderState.pendingVerify {
		if vf.votes >= vf.quorumSize {
			vf.respond(nil)
			// Успешное завершение verify-запроса: знаменатель доли
			// «дождавшихся пульса». Запрос считается дождавшимся, если между
			// постановкой в очередь и завершением успел сработать тик пульса
			// (счётчик тиков строго вырос относительно значения при постановке).
			cm.counters.verifyCompleted++
			if cm.leaderState.heartbeatTicks > vf.enqueuedAtHeartbeat {
				cm.counters.verifyWaitedHeartbeat++
			}
		} else {
			remaining = append(remaining, vf)
		}
	}
	cm.leaderState.pendingVerify = remaining
}

// replicationBackoffActiveLocked сообщает, активен ли задержка повторов для попытки
// репликации текущего лидерства: с последней реальной
// попытки прошло меньше задержки, вычисленной по числу транспортных
// ошибок. Требует удержания cm.mu.
func (cm *ConsensusModule) replicationBackoffActiveLocked(peerID, savedCurrentTerm int) bool {
	if cm.cmState.state != Leader || cm.cmState.currentTerm != savedCurrentTerm {
		return false
	}
	delay := replicationBackoffDelay(cm.leaderState.replFailures[peerID])
	return delay > 0 && !cm.leaderState.lastAttempt[peerID].IsZero() &&
		time.Since(cm.leaderState.lastAttempt[peerID]) < delay
}

// handleFailedAEReplyLocked: обработка отказа AppendEntries от ведомого.
// Обновляет nextIndex с учётом ConflictIndex/ConflictTerm, если ответ
// логически возможен. Аномальные ответы (которые не могут улучшить прогресс
// репликации) игнорируются, но фиксируются в счётчиках и трассировке.
// Это защищает от деградации состояния репликации при некорректных ответах.
// Требует удержания cm.mu; блокировку не берёт и не снимает.
func (cm *ConsensusModule) handleFailedAEReplyLocked(peerID int, reply AppendEntriesReply) {
	// Счётчик отказов AE: «follower отвергает
	// AppendEntries?» — ключевой признак цикла IS↔AE-fail.
	cm.counters.appendEntriesRejected = incPeerCount(cm.counters.appendEntriesRejected, peerID)
	newNi := reply.ConflictIndex
	if reply.ConflictTerm >= 0 {
		if lastIndex, ok := cm.cmState.termIndexMap[reply.ConflictTerm]; ok {
			newNi = lastIndex + 1
		}
	}
	// Предохранитель: отказ, результат которого не
	// может быть истинным (отрицательный ConflictIndex либо
	// новый nextIndex не больше доказанного сохранённого на диске префикса
	// matchIndex[peer]), не изменяет состояние репликации —
	// прецедент etcd/raft Progress.MaybeDecrTo. Аномалия
	// наблюдаема через счётчик и трассировку уровня 0.
	if reply.ConflictIndex < 0 || newNi <= cm.leaderState.matchIndex[peerID] {
		cm.counters.nextIndexRejectionIgnored = incPeerCount(
			cm.counters.nextIndexRejectionIgnored, peerID,
		)
		cm.traceLockedLogf(
			0, "AppendEntries reply from %d rejected (impossible): peer=%d term=%d ni=%d matchIndex=%d ConflictIndex=%d ConflictTerm=%d",
			peerID, peerID, reply.Term, newNi, cm.leaderState.matchIndex[peerID],
			reply.ConflictIndex, reply.ConflictTerm,
		)
		return
	}
	cm.leaderState.nextIndex[peerID] = newNi
	cm.traceLockedLogf(8, "%s", failedAETrace(
		peerID, cm.leaderState.nextIndex[peerID], cm.leaderState.matchIndex[peerID],
		reply.ConflictIndex, reply.ConflictTerm,
	))
}

// leaderSendAEs отправляет AppendEntries всем peer, для которых
// в данный момент не выполняется горутина репликации.
//
// Для каждого peer проверяет inflightAE[peerID] через atomic.CompareAndSwap:
//   - Если флаг уже установлен (true) — горутина уже выполняется,
//     вызов для этого peer пропускается (дедупликация).
//   - Если флаг свободен (false) — устанавливает его и запускает
//     leaderSendAEsToPeer в отдельной горутине.
//
// Вызывается из runLeaderLoop после dispatchLogs, commitCh,
// heartbeatTicker и при pendingVerify.
func (cm *ConsensusModule) leaderSendAEs() {
	cm.mu.Lock()
	if cm.cmState.state != Leader {
		cm.mu.Unlock()
		return
	}
	savedCurrentTerm := cm.cmState.currentTerm
	// Снимок эпохи верификации под cm.mu: ответы этих AE будут
	// голосами только для verify-запросов с epoch <= dispatchEpoch
	// (голоса привязываются к раунду рассылки).
	dispatchEpoch := cm.leaderState.verifyEpoch

	peers := make(map[int]struct{})
	for _, s := range cm.cmState.configurations.committed.ConfigServers {
		if int(s.ID) != cm.id {
			peers[int(s.ID)] = struct{}{}
		}
	}
	for _, s := range cm.cmState.configurations.latest.ConfigServers {
		if int(s.ID) != cm.id {
			peers[int(s.ID)] = struct{}{}
		}
	}

	for peerID := range peers {
		if cm.leaderState.inflightAE[peerID] == nil {
			continue
		}
		if cm.leaderState.inflightAE[peerID].CompareAndSwap(false, true) {
			cm.goSpawnLocked(func() {
				cm.leaderSendAEsToPeer(peerID, savedCurrentTerm, dispatchEpoch, true)
			})
		}
	}
	cm.mu.Unlock()
}

// leaderSendAEsToPeerIfIdle отправляет AppendEntries одному соседу, если для
// него не выполняется горутина репликации. Дедупликация — тот же флаг
// inflightAE и тот же CompareAndSwap, что и в leaderSendAEs: догоняющая
// репликация передачи лидерства не должна порождать второго отправителя
// одному соседу (конкурирующие попытки искажают счётчик транспортных ошибок
// и периодизацию задержки повторов).
//
// Флаг захватывается только через CompareAndSwap(false, true), и признак
// владения передаётся запущенной горутине по единому протоколу захвата —
// сброс флага выполняется только владельцем. При занятом флаге попытка
// пропускается: живая репликация догонит соседа сама.
//
// Эпоха верификации снимается в той же критической секции, что и захват
// флага, — как в leaderSendAEs.
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) leaderSendAEsToPeerIfIdle(peerID, savedCurrentTerm int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.leaderState.inflightAE[peerID] == nil {
		return
	}
	dispatchEpoch := cm.leaderState.verifyEpoch
	if cm.leaderState.inflightAE[peerID].CompareAndSwap(false, true) {
		cm.goSpawnLocked(func() {
			cm.leaderSendAEsToPeer(peerID, savedCurrentTerm, dispatchEpoch, true)
		})
	}
}

// leaderSendSnapshot отправляет последний снимок отстающему follower.
// Вызывается, когда лидер не может обслужить AppendEntries для текущего
// nextIndex[follower] (предикат).
func (cm *ConsensusModule) leaderSendSnapshot(peerID, term int) {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		cm.traceLogf(4, "leaderSendSnapshot elapsed %s", elapsed)
	}()
	cm.traceLogf(4, "leaderSendSnapshot peer %d: term=%d", peerID, term)

	// Защита от nil-хранилища. Инвариант проверяется и в вызывающем коде
	// (leaderSendAEsToPeer), но при рефакторинге он может нарушиться,
	// а паника здесь упала бы в отдельной горутине без recover().
	if isNilInterface(cm.snapshotStore) {
		cm.traceLogf(4, "leaderSendSnapshot: snapshotStore is nil, cannot send to %d", peerID)
		return
	}

	snapshots, err := cm.snapshotStore.List()
	if err != nil || len(snapshots) == 0 {
		// Пустой List у лидера с lastSnapshotIndex >= 0 — признак
		// повреждения/удаления постоянного хранилища: диагностика
		// и best-effort восстановление форсированием нового снимка.
		cm.traceLogf(0, "leaderSendSnapshot: cannot send snapshot to %d: err=%v, snapshots=%d", peerID, err, len(snapshots))
		cm.mu.Lock()
		lastSnapshotIndex := cm.cmState.lastSnapshotIndex
		cm.mu.Unlock()
		if lastSnapshotIndex >= 0 && cm.snapshotCh != nil {
			select {
			case cm.snapshotCh <- struct{}{}:
			default:
			}
		}
		return
	}
	// List() может вернуть слайс с nil-элементом или пустым ID — оба
	// случая приводят к панике или некорректной работе Open().
	if snapshots[0] == nil {
		cm.traceLogf(4, "leaderSendSnapshot: snapshots[0] is nil")
		return
	}
	if snapshots[0].ID == "" {
		cm.traceLogf(4, "leaderSendSnapshot: snapshots[0].ID is empty")
		return
	}
	meta, reader, err := cm.snapshotStore.Open(snapshots[0].ID)
	if err != nil {
		cm.traceLogf(0, "leaderSendSnapshot: cannot open snapshot %s for peer %d: %v", snapshots[0].ID, peerID, err)
		return
	}
	defer func() { _ = reader.Close() }()

	cfgData, _ := EncodeConfiguration(meta.Configuration)

	req := InstallSnapshotRequest{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		},
		Term:          term,
		LeaderID:      cm.id,
		LastLogIndex:  meta.Index,
		LastLogTerm:   meta.Term,
		Configuration: cfgData,
		ConfigIndex:   meta.ConfigIndex,
		DataSize:      meta.Size,
	}

	// Фиксация момента реальной попытки и счётчик
	// отправленных снимков: «лидер повторно шлёт
	// снимок?» — инкремент на каждую реальную отправку через транспорт.
	cm.mu.Lock()
	if cm.cmState.state == Leader && cm.cmState.currentTerm == term {
		cm.recordAttemptLocked(peerID)
	}
	cm.counters.installSnapshotSent = incPeerCount(cm.counters.installSnapshotSent, peerID)
	cm.mu.Unlock()

	reply, err := cm.transport.InstallSnapshot(ServerID(peerID), req, reader)
	if err != nil {
		// Задержка повторов активируется только транспортными ошибками
		// для текущего лидерства.
		cm.mu.Lock()
		if cm.cmState.state == Leader && cm.cmState.currentTerm == term {
			cm.incReplFailuresLocked(peerID)
		}
		cm.mu.Unlock()
		return
	}
	// Сосед, догоняемый снимком, отвечает дольше одного пульса, но остаётся
	// живым: его ответ — такое же доказательство контакта, как ответ
	// AppendEntries. Сброс задержки повторов и применение результата
	// выполняются в одной критической секции: наблюдатель не может увидеть
	// несогласованную пару «matchIndex вырос, счётчик ошибок не сброшен».
	cm.mu.Lock()
	cm.recordPeerReplyLocked(peerID, term)
	if reply.Term > cm.cmState.currentTerm {
		cm.becomeFollowerLocked(reply.Term)
		cm.mu.Unlock()
		return
	}
	// Симметрия с путём AppendEntries (ветка успеха AE): запоздавший ответ
	// вытесненного лидерства состояние репликации не меняет — результат
	// применяется только в терме отправки, совпадающем с текущим.
	// Сверка с термом ответа сохранена как защита от несогласованного ответа
	// соседа: при Success:true протокол гарантирует reply.Term == term.
	if reply.Success && cm.cmState.state == Leader &&
		cm.cmState.currentTerm == term && reply.Term == term {
		cm.leaderState.nextIndex[peerID] = meta.Index + 1
		cm.leaderState.matchIndex[peerID] = meta.Index
		// matchIndex = meta.Index означает сохранённое на диске владение префиксом
		// до meta.Index — ровно то, что гарантирует Success:true.
		// Проверка терма внутри setMatch работает через fallback
		// lookupTermLocked на lastSnapshotTerm.
		cm.leaderState.commitmentTracker.setMatch(peerID, meta.Index, cm.lookupTermLocked)
	}
	cm.mu.Unlock()
}

// failedAETrace формирует текст трассировки неудачного AppendEntries
// печатается фактически присвоенное значение
// nextIndex (ранее ошибочно печаталось ni-1), а также matchIndex и поля
// конфликта ответа — ровно тот набор, которого не хватило при разборе
// обоих инцидентов. Выделена в чистую функцию: мутация traceCM и
// _traceLogger в тестах запрещена, формат проверяется
// unit-тестом функции.
func failedAETrace(peerID, nextIndex, matchIndex, conflictIndex, conflictTerm int) string {
	return fmt.Sprintf(
		"AppendEntries reply from %d !success: nextIndex := %d (ConflictIndex=%d, ConflictTerm=%d, matchIndex=%d)",
		peerID, nextIndex, conflictIndex, conflictTerm, matchIndex,
	)
}

// maxReplicationBackoff — потолок задержки повторов репликации на каждого
// соседа (1000 мс). Объявлен на уровне пакета, а не внутри функции, чтобы
// тестовые бюджеты ожидания схождения кластера выводились из него, а не
// дублировали литерал. Значение ограничивает сверху задержку step-down
// изолированного лидера прежнего терма.
const maxReplicationBackoff = 1000 * time.Millisecond

// replicationBackoffDelay вычисляет задержку на каждого соседа между попытками
// репликации по числу подряд идущих транспортных ошибок:
// delay = min(HeartbeatTimeoutMs * 2^min(failures, 5), 1000) мс.
// Принудительное ограничение в заданных пределах: при HeartbeatTimeoutMs = 33
// и показателе 5 получается 33·2⁵ = 1056 мс > потолка 1000 мс.
// При нуле ошибок задержка нулевая — поведение прежнее.
func replicationBackoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := HeartbeatTimeoutMs * time.Millisecond * time.Duration(1<<min(failures, 5))
	if delay > maxReplicationBackoff {
		delay = maxReplicationBackoff
	}
	return delay
}

// recordAttemptLocked фиксирует момент реальной попытки отправки RPC пиру.
// Требует удержания cm.mu.
func (cm *ConsensusModule) recordAttemptLocked(peerID int) {
	if cm.leaderState.lastAttempt == nil {
		cm.leaderState.lastAttempt = make(map[int]time.Time)
	}
	cm.leaderState.lastAttempt[peerID] = time.Now()
}

// recordPeerReplyLocked обрабатывает ответ соседа, полученный без
// транспортной ошибки: сбрасывает задержку повторов и отмечает контакт.
// Содержание ответа значения не имеет — доказательством живого соседа
// служит сам факт ответа. Состояние меняется только для текущего
// лидерства: term — терм, снятый до отправки RPC.
// Требует удержания cm.mu.
func (cm *ConsensusModule) recordPeerReplyLocked(peerID, term int) {
	if cm.cmState.state != Leader || cm.cmState.currentTerm != term {
		return
	}
	cm.resetReplFailuresLocked(peerID)
	cm.recordContactLocked(peerID)
}

// recordContactLocked отмечает момент получения ответа RPC от соседа без
// транспортной ошибки: такой ответ доказывает, что сосед жив, независимо
// от содержания ответа.
// Требует удержания cm.mu.
func (cm *ConsensusModule) recordContactLocked(peerID int) {
	if cm.leaderState.lastContact == nil {
		cm.leaderState.lastContact = make(map[int]time.Time)
	}
	cm.leaderState.lastContact[peerID] = time.Now()
}

// incReplFailuresLocked инкрементирует число подряд идущих транспортных ошибок
// по пиру: вызывается только при err != nil от
// транспорта. Требует удержания cm.mu.
func (cm *ConsensusModule) incReplFailuresLocked(peerID int) {
	if cm.leaderState.replFailures == nil {
		cm.leaderState.replFailures = make(map[int]int)
	}
	cm.leaderState.replFailures[peerID]++
}

// resetReplFailuresLocked сбрасывает счётчик транспортных ошибок по пиру при
// любом RPC без ошибки транспорта — независимо от reply.Success.
// Требует удержания cm.mu.
func (cm *ConsensusModule) resetReplFailuresLocked(peerID int) {
	if cm.leaderState.replFailures != nil {
		cm.leaderState.replFailures[peerID] = 0
	}
}
