package raft

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/pkg/raft/contract"
)

// _preVoteJitterMs — верхняя граница случайной паузы (миллисекунды)
// перед выборами после выигранного предварительного голосования.
// Пауза разводит по времени узлы, одновременно победившие в pre-vote,
// предотвращая одновременный переход в кандидаты с одинаковым
// термом (split vote).
const _preVoteJitterMs = 50

// becomeFollowerLocked делает cm последователем и сбрасывает его состояние.
// Если cm был лидером, отправляет сигнал в stepDown, чтобы leaderLoop
// завершил работу и разрешил все ожидающие future с ErrLeadershipLost.
// Требует удержания cm.mu.
func (cm *ConsensusModule) becomeFollowerLocked(term int) {
	wasLeader := cm.cmState.state == Leader
	cm.traceLockedLogf(_traceLevelKeyEvents, "becomes Follower with term=%d; len(log)=%v", term, len(cm.cmState.log))

	// Сбрасываем inflightAE для всех соседей при потере лидерства.
	// Это гарантирует, что новый лидер начнёт с «чистого листа».
	// Проверка на nil защищает от обращения к несуществующему пиру:
	// запись в nil *atomic.Bool приведёт к панике.
	for peerID := range cm.leaderState.inflightAE {
		if cm.leaderState.inflightAE[peerID] != nil {
			cm.leaderState.inflightAE[peerID].Store(false)
		}
	}

	cm.cmState.state = Follower
	if wasLeader {
		if term > cm.cmState.currentTerm {
			cm.counters.stepDowns.higherTerm.Add(1)
		}
		// Очищаем состояние повторных попыток репликации, связанное с ролью лидера.
		// Жизненный цикл этих полей строго привязан к роли Leader;
		// такой сброс исключает утечки и некорректное использование при смене роли.
		cm.leaderState.replFailures = nil
		cm.leaderState.lastAttempt = nil
		select {
		case cm.stepDown <- struct{}{}:
		default:
		}
	}
	if term > cm.cmState.currentTerm {
		cm.cmState.currentTerm = term
		cm.cmState.votedFor = -1
		cm.persistToStorage()
	}
	cm.cmState.leaderLastContact = time.Time{}
	cm.cmState.leaderID = -1
	cm.cmState.electionResetEvent = time.Now()

	select {
	case <-cm.cmState.electionTimerDone:
	default:
		close(cm.cmState.electionTimerDone)
	}
	cm.cmState.electionTimerDone = make(chan struct{})
	cm.goSpawnLocked(cm.runElectionTimer)
}

// startElectionLocked запускает новые выборы с этим CM в качестве кандидата.
// Требует удержания cm.mu.
//
// Если candidateFromLeadershipTransfer установлен, в RequestVoteArgs
// передаётся LeadershipTransfer=true, что bypass проверку наличия лидера
// на узлах-получателях. Флаг сбрасывается после того, как все горутины
// отправили RequestVote, чтобы последующие обычные выборы не имели этого флага.
//
// Неголосующие исключаются из рассылки RequestVote — они не участвуют
// в голосовании и не должны получать запросы на предоставление голоса.
func (cm *ConsensusModule) startElectionLocked() {
	startTimeNow := time.Now()
	defer func() { cm.latency.election.observe(time.Since(startTimeNow)) }()
	cm.cmState.state = Candidate
	cm.cmState.currentTerm += 1
	savedCurrentTerm := cm.cmState.currentTerm
	cm.cmState.electionResetEvent = time.Now()
	cm.cmState.votedFor = cm.id
	cm.persistToStorage()
	cm.traceLockedLogf(
		_traceLevelKeyEvents,
		"becomes Candidate (currentTerm=%d); len(log)=%v",
		savedCurrentTerm, len(cm.cmState.log),
	)

	// Сохраняем флаг в локальную переменную до его сброса.
	isLeadershipTransfer := cm.cmState.candidateFromLeadershipTransfer.Load()
	// Сбрасываем флаг, чтобы последующие обычные выборы не имели этого флага.
	cm.cmState.candidateFromLeadershipTransfer.Store(false)

	// Определяем состав кластера из текущей конфигурации.
	cfg := cm.cmState.configurations.latest
	voters := voterIDs(cfg)
	voterCount := len(voters)

	// Одиночный узел: побеждает на выборах немедленно.
	if voterCount <= 1 {
		cm.startLeaderLocked()
		return
	}

	var votesReceived atomic.Int32
	votesReceived.Store(1)

	// Параллельно отправить RPC-запросы RequestVote всем голосующим.
	for _, s := range cfg.ConfigServers {
		peerID := int(s.ID)
		if peerID == cm.id {
			continue
		}
		if s.Suffrage != Voter {
			continue
		}
		cm.goSpawnLocked(func() {
			cm.requestVoteFromPeer(peerID, savedCurrentTerm, voterCount, isLeadershipTransfer, &votesReceived)
		})
	}

	// Запустить новый таймер выборов на случай, если текущие выборы не завершатся успешно.
	cm.goSpawnLocked(cm.runElectionTimer)
}

