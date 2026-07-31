// Package raft Реализация ядра Raft — модуль консенсуса.
// This code is in the public domain.
package raft

import (
	"bytes"
	"container/list"
	"encoding/gob"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DebugCM = 1
	Quantum = 2

	HeartbeatTimeoutMs  = 50 * Quantum
	ReelectionTimeoutMs = 150 * Quantum
	TickerTimeoutMs     = 10 * Quantum
)

// CommitEntry — это данные, которые Raft отправляет в канал фиксации.
// Каждая запись фиксации уведомляет клиента о том, что консенсус по команде
// был достигнут и эта команда может быть применена к машине состояний клиента.
type CommitEntry struct {
	// Command — это команда клиента, которая была зафиксирована.
	Command any

	// Index — это индекс журнала, по которому была зафиксирована команда клиента.
	Index int

	// Term — это терм Raft, в котором была зафиксирована команда клиента.
	Term int
}

type CMState int

const (
	Follower CMState = iota
	Candidate
	Leader
	Dead
)

func (s CMState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	case Dead:
		return "Dead"
	default:
		return "unreachable"
	}
}

// LogType определяет тип записи журнала.
type LogType int

const (
	LogCommand LogType = iota
	LogNoop
	LogConfiguration
)

// maxApplyBatchSize — максимальное количество команд, собираемых в один
// пакет при групповой фиксации (group commit). Значение 64 выбрано
// как разумный компромисс между задержкой (latency) и пропускной
// способностью (throughput): при интенсивной нагрузке лидер успевает
// собрать до 64 команд перед отправкой одного AppendEntries RPC,
// что снижает количество RPC-вызовов без значительного увеличения
// времени ожидания.
const maxApplyBatchSize = 64

type LogEntry struct {
	Index   int
	Term    int
	Type    LogType
	Command any
}

// commitTuple связывает запись журнала с future для отправки в FSM.
type commitTuple struct {
	log    *LogEntry
	future *logFuture
}

// ConsensusModule (CM) реализует единый узел консенсуса Raft.
type ConsensusModule struct {
	mu sync.Mutex

	id      int
	peerIds []int
	server  *Server
	storage Storage

	fsm         FSM
	fsmMutateCh chan []*commitTuple
	shutdownCh  chan struct{}

	applyCh  chan *logFuture
	inflight *list.List

	triggerAEChan chan struct{}

	currentTerm int
	votedFor    int
	log         []LogEntry

	commitIndex        int
	lastApplied        int
	state              CMState
	electionResetEvent time.Time
	electionTimerDone  chan struct{}

	nextIndex  map[int]int
	matchIndex map[int]int
}

// NewConsensusModule создаёт новый экземпляр CM.
// fsm — машина состояний клиента, к которой применяются закоммиченные записи.
// ready уведомляет CM, что все соседи подключены.
func NewConsensusModule(
	id int,
	peerIds []int,
	server *Server,
	storage Storage,
	fsm FSM,
	ready <-chan any,
) *ConsensusModule {
	cm := new(ConsensusModule)
	cm.id = id
	cm.peerIds = peerIds
	cm.server = server
	cm.storage = storage
	cm.fsm = fsm
	cm.fsmMutateCh = make(chan []*commitTuple, 16)
	cm.shutdownCh = make(chan struct{})
	cm.applyCh = make(chan *logFuture)
	cm.inflight = list.New()
	cm.triggerAEChan = make(chan struct{}, 1)
	cm.state = Follower
	cm.votedFor = -1
	cm.commitIndex = -1
	cm.lastApplied = -1
	cm.nextIndex = make(map[int]int)
	cm.matchIndex = make(map[int]int)
	cm.electionTimerDone = make(chan struct{})

	if cm.storage.HasData() {
		cm.restoreFromStorage()
	}

	go cm.runFSM()

	go func() {
		<-ready
		cm.mu.Lock()
		cm.electionResetEvent = time.Now()
		cm.mu.Unlock()
		cm.runElectionTimer()
	}()
	return cm
}

