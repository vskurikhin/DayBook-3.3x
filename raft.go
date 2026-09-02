package raft

import (
	"log"
	"sync/atomic"
	"time"
)

const (
	// Quantum — множитель для всех Raft-таймеров.
	// Для тестового ускорения Quantum не меняется — используются
	// test-only хуки (RAFT_FORCE_MORE_REELECTION и аналоги).
	Quantum             = 3
	HeartbeatTimeoutMs  = 11 * Quantum
	ReelectionTimeoutMs = 127 * Quantum
	TickerTimeoutMs     = 7 * Quantum

	// LeaktestBudget — единый бюджет leaktest:
	// max(_inmemRPCTimeout, TCPRPCTimeout) + 100ms = 600ms.
	LeaktestBudget = 600 * time.Millisecond

	// _applyBatchInterval — интервал, с которым runApplyLoop проверяет
	// необходимость применения записей к FSM. Накопление commitCh
	// уведомлений за этот интервал позволяет объединять несколько
	// мелких фиксаций в один батч.
	_applyBatchInterval = 50 * time.Millisecond

	// _batchApplyBuffer — ёмкость fsmMutateCh для burst-устойчивости.
	// Выбрана как 1024: при массовой фиксации processLogs не блокируется.
	_batchApplyBuffer = 1024

	// _defaultCheckQuorumTimeout — предельный срок без ответов от кворума
	// голосующих, после которого лидер шагает вниз, в ведомые. Значение
	// 2 × TCPRPCTimeout: отметка контакта по соседу обновляется не чаще,
	// чем завершается один RPC (AppendEntries к одному соседу
	// сериализованы флагом inflightAE), а дедлайн одного RPC равен
	// TCPRPCTimeout и масштабируется объёмом передаваемых записей;
	// двукратный запас покрывает один полный тайм-аут RPC. Итоговая
	// величина близка к ReelectionTimeoutMs: лидер обнаруживает потерю
	// кворума не позже, чем ведомый начинает выборы.
	_defaultCheckQuorumTimeout = 2 * _defaultTCPRPCTimeout

	// DefaultSnapshotInterval — интервал проверки необходимости снимка.
	// Каждые 3 секунды runSnapshots проверяет, не превышен ли порог
	// DefaultSnapshotThreshold.
	DefaultSnapshotInterval = 3 * time.Second

	// DefaultSnapshotThreshold — минимальное количество записей после
	// последнего снимка, при котором создаётся новый снимок.
	// Значение 1024 выбрано как компромисс: частые снимки создают
	// нагрузку на FSM.Snapshot(), редкие — увеличивают время восстановления
	// и объём журнала, который нужно передавать отстающим узлам.
	DefaultSnapshotThreshold = 1024

	// _defaultTakeSnapshotTimeout — тайм-аут отправки запроса на снимок
	// в fsmSnapshotCh. Если runFSM не принимает запрос за это время,
	// takeSnapshot возвращает ошибку вместо вечной блокировки.
	// Значение 30 секунд выбрано как разумный максимум для создания
	// снимка в production (запись состояния + persist на диск).
	_defaultTakeSnapshotTimeout = 30 * time.Second

	// _defaultTrailingLogs — количество записей журнала, сохраняемых
	// после самого свежего снимка. Нужно для поддержки репликации
	// без отправки полного снимка каждому новому follower.
	// Значение 128 — разумный минимум для большинства сценариев.
	_defaultTrailingLogs = 128

	// _leaderBatchSize — количество записей, которые лидер собирает из applyCh
	// перед одним вызовом dispatchLogs. Компромисс между latency (одиночные
	// записи) и throughput (групповой commit, Raft §5.1).
	_leaderBatchSize = 256

	// _maxApplyBatchSize — максимальное количество записей в одном батче,
	// отправляемом в fsmMutateCh. Если commitIndex - lastApplied превышает
	// этот порог, processLogs делит диапазон на под-батчи.
	_maxApplyBatchSize = 512

	// _maxSnapshotDataSize — максимальный допустимый размер данных снимка
	// в байтах. Значение 1 ГБ выбрано как разумный предел для production
	// (больше типичного размера состояния, но достаточно для обнаружения
	// некорректного DataSize в запросе InstallSnapshot).
	// Установлено в 1 << 30 для согласованности.
	// Защита от паники io.Copy при повреждённом DataSize.
	_maxSnapshotDataSize = 1 << 30

	// _maxUncommittedEntries — предел незафиксированного хвоста журнала,
	// при котором лидер перестаёт принимать новые команды клиентов.
	// Это не протокольная гарантия, а ограничение ущерба: лидер, потерявший
	// кворум, не должен наращивать журнал неограниченно. Предел действует
	// только на клиентском пути; служебные записи (noop вступления в
	// лидерство и записи конфигурации) им не проверяются, иначе кластер
	// с накопленным хвостом не смог бы ни избрать лидера, ни изменить
	// состав.
	_maxUncommittedEntries = 4096

	// _verifyRedispatchMinIntervalMs — минимальный интервал между немедленными
	// перерассылками AppendEntries одному соседу при плотном потоке verify.
	// Значение строго меньше HeartbeatTimeoutMs (33 мс), поэтому перерассылка
	// остаётся быстрее пульса: первая перерассылка немедленна, а запрос,
	// заставший окно занятым, дожидается либо следующей перерассылки, либо
	// пульса — не дольше пульса. По построению частота перерассылок на
	// каждого соседа ограничена 1000/24 ≈ 41,7 перерассылок/с независимо от
	// темпа клиентских запросов.
	_verifyRedispatchMinIntervalMs = 8 * Quantum

	// _verifyChBuffer — ёмкость verifyCh, канала запросов проверки
	// лидера от клиентов. Читает единственный цикл лидера (по одному
	// запросу за такт), буфер сглаживает пачки запросов между
	// тактами; при заполнении VerifyLeader ожидает места или
	// остановки узла, не блокируя горутины модуля.
	_verifyChBuffer = 64
)

