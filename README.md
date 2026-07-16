# Module vskurikhin/raft

`vskurikhin/raft` — Go 1.26.4, единственная зависимость `github.com/fortytw2/leaktest` (vendored).

## Текущая структура каталогов

```
raft/
├── .golangci.yml
├── AGENTS.md
├── go.mod / go.sum
├── cmd/
│   ├── main.go                (49 строк)  ← создаёт storage, передаёт в NewServer
│   └── main_test.go           (109 строк)
├── internal/
│   └── config/
│       ├── config.go          (100 строк)
│       └── config_test.go     (170 строк)
├── pkg/raft/                  ← АКТИВНАЯ полная реализация (2397 строк)
│   ├── raft.go                (779 строк)  ← +persistence, fast conflict, triggerAE
│   ├── server.go              (267 строк)  ← +Storage, RPCProxy drop simulation
│   ├── storage.go             (47 строк)   ← NEW: Storage interface + MapStorage
│   ├── testharness.go         (370 строк)  ← +CrashPeer/RestartPeer
│   └── raft_test.go           (934 строк)  ← 25 тестов (+11 новых)
├── part1/raft/                ← ЛЕГАСИ (только election, 905 строк, без изменений)
│   ├── raft.go                (382 строк)
│   ├── server.go              (196 строк)
│   ├── testharness.go         (159 строк)
│   └── raft_test.go           (168 строк)
└── part2/raft/                ← NEW: снепшот Part 2 (1468 строк, для справки)
    ├── raft.go                (599 строк)
    ├── server.go              (198 строк)
    ├── testharness.go         (294 строк)
    └── raft_test.go           (377 строк)
```

**Всего**: 18 .go файлов, 5351 строка.