// Report отчет о состоянии данного CM.
func (cm *ConsensusModule) Report() (id, term int, isLeader bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.id, cm.currentTerm, cm.state == Leader
}

// Stop останавливает этот CM.
func (cm *ConsensusModule) Stop() {
	cm.dLogf("CM.Stop called")
	cm.mu.Lock()
	cm.state = Dead
	cm.mu.Unlock()
	cm.dLogf("becomes Dead")

	close(cm.shutdownCh)
}

// restoreFromStorage восстанавливает постоянное состояние данного CM
// из хранилища. Должен вызываться в конструкторе до запуска какой-либо
// конкурентной работы.
func (cm *ConsensusModule) restoreFromStorage() {
	if termData, found := cm.storage.Get("currentTerm"); found {
		d := gob.NewDecoder(bytes.NewBuffer(termData))
		if err := d.Decode(&cm.currentTerm); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("currentTerm not found in storage")
	}
	if votedData, found := cm.storage.Get("votedFor"); found {
		d := gob.NewDecoder(bytes.NewBuffer(votedData))
		if err := d.Decode(&cm.votedFor); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("votedFor not found in storage")
	}
	if logData, found := cm.storage.Get("log"); found {
		d := gob.NewDecoder(bytes.NewBuffer(logData))
		if err := d.Decode(&cm.log); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("log not found in storage")
	}
}

// dLogf выводит отладочное сообщение, если DebugCM > 0.
func (cm *ConsensusModule) dLogf(format string, args ...any) {
	if DebugCM > 0 {
		format = fmt.Sprintf("[%d] ", cm.id) + format
		log.Printf(format, args...)
	}
}

// RequestVoteArgs См. рисунок 2 в статье.
type RequestVoteArgs struct {
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

// RequestVote RPC.
func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.state == Dead {
		return nil
	}
	lastLogIndex, lastLogTerm := cm.lastLogIndexAndTerm()
	cm.dLogf(
		"RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]",
		args, cm.currentTerm, cm.votedFor, lastLogIndex, lastLogTerm,
	)

	if args.Term > cm.currentTerm {
		cm.dLogf("... term out of date in RequestVote")
		cm.becomeFollower(args.Term)
	}

	if cm.currentTerm == args.Term &&
		(cm.votedFor == -1 || cm.votedFor == args.CandidateID) &&
		(args.LastLogTerm > lastLogTerm ||
			(args.LastLogTerm == lastLogTerm && args.LastLogIndex >= lastLogIndex)) {
		reply.VoteGranted = true
		cm.votedFor = args.CandidateID
		cm.electionResetEvent = time.Now()
	} else {
		reply.VoteGranted = false
	}
	reply.Term = cm.currentTerm
	cm.persistToStorage()
	cm.dLogf("... RequestVote reply: %+v", reply)
	return nil
}

// AppendEntriesArgs См. рисунок 2 в статье.
type AppendEntriesArgs struct {
	Term     int
	LeaderID int

	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool

	// Faster conflict resolution optimization (described near the end of section
	// 5.3 in the paper.)
	ConflictIndex int
	ConflictTerm  int
}

