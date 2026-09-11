# Справочник API консенсус-модуля (пакет `raft`)

## Назначение

Корневой пакет `raft` реализует протокол консенсуса Raft (версия 3): выборы
лидера с предварительным голосованием, репликацию журнала, фиксацию записей,
изменение состава кластера, снимки состояния машины состояний и проверку
лидерства без записи в журнал. Пакет используется KV-сервисом
(`pkg/kvservice`) и исполняемыми файлами `cmd/...`.

## 1. Константы

| Имя                         | Значение | Назначение                                                                                                                                                                                |
|-----------------------------|----------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ProtocolVersion`           | 3        | Версия протокола Raft. Совместимость с версиями 0–2 не поддерживается.                                                                                                                    |
| `TCPRPCTimeout`             | 165 мс   | Тайм-аут TCP RPC в промышленном транспорте; 330/2, вынесено в отдельную константу, чтобы ускорение таймеров в тестах не влияло на промышленный транспорт.                                 |
| `DefaultApplyBatchInterval` | 50 мс    | Интервал, с которым цикл лидера проверяет необходимость применения записей к машине состояний; накопленные фиксации объединяются в один пакет.                                            |
| `DefaultHeartbeatTimeout`   | 33 мс    | Период пульса лидера по умолчанию.                                                                                                                                                        |
| `DefaultReelectionTimeout`  | 340 мс   | База тайм-аута выборов по умолчанию: фактический тайм-аут выводится из неё случайной величиной.                                                                                           |
| `DefaultTickerTimeout`      | 20 мс    | Такт тикера выборов по умолчанию.                                                                                                                                                         |
| `LeaktestBudget`            | 600 мс   | Единый бюджет проверки утечек горутин: max(внутренний тайм-аут RPC в памяти, TCPRPCTimeout) + 100 мс.                                                                                     |
| `DefaultSnapshotInterval`   | 3 с      | Интервал проверки необходимости снимка.                                                                                                                                                   |
| `DefaultSnapshotThreshold`  | 1024     | Минимальное число записей после последнего снимка, при котором создаётся новый; компромисс между нагрузкой на FSM и временем восстановления.                                              |

Границы допустимых значений таймеров (`Min*`/`Max*`) — в разделе 2.

Типы записей журнала — значения `LogType`: `LogCommand` (команда клиента),
`LogNoop` (вступительная запись лидера), `LogConfiguration` (изменение
состава кластера).

Право голоса — значения `ServerSuffrage`: `Voter` (голосующий),
`Nonvoter` (неголосующий; не может быть избран лидером).

## 2. Границы таймеров и инварианты

```go
func ValidateTiming(tc TimerConfig) error
```

`ValidateTiming` проверяет временные параметры узла: индивидуальные границы
каждого параметра (диапазон и целое число миллисекунд). Функция чистая, без
побочных эффектов; все нарушения собираются в одну ошибку `errors.Join`.

Межпараметрические инварианты:

- `Reelection >= 10 * Heartbeat` — ведомый не начинает выборы между пульсами
  живого лидера;
- `Ticker <= Reelection / 10` — такт задаёт ошибку квантования тайм-аута
  выборов;
- `ApplyBatch <= Reelection`.

Все значения обязаны выражаться целым числом миллисекунд; нулевое или
отрицательное значение поля `TimerConfig` означает умолчание соответствующей
константы `Default*`.

## 3. Ошибки пакета raft

| Имя                               | Сообщение                                  | Когда возвращается                                                                                                                        |
|-----------------------------------|--------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `ErrNotLeader`                    | `raft: not leader`                         | Операция лидера (`Apply`, `VerifyLeader`, `LeadershipTransfer`, изменение состава) вызвана на узле, не являющемся лидером.                |
| `ErrLeadershipLost`               | `raft: leadership lost while committing`   | Лидерство утеряно до фиксации: ожидающие future разрешаются этой ошибкой при уходе лидера в ведомые.                                      |
| `ErrUnsupportedProtocol`          | `raft: unsupported protocol version`       | Входящий RPC использует версию протокола, отличную от 3 (`checkRPCHeader`).                                                               |
| `ErrLeadershipTransferInProgress` | `raft: leadership transfer in progress`    | `Apply` во время передачи лидерства; повторный `LeadershipTransfer` при уже идущей передаче.                                              |
| `ErrTooManyUncommittedEntries`    | `raft: too many uncommitted log entries`   | Незафиксированный хвост журнала лидера достиг предела (4096): кворум недоступен либо соседи не успевают за нагрузкой.                     |
| `ErrNothingNewToSnapshot`         | `raft: nothing new to snapshot`            | Нет новых зафиксированных записей для создания снимка.                                                                                    |
| `ErrBatchFSMResponseMismatch`     | `raft: ApplyBatch response count mismatch` | `BatchingFSM.ApplyBatch` вернул число ответов, не равное числу записей; ошибка передаётся всем future пакета через `ApplyFuture.Error()`. |

## 4. Маркерные ошибки contract.Err*

Маркерные ошибки протокола объявлены в `pkg/raft/contract` и используются
потребителями напрямую с квалификатором пакета. В корневой пакет они НЕ
переэкспортируются: переэкспорт через `var` создал бы второй экземпляр
ошибки, из-за которого сравнения `err == ErrX` и `errors.Is` перестали бы
работать между корневым и листовыми пакетами.

Полный перечень (снят с `go doc -all ./pkg/raft/contract`, раздел
VARIABLES; других маркерных ошибок в пакете нет):

| Имя                          | Сообщение                           | Когда возвращается                                                                                              |
|------------------------------|-------------------------------------|-----------------------------------------------------------------------------------------------------------------|
| `contract.ErrEnqueueTimeout` | `raft: timeout enqueuing operation` | Истечение срока ожидания постановки операции в очередь (например, `Apply` при заполненном `applyCh`).           |
| `contract.ErrNotImplemented` | `raft: not implemented`             | RPC-методы, которые ещё не реализованы (`TimeoutNow`, `InstallSnapshot`, `AppendEntriesPipeline` в транспорте). |
| `contract.ErrNotReachable`   | `raft: peer not reachable`          | Попытка отправки RPC узлу, который не подключён или отключён от данного транспорта.                             |
| `contract.ErrRaftShutdown`   | `raft: raft is shutdown`            | Операции остановленного узла Raft (после `Stop`/`Shutdown`), в том числе отправка RPC через закрытый транспорт. |

## 5. Конструктор и жизненный цикл

```go
func NewConsensusModule(
	id int,
	peerIds []int,
	transport Transport,
	storage Storage,
	fsm FSM,
	ready <-chan any,
	snapshots ...SnapshotStore,
) *ConsensusModule
```

`NewConsensusModule` создаёт новый экземпляр консенсусного модуля.
Предусловие: `transport != nil` — при нарушении функция немедленно завершается по стратегии немедленного отказа (паника);
проверка ловит и типизированный nil-указатель в интерфейсе.
Это единственная валидация до запуска горутин: восстановление из хранилища и снимка
выполняется в конструкторе до старта горутин, а выборы начинаются только
после закрытия канала `ready`.

```go
func (cm *ConsensusModule) Report() (id, term int, isLeader bool)
```

`Report` — отчёт о состоянии узла: идентификатор, текущий терм и признак лидера;
наблюдаемая точка состояния без побочных эффектов.

```go
func (cm *ConsensusModule) Stop()
```

`Stop` останавливает модуль: устанавливает состояние
`Dead`, закрывает канал завершения и ожидает все горутины (`wg.Wait()`).
Метод идемпотентный: повторные и параллельные вызовы безопасны; раннего
возврата без ожидания горутин не предусмотрено.

## 6. Роли и переходы

Автомат состояний `CMState` — `Follower`, `PreCandidate`, `Candidate`, `Leader`, `Dead` (терминальное; устанавливается `Stop`).
Метод `String()` возвращает имя состояния.

```
Follower ──тайм-аут выборов──▶ PreCandidate ──кворум PreVote──▶ Candidate ──кворум RequestVote──▶ Leader
   ▲                              │ потеря кворума/больший терм     │ больший терм                  │
   └──────────────────────────────┴─────────────────────────────────┴───────────────────────────────┘
                                     любое состояние ── Stop() ──▶ Dead
