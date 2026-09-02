# Q9.2 STABILIZATION-0. Разработчик — TASK-S0-009 (инъекция транспорта в Server)

## Промпт
Запусти subagent `developer` для реализации ОДНОЙ задачи TASK-S0-009
(веха STABILIZATION-0, решение ADR-S0-003 — Accepted).
Это задача 2 из 5; выполняется строго ПОСЛЕ TASK-S0-008; остальные
задачи этого запуска НЕ трогай.

## Роль
Ты — Senior Go Developer, реализуешь УТВЕРЖДЁННОЕ архитектурное
решение (SA-ревью: раунды 3–5, финал APPROVED).

- НЕ выполняй собственное архитектурное проектирование вместо Architect.
- НЕ меняй архитектурные решения самостоятельно.
- Производственный код изменяй ТОЛЬКО в объёме, предписанном задачей.

## КОНТЕКСТ

ADR-S0-003: `server.go` перестаёт ссылаться на конкретные реализации.
Эта задача: интерфейс `TransportManager` (Transport + Connect/
Disconnect/DisconnectAll/Close) в корне; `Config.Transport`; удаление
`NewServer` (вызовов нет) и мёртвых полей `TCPRPCTimeout`/`MaxPool`/
`RPCAddress`; валидация `isNilInterface(cfg.Transport)` в `New`
(паника с внятным текстом); удаление пяти nil-страховок (четыре
`!= nil` + одна `== nil` в `GetListenAddr`); `Serve()` без аргумента;
`cmd/main.go` — транспорт создаётся ПОСЛЕ хранилищ, непосредственно
перед `kvservice.New`, с defer-владением; `kvservice.NewKVService` —
инъекция.

Зафиксированные отличия (единственные допустимые): перенос фатала
транспорта к вызывающему; окно слушателя сокращено до прежнего.

## SA-ГЕЙТ И ПРЕДУСЛОВИЯ

1. review-round-5.md §5 = APPROVED; ADR-S0-003 Accepted. Иначе —
   ARCHITECTURE_NOT_APPROVED.
2. `.ai/developer/task-report-S0-008.md` завершён
   IMPLEMENTATION_COMPLETE. Иначе — STOP, TASK_ORDER_VIOLATION.
3. Идемпотентность: свой отчёт уже COMPLETE — не повторяй.
4. В рабочем дереве незакоммиченный дифф задачи 008 (или она уже
   закоммичена владельцем): НЕ откатывай; твой дифф — поверх.
5. Места правок ищи по именам и тексту «Было» из задачи (строки
   могли сдвинуться задачей 008).

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ ПЕРЕД ИЗМЕНЕНИЕМ КОДА

- AGENTS.md, CLAUDE.md
- .ai/architect/decisions.md — ADR-S0-003: Decision п.3, Политика,
  подраздел ревью (SA-404/405/410/411/414/418/419 — все закрыты,
  их решения вшиты в задачу)
- .ai/architect/tasks/TASK-S0-009-server-transport-injection.md —
  ЦЕЛИКОМ (объявление интерфейса с комментарием; блоки «SA-418»
  с контуром заглушки и полным литералом соседа; таблица
  диагностических строк; все гейты)
- Код: server.go (целиком), server_test.go, cmd/main.go (runWith),
  cmd/main_test.go:68–75, pkg/kvservice/kvservice.go (New/NewKVService),
  utils.go (isNilInterface), transport.go (интерфейс)

## ПОРЯДОК (исчерпывающий контракт — в задаче)

1. Введи `TransportManager` + compile-time assert
   `var _ TransportManager = (*TCPTransport)(nil)` (текст задачи).
2. `Config`: +`Transport TransportManager`; УДАЛИ `TCPRPCTimeout`,
   `MaxPool`, `RPCAddress`; `New` — валидация isNilInterface.
3. `Server`: поле `transport TransportManager`; удали пять
   страховок; `Serve()` без аргумента; `Shutdown` — без изменений
   порядка (`cm.Stop()` → `transport.Close()`).
4. Удали `NewServer`; `kvservice` — инъекция по задаче.
5. `cmd/main.go` (runWith): транспорт ПОСЛЕ `NewFileStorage`/
   `NewFileSnapshotStore`, перед `kvservice.New`; `ownTransport` +
   defer со снятием после передачи владения (шаблон задачи).
6. Тесты поимённо: заглушка `stubTransportManager` в server_test.go
   (контур 13 методов из задачи ДОСЛОВНО); `TestNewServer` —
   `Transport: stubTransportManager{}`; `TestRunWithPeerConnect` —
   полный литерал соседа из блока SA-418 п.2 (включая
   `Storage: raft.NewMapStorage()` — идентификатор ЭТАПА 009,
   в 010 переквалифицируется); судьбы остальных тестов — по задаче.
7. Диагностические строки — строго по таблице задачи.
8. gofmt/goimports; критерии; git diff; отчёт.

## КЛЮЧЕВЫЕ КОНТРОЛИ

- Производственный корень больше НЕ импортирует конкретные
  реализации: `grep -n "NewTCPTransport\|TCPTransport" server.go` —
  только интерфейс/assert (гейт задачи).
- `grep -rn "\braft\.NewServer\b\|^func NewServer(" --include='*.go'
  --exclude-dir='part*' .` — пусто (НЕ трогай httptest.NewServer!).
- `grep -cn "s.transport [=!]= nil" server.go` — 0.
- Эквивалентность дефолтов: `NewTCPTransport(":0", 0, 0)` даёт те же
  191 мс / maxPool=2, что подставлял NewServer (тело конструктора).
- Заглушка БЕЗ слушателя и горутин; тела методов — нулевые значения.

## КРИТЕРИИ ПРИЁМКИ (общие; полные — в задаче)

- [ ] Все grep-гейты задачи; `go vet .`; `golangci-lint` без новых;
- [ ] `go test .` и `go test ./pkg/...` зелёные; целевые
      `-race`-прогоны из Tests задачи;
- [ ] Промежуточная зелёность: build+tests после задачи;
- [ ] **Без коммита**.

## ЗАПРЕТЫ

- НЕ менять порядок `Shutdown`, тексты `NewTCPTransport`, сигнатуры
  интерфейса за пределами задачи.
- НЕ чинить httptest.NewServer; НЕ трогать part*.
- Никаких «заодно» (стиль, переименования сверх задачи).

## ОБРАБОТКА АРХИТЕКТУРНЫХ ПРОБЛЕМ

Противоречие — ARCHITECTURE_BLOCKER (место, суть), стоп участка.

## GIT DIFF REVIEW + ОТЧЁТ

Ревью диффа; **НЕ коммить**; отчёт `.ai/developer/task-report-S0-009.md`
(шапка с «Без коммита»; изменённое; судьбы тестов поимённо;
гейты с выводом; команды/результаты; блокеры; риски).

## ФИНАЛЬНЫЙ СТАТУС

IMPLEMENTATION_COMPLETE / BLOCKED / FAILED.

## ФИНАЛЬНОЕ СООБЩЕНИЕ МНЕ

Кратко: статус; гейты (NewServer пуст, страховки 0, server.go без
конкретных типов); тесты судьбами; команды и результаты; блокеры;
«Без коммита».