func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.state == Dead {
		return nil
	}
	cm.dLogf("AppendEntries: %+v", args)

	if args.Term > cm.currentTerm {
		cm.dLogf("... term out of date in AppendEntries")
		cm.becomeFollower(args.Term)
	}

	reply.Success = false
	if args.Term == cm.currentTerm {
		if cm.state != Follower {
			cm.becomeFollower(args.Term)
		}
		cm.electionResetEvent = time.Now()

		// Проверяет, содержит ли наш журнал запись с индексом PrevLogIndex,
		// у которой терм совпадает с PrevLogTerm.
		// Обратите внимание, что в особом случае, когда PrevLogIndex == -1,
		// условие считается истинным автоматически.
		if args.PrevLogIndex == -1 ||
			(args.PrevLogIndex < len(cm.log) && args.PrevLogTerm == cm.log[args.PrevLogIndex].Term) {
			reply.Success = true

			// Находит точку вставки — место, где происходит несовпадение термов
			// между существующими записями журнала, начиная с PrevLogIndex+1,t
			// и новыми записями, отправленными лидером через RPC.
			logInsertIndex := args.PrevLogIndex + 1
			newEntriesIndex := 0

			for logInsertIndex < len(cm.log) && newEntriesIndex < len(args.Entries) {
				if cm.log[logInsertIndex].Term != args.Entries[newEntriesIndex].Term {
					break
				}
				logInsertIndex++
				newEntriesIndex++
			}
			// После завершения этого цикла:
			// - logInsertIndex указывает на конец журнала
			//   или на индекс, где терм записи отличается от записи лидера.
			// - newEntriesIndex указывает на конец массива Entries
			//   или на индекс, где терм записи отличается от соответствующей записи журнала.
			if newEntriesIndex < len(args.Entries) {
				cm.dLogf("... inserting entries %v from index %d", args.Entries[newEntriesIndex:], logInsertIndex)
				cm.log = append(cm.log[:logInsertIndex], args.Entries[newEntriesIndex:]...)
				cm.dLogf("... log is now: %v", cm.log)
			}

			if args.LeaderCommit > cm.commitIndex {
				cm.commitIndex = min(args.LeaderCommit, len(cm.log)-1)
				cm.dLogf("... setting commitIndex=%d", cm.commitIndex)
				cm.persistToStorage()
				commitIdx := cm.commitIndex
				cm.mu.Unlock()
				cm.processLogs(commitIdx)
				cm.mu.Lock()
			}
		} else {
			// Не найдено совпадение для PrevLogIndex/PrevLogTerm.
			// Заполнить ConflictIndex и ConflictTerm, чтобы помочь лидеру
			// быстрее привести наш журнал в актуальное состояние.
			if args.PrevLogIndex >= len(cm.log) {
				reply.ConflictIndex = len(cm.log)
				reply.ConflictTerm = -1
			} else {
				// PrevLogIndex указывает на существующую запись в нашем журнале,
				// но PrevLogTerm не совпадает с термом записи
				// cm.log[PrevLogIndex].
				reply.ConflictTerm = cm.log[args.PrevLogIndex].Term

				var i int
				for i = args.PrevLogIndex - 1; i >= 0; i-- {
					if cm.log[i].Term != reply.ConflictTerm {
						break
					}
				}
				reply.ConflictIndex = i + 1
			}
		}
	}

	reply.Term = cm.currentTerm
	cm.persistToStorage()
	cm.dLogf("AppendEntries reply: %+v", *reply)
	return nil
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

// runElectionTimer реализует таймер выборов. Она должна запускаться всякий раз, когда
// мы хотим запустить таймер для выдвижения в качестве кандидата на новых выборах.
//
// Эта функция является блокирующей и должна запускаться в отдельной горутине;
// она предназначена для работы с одним (одноразовым) таймером выборов, поскольку она завершается
// всякий раз, когда состояние CM меняется с «ведомый/кандидат» или меняется срок полномочий.
func (cm *ConsensusModule) runElectionTimer() {
	timeoutDuration := cm.electionTimeout()
	cm.mu.Lock()
	termStarted := cm.currentTerm
	electionTimerDone := cm.electionTimerDone
	cm.mu.Unlock()
	cm.dLogf("election timer started (%v), term=%d", timeoutDuration, termStarted)

	// Этот цикл выполняется до тех пор, пока не произойдёт одно из двух:
	// - мы не обнаружим, что таймер выборов больше не нужен, или
	// - таймер выборов не истечёт и данный CM не станет кандидатом.
	// Для ведомого узла этот цикл обычно продолжает работать в фоновом режиме
	// в течение всего времени жизни CM.
	ticker := time.NewTicker(TickerTimeoutMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cm.mu.Lock()
			if cm.state != Candidate && cm.state != Follower {
				cm.dLogf("in election timer state=%s, bailing out", cm.state)
				cm.mu.Unlock()
				return
			}

			if termStarted != cm.currentTerm {
				cm.dLogf("in election timer term changed from %d to %d, bailing out", termStarted, cm.currentTerm)
				cm.mu.Unlock()
				return
			}

			// Начать выборы, если в течение времени ожидания мы не получили
			// сообщение от лидера или не проголосовали за кого-либо.
			if elapsed := time.Since(cm.electionResetEvent); elapsed >= timeoutDuration {
				cm.startElection()
				cm.mu.Unlock()
				return
			}
			cm.mu.Unlock()

		case <-electionTimerDone:
			return
		}
	}
}

