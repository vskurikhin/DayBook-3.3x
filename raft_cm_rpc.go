package raft

import (
	"time"
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
	cm.traceLogf(8, "handleRPC: %T", rpc.Command)

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
		cm.respondRPC(rpc, nil, ErrNotImplemented)
	}
}

// respondRPC отправляет ответ через rpc.RespChan с защитой от закрытого канала.
// Если канал закрыт или получатель недоступен — ответ теряется, что допустимо
// при разрыве соединения.
func (cm *ConsensusModule) respondRPC(rpc RPC, reply any, err error) {
	select {
	case rpc.RespChan <- RPCResponse{Reply: reply, Error: err}:
	default:
		cm.traceLogf(1, "rpc.RespChan closed/unavailable for %T", rpc.Command)
	}
}

//nolint:funlen,gocognit,gocritic
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

	cm.traceLockedLogf(8,
		"AppendEntries: %v, Term:%d LeaderID:%d PrevLogIndex:%d PrevLogTerm:%d len(Entries):%d",
		args.RPCHeader, args.Term, args.LeaderID, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries),
	)

	// Candidate от leadership transfer не должен делать step-down
	// при получении AppendEntries от старого лидера, если терм равен
	// текущему или на 1 меньше. Исключение: терм строго больше нашего.
	if cm.cmState.state == Candidate && cm.cmState.candidateFromLeadershipTransfer.Load() {
		if args.Term > cm.cmState.currentTerm {
			cm.traceLockedLogf(8, "... term out of date in AppendEntries (candidate from LT)")
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
		return nil
	}

	if args.Term > cm.cmState.currentTerm {
		cm.traceLockedLogf(8, "... term out of date in AppendEntries")
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
					8, "... inserting entries %v from index %d",
					args.Entries[newEntriesIndex:], logInsertIndex,
				)
				cm.cmState.log = append(cm.cmState.log[:logInsertPos], args.Entries[newEntriesIndex:]...)
				cm.traceLockedLogf(16, "... log is now: %v", cm.cmState.log)
				cm.rebuildLastLog()
				cm.rebuildTermIndexMap()
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
				cm.traceLockedLogf(8, "... setting commitIndex=%d", cm.cmState.commitIndex)
				cm.persistToStorage()
				commitIdx := cm.cmState.commitIndex
				cm.mu.Unlock()
				cm.processLogs(commitIdx)
				cm.mu.Lock()
			}
		} else {
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
	}

	reply.RPCHeader = RPCHeader{
		ProtocolVersion: ProtocolVersion,
		ServerID:        cm.id,
	}
	reply.Term = cm.cmState.currentTerm
	cm.traceLockedLogf(8, "AppendEntries reply: %+v", *reply)
	return nil
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
		cm.traceLockedLogf(4, "... RequestVote denied: not a voter")
		reply.VoteGranted = false
		reply.RPCHeader = RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		}
		reply.Term = cm.cmState.currentTerm
		return nil
	}

	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTerm()
	cm.traceLockedLogf(
		4, "RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]",
		args, cm.cmState.currentTerm, cm.cmState.votedFor, lastLogIndex, lastLogTerm,
	)

	if args.Term > cm.cmState.currentTerm {
		cm.traceLockedLogf(4, "... term out of date in RequestVote")
		cm.becomeFollowerLocked(args.Term)
	}

	// Проверка наличия известного лидера.
	// Если есть лидер, но это leadership transfer — голосуем (bypass).
	if cm.cmState.leaderID >= 0 && cm.cmState.leaderID != args.CandidateID && !args.LeadershipTransfer {
		cm.traceLockedLogf(4, "... leader known, denying vote for %d", args.CandidateID)
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
			4,
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
	cm.traceLockedLogf(4, "... RequestVote reply: %+v", reply)
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

	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTerm()
	cm.traceLockedLogf(
		4, "RequestPreVote: %+v [currentTerm=%d, log index/term=(%d, %d)]",
		args, cm.cmState.currentTerm, lastLogIndex, lastLogTerm,
	)

	// Отклоняем PreVote, если отправитель не является голосующим в текущей
	// конфигурации. Это defense-in-depth: runPreCandidate уже фильтрует
	// голосующих на стороне отправителя, но получатель тоже должен проверять.
	if !hasVote(cm.cmState.configurations.latest, args.GetRPCHeader().ServerID) {
		cm.traceLockedLogf(4, "... RequestPreVote denied: not a voter")
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
		cm.traceLockedLogf(4, "... RequestPreVote denied: older term")
		return nil
	}

	// Отклоняем PreVote, если недавно был контакт с лидером (leaderLastContact
	// обновлён в пределах election timeout). Это предотвращает выборы при
	// кратковременных сетевых сбоях — кандидат явно не актуальный лидер.
	if !cm.cmState.leaderLastContact.IsZero() {
		leaderKnown := time.Since(cm.cmState.leaderLastContact) < cm.electionTimeout()
		if leaderKnown && args.GetRPCHeader().ServerID != cm.cmState.leaderID {
			cm.traceLockedLogf(4, "... RequestPreVote denied: leader known")
			return nil
		}
	}

	// Log-safety: отклонить, если лог получателя новее лога кандидата.
	if lastLogTerm > args.LastLogTerm ||
		(lastLogTerm == args.LastLogTerm && lastLogIndex > args.LastLogIndex) {
		cm.traceLockedLogf(4, "... RequestPreVote denied: log is more up-to-date")
		return nil
	}

	reply.VoteGranted = true
	cm.traceLockedLogf(4, "... RequestPreVote granted")
	return nil
}
