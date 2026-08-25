package raft

import (
	"sync/atomic"
	"time"
)

// AddNonvoter добавляет новый не голосующий сервер или обновляет адрес существующего.
func (cm *ConsensusModule) AddNonvoter(id ServerID, addr ServerAddress) IndexFuture {
	future := &configurationChangeFuture{
		req: configurationChangeRequest{
			command:       AddNonvoter,
			serverID:      id,
			serverAddress: addr,
		},
	}
	future.init(cm.shutdownCh)

	if err := cm.enqueueConfigurationChange(future); err != nil {
		return errorFuture{err}
	}
	return future
}

// AddVoter добавляет новый голосующий сервер или обновляет адрес существующего.
func (cm *ConsensusModule) AddVoter(id ServerID, addr ServerAddress) IndexFuture {
	future := &configurationChangeFuture{
		req: configurationChangeRequest{
			command:       AddVoter,
			serverID:      id,
			serverAddress: addr,
		},
	}
	future.init(cm.shutdownCh)

	if err := cm.enqueueConfigurationChange(future); err != nil {
		return errorFuture{err}
	}
	return future
}

// Apply отправляет команду в Raft и возвращает ApplyFuture.
// Команда будет применена к FSM после фиксации.
//
// Обязательство отправителя: объект команды command сохраняется по ссылке
// (LogEntry.Data = command, без копирования); вызывающий не должен мутировать
// переданную команду после вызова Apply — в противном случае изменение увидят
// и журнал, и FSM, и любой потребитель, удержавший payload.
func (cm *ConsensusModule) Apply(command any, timeout time.Duration) ApplyFuture {
	select {
	case <-cm.shutdownCh:
		return errorFuture{ErrRaftShutdown}
	default:
	}

	future := &logFuture{
		log: LogEntry{
			Type: LogCommand,
			Data: command,
		},
	}
	future.init(cm.shutdownCh)

	cm.mu.Lock()
	if cm.cmState.state != Leader {
		cm.mu.Unlock()
		return errorFuture{ErrNotLeader}
	}
	// Во время передачи лидерства Apply блокируется — клиент получает
	// ErrLeadershipTransferInProgress. Это предотвращает ситуации, когда
	// новые команды приходят после того, как таргет уже догнал журнал.
	if atomic.LoadInt32(&cm.leaderState.leadershipTransferInProgress) != 0 {
		cm.mu.Unlock()
		return errorFuture{ErrLeadershipTransferInProgress}
	}
	cm.mu.Unlock()

	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}
	select {
	case <-timer:
		return errorFuture{ErrEnqueueTimeout}
	case <-cm.shutdownCh:
		return errorFuture{ErrRaftShutdown}
	case cm.applyCh <- future:
		return future
	}
}

// DemoteVoter понижает голосующего до неголосующего.
func (cm *ConsensusModule) DemoteVoter(id ServerID) IndexFuture {
	future := &configurationChangeFuture{
		req: configurationChangeRequest{
			command:  DemoteVoter,
			serverID: id,
		},
	}
	future.init(cm.shutdownCh)

	if err := cm.enqueueConfigurationChange(future); err != nil {
		return errorFuture{err}
	}
	return future
}