```

Выборы реализованы в `raft_cm_election.go`:

- **Ведомый → кандидат предварительного голосования.** При истечении
  тайм-аута выборов без сообщений от лидера таймер `runElectionTimer` запускает `runPreCandidate`.
  Узел переходит в `PreCandidate`, НЕ увеличивая собственный терм и НЕ сохраняется
  состояние: предварительное голосование (pre-vote) не увеличивает терм
  узла и не сбрасывает таймеры соседей, поэтому отделённый узел не
  разрушает работу кластера.
- **Сбор ответов.** `collectPreVoteReplies` собирает ответы RequestPreVote; 
  кворум положительных ответов приводит к `startElectionAfterPreVote`
  — узел выдерживает случайную паузу (до 50 мс, чтобы несколько узлов не начали выборы одновременно)
  и запускает настоящие выборы. Потеря кворума или больший терм возвращают
  узел в `Follower` через `becomeFollowerLocked`.
- **Кандидат.** `startElectionLocked` увеличивает терм, голосует за
  себя и рассылает `RequestVote` всем голосующим. Кворум голосов — переход
  в `Leader`; одиночный узел побеждает немедленно.
- **Шаг вниз.** При большем терме от другого узла (в ответах RequestVote и
  RequestPreVote — `becomeFollowerLocked`) узел становится
  ведомым; при истечении тайм-аута предварительного голосования без кворума
  — также возвращается в ведомые.
- **Связь с границами таймеров.** Инвариант `Reelection >= 10 * Heartbeat`
  гарантирует, что ведомый не начнёт выборы между пульсами живого лидера;
  случайный тайм-аут выборов `electionTimeoutLocked` возвращает
  длительность из диапазона [база; 2·база) — встроенная защита от
  одновременных выборов без кворума (split vote).
- **Наблюдение.** `Report()` возвращает `(id, term, isLeader)` 
  — точка наблюдения состояния автомата извне.

## 7. Клиентский путь

```go
func (cm *ConsensusModule) Apply(command any, timeout time.Duration) ApplyFuture
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture
```

`Apply` отправляет команду в Raft и возвращает
`ApplyFuture`; команда будет применена к машине состояний после фиксации.
Аргумент `timeout` ограничивает только постановку в канал `applyCh`: по истечении возвращается 
future с ошибкой `contract.ErrEnqueueTimeout`.
На не-лидере — `ErrNotLeader`, во время передачи лидерства — `ErrLeadershipTransferInProgress`,
после остановки узла — `contract.ErrRaftShutdown`.
Команда сохраняется по ссылке: вызывающий не должен изменять её после вызова.

```go
func (cm *ConsensusModule) VerifyLeader() Future
func (s *Server) VerifyLeader() Future
```

`VerifyLeader` проверяет, что узел всё ещё лидер, по механизму ReadIndex (Raft, §8):
подтверждение кворума без записи в журнал. На не-лидере немедленно возвращается future с `ErrNotLeader`;
порог кворума назначается от набора голосующих актуальной конфигурации.

```go
func (cm *ConsensusModule) LeadershipTransfer(targetID ServerID) LeadershipTransferFuture
```

`LeadershipTransfer` инициирует корректную передачу лидерства указанному
узлу: проверки (цель не сам узел; цель — голосующий; передача ещё не идёт)
и постановка future в канал передачи. Возвращает future с `ErrNotLeader`
на не-лидере и с `ErrLeadershipTransferInProgress`, если передача уже
идёт; результат ожидается через `LeadershipTransferFuture`.

## 8. RPC-обработчики

Обработчики входящих RPC консенсусного модуля (сигнатуры — часть
экспортируемого API, согласованная с контрактом транспорта):

```go
func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error
func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error
func (cm *ConsensusModule) RequestPreVote(args RequestPreVoteArgs, reply *RequestPreVoteReply) error
```

- `AppendEntries` — запрос лидера на добавление записей журнала (§5.3).
  Граница долговечности: ответ `Success: true` отправляется только после
  того, как принятые записи стали долговечными на ведомом, — лидер вправе
  зафиксировать запись по такому ответу.
- `RequestVote` — запрос голоса (§5.4.1). Кандидаты, не являющиеся
  голосующими в текущей конфигурации, отклоняются.
- `RequestPreVote` — предварительное голосование (§4 Pre-Vote): не меняет
  `votedFor` и `currentTerm`, не персистит состояние, не переводит узел в
  ведомые при большем терме; дополнительно проверяет, знает ли получатель
  о действующем лидере.

Отправку RPC выполняет интерфейс `Transport` (раздел 13): транспортные
ошибки (`contract.ErrNotReachable`, `contract.ErrRaftShutdown`,
`contract.ErrEnqueueTimeout`) возвращаются в `err`, логические ошибки Raft
(больший терм, конфликт журнала) — в `reply` при `err == nil`.

## 9. Членство

```go
func (cm *ConsensusModule) AddVoter(id ServerID, addr ServerAddress) IndexFuture
func (cm *ConsensusModule) AddNonvoter(id ServerID, addr ServerAddress) IndexFuture
func (cm *ConsensusModule) DemoteVoter(id ServerID) IndexFuture
func (cm *ConsensusModule) RemoveServer(id ServerID) IndexFuture
func (cm *ConsensusModule) GetConfiguration() ConfigurationFuture
```

`AddVoter` добавляет голосующего или обновляет адрес существующего;
`AddNonvoter` — неголосующего; `DemoteVoter` понижает голосующего до
неголосующего; `RemoveServer` удаляет сервер из конфигурации.
`GetConfiguration` возвращает текущую конфигурацию: для лидера — latest,
для ведомого — committed. Изменения состава проводятся через запись
`LogConfiguration` в журнале.

Тип команды изменения состава:

```go
type ConfigurationChangeCommand int

