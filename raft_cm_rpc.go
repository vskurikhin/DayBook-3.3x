package raft

import (
	"time"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// runRPCReader — горутина, читающая входящие RPC-запросы из транспорта
// и отправленная их по типу команды. Ответ отправляется через RespChan.
// Завершает работу при закрытии shutdownCh.
func (cm *ConsensusModule) runRPCReader() {
	consumer := cm.transport.Consumer()
	for {
		select {
		case rpc, ok := <-consumer:
			if !ok {
				return
			}
			cm.handleRPC(rpc)
		case <-cm.shutdownCh:
			return
		}
	}
}

// handleRPC отправляет один RPC-запрос по типу команды и отправляет ответ.
// Ответ отправляется с защитой от закрытого канала (select/default).
// timeoutNow и handleInstallSnapshot сами управляют RespChan.
func (cm *ConsensusModule) handleRPC(rpc RPC) {
	cm.traceLogf(_traceLevelReplication, "handleRPC: %T", rpc.Command)

	switch cmd := rpc.Command.(type) {
	case *RequestVoteArgs:
		var reply RequestVoteReply
		err := cm.RequestVote(*cmd, &reply)
		cm.respondRPC(rpc, &reply, err)
	case *AppendEntriesArgs:
		var reply AppendEntriesReply
		err := cm.AppendEntries(*cmd, &reply)
		cm.respondRPC(rpc, &reply, err)
	case *RequestPreVoteArgs:
		var reply RequestPreVoteReply
		err := cm.RequestPreVote(*cmd, &reply)
		cm.respondRPC(rpc, &reply, err)
	case *TimeoutNowRequest:
		cm.timeoutNow(rpc, cmd)
	case *InstallSnapshotRequest:
		cm.handleInstallSnapshot(rpc, cmd)
	default:
		cm.respondRPC(rpc, nil, contract.ErrNotImplemented)
	}
}

// respondRPC отправляет ответ через rpc.RespChan с защитой от закрытого канала.
// Если канал закрыт или получатель недоступен — ответ теряется, что допустимо
// при разрыве соединения.
func (cm *ConsensusModule) respondRPC(rpc RPC, reply any, err error) {
	select {
	case rpc.RespChan <- RPCResponse{Reply: reply, Error: err}:
	default:
		cm.traceLogf(_traceLevelLoops, "rpc.RespChan closed/unavailable for %T", rpc.Command)
	}
}

// AppendEntries обрабатывает входящий запрос лидера на добавление записей
// журнала (§5.3).
//
// Граница долговечности: ответ Success: true отправляется только после того,
// как принятые записи журнала стали долговечными. Лидер по такому ответу
// продвигает matchIndex ведомого и вправе зафиксировать запись, поэтому
// потеря записей при крэше ведомого нарушила бы безопасность машины
// состояний. Ответ формируется здесь, а отправляется вызывающим уже после
// возврата из обработчика, поэтому сохранение состояния выполняется до
// возврата и покрывает все ветки, изменившие постоянное состояние.
//
// Обработчик принимает аргументы по значению: такая сигнатура — часть
// экспортируемого API обработчиков RPC и согласована с контрактом транспорта,
// поэтому предупреждение о тяжёлом параметре подавлено.
//
//nolint:gocritic
func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	startTimeNow := time.Now()
	defer func() { cm.latency.appendEntries.observe(time.Since(startTimeNow)) }()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state == Dead {
		return nil
	}

	if err := checkRPCHeader(&args); err != nil {
		return err
	}

	cm.traceLockedLogf(
		_traceLevelReplication,
		"AppendEntries: %v, Term:%d LeaderID:%d PrevLogIndex:%d PrevLogTerm:%d len(Entries):%d",
		args.RPCHeader, args.Term, args.LeaderID, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries),
	)

	if cm.appendEntriesDuringLeadershipTransferLocked(&args, reply) {
		return nil
	}

	if args.Term > cm.cmState.currentTerm {
		cm.traceLockedLogf(_traceLevelReplication, "... term out of date in AppendEntries")
		cm.becomeFollowerLocked(args.Term)
	}

	reply.Success = false
	if args.Term == cm.cmState.currentTerm {
		if cm.cmState.state != Follower {
			cm.becomeFollowerLocked(args.Term)
		}
		cm.cmState.electionResetEvent = time.Now()
		cm.cmState.leaderLastContact = time.Now()
		cm.cmState.leaderID = args.LeaderID

		// Проверяет, содержит ли наш журнал запись с индексом PrevLogIndex,
		// у которой терм совпадает с PrevLogTerm.
		// Обратите внимание, что в особом случае, когда PrevLogIndex == -1,
		// условие считается истинным автоматически.
		if args.PrevLogIndex == -1 ||
			(args.PrevLogIndex <= cm.cmState.lastLogIndex && cm.lookupTermLocked(args.PrevLogIndex) == args.PrevLogTerm) {
			commitIdx, advanced := cm.appendMatchingEntriesLocked(&args, reply)
			if advanced {
				cm.mu.Unlock()
				cm.processLogs(commitIdx)
				cm.mu.Lock()
			}
		} else {
			cm.buildConflictReplyLocked(&args, reply)
		}
	}

	// Сохранение состояния до возврата закрывает случай, когда лидер шлёт
	// записи, не продвигая LeaderCommit: ветка продвижения индекса фиксации
	// сохраняет состояние сама, а ветка добавления записей — нет. Повторное
	// сохранение неизменившегося состояния записей на диск не выполняет.
	cm.persistToStorage()

	reply.RPCHeader = RPCHeader{
		ProtocolVersion: ProtocolVersion,
		ServerID:        cm.id,
	}
	reply.Term = cm.cmState.currentTerm
	cm.traceLockedLogf(_traceLevelReplication, "AppendEntries reply: %+v", *reply)
	return nil
}