// LeadershipTransfer инициирует корректную (graceful) передачу лидерства
// указанному узлу.
//
// Алгоритм:
//  1. Проверка: цель != cm.id (нельзя передать лидерство себе).
//  2. Проверка: цель — голосующий в текущей конфигурации.
//  3. Проверка: передача ещё не идёт (leadershipTransferInProgress == 0).
//  4. Создание leadershipTransferFuture, отправка в leadershipTransferCh.
//  5. Возврат future клиенту.
//
// Возвращает ErrNotLeader, если узел не является лидером.
// Возвращает ErrLeadershipTransferInProgress, если передача уже идёт.
func (cm *ConsensusModule) LeadershipTransfer(targetID ServerID) LeadershipTransferFuture {
	future := &leadershipTransferFuture{targetID: targetID}
	future.init(cm.shutdownCh)

	cm.mu.Lock()
	if cm.cmState.state != Leader {
		cm.mu.Unlock()
		return errorFuture{ErrNotLeader}
	}
	// Проверка, что цель — голосующий в текущей конфигурации.
	if !hasVote(cm.cmState.configurations.latest, int(targetID)) {
		cm.mu.Unlock()
		return errorFuture{ErrLeadershipLost}
	}
	if targetID == ServerID(cm.id) {
		cm.mu.Unlock()
		return errorFuture{ErrLeadershipLost}
	}
	// Проверка, что передача ещё не идёт.
	if atomic.LoadInt32(&cm.leaderState.leadershipTransferInProgress) != 0 {
		cm.mu.Unlock()
		return errorFuture{ErrLeadershipTransferInProgress}
	}
	cm.mu.Unlock()

	select {
	case cm.leaderState.leadershipTransferCh <- future:
		return future
	case <-cm.shutdownCh:
		return errorFuture{ErrRaftShutdown}
	}
}

// RemoveServer удаляет сервер из конфигурации кластера.
func (cm *ConsensusModule) RemoveServer(id ServerID) IndexFuture {
	future := &configurationChangeFuture{
		req: configurationChangeRequest{
			command:  RemoveServer,
			serverID: id,
		},
	}
	future.init(cm.shutdownCh)

	if err := cm.enqueueConfigurationChange(future); err != nil {
		return errorFuture{err}
	}
	return future
}

// VerifyLeader проверяет, что текущий узел всё ещё является лидером,
// используя механизм ReadIndex (Raft §8). Возвращает Future, которая
// завершится с ошибкой, если лидерство утеряно.
//
// Если узел не лидер, немедленно возвращает ошибку ErrNotLeader.
// Вызов на лидере проверяет кворум без записи в журнал (ReadIndex).
//
// Порог кворума и раунд верификации (epoch) назначаются в leaderLoop
// при обработке запроса — от набора голосующих актуальной конфигурации,
// а не от статического len(cm.peerIds).
func (cm *ConsensusModule) VerifyLeader() Future {
	cm.mu.Lock()
	isLeader := cm.cmState.state == Leader
	cm.mu.Unlock()
	if !isLeader {
		return errorFuture{err: ErrNotLeader}
	}
	vf := &verifyFuture{
		voted: make(map[int]struct{}),
	}
	vf.init(cm.shutdownCh)
	select {
	case cm.verifyCh <- vf:
		return vf
	case <-cm.shutdownCh:
		return errorFuture{err: ErrRaftShutdown}
	}
}

