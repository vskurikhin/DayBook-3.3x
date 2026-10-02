package raft

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"
)

const (
	// DefaultApplyBatchInterval — интервал, с которым цикл лидера проверяет
	// необходимость применения записей к FSM. Накопление уведомлений канала
	// фиксации за этот интервал позволяет объединять несколько мелких
	// фиксаций в один батч.
	DefaultApplyBatchInterval = 50 * time.Millisecond

	// DefaultHeartbeatTimeout — период пульса лидера по умолчанию.
	DefaultHeartbeatTimeout = 33 * time.Millisecond

	// DefaultReelectionTimeout — опорная длительность для тайм‑аута выборов по умолчанию.
	// Фактический тайм‑аут выбирается случайно из диапазона [значение; 2·значение).
	// Значение должно превышать окно проверки кворума (2 × TCPRPCTimeout).
	// При настройках по умолчанию выборы начинаются практически сразу после шага
	// изолированного лидера вниз.
	DefaultReelectionTimeout = 430 * time.Millisecond

	// DefaultTickerTimeout — такт тикера выборов по умолчанию.
	DefaultTickerTimeout = 20 * time.Millisecond

	// LeaktestBudget — единый бюджет leaktest:
	// max(_inmemRPCTimeout, TCPRPCTimeout) + 100ms = 600ms.
	LeaktestBudget = 600 * time.Millisecond

	// _batchApplyBuffer — ёмкость fsmMutateCh для burst-устойчивости.
	// Выбрана как 1024: при массовой фиксации processLogs не блокируется.
	_batchApplyBuffer = 1024

	// _defaultCheckQuorumTimeout — предельный срок без ответов от
	// кворума голосующих, после которого лидер шагает вниз, в ведомые.
	// Значение 2 × TCPRPCTimeout: отметка контакта по соседу обновляется
	// не чаще, чем завершается один RPC (AppendEntries к одному соседу
	// сериализованы флагом inflightAE); окно одного RPC на новом
	// соединении складывается из установки соединения и дедлайна обмена
	// (DialTimeout + SetDeadline), при повторном использовании — только
	// дедлайн обмена; двукратный запас покрывает один полный тайм-аут
	// RPC.
	// Соотношение с базой перевыборов: обнаружение потери кворума
	// приходится на [CQ; CQ+HB) (проверка выполняется по тику пульса),
	// а ведомый начинает выборы на [RE; 2·RE). На умолчаниях порядок
	// практически сохранён (перекрытие [430; 433) — 3 мс, ≈0,7 %):
	// [400; 433) мс обнаружение против [430; 860) мс выборы.
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

	// _verifyChBuffer — ёмкость verifyCh, канала запросов проверки
	// лидера от клиентов. Читает единственный цикл лидера (по одному
	// запросу за такт), буфер сглаживает пачки запросов между
	// тактами; при заполнении VerifyLeader ожидает места или
	// остановки узла, не блокируя горутины модуля.
	_verifyChBuffer = 64
)

// Границы допустимых значений временных параметров узла. Все границы
// выражены целым числом миллисекунд и используются функцией ValidateTiming.
const (
	// MinHeartbeatTimeout — минимальный период пульса лидера.
	MinHeartbeatTimeout = 5 * time.Millisecond
	// MaxHeartbeatTimeout — максимальный период пульса лидера. Верхняя
	// граница выводится из фиксированного check-quorum тайм-аута
	// (400 мс): в окне проверки кворума лидер должен успеть не менее
	// четырёх раз связаться с соседями. Целочисленное выражение
	// ⌊400/4⌋ = 100 мс сохраняет требование целого числа миллисекунд.
	//nolint:durationcheck // целочисленное округление вниз до целых мс (⌊400/4⌋ = 100)
	MaxHeartbeatTimeout = _defaultCheckQuorumTimeout / 4 / time.Millisecond * time.Millisecond

	// MinTickerTimeout — минимальный такт тикера выборов.
	MinTickerTimeout = 1 * time.Millisecond
	// MaxTickerTimeout — максимальный такт тикера выборов. Санитарный
	// предел: такт задаёт ошибку квантования тайм-аута выборов.
	MaxTickerTimeout = 1000 * time.Millisecond

	// MinReelectionTimeout — минимальная база тайм-аута выборов. Нижняя
	// граница связана с джиттером pre-vote (50 мс): джиттер не должен быть
	// сопоставим с тайм-аутом, 4 × 50 = 200 мс.
	MinReelectionTimeout = 4 * _preVoteJitterMs * time.Millisecond
	// MaxReelectionTimeout — максимальная база тайм-аута выборов.
	// Санитарный предел: цикл перевыборов до 2 × 30 с, и окно подавления
	// pre-vote на получателе также до 2 × 30 с.
	MaxReelectionTimeout = 30000 * time.Millisecond

	// MinApplyBatchInterval — минимальный интервал батча применения.
	MinApplyBatchInterval = 1 * time.Millisecond
	// MaxApplyBatchInterval — максимальный интервал батча применения.
	// Санитарный предел страховочного тика лидера.
	MaxApplyBatchInterval = 5000 * time.Millisecond
)