// appendEntriesDuringLeadershipTransferLocked обрабатывает AppendEntries,
// пришедший кандидату, выдвинутому передачей лидерства. Такой кандидат
// отвечает отказом и сохраняет свою роль; в ведомые его возвращает только
// строго больший терм. Возвращает true, когда ответ сформирован здесь и
// обработчику больше делать нечего.
//
// Требует удержания cm.mu.
func (cm *ConsensusModule) appendEntriesDuringLeadershipTransferLocked(
	args *AppendEntriesArgs, reply *AppendEntriesReply,
) (handled bool) {
	// Candidate от leadership transfer не должен делать step-down
	// при получении AppendEntries от старого лидера, если терм равен
	// текущему или на 1 меньше. Исключение: терм строго больше нашего.
	if cm.cmState.state == Candidate && cm.cmState.candidateFromLeadershipTransfer.Load() {
		if args.Term > cm.cmState.currentTerm {
			cm.traceLockedLogf(_traceLevelReplication, "... term out of date in AppendEntries (candidate from LT)")
			cm.becomeFollowerLocked(args.Term)
			cm.cmState.candidateFromLeadershipTransfer.Store(false)
		}
		reply.RPCHeader = RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		}
		reply.Term = cm.cmState.currentTerm
		reply.Success = false
		cm.persistToStorage()
		return true
	}
	return false
}

// appendMatchingEntriesLocked выполняет путь успеха: журнал содержит запись
// PrevLogIndex с термом PrevLogTerm. Метод находит точку вставки, дописывает
// недостающие записи лидера, обрабатывает попавшие в диапазон записи
// конфигурации и продвигает индекс фиксации. Возвращает индекс фиксации и
// признак того, что продвижение состоялось; применение записей к машине
// состояний остаётся за вызывающим и выполняется без удержания блокировки.
//
// Требует удержания cm.mu.
func (cm *ConsensusModule) appendMatchingEntriesLocked(
	args *AppendEntriesArgs, reply *AppendEntriesReply,
) (commitIdx int, advanced bool) {
	reply.Success = true

	// Находит точку вставки — место, где происходит несовпадение термов
	// между существующими записями журнала, начиная с PrevLogIndex+1,
	// и новыми записями, отправленными лидером через RPC.
	// Используем logPositionLocked для поиска позиции в срезе после сжатия.
	logInsertIndex := args.PrevLogIndex + 1
	logInsertPos := cm.logPositionLocked(logInsertIndex)
	newEntriesIndex := 0

	for logInsertPos < len(cm.cmState.log) && newEntriesIndex < len(args.Entries) {
		if cm.cmState.log[logInsertPos].Term != args.Entries[newEntriesIndex].Term {
			break
		}
		logInsertPos++
		logInsertIndex++
		newEntriesIndex++
	}
	// После завершения этого цикла:
	// - logInsertIndex указывает на конец журнала
	//   или на индекс, где терм записи отличается от записи лидера.
	// - newEntriesIndex указывает на конец массива Entries
	//   или на индекс, где терм записи отличается от соответствующей записи журнала.
	if newEntriesIndex < len(args.Entries) {
		cm.traceLockedLogf(
			_traceLevelReplication,
			"... inserting entries %v from index %d",
			args.Entries[newEntriesIndex:], logInsertIndex,
		)
		cm.cmState.log = append(cm.cmState.log[:logInsertPos], args.Entries[newEntriesIndex:]...)
		cm.traceLockedLogf(_traceLevelLogDump, "... log is now: %v", cm.cmState.log)
		cm.rebuildLastLogLocked()
		cm.rebuildTermIndexMapLocked()
		cm.cmState.logNeedsPersist = true
		// Если перезапись затронула конфигурацию, откатываем latest.
		cm.processLogConflict(logInsertIndex)
	}

	// Обработать LogConfiguration записи в полученном диапазоне.
	for _, entry := range args.Entries[newEntriesIndex:] {
		if entry.Type == LogConfiguration {
			cm.processConfigurationLogEntry(&entry)
		}
	}

	if args.LeaderCommit > cm.cmState.commitIndex {
		cm.cmState.commitIndex = min(args.LeaderCommit, cm.cmState.lastLogIndex)
		cm.traceLockedLogf(_traceLevelReplication, "... setting commitIndex=%d", cm.cmState.commitIndex)
		cm.persistToStorage()
		return cm.cmState.commitIndex, true
	}
	return 0, false
}