// appendConfigurationEntry записывает запись LogConfiguration в журнал лидера
// и запускает репликацию. Вызывается из leaderLoop.
func (cm *ConsensusModule) appendConfigurationEntry(future *configurationChangeFuture) {
	cm.mu.Lock()
	nextCfg, err := nextConfiguration(
		cm.cmState.configurations.committed,
		cm.cmState.configurations.committedIndex,
		future.req,
	)
	if err != nil {
		cm.mu.Unlock()
		future.respond(err)
		return
	}

	data, err := EncodeConfiguration(nextCfg)
	if err != nil {
		cm.mu.Unlock()
		future.respond(err)
		return
	}

	entry := &logFuture{
		log: LogEntry{
			Type: LogConfiguration,
			Data: data,
		},
	}
	entry.init(cm.shutdownCh)

	cm.cmState.configurations.latest = nextCfg
	cm.cmState.configurations.latestIndex = cm.cmState.lastLogIndex + 1

	cm.dispatchLogsUnsafe([]*logFuture{entry})
	cm.cmState.configurations.latestIndex = entry.log.Index
	future.index = entry.log.Index

	cm.leaderState.commitmentTracker.setConfiguration(voterIDs(nextCfg), cm.lookupTermLocked)

	// Запустить репликацию новым серверам с момента append config entry
	// (асимметричная форма): без этого AddVoter из
	// кластера с одним голосующим — вечный deadlock (новый голосующий не
	// получает AE до фиксации своей config entry, а его ack входит в
	// кворум 2/2 этой записи). Останов репликации удаляемым серверам
	// остаётся в post-commit горутине (startStopReplicationLocked) — удаляемый
	// сервер сохраняет шанс получить запись о собственном удалении.
	cm.ensureReplicationForLocked(nextCfg)

	cm.leaderState.commitmentTracker.commit(cm.cmState.lastLogIndex, cm.lookupTermLocked)
	savedCommitIndex := cm.cmState.commitIndex
	if newCI := cm.leaderState.commitmentTracker.getCommitIndex(); newCI > cm.cmState.commitIndex {
		cm.traceLockedLogf(2, "leader sets commitIndex := %d", newCI)
		cm.cmState.commitIndex = newCI
	}
	// Значение снимается в критической секции до Unlock (RISK-002):
	// чтение cm.cmState.commitIndex вне cm.mu — data race.
	commitIndex := cm.cmState.commitIndex
	cm.mu.Unlock()

	if commitIndex > savedCommitIndex {
		cm.processLogs(commitIndex)
	}
	cm.leaderSendAEs()

	cm.goSpawn(func() {
		err := entry.Error()
		if err == nil {
			cm.mu.Lock()
			cm.startStopReplicationLocked(nextCfg)
			cm.cmState.configurations.committed = nextCfg
			cm.cmState.configurations.committedIndex = entry.log.Index
			cm.mu.Unlock()
			cm.leaderSendAEs()
		}
		future.respond(err)
	})
}

// configurationChangeChIfStable возвращает канал confChangeCh только когда
// конфигурация стабильна (latest == committed), noop лидера зафиксирован
// и сервер всё ещё является лидером.
func (cm *ConsensusModule) configurationChangeChIfStable() chan *configurationChangeFuture {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state != Leader {
		return nil
	}
	if cm.cmState.configurations.latestIndex != cm.cmState.configurations.committedIndex {
		return nil
	}
	if cm.cmState.commitIndex < cm.leaderState.leaderStartIndex {
		return nil
	}
	return cm.confChangeCh
}

// enqueueConfigurationChange отправляет запрос на изменение конфигурации
// в канал confChangeCh лидера. Возвращает ошибку, если сервер не лидер.
func (cm *ConsensusModule) enqueueConfigurationChange(future *configurationChangeFuture) error {
	cm.mu.Lock()
	if cm.cmState.state != Leader {
		cm.mu.Unlock()
		return ErrNotLeader
	}
	// Во время передачи лидерства изменения конфигурации блокируются.
	if atomic.LoadInt32(&cm.leaderState.leadershipTransferInProgress) != 0 {
		cm.mu.Unlock()
		return ErrLeadershipTransferInProgress
	}
	cm.mu.Unlock()

	select {
	case cm.confChangeCh <- future:
		return nil
	case <-cm.shutdownCh:
		return ErrRaftShutdown
	}
}

