package raft

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/internal/tracelog"
)

// ConsensusModule (CM) реализует единый узел консенсуса Raft.
type ConsensusModule struct {
	mu sync.Mutex

	// wg — регистрация всех горутин CM. Регистрация выполняется атомарно
	// с проверкой «узел не остановлен» под cm.mu; Stop() присоединяет
	// все горутины через wg.Wait().
	wg sync.WaitGroup

	applyCh      chan *logFuture
	commitCh     chan int
	confChangeCh chan *configurationChangeFuture
	shutdownCh   chan struct{}
	stepDown     chan struct{}
	verifyCh     chan *verifyFuture

	// cmState — состояние узла Raft (persistent + volatile + election + log + config).
	// Все поля защищены cm.mu; cmState не содержит собственного mutex.
	cmState cmState

	fsm         FSM
	fsmMutateCh chan []*commitTuple

	// fsmSnapshotCh — небуферизированный канал для запроса снимка у runFSM.
	fsmSnapshotCh chan *reqSnapshotFuture

	// id — идентификатор сервера этого экземпляра CM (ConsensusModule).
	id int
	// peerIds содержит список идентификаторов узлов-соседей в кластере.
	peerIds []int
	// storage — постоянное хранилище состояния узла.
	storage Storage

	// counters — счётчики ключевых событий репликации и снимков.
	// Нулевое значение готово к использованию;
	// карты «по каждому соседу» защищены cm.mu,
	// глобальные поля — atomic.Int64.
	counters raftCounters

	// checkQuorumTimeout — предельный срок без ответов от кворума голосующих,
	// после которого лидер шагает вниз, в ведомые. Значение по умолчанию —
	// _defaultCheckQuorumTimeout; поле, а не константа, чтобы тесты пакета
	// могли задать заведомо больший или меньший срок. Читается только
	// в цикле лидера под cm.mu.
	checkQuorumTimeout time.Duration

	// Временные параметры узла. Записываются один раз до close(ready)
	// (конструктор — умолчания, setTimerConfig — конфигурация), все записи
	// и чтения — под cm.mu; чтение нормализует нулевое или отрицательное
	// значение в соответствующее умолчание Default*.
	applyBatchInterval time.Duration // интервал батча применения к FSM
	heartbeatTimeout   time.Duration // период пульса лидера
	reelectionTimeout  time.Duration // база тайм-аута выборов
	tickerTimeout      time.Duration // такт тикера выборов

	// latency — структура с агрегированными показателями задержки (латентности) ConsensusModule.
	// Нулевое значение структуры корректно и готово к использованию: явная инициализация не требуется.
	// Все поля имеют тип atomic.Int64, поэтому безопасны для чтения из любого контекста —
	// в том числе одновременно с удержанной блокировкой cm.mu.
	latency cmLatency

	// disableStatsOutput — неизменяемый выбор вывода периодической
	// статистики: зафиксирован конструктором до запуска первой горутины.
	// При true снимок и сброс латентности выполняются на каждом тике,
	// но сортировка, форматирование и запись трёх строк пропускаются.
	disableStatsOutput bool

	// statsStartedAt — момент создания CM; служит началом монотонного
	// возраста в периодическом отчёте. После конструктора не изменяется.
	statsStartedAt time.Time

	// statsInstance — идентификатор экземпляра CM в ряду PersistV1:
	// различает экземпляры в процессе и не повторяется после перезапуска
	// процесса. После конструктора не изменяется.
	statsInstance uint64

	// statsSeq — номер попытки выпуска периодического отчёта. Растёт
	// на каждую попытку при разрешённом выводе; при выключенном выводе
	// попыток нет. Поле принадлежит горутине stats: отдельной
	// синхронизации нет, в производстве его читает и пишет только stats,
	// в тестах — прямой вызов публикации на CM без запущенной stats.
	statsSeq uint64

	// statsOutputErr — первая («липкая») ошибка вывода периодического
	// отчёта. Принадлежит горутине stats; публикуется в следующей
	// успешной строке PersistV1 и не очищается.
	statsOutputErr error

	// leaderLoopsAlive — число живых горутин цикла лидера на этом узле.
	// Инвариант: значение не превышает 1. Увеличивается на входе в цикл
	// и уменьшается при выходе, оба раза под cm.mu; читается тестами пакета.
	leaderLoopsAlive int

	// leaderState — состояние, актуальное только пока узел исполняет роль лидера.
	// Все поля структуры защищены мьютексом cm.mu; отдельного мьютекса у неё нет.
	// Доступ к состоянию возможен из горутин репликации (по каждому соседу)
	// и из apply‑пути — при удержанной блокировке cm.mu.
	// Структура всегда инициализирована (в том числе на ведомом узле):
	// без этого вызов sendBatch приведёт к панике.
	leaderState leaderState

	// preVoteDisabled отключает механизм Pre-Vote.
	// Если true, выборы начинаются сразу (как в классическом Raft).
	// По умолчанию false — Pre-Vote включён.
	preVoteDisabled bool

	// shutdownClosed — флаг, который становится true после установки state=Dead в методе Stop().
	// Поле читается и изменяется только под блокировкой cm.mu.
	// Он дублирует проверку state == Dead внутри goSpawnLocked: это необходимо, потому что
	// закрытие канала shutdownCh происходит вне cm.mu, и по одному только состоянию канала
	// нельзя атомарно определить, что модуль остановлен.
	shutdownClosed bool

	// snapshotCh — канал для сигнала runSnapshots о необходимости
	// проверки создания снимка. Буферизирован (cap=1).
	snapshotCh chan struct{}

	// snapshotInterval — интервал проверки необходимости снимка.
	snapshotInterval time.Duration

	// snapshotStore — хранилище снимков. Если nil, снимки отключены.
	snapshotStore SnapshotStore

	// snapshotThreshold — минимальное количество записей после
	// последнего снимка для создания нового.
	snapshotThreshold int

	// stopOnce — постусловие идемпотентного Stop(): любой возврат из Stop()
	// (включая повторный/параллельный) гарантирует завершение всех горутин
	// CM. sync.Once.Do блокирует параллельных вызывающих до завершения
	// первого вызова.
	stopOnce sync.Once

	transport Transport

	// trailingLogs — количество записей журнала, сохраняемых после
	// последнего снимка.
	trailingLogs int

	// verifyRedispatchMinInterval — минимальный интервал между немедленными
	// перерассылками AppendEntries одному соседу при неудовлетворённом
	// verify-запросе. Значение по умолчанию вычисляется как
	// heartbeatTimeout × 8 / 11 (строго меньше пульса); поле, а не
	// константа, чтобы тесты пакета могли задать заведомо малое значение
	// (укороченное окно) для проверки границы частоты. Читается и
	// записывается только под cm.mu в redispatchVerifyIfPendingLocked.
	verifyRedispatchMinInterval time.Duration
}