// startElection запускает новые выборы с этим CM в качестве кандидата.
// Ожидается, что cm.mu будет заблокирован.
func (cm *ConsensusModule) startElection() {
	cm.state = Candidate
	cm.currentTerm += 1
	savedCurrentTerm := cm.currentTerm
	cm.electionResetEvent = time.Now()
	cm.votedFor = cm.id
	cm.persistToStorage()
	cm.dLogf("becomes Candidate (currentTerm=%d); log=%v", savedCurrentTerm, cm.log)

	// Одиночный узел: побеждает на выборах немедленно.
	if len(cm.peerIds) == 0 {
		cm.startLeader()
		return
	}

	var votesReceived atomic.Int32
	votesReceived.Store(1)

	// Параллельно отправить RPC-запросы RequestVote всем остальным серверам.
	for _, peerID := range cm.peerIds {
		go func() {
			cm.mu.Lock()
			savedLastLogIndex, savedLastLogTerm := cm.lastLogIndexAndTerm()
			cm.mu.Unlock()

			args := RequestVoteArgs{
				Term:         savedCurrentTerm,
				CandidateID:  cm.id,
				LastLogIndex: savedLastLogIndex,
				LastLogTerm:  savedLastLogTerm,
			}

			cm.dLogf("sending RequestVote to %d: %+v", peerID, args)
			var reply RequestVoteReply
			if err := cm.server.Call(peerID, "ConsensusModule.RequestVote", args, &reply); err == nil {
				cm.mu.Lock()
				defer cm.mu.Unlock()
				cm.dLogf("received RequestVoteReply %+v", reply)

				if cm.state != Candidate {
					cm.dLogf("while waiting for reply, state = %v", cm.state)
					return
				}

				if reply.Term > cm.currentTerm {
					cm.dLogf("term out of date in RequestVoteReply")
					cm.becomeFollower(reply.Term)
					return
				} else if reply.Term == cm.currentTerm {
					if reply.VoteGranted {
						votesReceived.Add(1)
						if int(votesReceived.Load())*2 > len(cm.peerIds)+1 {
							// Выиграл выборы!
							cm.dLogf("wins election with %d votes", votesReceived.Load())
							cm.startLeader()
							slog.Info("wins election", slog.Int("votes", int(votesReceived.Load())))
							return
						}
					}
				}
			}
		}()
	}

	// Запустить новый таймер выборов на случай, если текущие выборы не завершатся успешно.
	go cm.runElectionTimer()
}

// becomeFollower делает cm последователем и сбрасывает его состояние.
// Ожидается, что cm.mu будет заблокирован.
func (cm *ConsensusModule) becomeFollower(term int) {
	cm.dLogf("becomes Follower with term=%d; log=%v", term, cm.log)
	cm.state = Follower
	if term > cm.currentTerm {
		cm.currentTerm = term
		cm.votedFor = -1
		cm.persistToStorage()
	}
	cm.electionResetEvent = time.Now()

	select {
	case <-cm.electionTimerDone:
	default:
		close(cm.electionTimerDone)
	}
	cm.electionTimerDone = make(chan struct{})
	go cm.runElectionTimer()
}