// handleLeadershipTransfer реализует логику передачи лидерства.
//
// Вызывается только из leaderLoop. Блокирует leaderLoop до завершения
// передачи или таймаута. Все новые Apply и confChange во время передачи
// отклоняются через флаг leadershipTransferInProgress.
func (cm *ConsensusModule) handleLeadershipTransfer(future *leadershipTransferFuture) {
	targetID := int(future.targetID)

	// 1. Устанавливаем флаг блокировки Apply.
	// Флаг будет снят в stepDown или в defer leaderLoop при ошибке.
	atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 1)

	// 2. Проверяем, что цель — голосующий и достижима.
	cm.mu.Lock()
	if !hasVote(cm.cmState.configurations.latest, targetID) {
		cm.mu.Unlock()
		atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
		future.respond(ErrLeadershipLost)
		return
	}
	lastIdx := cm.cmState.lastLogIndex
	targetNextIdx := cm.leaderState.nextIndex[targetID]
	savedCurrentTerm := cm.cmState.currentTerm
	cm.mu.Unlock()

	// 3. Если цель отстаёт по журналу — догоняем.
	if targetNextIdx <= lastIdx {
		done := make(chan error, 1)
		cm.goSpawn(func() {
			for {
				// Отправляем сигнал репликации целевому узлу через ту же
				// дедупликацию, что и рассылка лидера: догоняющий цикл не
				// порождает второго отправителя одному соседу.
				cm.leaderSendAEsToPeerIfIdle(targetID, savedCurrentTerm)

				// Ждём короткий интервал, затем проверяем nextIndex.
				time.Sleep(10 * time.Millisecond)

				cm.mu.Lock()
				if cm.leaderState.nextIndex[targetID] > lastIdx {
					cm.mu.Unlock()
					done <- nil
					return
				}
				// Если лидерство потеряно — выход.
				if cm.cmState.state != Leader {
					cm.mu.Unlock()
					done <- ErrLeadershipLost
					return
				}
				cm.mu.Unlock()
			}
		})

		// Таймаут догоняющей репликации.
		select {
		case err := <-done:
			if err != nil {
				atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
				future.respond(err)
				return
			}
		case <-cm.shutdownCh:
			atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
			future.respond(ErrRaftShutdown)
			return
		case <-time.After(cm.electionTimeout()):
			atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
			future.respond(ErrLeadershipLost)
			return
		}
	}

	// 4. Отправляем TimeoutNow цели.
	reply, err := cm.transport.TimeoutNow(
		ServerID(targetID),
		TimeoutNowRequest{
			RPCHeader: RPCHeader{
				ProtocolVersion: ProtocolVersion,
				ServerID:        cm.id,
			},
		},
	)
	if err != nil {
		atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
		future.respond(err)
		return
	}
	if !reply.Success {
		// Цель уже была лидером или в процессе выборов.
		atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
		future.respond(ErrLeadershipLost)
		return
	}

	// 5. TimeoutNow отправлен успешно — сохраняем future для ответа
	// в stepDown, когда leaderLoop обнаружит более высокий term.
	// Флаг leadershipTransferInProgress остаётся установленным
	// до stepDown — так Apply блокируется на всё время передачи.
	cm.leaderState.leadershipTransferFuture = future
}