// requestVoteFromPeer отправляет RequestVote одному соседу и учитывает ответ;
// голос засчитывается, пока узел остаётся кандидатом того же терма.
//
// Самостоятельно захватывает и освобождает cm.mu: две короткие критические
// секции — снимок lastLogIndexAndTermLocked() и разбор ответа; вызов транспорта
// происходит между ними, без блокировки. votesReceived передаётся указателем,
// чтобы общий счётчик голосов выборов разделялся между горутинами соседей.
func (cm *ConsensusModule) requestVoteFromPeer(
	peerID, savedCurrentTerm, voterCount int,
	isLeadershipTransfer bool,
	votesReceived *atomic.Int32,
) {
	cm.mu.Lock()
	savedLastLogIndex, savedLastLogTerm := cm.lastLogIndexAndTermLocked()
	cm.mu.Unlock()

	args := RequestVoteArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		},
		Term:               savedCurrentTerm,
		CandidateID:        cm.id,
		LastLogIndex:       savedLastLogIndex,
		LastLogTerm:        savedLastLogTerm,
		LeadershipTransfer: isLeadershipTransfer,
	}

	cm.traceLogf(_traceLevelKeyEvents, "sending RequestVote to %d: %+v", peerID, args)
	reply, err := cm.transport.RequestVote(ServerID(peerID), args)
	if err == nil {
		cm.mu.Lock()
		cm.traceLockedLogf(_traceLevelKeyEvents, "received RequestVoteReply %+v", reply)

		if cm.cmState.state != Candidate {
			cm.traceLockedLogf(_traceLevelKeyEvents, "while waiting for reply, state = %v", cm.cmState.state)
			cm.mu.Unlock()
			return
		}

		if reply.Term > cm.cmState.currentTerm {
			cm.traceLockedLogf(_traceLevelKeyEvents, "term out of date in RequestVoteReply")
			cm.becomeFollowerLocked(reply.Term)
			cm.mu.Unlock()
			return
		} else if reply.Term == cm.cmState.currentTerm {
			if reply.VoteGranted {
				votesReceived.Add(1)
				if int(votesReceived.Load()) >= quorumSize(voterCount) {
					// Выиграл выборы!
					cm.traceLockedLogf(_traceLevelProgress, "wins election with %d votes", votesReceived.Load())
					cm.startLeaderLocked()
					cm.mu.Unlock()
					slog.Info("wins election", slog.Int("votes", int(votesReceived.Load())))
					return
				}
			}
		}
		cm.mu.Unlock()
	}
}

// runElectionTimer — фоновый таймер выборов.
// Не запускается для узлов без права голоса.
// При истечении таймаута инициирует выборы или Pre‑Vote.
// Останавливается при смене терма, изменении роли, получении сигнала
// остановки таймера или завершении работы CM.
func (cm *ConsensusModule) runElectionTimer() {
	timeoutDuration := cm.electionTimeout()
	cm.mu.Lock()
	termStarted := cm.cmState.currentTerm
	electionTimerDone := cm.cmState.electionTimerDone

	// Nonvoter не участвует в выборах — не запускаем таймер.
	if !hasVote(cm.cmState.configurations.latest, cm.id) {
		cm.mu.Unlock()
		return
	}

	cm.mu.Unlock()
	cm.traceLogf(_traceLevelLoops, "election timer started (%v), term=%d", timeoutDuration, termStarted)

	// Цикл таймера работает, пока не наступит одно из условий:
	//   - Истекло время ожидания без получения сообщений от лидера — тогда
	//     узел инициирует выборы (или Pre‑Vote, если включён).
	//   - Изменился текущий терм — значит, контекст выборов устарел.
	//   - Узел вышел из состояний Follower/Candidate (например, стал лидером).
	//   - Получен сигнал остановки таймера (electionTimerDone) или общий сигнал
	//     завершения работы модуля (shutdownCh).
	ticker := time.NewTicker(TickerTimeoutMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cm.mu.Lock()
			if cm.cmState.state != Candidate && cm.cmState.state != Follower {
				cm.traceLockedLogf(_traceLevelLoops, "in election timer state=%s, bailing out", cm.cmState.state)
				cm.mu.Unlock()
				return
			}

			if termStarted != cm.cmState.currentTerm {
				cm.traceLockedLogf(
					_traceLevelLoops,
					"in election timer term changed from %d to %d, bailing out",
					termStarted, cm.cmState.currentTerm,
				)
				cm.mu.Unlock()
				return
			}

			// Начать выборы (или Pre-Vote), если в течение времени ожидания мы
			// не получили сообщение от лидера или не проголосовали за кого-либо.
			if elapsed := time.Since(cm.cmState.electionResetEvent); elapsed >= timeoutDuration {
				if cm.preVoteDisabled || cm.cmState.candidateFromLeadershipTransfer.Load() {
					cm.startElectionLocked()
					cm.mu.Unlock()
					return
				}
				cm.mu.Unlock()
				cm.goSpawn(cm.runPreCandidate)
				return
			}
			cm.mu.Unlock()

		case <-electionTimerDone:
			return
		case <-cm.shutdownCh:
			return
		}
	}
}