// cmState — состояние узла Raft.
// Не содержит собственного mutex: все поля защищены cm.mu в ConsensusModule.
type cmState struct {
	// candidateFromLeadershipTransfer — true, если данный узел стал
	// кандидатом из-за получения TimeoutNow. Влияет на:
	//   — пропуск PreVote в runElectionTimer
	//   — обработку RequestVote (голосуем даже при известном лидере)
	//   — обработку AppendEntries (не step-down от старого лидера)
	candidateFromLeadershipTransfer atomic.Bool

	// Непостоянное состояние Raft на всех серверах
	commitIndex int

	// configurations — текущие конфигурации кластера (committed/latest).
	configurations configurations

	// Постоянное состояние Raft на всех серверах
	currentTerm     int
	votedFor        int
	log             []LogEntry
	logNeedsPersist bool

	// fsmAppliedIndex — максимальный индекс записи журнала, который фактически
	// был применён машиной состояний (т.е. для которого вызов apply вернул управление).
	// Используется как основа для формирования индекса снимка в handleFsmSnapshot.
	//
	// Поле защищено блокировкой cm.mu.
	// Писатели:
	//   - runFSM — обновляет монотонно после возврата из applyBatch/applySingle;
	//   - restoreFromSnapshotStore и handleInstallSnapshot — устанавливают значение
	//     безусловно после fsm.Restore, который полностью замещает состояние FSM.
	//
	// Значение не сохраняется отдельно: между перезапусками состояние восстанавливается
	// по meta.Index снимка.
	// Поле остаётся в cmState (а не в leaderState), потому что применение записей
	// машиной состояний происходит в любой роли узла, а не только у лидера.
	fsmAppliedIndex    int
	state              CMState
	electionResetEvent time.Time
	electionTimerDone  chan struct{}

	// lastApplied — максимальный индекс, диапазон до которого уже отправлен
	// в очередь машины состояний (fsmMutateCh). Это отметка диспетчеризации,
	// а не фактического применения: продвигается в processLogs до отправки
	// батча.
	lastApplied int

	// lastLogIndex — кэш индекса последней записи в журнале.
	// Обновляется через setLastLogLocked при любом изменении журнала.
	lastLogIndex int

	// lastLogTerm — кэш терма последней записи в журнале.
	lastLogTerm int

	// lastSnapshotIndex — индекс последнего созданного снимка.
	// Инициализируется как -1 (нет снимка).
	lastSnapshotIndex int

	// lastSnapshotTerm — терм последнего созданного снимка.
	lastSnapshotTerm int

	// leaderID — ID текущего лидера (если известен).
	// Устанавливается при получении AppendEntries от лидера.
	leaderID int

	// leaderLastContact — время последнего успешного AppendEntries от лидера.
	// Используется в RequestPreVote для проверки: если недавно был контакт с
	// лидером, Pre-Vote отклоняется.
	leaderLastContact time.Time

	// termIndexMap — карта term → последний LogEntry.Index с этим term.
	// O(1) lookup для ConflictTerm.
	// Инкрементально обновляется в dispatchLogsLocked; перестраивается
	// целиком при обрезке/сжатии/замене журнала.
	// Требует удержания cm.mu (Lock) при чтении и записи.
	termIndexMap map[int]int
}