// runLeaderLoop — единый центральный цикл лидера. Запускается при переходе
// в состояние Leader и завершается при потере лидерства или остановке CM.
//
// Обрабатывает все события лидера в одном select:
//   - applyCh: новые команды от клиентов (групповой commit, §5.1)
//   - commitCh: уведомление от commitmentTracker об изменении commitIndex
//   - stepDown: обнаружение более высокого term из RPC-ответа
//   - confChangeCh: изменения конфигурации кластера (placeholder)
//   - heartbeatTicker: регулярная отправка Heartbeat/AppendEntries
//   - shutdownCh: остановка модуля
//
// После выхода из цикла все ожидающие future из списка inflight
// получают ошибку ErrLeadershipLost, после чего список очищается.
//
//nolint:funlen,gocognit
func (cm *ConsensusModule) runLeaderLoop() {
	startNow := time.Now()
	cm.mu.Lock()
	cm.leaderLoopsAlive++
	cm.mu.Unlock()
	defer func() {
		cm.mu.Lock()
		cm.leaderLoopsAlive--
		if cm.leaderState.leadershipTransferFuture != nil {
			atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
			cm.leaderState.leadershipTransferFuture.respond(ErrLeadershipLost)
			cm.leaderState.leadershipTransferFuture = nil
		}
		if len(cm.leaderState.pendingVerify) > 0 {
			for _, vf := range cm.leaderState.pendingVerify {
				vf.respond(ErrLeadershipLost)
			}
			cm.leaderState.pendingVerify = nil
		}
		inflightCount := len(cm.leaderState.inflight)
		cm.traceLockedLogf(1, "leaderLoop exit: responding to %d inflight futures", inflightCount)
		for _, future := range cm.leaderState.inflight {
			future.respond(ErrLeadershipLost)
		}
		cm.leaderState.inflight = make(map[int]*logFuture)

		// Сбросить inflightAE для всех соседей при выходе из leader loop.
		// Это гарантирует, что "зависшие" true флаги (CAS успешен,
		// но горутина не стартовала из-за завершения цикла) не блокируют
		// следующего лидера.
		//
		// nil-проверка защищает от nil-элемента: если конфигурация
		// изменилась после старта лидера, а leader loop завершился до
		// вызова startStopReplicationLocked для нового peer, inflightAE[peerID]
		// может быть nil. Store на nil *atomic.Bool паникует без recover().
		for peerID := range cm.leaderState.inflightAE {
			if cm.leaderState.inflightAE[peerID] != nil {
				cm.leaderState.inflightAE[peerID].Store(false)
			}
		}
		cm.mu.Unlock()
		elapsed := time.Since(startNow)
		cm.traceLogf(1, "leaderLoop exit: elapsed=%v", elapsed)
	}()

	heartbeatTicker := time.NewTicker(HeartbeatTimeoutMs * time.Millisecond)
	defer heartbeatTicker.Stop()

	// Страховка на случай пропущенного уведомления о фиксации: основной
	// путь применения — ветка commitCh, применяющая записи по факту
	// фиксации.
	applyTicker := time.NewTicker(applyBatchInterval)
	defer applyTicker.Stop()

	for {
		select {
		case future := <-cm.applyCh:
			cm.mu.Lock()
			if cm.cmState.state != Leader {
				cm.mu.Unlock()
				future.respond(ErrNotLeader)
				return
			}
			cm.mu.Unlock()

			ready := []*logFuture{future}
		groupCommit:
			for range leaderBatchSize - 1 {
				select {
				case f := <-cm.applyCh:
					ready = append(ready, f)
				default:
					break groupCommit
				}
			}
			cm.dispatchLogs(ready)
			// Немедленная репликация после записи в журнал.
			cm.leaderSendAEs()

			heartbeatTicker.Stop()
			heartbeatTicker.Reset(HeartbeatTimeoutMs * time.Millisecond)

		case newCommitIndex := <-cm.commitCh:
			cm.mu.Lock()
			if newCommitIndex > cm.cmState.commitIndex {
				cm.traceLockedLogf(2, "leader sets commitIndex := %d", newCommitIndex)
				cm.cmState.commitIndex = newCommitIndex

				// Обновить committed конфигурацию, если latest был зафиксирован.
				if cm.cmState.configurations.latestIndex > cm.cmState.configurations.committedIndex &&
					newCommitIndex >= cm.cmState.configurations.latestIndex {
					cm.cmState.configurations.committed = cm.cmState.configurations.latest
					cm.cmState.configurations.committedIndex = cm.cmState.configurations.latestIndex
				}

				// Проверить, остался ли лидер в committed конфигурации.
				if !hasVote(cm.cmState.configurations.committed, cm.id) {
					cm.traceLockedLogf(1, "leader stepping down: not in committed configuration")
					cm.counters.stepDowns.configExit.Add(1)
					cm.becomeFollowerLocked(cm.cmState.currentTerm)
					cm.mu.Unlock()
					return
				}
				cm.mu.Unlock()
				// Рассылка нового индекса фиксации соседям выполняется
				// первой: применение к машине состояний её не задерживает.
				cm.leaderSendAEs()
				// Применение по факту фиксации, без ожидания тика.
				// processLogs сам захватывает cm.mu, поэтому вызывается
				// только после её освобождения.
				cm.processLogs(newCommitIndex)
			} else {
				cm.mu.Unlock()
			}

		case <-applyTicker.C:
			cm.mu.Lock()
			ci := cm.cmState.commitIndex
			applied := cm.cmState.lastApplied
			cm.mu.Unlock()
			// Сравниваем локальные копии: оба значения могут устареть
			// сразу после Unlock. Это допустимо — авторитетная проверка
			// того же условия выполняется внутри processLogs под cm.mu,
			// а processLogs обязан вызываться без удержания cm.mu.
			if ci > applied {
				cm.processLogs(ci)
			}

		case <-cm.stepDown:
			cm.mu.Lock()
			cm.traceLockedLogf(1, "leader stepping down")
			if cm.leaderState.leadershipTransferFuture != nil {
				atomic.StoreInt32(&cm.leaderState.leadershipTransferInProgress, 0)
				cm.leaderState.leadershipTransferFuture.respond(nil)
				cm.leaderState.leadershipTransferFuture = nil
			}
			cm.mu.Unlock()
			return

		case <-heartbeatTicker.C:
			cm.mu.Lock()
			cm.leaderState.heartbeatTicks++
			cm.mu.Unlock()
			cm.checkQuorumContact()
			cm.leaderSendAEs()

		case future := <-cm.configurationChangeChIfStable():
			cm.appendConfigurationEntry(future)

		case vf := <-cm.verifyCh:
			cm.mu.Lock()
			if cm.cmState.state != Leader {
				vf.respond(ErrNotLeader)
				cm.mu.Unlock()
				break
			}
			// Порог кворума — от набора голосующих актуальной (latest)
			// конфигурации, вычисленный в момент обработки запроса,
			// а не в момент вызова VerifyLeader.
			if !hasVote(cm.cmState.configurations.latest, cm.id) {
				// Лидер не голосующий собственной конфигурации (self-removal):
				// подтвердить кворумное лидерство нельзя.
				vf.respond(ErrNotLeader)
				cm.mu.Unlock()
				break
			}
			vf.quorumSize = quorumSize(len(voterIDs(cm.cmState.configurations.latest)))

			// Привязка запроса к раунду: эпоха инкрементируется под cm.mu,
			// голоса засчитываются только от AE с dispatchEpoch >= epoch.
			cm.leaderState.verifyEpoch++
			vf.epoch = cm.leaderState.verifyEpoch

			// Self-голос лидера (ReadIndex: узел знает свой журнал).
			vf.vote(cm.id, true)
			if vf.votes >= vf.quorumSize {
				cm.mu.Unlock()
				break
			}
			vf.enqueuedAtHeartbeat = cm.leaderState.heartbeatTicks
			cm.leaderState.pendingVerify = append(cm.leaderState.pendingVerify, vf)
			// Снимок длины под cm.mu: чтение len(pendingVerify) вне
			// блокировки гоняло с записью списка из горутин репликации.
			// Механика списка и снятия завершённых — без изменений.
			firstPending := len(cm.leaderState.pendingVerify) == 1
			cm.mu.Unlock()
			if firstPending {
				cm.leaderSendAEs()
			}

		case future := <-cm.leaderState.leadershipTransferCh:
			// Запуск graceful передачи лидерства.
			// Блокирует leaderLoop до завершения передачи или таймаута.
			cm.handleLeadershipTransfer(future)

		case <-cm.shutdownCh:
			return
		}
	}
}

