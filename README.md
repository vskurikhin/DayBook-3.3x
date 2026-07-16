# Analysis Step 1 — Project Structure and Architecture

## Module

`vskurikhin/raft` — Go 1.26.4, единственная зависимость `github.com/fortytw2/leaktest` (vendored).

## Структура каталогов

```
raft/
├── .golangci.yml          # конфигурация линтера
├── AGENTS.md               # правила для AI-агентов
├── go.mod / go.sum
├── cmd/main.go             # точка входа, CLI-сервер
├── internal/
│   ├── config/config.go    # парсинг флагов
│   └── raft/
│       ├── raft.go          # ядро Raft (383 строки)
│       ├── server.go        # RPC-обёртка (195 строк)
│       ├── testharness.go   # тестовая инфраструктура (157 строк)
│       └── raft_test.go     # тесты (166 строк)
├── vendor/                  # leaktest
└── docs/                    # (новая директория)
```

## Ключевые типы и отношения

```
ConsensusModule (ядро консенсуса)
  ├── содержит: id, term, votedFor, log[], state, electionResetEvent
  ├── методы: RequestVote, AppendEntries, startElection, startLeader
  └── владеет *Server

Server (RPC-обёртка)
  ├── содержит: rpcServer, listener, peerClients, cm, rpcProxy
  ├── методы: Serve, ConnectToPeer, Call, Shutdown
  └── содержит RPCProxy → проксирует RPC с искусственной задержкой/потерей

RPCProxy — обёртка для регистрации в net/rpc, добавляет:
  - 1-5ms случайной задержки
  - при RAFT_UNRELIABLE_RPC: 10% дроп, 10% задержка 75ms
```

## Охват алгоритма Raft

Реализована **только leader election** (Section 5.2). Репликация лога определена в структурах (`LogEntry`, `AppendEntriesArgs`) но **не реализована**:
- `AppendEntries` handler только сбрасывает election timer — логи не добавляются
- `RequestVote` игнорирует `LastLogIndex`/`LastLogTerm` при голосовании
- `log[]` хранится, но никогда не изменяется

## Поток исполнения

```
cmd/main.go
  → config.ParseFlags() → Values{Address, Number, Peers}
  → raft.NewServer(id, peers, ready)
  → Server.Serve(addr)
      → NewConsensusModule(id, peers, server, ready)
          → goroutine: runElectionTimer()  [150-300ms рандом]
              → startElection()            [Candidate → RequestVote всем]
              → startLeader()              [heartbeat 50ms всем]
      → rpc.RegisterName("ConsensusModule", RPCProxy{cm})
      → net.Listen + accept loop
  → ConnectToPeer(peerId, addr) [retry 9 раз]
```

## Состояния

```
Follower → (election timeout) → Candidate → (majority votes) → Leader
Любое состояние → (Stop) → Dead
Leader/Candidate → (higher term в reply) → Follower
```

## Тесты (8 тестов, бело-ящичные)

| Тест                                       | Узлов | Что проверяет                                |
|--------------------------------------------|-------|----------------------------------------------|
| TestElectionBasic                          | 3     | Один лидер в стабильном кластере             |
| TestElectionLeaderDisconnect               | 3     | Отключение лидера → новый лидер              |
| TestElectionLeaderAndAnotherDisconnect     | 3     | Потеря кворума → нет лидера → восстановление |
| TestDisconnectAllThenRestore               | 3     | Полный разрыв → восстановление               |
| TestElectionLeaderDisconnectThenReconnect  | 3     | Старый лидер вернулся → не reclaim           |
| TestElectionLeaderDisconnectThenReconnect5 | 5     | То же, 5 узлов + leaktest                    |
| TestElectionFollowerComesBack              | 3     | Follower отсутствовал долго → term растёт    |
| TestElectionDisconnectLoop                 | 3     | 5 циклов partition/recovery                  |

Паттерн: `NewHarness` → `CheckSingleLeader` → манипуляции → `CheckSingleLeader/CheckNoLeader` → `Shutdown`.

## Покрытие (пробелы)

- Репликация лога / коммит
- `RAFT_UNRELIABLE_RPC` и `RAFT_FORCE_MORE_REELECTION`
- Состояние `Dead` / `Stop()`
- `Report()`
- Крупные кластеры (>5)
- Конкурентные client-запросы
- Персистентность / восстановление

## Инфраструктура тестов (`Harness`)

- Создаёт n реальных Raft-серверов с TCP-соединениями на случайных портах (`:0`)
- `DisconnectPeer` — двусторонняя изоляция
- `ReconnectPeer` — двустороннее восстановление
- `CheckSingleLeader` — опрос до 5 раз с интервалом 150ms
- `CheckNoLeader` —assert отсутствия лидера

## Зависимости

Runtime: **ноль внешних** — только stdlib (`net/rpc`, `sync`, `net`, `math/rand`, `flag`).
Тесты: `github.com/fortytw2/leaktest` v1.3.0 (vendored).

## Сборка

```
go build ./cmd/main.go               # бинарник
go test ./internal/raft/             # тесты
go vet ./...                         # статический анализ
golangci-lint run ./...              # линтинг
```

## Вывод

Проект — минимальная, педагогическая реализация Raft, покрывающая только leader election. ~700 строк не-тестового Go-кода. Код чистый, идиоматичный, хорошо протестирован. Фундамент готов для расширения: репликации лога, коммитов, персистентности.