type leaderState struct {
	// commitmentTracker — продвижение commitIndex на лидере.
	commitmentTracker *commitmentTracker

	// heartbeatTicks — монотонный счётчик тиков пульса текущего лидерства.
	// Инкрементируется в leaderLoop по тику пульса под cm.mu. Значение,
	// снятое при постановке verify в pendingVerify, служит нижней границей
	// «дождался ли запрос тика пульса» при его успешном завершении.
	heartbeatTicks uint64

	// inflightAE — флаги по каждому соседу активных горутин AppendEntries.
	//
	// Дедуплицирует вызовы leaderSendAEs: если для соседа уже есть активная
	// горутина leaderSendAEsToPeer, новая не создаётся.
	// Это предотвращает гонку nextIndex и экспоненциальный рост горутин
	// при частых пульсах.
	//
	// Доступ через atomic.CompareAndSwap без cm.mu — минимизация блокировок.
	// Инвариант:
	//		nil — сосед вне конфигурации;
	//		false — нет активной горутины;
	//		true — leaderSendAEsToPeer выполняется.
	// Захват — только через CompareAndSwap(false, true), единый для всех
	// точек запуска; сброс флага — только горутиной, владеющей им (признак
	// владения передаётся в leaderSendAEsToPeer). Сброс также выполняется в
	// runLeaderLoop и becomeFollowerLocked.
	inflightAE map[int]*atomic.Bool

	// inflight — карта future по индексу записи в журнале.
	// Обеспечивает O(1) поиск при отправке батча в FSM.
	inflight map[int]*logFuture

	// lastAttempt — время последней попытки отправки RPC каждому соседу.
	// Задержка повторов реализуется сравнением времени (без time.Sleep), чтобы
	// не удерживать inflightAE[peer] и не задерживать восстановление узла.
	lastAttempt map[int]time.Time

	// lastContact — время последнего ответа RPC без транспортной ошибки
	// по каждому соседу. Ответ соседа любого содержания (в том числе отказ
	// AppendEntries и ответ InstallSnapshot) доказывает, что сосед жив,
	// поэтому отметка обновляется в тех же критических секциях, где
	// сбрасывается задержка повторов.
	//
	// Жизненный цикл привязан к роли лидера: карта создаётся заново
	// в startLeaderLocked (отметка «сейчас» для соседей актуальной
	// конфигурации), пополняется в ensureReplicationForLocked при
	// добавлении сервера и очищается от удалённых серверов в
	// startStopReplicationLocked. Отсутствующая отметка трактуется как
	// «контакт только что был» — льгота, защищающая от ложного шага вниз
	// сразу после изменения состава кластера. Все обращения — под cm.mu.
	lastContact map[int]time.Time

	// leaderStartIndex — первый индекс текущего терма лидера.
	leaderStartIndex int

	// leadershipTransferCh — канал для запросов на передачу лидерства.
	// Запросы обрабатываются в leaderLoop.
	leadershipTransferCh chan *leadershipTransferFuture

	// leadershipTransferFuture — текущий future передачи лидерства.
	// Устанавливается в handleLeadershipTransfer, отвечается в stepDown.
	leadershipTransferFuture *leadershipTransferFuture

	// leadershipTransferInProgress — atomic-флаг, блокирующий Apply
	// во время передачи лидерства.
	leadershipTransferInProgress int32

	// matchIndex — индекс последней зафиксированной записи, реплицированной на каждом соседе.
	matchIndex map[int]int

	// nextIndex — индекс следующей записи журнала для отправки каждому соседу.
	nextIndex map[int]int

	// nextVerifyRedispatchAt — момент, раньше которого немедленная
	// перерассылка AppendEntries соседу не выполняется (окно троттлинга).
	// Ограничивает частоту перерассылок доказуемой границей: не более одной
	// перерассылки за verifyRedispatchMinInterval на каждого соседа, что
	// исключает самоподдерживающуюся эстафету при плотном потоке verify.
	//
	// Жизненный цикл привязан к роли лидера: карта создаётся заново в
	// startLeaderLocked (каждому лидерству — своя карта, по образцу
	// lastContact), поэтому окно не переживает смену лидерства — первая
	// перерассылка нового лидерства немедленна. Отсутствующий ключ — нулевое
	// время, окно свободно (льгота, симметричная отсутствующей отметке
	// контакта). Метка ставится только при фактической перерассылке под
	// cm.mu; все обращения — под cm.mu.
	nextVerifyRedispatchAt map[int]time.Time

	// replFailures — число подряд идущих транспортных ошибок по соседу.
	// Жизненный цикл привязан к роли лидера: создаётся в startLeaderLocked,
	// очищается в becomeFollowerLocked.
	replFailures map[int]int

	// pendingVerify — очередь verifyFuture, ожидающих подтверждения от ведомых.
	// Успешный ответ AppendEntries голосует за все ожидающие verifyFuture,
	// если AE отправлен не раньше постановки запроса (vf.epoch <= dispatchEpoch).
	pendingVerify []*verifyFuture

	// verifyEpoch — монотонный счётчик раундов верификации ReadIndex.
	// Инкрементируется в leaderLoop при каждом verify‑запросе.
	// Значение снимается в leaderSendAEs и передаётся как dispatchEpoch.
	verifyEpoch uint64
}