// buildConflictReplyLocked заполняет подсказку о конфликте, когда журнал не
// содержит записи PrevLogIndex с термом PrevLogTerm: по паре
// ConflictIndex/ConflictTerm лидер откатывает nextIndex за один шаг вместо
// последовательного уменьшения.
//
// Требует удержания cm.mu.
func (cm *ConsensusModule) buildConflictReplyLocked(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	// Не найдено совпадение для PrevLogIndex/PrevLogTerm.
	if args.PrevLogIndex > cm.cmState.lastLogIndex {
		// Нижняя граница по границе снимка
		// follower никогда не
		// запрашивает записи, покрытые его собственным снимком.
		// Обе величины совпадают; правило —
		// дополнительная линия защиты.
		reply.ConflictIndex = max(cm.cmState.lastLogIndex, cm.cmState.lastSnapshotIndex) + 1
		reply.ConflictTerm = -1
	} else {
		reply.ConflictTerm = cm.lookupTermLocked(args.PrevLogIndex)
		// Ищем первую запись с данным термом, двигаясь назад от PrevLogIndex.
		// Используем бинарный поиск вместо линейного, так как лог может
		// быть сжат, и prevLogIndex может быть вне среза.
		pos := cm.logPositionLocked(args.PrevLogIndex)
		i := pos - 1
		for 0 <= i && i < len(cm.cmState.log) && cm.cmState.log[i].Term == reply.ConflictTerm {
			i--
		}
		if i+1 < len(cm.cmState.log) {
			reply.ConflictIndex = cm.cmState.log[i+1].Index
		} else {
			// Журнал пуст или conflictIndex вне диапазона.
			// Используем PrevLogIndex как fallback.
			reply.ConflictIndex = args.PrevLogIndex
		}
	}
}

// RequestVote обрабатывает входящий запрос голосования (§5.4.1).
//
// Кандидаты, не являющиеся голосующими в текущей конфигурации, отклоняются.
// Nonvoter не может быть избран лидером ни при каких обстоятельствах.
func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	startTimeNow := time.Now()
	defer func() { cm.latency.requestVote.observe(time.Since(startTimeNow)) }()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.cmState.state == Dead {
		return nil
	}

	if err := checkRPCHeader(&args); err != nil {
		return err
	}

	// Nonvoter не может быть избран лидером — отклоняем запрос.
	if !hasVote(cm.cmState.configurations.latest, args.GetRPCHeader().ServerID) {
		cm.traceLockedLogf(_traceLevelPreVote, "... RequestVote denied: not a voter")
		reply.VoteGranted = false
		reply.RPCHeader = RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		}
		reply.Term = cm.cmState.currentTerm
		return nil
	}

	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTermLocked()
	cm.traceLockedLogf(
		_traceLevelPreVote,
		"RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]",
		args, cm.cmState.currentTerm, cm.cmState.votedFor, lastLogIndex, lastLogTerm,
	)

	if args.Term > cm.cmState.currentTerm {
		cm.traceLockedLogf(_traceLevelPreVote, "... term out of date in RequestVote")
		cm.becomeFollowerLocked(args.Term)
	}

	// Проверка наличия известного лидера.
	// Если есть лидер, но это leadership transfer — голосуем (bypass).
	leaderIDKnown := cm.cmState.leaderID >= 0
	if leaderIDKnown && cm.cmState.leaderID != args.CandidateID && !args.LeadershipTransfer {
		cm.traceLockedLogf(_traceLevelPreVote, "... leader known, denying vote for %d", args.CandidateID)
		reply.VoteGranted = false
		reply.RPCHeader = RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		}
		reply.Term = cm.cmState.currentTerm
		return nil
	}

	termOk := cm.cmState.currentTerm == args.Term
	voteOk := cm.cmState.votedFor == -1 || cm.cmState.votedFor == args.CandidateID
	logOk := args.LastLogTerm > lastLogTerm ||
		(args.LastLogTerm == lastLogTerm && args.LastLogIndex >= lastLogIndex)
	if !termOk || !voteOk || !logOk {
		cm.traceLockedLogf(
			_traceLevelPreVote,
			"... vote denied: termOk=%v (cur=%d, args=%d), voteOk=%v (votedFor=%d, cand=%d),"+
				" logOk=%v (args=(%d,%d), local=(%d,%d))",
			termOk, cm.cmState.currentTerm, args.Term, voteOk, cm.cmState.votedFor, args.CandidateID,
			logOk, args.LastLogTerm, args.LastLogIndex, lastLogTerm, lastLogIndex)
	}
	if termOk && voteOk && logOk {
		reply.VoteGranted = true
		cm.cmState.votedFor = args.CandidateID
		cm.cmState.electionResetEvent = time.Now()
	} else {
		reply.VoteGranted = false
	}
	reply.RPCHeader = RPCHeader{
		ProtocolVersion: ProtocolVersion,
		ServerID:        cm.id,
	}
	reply.Term = cm.cmState.currentTerm
	cm.persistToStorage()
	cm.traceLockedLogf(_traceLevelPreVote, "... RequestVote reply: %+v", reply)
	return nil
}