// CommitEntry — это данные, которые Raft отправляет в канал фиксации.
// Каждая запись фиксации уведомляет клиента о том, что консенсус по команде
// был достигнут и эта команда может быть применена к машине состояний клиента.
type CommitEntry struct {
	// Data — это команда клиента, которая была зафиксирована.
	Data any

	// Index — это индекс журнала, по которому была зафиксирована команда клиента.
	Index int

	// Term — это терм Raft, в котором была зафиксирована команда клиента.
	Term int
}

type CMState int

const (
	Follower CMState = iota
	PreCandidate
	Candidate
	Leader
	Dead
)

func (s CMState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case PreCandidate:
		return "PreCandidate"
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

// commitTuple связывает запись журнала с future для отправки в FSM.
type commitTuple struct {
	log    *LogEntry
	future *logFuture
}

// configurationChangeFuture — future для изменения конфигурации кластера.
type configurationChangeFuture struct {
	deferError

	req   configurationChangeRequest
	index int
}

var _ IndexFuture = (*configurationChangeFuture)(nil)

func (f *configurationChangeFuture) Index() int {
	return f.index
}

// NewConsensusModule создаёт новый экземпляр ConsensusModule.
//
// Предусловие: аргумент transport не должен быть nil.
// При нарушении предусловия функция немедленно завершается с паникой (fail‑fast).
// Это единственная валидация, выполняемая до запуска горутин.
// Проверка корректно обрабатывает все виды nil: как пустой интерфейс,
// так и типизированный nil‑указатель.
func NewConsensusModule(
	id int,
	peerIds []int,
	transport Transport,
	storage Storage,
	fsm FSM,
	ready <-chan any,
	snapshots ...SnapshotStore,
) *ConsensusModule {
	if isNilInterface(transport) {
		log.Fatalln("raft: NewConsensusModule: transport is nil")
	}
	// Отмечаем факт создания CM для трассировки (set-once).
	_traceCMCreated.Store(true)
	cm := new(ConsensusModule)
	cm.id = id
	cm.peerIds = peerIds
	cm.transport = transport
	cm.storage = storage
	cm.fsm = fsm
	// Канал для передачи зафиксированных записей в runFSM.
	// Ёмкость _batchApplyBuffer обеспечивает устойчивость к всплескам:
	// при массовой фиксации processLogs отправляет записи батчем без блокировки.
	cm.fsmMutateCh = make(chan []*commitTuple, _batchApplyBuffer)
	cm.shutdownCh = make(chan struct{})
	cm.applyCh = make(chan *logFuture)
	// Канал для ReadIndex‑подтверждения лидерства.
	// Ёмкость 64 рассчитана на всплески нагрузки: при 10k RPS VerifyLeader не блокируется.
	cm.verifyCh = make(chan *verifyFuture, _verifyChBuffer)
	cm.leaderState.inflight = make(map[int]*logFuture)
	cm.commitCh = make(chan int, 1)
	cm.stepDown = make(chan struct{}, 1)
	cm.confChangeCh = make(chan *configurationChangeFuture, 1)
	cm.cmState.state = Follower
	cm.cmState.votedFor = -1
	cm.cmState.commitIndex = -1
	cm.cmState.lastApplied = -1
	cm.cmState.fsmAppliedIndex = -1
	cm.leaderState.leaderStartIndex = -1
	cm.cmState.lastLogIndex = -1
	cm.cmState.lastLogTerm = -1
	cm.leaderState.nextIndex = make(map[int]int)
	cm.leaderState.matchIndex = make(map[int]int)
	cm.leaderState.lastContact = make(map[int]time.Time)
	cm.checkQuorumTimeout = _defaultCheckQuorumTimeout
	cm.verifyRedispatchMinInterval = _verifyRedispatchMinIntervalMs * time.Millisecond
	cm.leaderState.inflightAE = make(map[int]*atomic.Bool)
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.electionTimerDone = make(chan struct{})
	cm.preVoteDisabled = false
	cm.cmState.leaderLastContact = time.Time{}
	cm.cmState.leaderID = -1
	cm.leaderState.leadershipTransferCh = make(chan *leadershipTransferFuture, 1)
	cm.cmState.logNeedsPersist = true

	// Инициализация полей для снимков.
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	if len(snapshots) > 0 && snapshots[0] != nil {
		cm.snapshotStore = snapshots[0]
		cm.snapshotThreshold = DefaultSnapshotThreshold
		cm.snapshotInterval = DefaultSnapshotInterval
		cm.trailingLogs = _defaultTrailingLogs
		cm.snapshotCh = make(chan struct{}, 1)
		cm.fsmSnapshotCh = make(chan *reqSnapshotFuture)
	}

	// Восстановление состояния из хранилища.
	if cm.storage.HasData() {
		cm.restoreFromStorage()
		cm.cmState.logNeedsPersist = false
		// Однопоточное восстановление FSM из снимка до запуска горутин.
		// Fail‑fast: узел должен громко упасть при невосстановимом состоянии,
		// чтобы избежать молчаливой потери подтверждённых данных.
		if err := cm.restoreFromSnapshotStore(); err != nil {
			log.Fatalf("raft: startup snapshot restore failed: %v", err)
		}
	}

	// Установка начальной конфигурации, если она отсутствует
	// (первый запуск либо хранилище не содержит конфигурации).
	if len(cm.cmState.configurations.latest.ConfigServers) == 0 {
		cm.setInitialConfiguration()
	}

	cm.goSpawn(cm.runFSM)
	cm.goSpawn(cm.runRPCReader)

	if cm.snapshotStore != nil {
		cm.goSpawn(cm.runSnapshots)
	}

	cm.goSpawn(func() {
		<-ready
		cm.mu.Lock()
		cm.cmState.electionResetEvent = time.Now()
		cm.mu.Unlock()
		cm.runElectionTimer()
	})
	cm.goSpawn(func() { cm.stats(cm.shutdownCh) })
	return cm
}

// checkRPCHeader проверяет, что RPC-сообщение использует поддерживаемую
// версию протокола. Для v3-only возвращает nil только при ProtocolVersion == 3.
func checkRPCHeader(rpc WithRPCHeader) error {
	header := rpc.GetRPCHeader()
	if header.ProtocolVersion != ProtocolVersion {
		return ErrUnsupportedProtocol
	}

	return nil
}

// quorumSize возвращает минимальное количество голосов для достижения кворума.
func quorumSize(voterCount int) int {
	return voterCount/2 + 1
}