// TimerConfig — набор временных параметров узла Raft. Все значения —
// time.Duration; нулевое или отрицательное значение означает умолчание
// соответствующей константы Default*.
type TimerConfig struct {
	// ApplyBatch — интервал батча применения записей к FSM.
	ApplyBatch time.Duration
	// Heartbeat — период пульса лидера.
	Heartbeat time.Duration
	// Reelection — база тайм-аута выборов.
	Reelection time.Duration
	// Ticker — такт тикера выборов.
	Ticker time.Duration
}

// ValidateTiming проверяет временные параметры узла: индивидуальные
// границы каждого параметра (диапазон и целое число миллисекунд).
// Функция чистая, без побочных эффектов; все нарушения собираются
// в одну ошибку (errors.Join).
func ValidateTiming(tc TimerConfig) error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(
		tc.Heartbeat >= MinHeartbeatTimeout && tc.Heartbeat <= MaxHeartbeatTimeout && tc.Heartbeat%time.Millisecond == 0,
		"heartbeat-timeout must be between %v and %v, got %v", MinHeartbeatTimeout, MaxHeartbeatTimeout, tc.Heartbeat,
	)
	check(
		tc.Ticker >= MinTickerTimeout && tc.Ticker <= MaxTickerTimeout && tc.Ticker%time.Millisecond == 0,
		"ticker-timeout must be between %v and %v, got %v", MinTickerTimeout, MaxTickerTimeout, tc.Ticker,
	)
	check(
		tc.Reelection >= MinReelectionTimeout && tc.Reelection <= MaxReelectionTimeout && tc.Reelection%time.Millisecond == 0,
		"reelection-timeout must be between %v and %v, got %v", MinReelectionTimeout, MaxReelectionTimeout, tc.Reelection,
	)
	check(
		tc.ApplyBatch >= MinApplyBatchInterval && tc.ApplyBatch <= MaxApplyBatchInterval && tc.ApplyBatch%time.Millisecond == 0,
		"apply-batch-interval must be between %v and %v, got %v", MinApplyBatchInterval, MaxApplyBatchInterval, tc.ApplyBatch,
	)
	check(
		tc.Reelection >= 10*tc.Heartbeat,
		"reelection-timeout must be at least 10x heartbeat-timeout: got %v vs heartbeat-timeout %v",
		tc.Reelection, tc.Heartbeat,
	)
	check(
		tc.Ticker <= tc.Reelection/10,
		"ticker-timeout must be at most reelection-timeout/10: got %v vs reelection-timeout %v",
		tc.Ticker, tc.Reelection,
	)
	check(
		tc.ApplyBatch <= tc.Reelection,
		"apply-batch-interval must not exceed reelection-timeout: got %v vs reelection-timeout %v",
		tc.ApplyBatch, tc.Reelection,
	)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

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
	if IsNilInterface(transport) {
		log.Fatalln("raft: NewConsensusModule: transport is nil")
	}
	// Отмечаем факт создания CM для трассировки (set-once).
	_traceCMCreated.Store(true)
	// Единственное чтение переменной окружения хука форсирования выборов
	// выполняется при старте: значение кэшируется в переменную пакета на
	// весь процесс.
	_forcedReelectionHook.Store(os.Getenv(forcedReelectionEnv) != "")
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
	// Временные параметры инициализируются умолчаниями безусловно, до
	// первого goSpawn; зависимые величины вычисляются от полей.
	cm.initTimerDefaults()
	cm.leaderState.inflightAE = make(map[int]*atomic.Bool)
	cm.cmState.termIndexMap = make(map[int]int)
	cm.cmState.electionTimerDone = make(chan struct{})
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
		// Стратегия немедленного отказа: при невосстановимом состоянии узел
		// аварийно завершается с явной ошибкой, чтобы избежать молчаливой
		// потери подтверждённых данных.
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