// checkQuorumContact шагает вниз, в ведомые, если кворум голосующих
// актуальной конфигурации не отвечал дольше checkQuorumTimeout. Вызывается
// из цикла лидера по тику пульса; собственных горутин и таймеров не заводит.
//
// Состояние переводится в Follower, но цикл не завершается немедленно:
// becomeFollowerLocked кладёт токен в канал шага вниз, и цикл забирает
// собственный токен на ближайшей итерации — той же веткой, что и при
// обнаружении большего терма. Немедленный возврат оставил бы токен в канале
// ёмкости 1 и завершил бы следующий цикл лидера этого узла.
//
// Самостоятельно захватывает и освобождает cm.mu.
func (cm *ConsensusModule) checkQuorumContact() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.cmState.state != Leader || cm.quorumContactedLocked(time.Now()) {
		return
	}
	cm.traceLockedLogf(
		0, "leader stepping down: no contact with quorum of voters for %v", cm.checkQuorumTimeout,
	)
	cm.counters.stepDowns.checkQuorum.Add(1)
	cm.becomeFollowerLocked(cm.cmState.currentTerm)
}

// quorumContactedLocked сообщает, отвечал ли кворум голосующих актуальной
// конфигурации в пределах checkQuorumTimeout к моменту now. Собственный узел
// считается контактировавшим всегда; отсутствующая отметка трактуется как
// «контакт только что был».
// Требует удержания cm.mu.
func (cm *ConsensusModule) quorumContactedLocked(now time.Time) bool {
	voters := voterIDs(cm.cmState.configurations.latest)
	if len(voters) == 0 {
		return true
	}
	contacted := 0
	for _, voterID := range voters {
		if voterID == cm.id {
			contacted++
			continue
		}
		last, ok := cm.leaderState.lastContact[voterID]
		if !ok || now.Sub(last) <= cm.checkQuorumTimeout {
			contacted++
		}
	}
	return contacted >= quorumSize(len(voters))
}