const (
	AddVoter ConfigurationChangeCommand = iota + 1
	AddNonvoter
	DemoteVoter
	RemoveServer
)
```

Кодирование конфигурации для записи в журнал (формат gob):

```go
func EncodeConfiguration(c Configuration) ([]byte, error)
func DecodeConfiguration(data []byte) (Configuration, error)
```

## 10. Снимки

```go
func (cm *ConsensusModule) SetSnapshotConfig(threshold, trailing int, interval time.Duration)
```

`SetSnapshotConfig` (`raft_cm_snapshot.go`) обновляет параметры снимков
(порог числа записей, число замыкающих записей, интервал проверки) и
сигнализирует циклу снимков о необходимости проверить, нужно ли создать
снимок.

Инфраструктура снимков — прозрачные алиасы из `pkg/raft/contract`
(детали — раздел 13 и godoc `SnapshotStore`):

- `SnapshotStore` — хранилище снимков: `Create`, `List`, `Open`;
- `FSMSnapshot` — снимок состояния машины: `Persist(SnapshotSink) error`,
  `Release()`;
- `SnapshotSink` — получатель данных снимка (`io.WriteCloser` + `ID`,
  `Cancel`);
- `SnapshotMeta` — метаданные снимка (индекс, терм, конфигурация, размер).

## 11. Сервер Server

`Server` — тонкая обёртка над транспортом и консенсусным модулем для
совместимости с `cmd/main.go` и `pkg/kvservice`.

```go
func New(cfg *Config, ready <-chan any) *Server
```

`New` (`server.go`) создаёт сервер по конфигурации; предусловие —
`cfg.Transport != nil` (паника при нарушении, как и в
`NewConsensusModule`).

```go
func (s *Server) Serve()
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture
func (s *Server) ConnectToPeer(peerID int, addr net.Addr) error
func (s *Server) ConnectToPeerWithTimeout(peerID int, addr net.Addr, _ time.Duration) error
func (s *Server) DisconnectPeer(peerID int) error
func (s *Server) DisconnectAll()
func (s *Server) GetListenAddr() net.Addr
func (s *Server) IsLeader() bool
func (s *Server) VerifyLeader() Future
func (s *Server) Shutdown()
```

`Serve` создаёт консенсусный модуль поверх транспорта,
нормализует таймеры (значение ≤ 0 заменяется умолчанием), проверяет их
`ValidateTiming` (при нарушении — немедленный отказ) и применяет параметры
снимков. `ConnectToPeerWithTimeout` сохраняет адрес соседа в
транспорте (подключение ленивое, при первом RPC; аргумент тайм-аута
игнорируется), `ConnectToPeer` — его сокращение. `DisconnectPeer`
отключает соседа, `DisconnectAll` — всех. `GetListenAddr`
возвращает адрес прослушивания транспорта; `IsLeader` —
признак лидера (через `Report`). `Shutdown` останавливает сервер:
`cm.Stop()` (с присоединением горутин), затем закрытие транспорта.

## 12. Конфигурации и контракты

### Config

```go
type Config struct {
	ApplyBatchInterval time.Duration
	Fsm FSM
	HeartbeatTimeout time.Duration
	PeerAddresses map[int]net.Addr
	PeerIds       []int
	ServerID      int
	ReelectionTimeout time.Duration
	SnapshotInterval time.Duration
	SnapshotStore SnapshotStore
	SnapshotThreshold int
	Storage Storage
	TickerTimeout time.Duration
	Transport TransportManager
}
```

Конфигурация для создания нового сервера. Таймерные поля и параметры
снимков со значением 0 заменяются умолчаниями; `SnapshotStore == nil`
отключает снимки; `Transport` создаётся вызывающим (предусловие
`New`/`Serve`).

### TimerConfig

```go
type TimerConfig struct {
	ApplyBatch time.Duration
	Heartbeat time.Duration
	Reelection time.Duration
	Ticker time.Duration
}
```

Набор временных параметров узла; нулевое или отрицательное значение —
умолчание соответствующей константы `Default*`. Проверяется
`ValidateTiming`.

### TraceConfig и SetTrace

```go
type TraceConfig struct {
	Level int
	LogFile string
}
func SetTrace(cfg TraceConfig) error
```

`SetTrace` конфигурирует трассировку консенсусного модуля: порог
детализации `Level` (0 — выключена) и назначение вывода `LogFile` (пустая
строка — стандартный логгер). Контракт строгий set-once: успешный вызов
возможен ровно один раз на процесс и только до создания первого
консенсусного модуля; повторный вызов (в том числе после создания модуля) —
детерминированная ошибка. Ошибка ввода-вывода окно конфигурации не
расходует.

### CommitEntry

```go
type CommitEntry struct {
	Data any
	Index int
	Term int
}
```

Данные, которые Raft отправляет в канал фиксации: команда, индекс журнала
и терм фиксации.

### CMState

Тип роли узла — см. раздел 6 (`Follower`/`PreCandidate`/`Candidate`/
`Leader`/`Dead`, метод `String()`).

### CommitChannelFSM (только для тестов)

```go
type CommitChannelFSM struct{ /* ... */ }
func NewCommitChannelFSM(commitChan chan<- CommitEntry) *CommitChannelFSM
func (f *CommitChannelFSM) Apply(log *LogEntry) any
func (f *CommitChannelFSM) Restore(_ io.ReadCloser) error
func (f *CommitChannelFSM) Snapshot() (FSMSnapshot, error)
```

Обёртка канала фиксации в интерфейс `FSM` для обратной совместимости с
тестовой инфраструктурой: передаёт `log.Data` в канал. Test-only: канал
обязан иметь читателя при живом узле (`Apply` не должен блокироваться
навсегда, иначе `Stop` зависнет).

## 13. Интерфейсы

```go
type FSM interface {
	Apply(*LogEntry) any
	Snapshot() (FSMSnapshot, error)
	Restore(io.ReadCloser) error
}
```

Машина состояний клиента: `Apply` применяет зафиксированную запись,
`Snapshot` возвращает снимок состояния (быстрый захват; сериализация —
в `FSMSnapshot.Persist`), `Restore` восстанавливает состояние из снимка,
сбрасывая предыдущее. Записи передаются как собственные копии; изменять
payload после передачи нельзя.

```go
type BatchingFSM interface {
	FSM
	ApplyBatch([]*LogEntry) []any
}
```

Необязательное расширение `FSM` для группового применения записей: длина
среза ответов обязана совпадать с длиной входа, иначе все future пакета
завершаются с `ErrBatchFSMResponseMismatch`.

```go
type Storage interface {
	Set(key string, value []byte)
	Get(key string) ([]byte, bool)
	HasData() bool
}
```

Поставщик постоянного хранилища ключ-значение; `Set` сохраняет копию,
`Get` возвращает защитную копию.

```go
type Transport interface {
	Consumer() <-chan RPC
	AppendEntries(ServerID, AppendEntriesArgs) (AppendEntriesReply, error)
	RequestVote(ServerID, RequestVoteArgs) (RequestVoteReply, error)
	RequestPreVote(ServerID, RequestPreVoteArgs) (RequestPreVoteReply, error)
	TimeoutNow(ServerID, TimeoutNowRequest) (TimeoutNowResponse, error)
	InstallSnapshot(ServerID, InstallSnapshotRequest, io.Reader) (InstallSnapshotResponse, error)
	AppendEntriesPipeline(ServerID) (AppendPipeline, error)
	SetHeartbeatHandler(func(RPC))
	LocalAddr() ServerAddress
}
```

Абстракция сетевого уровня: отправка RPC соседям и приём входящих через
`Consumer()`. Все методы потокобезопасны; после закрытия транспорта методы
возвращают `contract.ErrRaftShutdown`. `AppendEntries` и `RequestVote`
блокирующие; `RequestPreVote` и `InstallSnapshot` в текущей реализации
транспорта возвращают `contract.ErrNotImplemented` для неподдерживаемых
веток.

```go
type TransportManager interface {
	Transport
	Connect(peerID ServerID, addr string)
	Disconnect(peerID ServerID)
	DisconnectAll()
	Close()
}
```

Транспорт с управлением соединениями: адресная книга соседей и закрытие.
Реализация — `TCPTransport` в `pkg/raft/transp` (листовой пакет, в
справочнике не описывается).

```go
type AppendPipeline interface {
	AppendEntries(AppendEntriesArgs) (AppendEntriesReply, error)
	Consumer() <-chan RPCResponse
	Close() error
}
```

Конвейерная отправка `AppendEntries` без ожидания ответа; в текущей
реализации получение конвейера возвращает `contract.ErrNotImplemented`.

## 14. Иерархия Future

```go
type Future interface {
	Error() error
	ErrorCh() <-chan error
}
type IndexFuture interface {
	Future
	Index() int
}
type ApplyFuture interface {
	IndexFuture
	Response() any
}
type ConfigurationFuture interface {
	IndexFuture
	Configuration() Configuration
}
type LeadershipTransferFuture interface {
	Future
}
```

`Future` — асинхронная операция: `Error` — блокирующее ожидание ошибки,
`ErrorCh` — канал ошибки для `select` без горутин. `IndexFuture` добавляет
индекс журнала; `ApplyFuture` — ответ машины состояний;
`ConfigurationFuture` — конфигурацию кластера; `LeadershipTransferFuture`
ожидает результат передачи лидерства (возвращается `LeadershipTransfer`,
см. ниже).

```go
type SnapshotFuture struct{ /* ... */ }
func (d *SnapshotFuture) Error() error
func (d *SnapshotFuture) ErrorCh() <-chan error
```

Публичный future пользовательского запроса снимка.

## 15. RPC-структуры и доменные типы

Все перечисленные ниже типы — прозрачные алиасы объявлений из
`pkg/raft/contract` (`aliases.go`); владелец — пакет `contract`.

| Тип                                                  | Владелец | Назначение                                                                 |
|------------------------------------------------------|----------|----------------------------------------------------------------------------|
| `RPCHeader`                                          | contract | Общий заголовок RPC: версия протокола и идентификатор отправителя.         |
| `WithRPCHeader`                                      | contract | Интерфейс получения `RPCHeader` из RPC-сообщения.                          |
| `RequestVoteArgs` / `RequestVoteReply`               | contract | Аргументы и ответ запроса голоса.                                          |
| `RequestPreVoteArgs` / `RequestPreVoteReply`         | contract | Аргументы и ответ предварительного голосования.                            |
| `AppendEntriesArgs` / `AppendEntriesReply`           | contract | Аргументы и ответ добавления записей (включая поля разрешения конфликтов). |
| `InstallSnapshotRequest` / `InstallSnapshotResponse` | contract | Запрос и ответ установки снимка.                                           |
| `TimeoutNowRequest` / `TimeoutNowResponse`           | contract | Запрос и ответ немедленного начала выборов (передача лидерства).           |
| `RPC`                                                | contract | Входящий RPC-запрос из `Consumer()`: команда, поток данных, канал ответа.  |
| `RPCResponse`                                        | contract | Ответ на RPC: `Reply` либо `Error`.                                        |
| `LogEntry`                                           | contract | Запись журнала: индекс, терм, тип, данные.                                 |
| `LogType`                                            | contract | Тип записи журнала (см. раздел 1).                                         |
| `Configuration`                                      | contract | Состав кластера.                                                           |
| `ConfigServer`                                       | contract | Один сервер конфигурации: идентификатор, адрес, право голоса.              |
| `ServerSuffrage`                                     | contract | Право голоса: `Voter`/`Nonvoter`.                                          |
| `ServerID`                                           | contract | Идентификатор сервера.                                                     |
| `ServerAddress`                                      | contract | Адрес сервера (строка).                                                    |

## 16. Утилиты

```go
func ValidateTiming(tc TimerConfig) error
func EncodeConfiguration(c Configuration) ([]byte, error)
func DecodeConfiguration(data []byte) (Configuration, error)
func SetTrace(cfg TraceConfig) error
func IsNilInterface(v any) bool
func RandomInt(m int64) (int64, error)
```

- `ValidateTiming` — проверка таймеров (раздел 2);
- `EncodeConfiguration` / `DecodeConfiguration` — кодирование конфигурации
  в gob и обратно (раздел 9);
- `SetTrace` — конфигурация трассировки (раздел 12);
- `IsNilInterface` — истинно для пустого интерфейса и для типизированного
  nil (nil-указатель, nil-канал и т. п.) — обнаруживается рефлексией;
  используется предусловием `NewConsensusModule` и `New`;
- `RandomInt` — равномерно распределённое целое из диапазона.

## Синхронизация версий

Русская версия (`docs/API-ConsensusModule.md`) — основная. Английский
двойник `docs/API-ConsensusModule-en.md` переводится в той же задаче, что и
любое изменение фактов русской версии (сигнатура, константа, сообщение
ошибки, таблица границ). Изменение только английской версии без изменения
русской не допускается, кроме исправления качества перевода.