// startLeader переводит cm в состояние лидера и начинает процесс пульсации.
// Ожидается, что cm.mu будет заблокирован.
func (cm *ConsensusModule) startLeader() {
	cm.state = Leader

	for _, peerID := range cm.peerIds {
		cm.nextIndex[peerID] = len(cm.log)
		cm.matchIndex[peerID] = -1
	}
	cm.dLogf(
		"becomes Leader; term=%d, nextIndex=%v, matchIndex=%v; log=%v",
		cm.currentTerm, cm.nextIndex, cm.matchIndex, cm.log,
	)

	// Noop entry при утверждении лидерства (§5.4.2).
	// Лидер должен закоммитить noop запись своего терма, чтобы
	// зафиксировать любые незакоммиченные записи из предыдущих термов.
	noop := &logFuture{
		log: LogEntry{
			Type: LogNoop,
		},
	}
	noop.init(cm.shutdownCh)
	cm.dispatchLogsUnsafe([]*logFuture{noop})

	go cm.runLeaderLoop()

	// Эта горутина выполняется в фоновом режиме и отправляет сообщения
	// AppendEntries соседям:
	// * каждый раз, когда в triggerAEChan поступает уведомление;
	// * либо каждые HeartbeatTimeoutMs мс, если в triggerAEChan не происходит событий.
	go func(heartbeatTimeout time.Duration) {
		// Немедленно отправить сообщения AppendEntries всем соседям.
		cm.leaderSendAEs()

		t := time.NewTimer(heartbeatTimeout)
		defer t.Stop()
		for {
			doSend := false //nolint
			select {
			case <-t.C:
				doSend = true

				// Перезапустить таймер, чтобы он снова сработал через
				// heartbeatTimeout.
				t.Stop()
				t.Reset(heartbeatTimeout)
			case _, ok := <-cm.triggerAEChan:
				if ok {
					doSend = true
				} else {
					return
				}

				// Перезапустить таймер heartbeatTimeout.
				if !t.Stop() {
					<-t.C
				}
				t.Reset(heartbeatTimeout)
			}

			if doSend {
				// Если этот узел больше не является лидером,
				// остановить цикл отправки сообщений.
				cm.mu.Lock()
				if cm.state != Leader {
					cm.mu.Unlock()
					return
				}
				cm.mu.Unlock()
				cm.leaderSendAEs()
			}
		}
	}(HeartbeatTimeoutMs * time.Millisecond)
}

func (cm *ConsensusModule) nextIndexArgsEntries(peerID, savedCurrentTerm int) (int, AppendEntriesArgs, []LogEntry) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	ni := cm.nextIndex[peerID]
	prevLogIndex := ni - 1
	prevLogTerm := -1
	if 0 <= prevLogIndex && prevLogIndex < len(cm.log) {
		prevLogTerm = cm.log[prevLogIndex].Term
	}
	var entries []LogEntry
	if ni < len(cm.log) {
		entries = append([]LogEntry{}, cm.log[ni:]...)
	} else {
		entries = nil
	}
	return ni, AppendEntriesArgs{
		Term:         savedCurrentTerm,
		LeaderID:     cm.id,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: cm.commitIndex,
	}, entries
}