// RequestPreVote обрабатывает входящий PreVote RPC (§4 Pre-Vote).
//
// В отличие от RequestVote:
//   - НЕ меняет votedFor, currentTerm
//   - НЕ персистит состояние
//   - НЕ переводит в Follower при args.Term > cm.cmState.currentTerm
//   - Добавляет проверку: знает ли получатель о действующем лидере
//
// PreVote использует ту же log-safety проверку, что и RequestVote (§5.4.1).
// Если получатель недавно получал heartbeat от лидера или отстаёт по логу —
// PreVote отклоняется, чтобы предотвратить лишние выборы.
func (cm *ConsensusModule) RequestPreVote(args RequestPreVoteArgs, reply *RequestPreVoteReply) error {
	startTimeNow := time.Now()
	defer func() { cm.latency.requestPreVote.observe(time.Since(startTimeNow)) }()
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.cmState.state == Dead {
		return nil
	}

	if err := checkRPCHeader(&args); err != nil {
		return err
	}

	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTermLocked()
	cm.traceLockedLogf(
		_traceLevelPreVote,
		"RequestPreVote: %+v [currentTerm=%d, log index/term=(%d, %d)]",
		args, cm.cmState.currentTerm, lastLogIndex, lastLogTerm,
	)

	// Отклоняем PreVote, если отправитель не является голосующим в текущей
	// конфигурации. Это defense-in-depth: runPreCandidate уже фильтрует
	// голосующих на стороне отправителя, но получатель тоже должен проверять.
	if !hasVote(cm.cmState.configurations.latest, args.GetRPCHeader().ServerID) {
		cm.traceLockedLogf(_traceLevelPreVote, "... RequestPreVote denied: not a voter")
		reply.RPCHeader = RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		}
		reply.Term = cm.cmState.currentTerm
		reply.VoteGranted = false
		return nil
	}

	reply.RPCHeader = RPCHeader{
		ProtocolVersion: ProtocolVersion,
		ServerID:        cm.id,
	}
	reply.Term = cm.cmState.currentTerm
	reply.VoteGranted = false

	// Предлагаемый term должен быть не меньше текущего.
	if args.Term < cm.cmState.currentTerm {
		cm.traceLockedLogf(_traceLevelPreVote, "... RequestPreVote denied: older term")
		return nil
	}

	// Отклоняем PreVote, если недавно был контакт с лидером (leaderLastContact
	// обновлён в пределах election timeout). Это предотвращает выборы при
	// кратковременных сетевых сбоях — кандидат явно не актуальный лидер.
	if !cm.cmState.leaderLastContact.IsZero() {
		leaderKnown := time.Since(cm.cmState.leaderLastContact) < cm.electionTimeout()
		if leaderKnown && args.GetRPCHeader().ServerID != cm.cmState.leaderID {
			cm.traceLockedLogf(_traceLevelPreVote, "... RequestPreVote denied: leader known")
			return nil
		}
	}

	// Log-safety: отклонить, если лог получателя новее лога кандидата.
	localLogNewer := lastLogTerm > args.LastLogTerm ||
		(lastLogTerm == args.LastLogTerm && lastLogIndex > args.LastLogIndex)
	if localLogNewer {
		cm.traceLockedLogf(_traceLevelPreVote, "... RequestPreVote denied: log is more up-to-date")
		return nil
	}

	reply.VoteGranted = true
	cm.traceLockedLogf(_traceLevelPreVote, "... RequestPreVote granted")
	return nil
}