// startLeaderLocked переводит cm в состояние лидера и запускает единый leaderLoop.
// Требует удержания cm.mu.
func (cm *ConsensusModule) startLeaderLocked() {
	cm.cmState.state = Leader
	cm.cmState.leaderLastContact = time.Now()
	cm.cmState.leaderID = cm.id

	// Токен предыдущего лидерства, который никто не забрал, завершил бы
	// новый цикл лидера сразу после запуска, не переведя состояние
	// в Follower. Забираем его здесь: канал ёмкости 1, оставленное
	// значение относится к уже завершённому циклу.
	select {
	case <-cm.stepDown:
	default:
	}

	// Создание состояния задержки повторов вместе с nextIndex/matchIndex
	// карты свежие на каждое лидерство.
	cm.leaderState.replFailures = make(map[int]int)
	cm.leaderState.lastAttempt = make(map[int]time.Time)
	// Отметки контакта не переносятся между термами: каждому лидерству —
	// своя карта. Момент вступления считается контактом со всеми соседями,
	// иначе проверка кворума сработала бы до первого пульса.
	cm.leaderState.lastContact = make(map[int]time.Time)
	now := time.Now()

	cfg := cm.cmState.configurations.latest
	for _, s := range cfg.ConfigServers {
		peerID := int(s.ID)
		if peerID == cm.id {
			continue
		}
		cm.leaderState.nextIndex[peerID] = cm.cmState.lastLogIndex + 1
		cm.leaderState.matchIndex[peerID] = -1
		cm.leaderState.lastContact[peerID] = now

		// Инициализировать inflightAE для нового peer.
		if cm.leaderState.inflightAE[peerID] == nil {
			cm.leaderState.inflightAE[peerID] = new(atomic.Bool)
		}
		cm.leaderState.inflightAE[peerID].Store(false)
	}
	cm.traceLockedLogf(
		0, "becomes Leader; term=%d, nextIndex=%v, matchIndex=%v; len(log)=%d",
		cm.cmState.currentTerm, cm.leaderState.nextIndex, cm.leaderState.matchIndex, len(cm.cmState.log),
	)

	cm.leaderState.leaderStartIndex = cm.cmState.lastLogIndex
	cm.leaderState.commitmentTracker = newCommitmentTracker(
		cm.id, cm.cmState.currentTerm, cm.cmState.lastLogIndex, cm.commitCh,
	)
	cm.leaderState.commitmentTracker.setConfiguration(voterIDs(cfg), cm.lookupTermLocked)
	cm.leaderState.commitmentTracker.setMatch(cm.id, cm.cmState.lastLogIndex, cm.lookupTermLocked)

	// Noop entry при утверждении лидерства (§5.4.2).
	// Лидер должен зафиксировать noop запись своего терма, чтобы
	// зафиксировать любые незафиксированные записи из предыдущих термов.
	noop := &logFuture{
		log: LogEntry{
			Type: LogNoop,
		},
	}
	noop.init(cm.shutdownCh)
	cm.dispatchLogsUnsafe([]*logFuture{noop})

	// Синхронизировать self-match трекера с только что добавленной
	// noop-записью: dispatchLogsUnsafe трекер
	// не обновляет (в отличие от dispatchLogs — raft_cm_log.go), а
	// setMatch(self, …) выше был выполнен ДО noop. Без этого вызова
	// медиана при чётном n попадает на запись прошлого терма и
	// term-guard блокирует фиксацию законного кворума (включая noop
	// текущего терма), а configurationChangeChIfStable остаётся
	// закрытым. Вызов изолирован от appendConfigurationEntry (там
	// commit выполняется под новой конфигурацией трекера).
	cm.leaderState.commitmentTracker.commit(cm.cmState.lastLogIndex, cm.lookupTermLocked)

	cm.goSpawnLocked(cm.runLeaderLoop)
}