// leaderSendAEsToPeer отправляет очередной раунд сообщений AppendEntries соседу,
// обрабатывает их ответы и обновляет состояние CM.
//
// peerID - сосед
// savedCurrentTerm - терм.
func (cm *ConsensusModule) leaderSendAEsToPeer(peerID, savedCurrentTerm int) {
	ni, args, entries := cm.nextIndexArgsEntries(peerID, savedCurrentTerm)
	cm.dLogf("sending AppendEntries to %v: ni=%d, args=%+v", peerID, ni, args)
	var reply AppendEntriesReply
	if err := cm.server.Call(peerID, "ConsensusModule.AppendEntries", args, &reply); err == nil {
		cm.mu.Lock()
		// К сожалению, здесь нельзя просто использовать
		// defer cm.mu.Unlock(), поскольку в одной из ветвей
		// выполнения требуется отправка данных в каналы.
		// Поэтому необходимо явно вызывать cm.mu.Unlock()
		// во всех путях выхода, начиная с этого места.
		if reply.Term > cm.currentTerm {
			cm.dLogf("term out of date in heartbeat reply")
			cm.becomeFollower(reply.Term)
			cm.mu.Unlock()
			return
		}
		if cm.nextIndex[peerID] != ni {
			cm.dLogf("#44 ni out of date in heartbeat reply")
			cm.mu.Unlock()
			return
		}

		if cm.state == Leader && savedCurrentTerm == reply.Term {
			if reply.Success {
				cm.nextIndex[peerID] = ni + len(entries)
				cm.matchIndex[peerID] = cm.nextIndex[peerID] - 1

				savedCommitIndex := cm.commitIndex
				for i := cm.commitIndex + 1; i < len(cm.log); i++ {
					if cm.log[i].Term == cm.currentTerm {
						matchCount := 1
						for _, peerID := range cm.peerIds {
							if cm.matchIndex[peerID] >= i {
								matchCount++
							}
						}
						if matchCount*2 > len(cm.peerIds)+1 {
							cm.commitIndex = i
						}
					}
				}
				cm.dLogf(
					"AppendEntries reply from %d success: nextIndex := %v, matchIndex := %v; commitIndex := %d",
					peerID, cm.nextIndex, cm.matchIndex, cm.commitIndex,
				)
				if cm.commitIndex != savedCommitIndex {
					cm.dLogf("leader sets commitIndex := %d", cm.commitIndex)
					commitIdx := cm.commitIndex
					cm.persistToStorage()
					cm.mu.Unlock()
					cm.processLogs(commitIdx)
					select {
					case cm.triggerAEChan <- struct{}{}:
					default:
					}
				} else {
					cm.mu.Unlock()
				}
			} else {
				if reply.ConflictTerm >= 0 {
					lastIndexOfTerm := -1
					for i, v := range slices.Backward(cm.log) {
						if v.Term == reply.ConflictTerm {
							lastIndexOfTerm = i
							break
						}
					}
					if lastIndexOfTerm >= 0 {
						cm.nextIndex[peerID] = lastIndexOfTerm + 1
					} else {
						cm.nextIndex[peerID] = reply.ConflictIndex
					}
				} else {
					cm.nextIndex[peerID] = reply.ConflictIndex
				}
				cm.dLogf("AppendEntries reply from %d !success: nextIndex := %d", peerID, ni-1)
				cm.mu.Unlock()
			}
		} else {
			cm.mu.Unlock()
		}
	}
}

// leaderSendAEs отправляет очередной раунд сообщений AppendEntries всем
// соседям, обрабатывает их ответы и обновляет состояние CM.
func (cm *ConsensusModule) leaderSendAEs() {
	cm.mu.Lock()
	if cm.state != Leader {
		cm.mu.Unlock()
		return
	}
	savedCurrentTerm := cm.currentTerm
	cm.mu.Unlock()

	for _, peerID := range cm.peerIds {
		go cm.leaderSendAEsToPeer(peerID, savedCurrentTerm)
	}
}

// lastLogIndexAndTerm возвращает индекс и терм последней записи журнала.
func (cm *ConsensusModule) lastLogIndexAndTerm() (int, int) {
	if len(cm.log) > 0 {
		lastIndex := len(cm.log) - 1
		return lastIndex, cm.log[lastIndex].Term
	}
	return -1, -1
}