// GetConfiguration возвращает текущую конфигурацию кластера.
// Для лидера возвращает latest конфигурацию; для follower — committed.
func (cm *ConsensusModule) GetConfiguration() ConfigurationFuture {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	// Защитная копия среза на границе: мутация вызывающим не влияет на конфигурацию.
	cfg := cm.cmState.configurations.latest
	cfg.ConfigServers = slices.Clone(cfg.ConfigServers)
	return &configurationsFuture{config: cfg}
}

// Report отчет о состоянии данного CM.
func (cm *ConsensusModule) Report() (id, term int, isLeader bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.id, cm.cmState.currentTerm, cm.cmState.state == Leader
}

// Stop останавливает работу ConsensusModule. Метод можно вызывать многократно:
// повторные и параллельные вызовы безопасны.
//
// После любого возврата из Stop() гарантированно не остаётся работающих горутин CM.
// Реализация построена на sync.Once: он обеспечивает, что настоящая остановка
// выполнится ровно один раз, а все параллельные вызывающие дождутся её завершения.
// Внутри сначала под блокировкой cm.mu устанавливается состояние Dead и флаг shutdownClosed,
// затем закрывается shutdownCh и ожидается завершение всех горутин через wg.Wait().
// Раннего возврата без ожидания горутин не предусмотрено.
func (cm *ConsensusModule) Stop() {
	cm.stopOnce.Do(func() {
		cm.mu.Lock()
		cm.cmState.state = Dead
		cm.shutdownClosed = true
		cm.mu.Unlock()

		if traceEnabled(_traceLevelKeyEvents) {
			cm.traceLogf("CM.Stop called / becomes Dead")
		}
		close(cm.shutdownCh)
		cm.wg.Wait()
	})
}