// ensureReplicationForLocked добавляет репликационные структуры (nextIndex,
// matchIndex, inflightAE) для серверов cfg, которых ещё нет в leaderState.
// Add-only процедура: НИЧЕГО не удаляет — остановка репликации отсутствующим
// серверам остаётся в post-commit горутине (startStopReplicationLocked),
// чтобы удаляемый сервер сохранял шанс получить запись о собственном удалении.
//
// inflightAE.Store(false) выполняется ТОЛЬКО для вновь созданных флагов
// сброс флага существующей горутины репликации привёл бы к двум параллельным
// RPC к одному peer.
//
// Требует удержания cm.mu; сама блокировку не берёт.
func (cm *ConsensusModule) ensureReplicationForLocked(cfg Configuration) {
	for _, s := range cfg.ConfigServers {
		peerID := int(s.ID)
		if peerID == cm.id {
			continue
		}
		if _, ok := cm.leaderState.nextIndex[peerID]; !ok {
			cm.leaderState.nextIndex[peerID] = cm.cmState.lastLogIndex + 1
			cm.leaderState.matchIndex[peerID] = -1
			cm.recordContactLocked(peerID)
		}
		if _, ok := cm.leaderState.inflightAE[peerID]; !ok {
			cm.leaderState.inflightAE[peerID] = new(atomic.Bool)
			cm.leaderState.inflightAE[peerID].Store(false)
		}
	}
}

// startStopReplicationLocked обновляет набор реплицируемых соседей в соответствии
// с новой конфигурацией. Удаляет серверы, отсутствующие в cfg, и добавляет
// новые (add-only часть выделена в ensureReplicationForLocked).
// Требует удержания cm.mu.
func (cm *ConsensusModule) startStopReplicationLocked(cfg Configuration) {
	for peerID := range cm.leaderState.nextIndex {
		if peerID == cm.id {
			continue
		}
		found := false
		for _, s := range cfg.ConfigServers {
			if int(s.ID) == peerID {
				found = true
				break
			}
		}
		if !found {
			// Удалить inflightAE для удалённого peer.
			delete(cm.leaderState.inflightAE, peerID)
			delete(cm.leaderState.nextIndex, peerID)
			delete(cm.leaderState.matchIndex, peerID)
			delete(cm.leaderState.lastContact, peerID)
		}
	}
	cm.ensureReplicationForLocked(cfg)
}