// Apply отправляет команду в Raft и возвращает ApplyFuture.
// Команда будет применена к FSM после коммита.
func (cm *ConsensusModule) Apply(command any, timeout time.Duration) ApplyFuture {
	select {
	case <-cm.shutdownCh:
		return errorFuture{ErrRaftShutdown}
	default:
	}

	future := &logFuture{
		log: LogEntry{
			Type:    LogCommand,
			Command: command,
		},
	}
	future.init(cm.shutdownCh)

	cm.mu.Lock()
	if cm.state != Leader {
		cm.mu.Unlock()
		return errorFuture{ErrNotLeader}
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

// Submit отправляет команду напрямую в журнал (без future).
// Возвращает индекс записи или -1, если не лидер.
func (cm *ConsensusModule) Submit(command any) int {
	cm.mu.Lock()
	if cm.state != Leader {
		cm.mu.Unlock()
		return -1
	}
	cm.dLogf("Submit received by %v: %v", cm.state, command)
	submitIndex := len(cm.log)
	cm.log = append(cm.log, LogEntry{
		Index:   submitIndex,
		Term:    cm.currentTerm,
		Type:    LogCommand,
		Command: command,
	})
	cm.persistToStorage()
	cm.dLogf("... log=%v", cm.log)
	cm.mu.Unlock()
	select {
	case cm.triggerAEChan <- struct{}{}:
	default:
	}
	return submitIndex
}

// runFSM — отдельная горутина, применяющая закоммиченные записи к FSM.
// Получает батчи через fsmMutateCh и вызывает fsm.Apply для каждой записи
// типа LogCommand. После применения отвечает в соответствующий future,
// если он существует. Завершает работу при закрытии shutdownCh.
func (cm *ConsensusModule) runFSM() {
	for {
		select {
		case batch := <-cm.fsmMutateCh:
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
		case <-cm.shutdownCh:
			return
		}
	}
}

// processLogs собирает закоммиченные записи от lastApplied+1 до commitIndex
// и отправляет их в FSM goroutine. Метод не требует удержания cm.mu при вызове,
// но сам захватывает её внутри для чтения журнала и поиска соответствующих
// future в списке inflight. После формирования батча отпускает блокировку
// и отправляет данные в fsmMutateCh.
func (cm *ConsensusModule) processLogs(commitIndex int) {
	cm.mu.Lock()
	if commitIndex <= cm.lastApplied {
		cm.mu.Unlock()
		return
	}
	var batch []*commitTuple
	for idx := cm.lastApplied + 1; idx <= commitIndex; idx++ {
		var future *logFuture
		for e := cm.inflight.Front(); e != nil; e = e.Next() {
			if lf := e.Value.(*logFuture); lf.log.Index == idx {
				future = lf
				cm.inflight.Remove(e)
				break
			}
		}
		if idx >= len(cm.log) {
			continue
		}
		log := &cm.log[idx]
		batch = append(batch, &commitTuple{log: log, future: future})
	}
	cm.lastApplied = commitIndex
	cm.persistToStorage()
	cm.mu.Unlock()
	cm.fsmMutateCh <- batch
}

// dispatchLogs присваивает индексы и термы записям, добавляет их в журнал
// лидера (cm.log), помещает future в очередь inflight и инициирует
// репликацию через triggerAEChan.
//
// Вызывается только из runLeaderLoop после проверки, что сервер всё ещё
// является лидером. Предполагается, что будущие ответы будут разрешены
// после коммита в processLogs, либо при потере лидерства в runLeaderLoop.
func (cm *ConsensusModule) dispatchLogs(applyLogs []*logFuture) {
	cm.mu.Lock()
	cm.dispatchLogsUnsafe(applyLogs)

	// Попытка сразу продвинуть commitIndex (необходимо для одиночного узла
	// и ускоряет коммит в многопоточном кластере после dispatchLogs).
	savedCommitIndex := cm.commitIndex
	for i := cm.commitIndex + 1; i < len(cm.log); i++ {
		if cm.log[i].Term == cm.currentTerm {
			matchCount := 1
			for _, peerID := range cm.peerIds {
				if cm.matchIndex[peerID] >= i {
					matchCount++
				}
			}
			if matchCount*2 > len(cm.peerIds)+1 {
				cm.commitIndex = i
			}
		}
	}
	cm.mu.Unlock()

	if cm.commitIndex != savedCommitIndex {
		cm.dLogf("leader sets commitIndex := %d", cm.commitIndex)
		cm.processLogs(cm.commitIndex)
	}
}

// dispatchLogsUnsafe — внутренняя версия dispatchLogs без захвата блокировки.
// Вызывающий должен удерживать cm.mu.
func (cm *ConsensusModule) dispatchLogsUnsafe(applyLogs []*logFuture) {
	term := cm.currentTerm
	lastIndex := len(cm.log) - 1
	now := time.Now()
	for _, f := range applyLogs {
		f.dispatch = now
		lastIndex++
		f.log.Index = lastIndex
		f.log.Term = term
		cm.log = append(cm.log, f.log)
	}
	cm.persistToStorage()
	cm.matchIndex[cm.id] = lastIndex

	for _, f := range applyLogs {
		cm.inflight.PushBack(f)
	}

	select {
	case cm.triggerAEChan <- struct{}{}:
	default:
	}
}

// runLeaderLoop — центральный цикл лидера. Запускается при переходе в
// состояние Leader и завершается при потере лидерства или остановке CM.
//
// Цикл получает future из applyCh, собирает их в пакеты (group commit)
// и передаёт в dispatchLogs. После успешной репликации и коммита
// processLogs разрешает соответствующие future.
//
// При потере лидерства (выход из цикла) все ожидающие future из списка
// inflight получают ошибку ErrLeadershipLost, после чего список
// очищается.
func (cm *ConsensusModule) runLeaderLoop() {
	defer func() {
		cm.mu.Lock()
		for e := cm.inflight.Front(); e != nil; e = e.Next() {
			e.Value.(*logFuture).respond(ErrLeadershipLost)
		}
		cm.inflight = list.New()
		cm.mu.Unlock()
	}()

	checkLeaderTicker := time.NewTicker(TickerTimeoutMs * time.Millisecond)
	defer checkLeaderTicker.Stop()

	for {
		select {
		case future := <-cm.applyCh:
			cm.mu.Lock()
			if cm.state != Leader {
				cm.mu.Unlock()
				future.respond(ErrNotLeader)
				return
			}
			cm.mu.Unlock()

			ready := []*logFuture{future}
		groupCommit:
			for i := 0; i < maxApplyBatchSize-1; i++ {
				select {
				case f := <-cm.applyCh:
					ready = append(ready, f)
				default:
					break groupCommit
				}
			}
			cm.dispatchLogs(ready)

		case <-checkLeaderTicker.C:
			cm.mu.Lock()
			if cm.state != Leader {
				cm.mu.Unlock()
				return
			}
			cm.mu.Unlock()

		case <-cm.shutdownCh:
			return
		}
	}
}

// persistToStorage сохраняет постоянное состояние CM в cm.storage.
func (cm *ConsensusModule) persistToStorage() {
	var termData bytes.Buffer
	if err := gob.NewEncoder(&termData).Encode(cm.currentTerm); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("currentTerm", termData.Bytes())

	var votedData bytes.Buffer
	if err := gob.NewEncoder(&votedData).Encode(cm.votedFor); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("votedFor", votedData.Bytes())

	var logData bytes.Buffer
	if err := gob.NewEncoder(&logData).Encode(cm.log); err != nil {
		log.Fatal(err)
	}
	cm.storage.Set("log", logData.Bytes())
}