// runPreCandidate выполняет предварительное голосование (§4 Pre-Vote).
//
// Узел переходит в PreCandidate (не увеличивая currentTerm), отправляет
// RequestPreVote всем голосующим и собирает ответы. Если получен кворум
// положительных ответов — вызывает startElectionLocked() для настоящих выборов.
// Если кворум не получен или обнаружен более высокий term — возвращается
// в Follower.
//
// Вызов из состояния Candidate допускается: это retry-путь кандидата,
// проигравшего реальные выборы (split vote, когда два кандидата в одном
// терме голосуют каждый за себя и не набирают кворум). Таймер выборов
// (runElectionTimer) передаёт управление сюда независимо от состояния, и
// без перехода Candidate → PreCandidate такой кандидат навсегда остался бы
// в Candidate: runPreCandidate выходил бы сразу, а горутина таймера уже
// завершилась, поэтому новые выборы больше никогда не запускались бы
// (livelock без лидера).
//
// Горутина завершается при:
//   - получении кворума PreVote → вызов startElectionLocked()
//   - истечении таймаута выборов без кворума → becomeFollowerLocked()
//   - обнаружении более высокого терма от другого узла → becomeFollowerLocked()
//
// Вызывается из runElectionTimer(), когда preVoteDisabled == false.
func (cm *ConsensusModule) runPreCandidate() {
	cm.mu.Lock()
	if cm.cmState.state != Follower && cm.cmState.state != PreCandidate && cm.cmState.state != Candidate {
		cm.mu.Unlock()
		return
	}
	cm.cmState.state = PreCandidate
	cm.traceLockedLogf(
		_traceLevelKeyEvents,
		"becomes PreCandidate; term=%d, len(log)=%d",
		cm.cmState.currentTerm, len(cm.cmState.log),
	)

	cfg := cm.cmState.configurations.latest
	voters := voterIDs(cfg)
	voterCount := len(voters)

	// Одиночный узел: PreVote не нужен, сразу выборы.
	if voterCount <= 1 {
		cm.startElectionLocked()
		cm.mu.Unlock()
		return
	}

	proposedTerm := cm.cmState.currentTerm + 1
	cm.mu.Unlock()

	// Канал для сбора ответов.
	preVoteRespCh := make(chan *RequestPreVoteReply, voterCount-1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Отправить RequestPreVote параллельно всем голосующим (кроме себя).
	for _, s := range cfg.ConfigServers {
		peerID := int(s.ID)
		if peerID == cm.id {
			continue
		}
		if s.Suffrage != Voter {
			continue
		}
		cm.goSpawn(func() {
			cm.sendPreVoteToPeer(ctx, peerID, proposedTerm, preVoteRespCh)
		})
	}

	cm.collectPreVoteReplies(voterCount, preVoteRespCh)
}

// sendPreVoteToPeer отправляет RequestPreVote одному соседу и помещает его
// ответ в канал ответов. В запросе идёт предлагаемый терм выборов;
// собственный терм узла при этом не меняется. Если транспорт отсутствует или
// вызов завершился ошибкой, в канал помещается синтетический ответ: отказ, а
// при отсутствии поддержки предварительного голосования у транспорта — грант,
// ради совместимости с соседями. Каждая отправка уступает отмене контекста,
// поэтому горутина не зависает, когда сбор ответов уже завершён.
//
// Самостоятельно захватывает и освобождает cm.mu: под блокировкой снимаются
// только последний индекс и терм журнала вместе с текущим термом; вызов
// транспорта происходит без блокировки. Канал в сигнатуре — только для
// отправки.
func (cm *ConsensusModule) sendPreVoteToPeer(
	ctx context.Context,
	peerID, proposedTerm int,
	respCh chan<- *RequestPreVoteReply,
) {
	cm.mu.Lock()
	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTermLocked()
	savedTerm := cm.cmState.currentTerm
	cm.mu.Unlock()

	if cm.transport == nil {
		cm.traceLogf(_traceLevelKeyEvents, "runPreCandidate: transport is nil, cannot send to %d", peerID)
		select {
		case respCh <- &RequestPreVoteReply{
			RPCHeader:   RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
			Term:        savedTerm,
			VoteGranted: false,
		}:
		case <-ctx.Done():
		}
		return
	}

	args := RequestPreVoteArgs{
		RPCHeader: RPCHeader{
			ProtocolVersion: ProtocolVersion,
			ServerID:        cm.id,
		},
		Term:         proposedTerm,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	cm.traceLogf(_traceLevelPreVote, "sending RequestPreVote to %d: %+v", peerID, args)
	reply, err := cm.transport.RequestPreVote(ServerID(peerID), args)
	if err != nil {
		cm.traceLogf(_traceLevelPreVote, "RequestPreVote to %d failed: %v", peerID, err)
		var resp *RequestPreVoteReply
		if err == contract.ErrNotImplemented {
			// Если транспорт не поддерживает PreVote, считаем голос
			// предоставленным.
			resp = &RequestPreVoteReply{
				RPCHeader:   RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
				Term:        savedTerm,
				VoteGranted: true,
			}
		} else {
			resp = &RequestPreVoteReply{
				RPCHeader:   RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
				Term:        savedTerm,
				VoteGranted: false,
			}
		}
		select {
		case respCh <- resp:
		case <-ctx.Done():
		}
		return
	}
	select {
	case respCh <- &reply:
	case <-ctx.Done():
	}
}

// collectPreVoteReplies собирает ответы соседей на предварительное голосование
// и решает его исход. Голос за себя учтён заранее, каждый пришедший ответ
// засчитывается до проверок роли и терма. Ответ с более высоким термом, как и
// потеря роли, прекращает сбор. При наборе кворума узел выжидает случайную
// паузу — чтобы несколько узлов не начали выборы лидера одновременно с
// одинаковым термом — и, если он всё ещё кандидат предварительного
// голосования, запускает выборы лидера. Истечение тайм-аута выборов и
// исчерпание ответов без кворума возвращают узел в ведомые; остановка узла
// прекращает сбор, не трогая состояния.
//
// Самостоятельно захватывает и освобождает cm.mu. Канал в сигнатуре — только
// для приёма.
func (cm *ConsensusModule) collectPreVoteReplies(
	voterCount int,
	respCh <-chan *RequestPreVoteReply,
) {
	grantedVotes := 1 // голос за себя
	neededVotes := quorumSize(voterCount)
	votersResponded := 0
	totalVoters := voterCount - 1

	timeout := time.After(cm.electionTimeout())

	// Собирать ответы от соседей.
	for votersResponded < totalVoters {
		select {
		case reply := <-respCh:
			votersResponded++
			cm.mu.Lock()
			if cm.cmState.state != PreCandidate {
				cm.traceLockedLogf(_traceLevelPreVote, "runPreCandidate: state changed to %s, bailing out", cm.cmState.state)
				cm.mu.Unlock()
				return
			}
			if reply.Term > cm.cmState.currentTerm {
				cm.traceLockedLogf(_traceLevelPreVote, "runPreCandidate: found higher term %d", reply.Term)
				cm.becomeFollowerLocked(reply.Term)
				cm.mu.Unlock()
				return
			}
			cm.mu.Unlock()
			if reply.VoteGranted {
				grantedVotes++
				cm.traceLogf(
					_traceLevelPreVote, "runPreCandidate: granted vote from peer, total=%d, needed=%d",
					grantedVotes, neededVotes,
				)
				if grantedVotes >= neededVotes {
					cm.startElectionAfterPreVote(grantedVotes)
					return
				}
			}
		case <-timeout:
			cm.mu.Lock()
			if cm.cmState.state == PreCandidate {
				cm.traceLockedLogf(_traceLevelPreVote, "runPreCandidate: pre-vote timeout, returning to follower")
				cm.becomeFollowerLocked(cm.cmState.currentTerm)
			}
			cm.mu.Unlock()
			return
		case <-cm.shutdownCh:
			return
		}

		// После обработки всех ответов пересчитать необходимые голоса.
		if votersResponded >= totalVoters && grantedVotes < neededVotes {
			cm.mu.Lock()
			if cm.cmState.state == PreCandidate {
				cm.traceLockedLogf(
					_traceLevelPreVote, "runPreCandidate: pre-vote lost (%d/%d), returning to follower",
					grantedVotes, neededVotes,
				)
				cm.becomeFollowerLocked(cm.cmState.currentTerm)
			}
			cm.mu.Unlock()
			return
		}
	}
}

// startElectionAfterPreVote запускает выборы лидера после победы в
// предварительном голосовании. Перед переходом узел выжидает случайную паузу,
// чтобы несколько узлов не начали выборы одновременно с одинаковым термом, и
// начинает выборы, только если за время паузы он остался кандидатом
// предварительного голосования.
//
// Самостоятельно захватывает и освобождает cm.mu: пауза выдерживается без
// блокировки, под блокировкой выполняются перепроверка роли и запуск выборов.
func (cm *ConsensusModule) startElectionAfterPreVote(grantedVotes int) {
	// Jitter перед выборами, чтобы одновременно несколько узлов
	// не перешли в Candidate с одинаковым term (split vote).
	time.Sleep(time.Duration(rand.Intn(_preVoteJitterMs)) * time.Millisecond)
	cm.mu.Lock()
	if cm.cmState.state == PreCandidate {
		cm.traceLockedLogf(
			_traceLevelPreVote,
			"runPreCandidate: won pre-vote with %d votes, starting election",
			grantedVotes,
		)
		cm.startElectionLocked()
	}
	cm.mu.Unlock()
}

// electionTimeout генерирует псевдослучайную длительность тайм-аута выборов.
func (cm *ConsensusModule) electionTimeout() time.Duration {
	// Если установлен параметр RAFT_FORCE_MORE_REELECTION, проведите стресс-тест, намеренно
	// генерируя жестко заданное число очень часто. Это вызовет коллизии
	// между различными серверами и приведет к увеличению количества перевыборов.
	if os.Getenv("RAFT_FORCE_MORE_REELECTION") != "" && rand.Intn(3) == 0 {
		return time.Duration(ReelectionTimeoutMs) * time.Millisecond
	}
	return time.Duration(ReelectionTimeoutMs+rand.Intn(ReelectionTimeoutMs)) * time.Millisecond
}

// timeoutNow обрабатывает входящий TimeoutNowRequest.
// Устанавливает candidateFromLeadershipTransfer и форсирует немедленные
// выборы (без PreVote, без ожидания election timeout).
func (cm *ConsensusModule) timeoutNow(rpc RPC, req *TimeoutNowRequest) {
	startTimeNow := time.Now()
	defer func() { cm.latency.timeoutNowRequest.observe(time.Since(startTimeNow)) }()
	cm.traceLogf(_traceLevelKeyEvents, "received TimeoutNow from %d", req.ServerID)

	// Уже лидер — no-op.
	cm.mu.Lock()
	if cm.cmState.state == Leader {
		term := cm.cmState.currentTerm
		cm.mu.Unlock()
		rpc.RespChan <- RPCResponse{
			Reply: &TimeoutNowResponse{
				RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
				Success:   false,
				Term:      term,
			},
		}
		return
	}
	// Если не лидер, проверяем только состояние Follower (Candidate/PreCandidate
	// не могут получить TimeoutNow).
	if cm.cmState.state != Follower {
		cm.mu.Unlock()
		rpc.RespChan <- RPCResponse{
			Reply: &TimeoutNowResponse{
				RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
				Success:   false,
				Term:      cm.cmState.currentTerm,
			},
		}
		return
	}

	// Форсированная остановка текущего election timer.
	select {
	case <-cm.cmState.electionTimerDone:
	default:
		close(cm.cmState.electionTimerDone)
	}
	cm.cmState.electionTimerDone = make(chan struct{})
	cm.cmState.candidateFromLeadershipTransfer.Store(true)
	cm.traceLockedLogf(_traceLevelLoops, "candidateFromLeadershipTransfer set to true (cm.id=%d)", cm.id)

	// Запускаем форсированные выборы немедленно, без ожидания election timeout.
	// cm.mu удерживается, что гарантирует атомарность.
	cm.startElectionLocked()
	newTerm := cm.cmState.currentTerm
	cm.traceLockedLogf(
		_traceLevelLoops,
		"started immediate election after TimeoutNow, term=%d (cm.id=%d)",
		newTerm, cm.id,
	)
	cm.mu.Unlock()

	rpc.RespChan <- RPCResponse{
		Reply: &TimeoutNowResponse{
			RPCHeader: RPCHeader{ProtocolVersion: ProtocolVersion, ServerID: cm.id},
			Success:   true,
			Term:      newTerm,
		},
	}
}