// goSpawnLocked запускает функцию fn в отдельной горутине и регистрирует её в группе ожидания cm.wg.
//
// Важное условие: метод должен вызываться только когда блокировка cm.mu уже удерживается
// (например, внутри becomeFollowerLocked, startElectionLocked, startLeaderLocked, leaderSendAEs).
//
// Ключевой контракт безопасности: проверка состояния узла (Dead/shutdownClosed) и добавление
// счётчика в wg (wg.Add) выполняются под одной блокировкой cm.mu. Это гарантирует атомарность:
// горутина не будет добавлена в wg после того, как Stop() начал ожидание завершения всех горутин (wg.Wait).
//
// Критическое ограничение: нельзя временно освобождать cm.mu внутри вызывающих функций,
// чтобы использовать «unlocked»-вариант. Если это сделать, атомарность нарушится:
// между проверкой состояния и wg.Add может успеть выполниться Stop(), и возникнет гонка.
//
// Требует удержания cm.mu.
func (cm *ConsensusModule) goSpawnLocked(fn func()) {
	if cm.cmState.state == Dead || cm.shutdownClosed {
		return // узел останавливается: запускать новую горутину нельзя
	}
	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()
		fn()
	}()
}

// goSpawn — то же, что goSpawnLocked, для вызывающих, НЕ удерживающих
// cm.mu (конструктор, runPreCandidate, catch-up передачи лидерства,
// post-commit горутина конфигурации). Самостоятельно захватывает cm.mu.
// Не вызывает пользовательский код под блокировкой
// (только проверка, Add и go).
func (cm *ConsensusModule) goSpawn(fn func()) {
	cm.mu.Lock()
	cm.goSpawnLocked(fn)
	cm.mu.Unlock()
}

// initTimerDefaults устанавливает временные поля в значения по умолчанию
// и пересчитывает зависимую величину verify-перерассылки от пульса.
// Вызывается из конструктора до первого goSpawn — горутины ещё не запущены,
// поэтому блокировка cm.mu не требуется.
func (cm *ConsensusModule) initTimerDefaults() {
	cm.applyBatchInterval = DefaultApplyBatchInterval
	cm.heartbeatTimeout = DefaultHeartbeatTimeout
	cm.reelectionTimeout = DefaultReelectionTimeout
	cm.tickerTimeout = DefaultTickerTimeout
	cm.verifyRedispatchMinInterval = DefaultHeartbeatTimeout * 8 / 11
}

// setTimerConfig устанавливает временные параметры узла. Вызывается только
// до закрытия канала готовности (close(ready)).
// Требования:
// - Вызывающий код должен передать нормализованные значения > 0.
// - Метод принимает значения без изменений и пересчитывает зависимую величину.
// Метод автоматически захватывает блокировку cm.mu и снимает её через defer.
func (cm *ConsensusModule) setTimerConfig(tc TimerConfig) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.applyBatchInterval = tc.ApplyBatch
	cm.heartbeatTimeout = tc.Heartbeat
	cm.reelectionTimeout = tc.Reelection
	cm.tickerTimeout = tc.Ticker
	cm.verifyRedispatchMinInterval = tc.Heartbeat * 8 / 11
}