Part 3 (коммит `be4176f`, PR #9) добавил **2483 вставки, 77 удалений**:

| Файл                          | Было | Стало | Изменения                                       |
|-------------------------------|------|-------|--------------------------------------------------|
| `pkg/raft/raft.go`            | 591  | 779   | +persistence, fast conflict, triggerAE, Stop fix  |
| `pkg/raft/server.go`          | 198  | 267   | +Storage, RPCProxy drop simulation               |
| `pkg/raft/storage.go`         | —    | 47    | NEW: Storage interface + MapStorage              |
| `pkg/raft/testharness.go`     | 294  | 370   | +CrashPeer/RestartPeer, alive[], storage[]        |
| `pkg/raft/raft_test.go`       | 378  | 934   | 14→25 тестов (+11 новых)                          |
| `cmd/main.go`                 | 48   | 49    | +storage, передача в NewServer                   |
| `cmd/main_test.go`            | 108  | 109   | +storage в TestRunWithPeerConnect                |
| `part2/raft/*`                | —    | 4 файла | NEW: снепшот Part 2 для справки                |

## Новые возможности (Part 3)

### 1. Персистентность (`Storage` interface)
**`pkg/raft/storage.go:1-47`**
```go
type Storage interface {
    Put(key string, value []byte) error
    Get(key string) ([]byte, bool, error)
    HasData() bool
}
```
- `MapStorage` — in-memory реализация через `sync.Map`
- Все три persistent state (`currentTerm`, `votedFor`, `log`) кодируются через `gob` и сохраняются после каждой мутации
- `restoreFromStorage()` — восстанавливает состояние при старте, если `HasData() == true`
- `persistToStorage()` — вызывается после каждого изменения term/votedFor/log

### 2. Crash/Restart в тестовом harness
- `CrashPeer(id)` — дисконнект + shutdown, очистка commit history, storage сохраняется
- `RestartPeer(id)` — новый `Server` + `ConsensusModule` с тем же `MapStorage`
- `alive []bool` — отслеживание живых/упавших узлов

### 3. Fast Log Conflict Resolution (Section 5.3)
- `AppendEntriesReply` теперь содержит `ConflictIndex` и `ConflictTerm`
- Follower при несовпадении лога возвращает: терм конфликтующей записи и первый индекс этого терма
- Лидер использует эту информацию чтобы перепрыгнуть к последнему записи совпадающего терма (вместо декремента `nextIndex` по одной записи)

### 4. Trigger-based AppendEntries
- Добавлен `triggerAEChan` (буфер 1)
- Лидер отправляет AE немедленно при:
    - `Submit()` — новый entry
    - `AppendEntries` reply — `matchIndex` / `commitIndex` изменился
    - `RequestVote` — стал лидером
- Горутина лидера использует `select` на `triggerAEChan` + таймер 50ms

### 5. RPC Drop Simulation
- `RPCProxy` получил `numCallsBeforeDrop` счётчик
- `DropCallsAfterN(n)` / `DontDropCalls()` — для тестирования сценариев потери RPC
- `TestCommitAfterCallDrops` — лидер дропает 2 вызова, потом восстанавливается

### 6. Safety Fixes
- `becomeFollower` теперь сохраняет `votedFor` при переходе в тот же term (был баг: сбрасывал `votedFor = -1` всегда)
- `becomeFollower` сбрасывает `votedFor = -1` только если `term > cm.currentTerm`
- `TestBecomeFollowerSameTermPreservesVotedFor` — верификация
- `TestBecomeFollowerHigherTermResetsVotedFor` — верификация

### 7. Submit возвращает int
- `Submit(command) (int, bool)` — возвращает индекс в логе вместо `bool`

## Ключевые типы (обновлённые)

```go
// Storage — интерфейс персистентности
type Storage interface {
    Put(key string, value []byte) error
    Get(key string) ([]byte, bool, error)
    HasData() bool
}

// AppendEntriesReply — расширен для fast conflict resolution
type AppendEntriesReply struct {
    Term          int
    Success       bool
    ConflictIndex int    // NEW: первый индекс конфликтующего терма
    ConflictTerm  int    // NEW: терм конфликтующей записи
}

// ConsensusModule — добавлены поля:
//   storage Storage
//   triggerAEChan chan struct{}
//   newCommitReadyChanWg sync.WaitGroup
```

## Полный поток исполнения

```
cmd/main.go
  → config.ParseFlags() → Values{Address, Number, Peers}
  → storage := raft.NewMapStorage()
  → raft.NewServer(id, peers, ready, storage)
      → NewConsensusModule(id, peers, server, ready, storage)
          → если storage.HasData(): restoreFromStorage()
          → goroutine: runElectionTimer()
          → goroutine: leaderSendAEs()  [triggerAEChan + timer]
          → goroutine: commitChanSender()
      → rpc.RegisterName("ConsensusModule", RPCProxy{cm})
      → net.Listen + accept loop
  → ConnectToPeer(peerId, addr) [retry 9 раз]
```

## Замечания по качеству кода

### Сильные стороны
- Идиоматичный Go: мьютексы, каналы, горутины, правильные defer
- Чистое разделение: алгоритм ↔ сеть ↔ симуляция ↔ персистентность
- 25 тестов покрывают elections, commits, persistence, crash/recovery, safety
- Safety-critical баги исправлены (votedFor reset, startElection persist)
- Fast conflict resolution из Section 5.3 реализован и протестирован

### Замечания
1. `restoreFromStorage()` вызывает `log.Fatal` при отсутствии ключей (строки 217, 225, 233) — нет graceful handling
2. `persistToStorage()` использует `gob` без defensive checks — может запаниковать на не-кодируемых типах
3. `leaderSendAEs` использует ручной `Unlock()` вместо `defer` (строки 659-727) — хрупкий код
4. `part2/raft/` — замёрзший снепшот Part 2 без persistence; дублирует код
5. `part1/raft/` — легаси, не импортируется из `pkg/raft`, мёртвый код
6. `triggerAEChan` буфер 1 — второй триггер дропается (intentional, но может пропустить wakeup)
7. `go.mod` требует `go 1.26.4` — будущая версия, может не компилироваться текущим toolchain
8. Нет snapshot/compaction — лог растёт бесконечно
9. `bin/raft`, `coverage.out`, `cmd_coverage.out` закоммичены

## Вывод

Проект — **полная реализация Raft Consensus Algorithm** (Figure 2 paper) в `pkg/raft/` с:
- Leader election (Section 5.2)
- Log replication + commitment (Section 5.3)
- **Persistence** через `Storage` interface + `gob` encoding
- **Crash/recovery** в тестовом harness
- **Fast log conflict resolution** (Section 5.3 optimization)
- **Trigger-based AppendEntries**
- **RPC drop simulation** для тестов
- **Safety fixes** (votedFor preservation)