// traceLogfLocked ставит отладочное сообщение в очередь писателя.
// Форматирование префикса и тела выполняет писатель; скаляры состояния
// снимаются здесь под уже удержанной cm.mu.
// Требует удержания cm.mu и внешней проверки порога вызывающим
// (traceEnabled с уровнем данного места); вызов без guard — нарушение
// контракта.
//
//nolint:goprintffuncname // имя закреплено контрактом вывода, формат передаётся писателю.
func (cm *ConsensusModule) traceLogfLocked(format string, args ...any) {
	cm.enqueueTraceLocked(_traceWriter, format, args...)
}

// traceSprintfLocked ставит отладочное сообщение с телом, сформированным
// синхронно. Применяется в местах со ссылочными аргументами:
// форматирование под cm.mu исключает чтение изменяемых объектов
// в асинхронном пути.
// Требует удержания cm.mu и внешней проверки порога вызывающим
// (traceEnabled с уровнем данного места); вызов без guard — нарушение
// контракта.
//
//nolint:goprintffuncname // имя закреплено контрактом вывода, формат передаётся писателю.
func (cm *ConsensusModule) traceSprintfLocked(format string, args ...any) {
	cm.enqueueTraceSprintfLocked(_traceWriter, format, args...)
}

// traceLogf — потокобезопасная обёртка для вызовов БЕЗ удержания cm.mu:
// безусловно захватывает блокировку и снимает её через defer, затем
// ставит сообщение в очередь писателя непосредственно. Требует внешней
// проверки порога вызывающим (traceEnabled с уровнем данного места);
// вызов без guard — нарушение контракта. Для вызовов из кода, который
// уже держит cm.mu, используйте traceLogfLocked — иначе будет deadlock.
func (cm *ConsensusModule) traceLogf(format string, args ...any) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.enqueueTraceLocked(_traceWriter, format, args...)
}

// enqueueTraceLocked добавляет сообщение с префиксными скалярными значениями в очередь
// для записи: скалярные значения извлекаются с захваченной блокировкой cm.mu,
// а тело сообщения форматируется модулем записи.
//
// Вызывающий код уже выполнил проверку порога и всех необходимых условий.
// Метод требует, чтобы блокировка cm.mu была удержана на момент вызова.
//
//nolint:goprintffuncname // имя закреплено контрактом вывода, формат передаётся писателю.
func (cm *ConsensusModule) enqueueTraceLocked(w *tracelog.Writer, format string, args ...any) {
	w.Enqueue(tracelog.Prefix{
		Letter: stateLetter(cm.cmState.state),
		ID:     cm.id,
		Term:   cm.cmState.currentTerm,
	}, format, args...)
}

// enqueueTraceSprintfLocked ставит сообщение с готовым телом: fmt.Sprintf
// выполняется синхронно под блокировкой cm.mu — это защищает от чтения
// ссылочных аргументов после снятия блокировки.
// Порог и все требуемые проверки уже выполнены вызывающим кодом.
// Требуется удержание блокировки cm.mu.
//
//nolint:goprintffuncname // имя закреплено контрактом вывода.
func (cm *ConsensusModule) enqueueTraceSprintfLocked(w *tracelog.Writer, format string, args ...any) {
	w.EnqueueText(tracelog.Prefix{
		Letter: stateLetter(cm.cmState.state),
		ID:     cm.id,
		Term:   cm.cmState.currentTerm,
	}, fmt.Sprintf(format, args...))
}

// stateLetter сопоставляет состоянию консенсус-модуля букву префикса
// строки трассировки; состояния без собственной буквы печатаются как «?».
// Вызывается только под cm.mu. Чистая функция без аллокаций.
func stateLetter(s CMState) rune {
	switch s {
	case Follower:
		return 'F'
	case Leader:
		return 'L'
	case Candidate:
		return 'C'
	default:
		return '?'
	}
}
