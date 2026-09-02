# Q9.4 STABILIZATION-0. Разработчик — TASK-S0-011 (пакет pkg/raft/transp + golden-тест провода)

## Промпт
Запусти subagent `developer` для реализации ОДНОЙ задачи TASK-S0-011
(веха STABILIZATION-0, решение ADR-S0-003 — Accepted).
Это задача 4 из 5; выполняется строго ПОСЛЕ TASK-S0-010; остальные
задачи этого запуска НЕ трогай.

## Роль
Ты — Senior Go Developer, реализуешь УТВЕРЖДЁННОЕ архитектурное
решение (SA-ревью: раунды 3–5, финал APPROVED).

- НЕ выполняй собственное архитектурное проектирование вместо Architect.
- НЕ меняй архитектурные решения самостоятельно.
- Производственный код изменяй ТОЛЬКО в объёме, предписанном задачей.

## КОНТЕКСТ

ADR-S0-003: перенос транспортов в `pkg/raft/transp` (импортирует
contract + std; тесты — ещё и raft). Эта задача: переносятся
transport_tcp.go, transport_inmem.go + 4 тестовых файла; экспорт
`_inmemTransportTimeout`→`InmemTransportTimeout`; init() с десятью
`gob.RegisterName` на полных метках переезжает с файлом (замены
гоб-регистраций НЕТ — метки из задачи 008 дословно); копии хелперов
`newInmemPair`/`newTCPTransportPair` в корневом
`transp_helpers_test.go`; ОБЯЗАТЕЛЬНЫЙ перенос инвариантов
`TestServeMaxPool`/`TestServeTCPRPCTimeout` → `transport_tcp_test.go`
(`TestTCPNewTransportMaxPool` + расширенный `TestTCPNewTransport`);
compile-time assert в server_test.go → `(*transp.TCPTransport)(nil)`;
пакетный doc-комментарий transp; правка корневых тестов по перечню
(включая `raft_cm_file_storage_test.go` → `transp.NewInmemTransport`).

ГЛАВНОЕ СОБЫТИЕ ЗАДАЧИ: **постоянный golden-тест меток провода**
`pkg/raft/transp/transport_wire_golden_test.go` +
`testdata/wire_labels.txt` (десять строк-эталонов `github.com/
vskurikhin/raft.<ИмяТипа>`) — барьер контракта провода, переживающий
веху. Его состав и критерий — в задаче (Implementation п.5).

## SA-ГЕЙТ И ПРЕДУСЛОВИЯ

1. review-round-5.md §5 = APPROVED; ADR-S0-003 Accepted. Иначе —
   ARCHITECTURE_NOT_APPROVED.
2. `.ai/developer/task-report-S0-010.md` завершён
   IMPLEMENTATION_COMPLETE. Иначе — STOP, TASK_ORDER_VIOLATION.
3. Идемпотентность: свой отчёт уже COMPLETE — не повторяй.
4. Незакоммиченный дифф задач 008–010: НЕ откатывай; поверх.
   Якоря — имена/текст «Было» (строки сдвинуты задачей 010).

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ ПЕРЕД ИЗМЕНЕНИЕМ КОДА

- AGENTS.md, CLAUDE.md
- .ai/architect/decisions.md — ADR-S0-003: Decision п.2 (метки),
  п.5; подраздел ревью (SA-405/406/409/414 — решения вшиты)
- .ai/architect/tasks/TASK-S0-011-transp-package-extraction.md —
  ЦЕЛИКОМ (перечни правимых корневых тестов; золотой тест —
  состав и критерий; таблица хелперов; переносы инвариантов;
  doc-текст; гейты)
- Код: transport_tcp.go (init, конструктор — подстановка дефолтов),
  transport_inmem.go, transport_contract_test.go, server_test.go
  (assert), raft_cm_file_storage_test.go (из 010)

## ПОРЯДОК (исчерпывающий контракт — в задаче)

1. Базовые grep «до» (`raft.NewTCPTransport`, `raft.NewInmemTransport`,
   `raft.TCPTransport`, `raft.InmemTransport`); сверка с таблицей.
2. Перенос transport_tcp.go + transport_inmem.go в pkg/raft/transp
   (git mv); `_defaultTCPRPCTimeout` → `contract.TCPRPCTimeout`;
   экспорт `InmemTransportTimeout`; doc-пакета.
3. Перенос 4 тестовых файлов; перенос инвариантов
   `TestTCPNewTransportMaxPool`/расширенного `TestTCPNewTransport`
   в transport_tcp_test.go — ОБЯЗАТЕЛЬНЫЙ элемент (SA-405).
4. Golden-тест: `transport_wire_golden_test.go` + генерация
   `testdata/wire_labels.txt` (десять эталонов; критерий — вхождение
   байт эталона метки в кодированный поток).
5. Хелперы: копии `newInmemPair`/`newTCPTransportPair` в корневом
   `transp_helpers_test.go` (с комментарием-дублем, по образцу
   задачи); assert в server_test.go → `(*transp.TCPTransport)(nil)`.
6. Квалификация корневых тестов по перечню (включая
   raft_cm_file_storage_test.go → transp.NewInmemTransport;
   новые файлы задач 009/010 — по списку задачи).
7. gofmt/goimports; критерии; git diff; отчёт.

## КЛЮЧЕВЫЕ КОНТРОЛИ

- Гейты: `grep -rn "raft\.NewTCPTransport\|raft\.NewInmemTransport\|
  raft\.TCPTransport\|raft\.InmemTransport" --include='*.go'
  --exclude-dir='part*' --exclude-dir='.doc' .` — пусто;
  `grep -rn "NewInmemTransport\|NewTCPTransport" pkg/raft/store` —
  пусто.
- Метки в init() — ПОСЛОВНО из задачи 008 (полный путь); проверка
  их фактического поведения — golden-тест (прогони, приложи вывод).
- `RAFT_UNRELIABLE_RPC` в производственном коде не читается — семантика
  не затрагивается (проверь, что не привнёс чтение).
- Промежуточная зелёность после задачи: build + полный набор тестов.

## КРИТЕРИИ ПРИЁМКИ (общие; полные — в задаче)

- [ ] Golden-тест создан и ЗЕЛЁНЫЙ (testdata приложена);
- [ ] Все grep-гейты задачи; `go vet . ./pkg/raft/transp`;
      `golangci-lint` без новых;
- [ ] `go test .` и `go test ./pkg/...` зелёные; целевые
      `-race`-прогоны Tests задачи;
- [ ] **Без коммита**.

## ЗАПРЕТЫ

- НЕ менять gob-метки, framing-байты `_rpc*`, тела функций,
  пул соединений, таймауты.
- НЕ удалять перенесённые инварианты дефолтов (это условие
  снятия SA-405 — TestTCPNewTransportMaxPool обязан существовать).
- Учебные part* не трогать; никаких «заодно».

## ОБРАБОТКА АРХИТЕКТУРНЫХ ПРОБЛЕМ

Противоречие — ARCHITECTURE_BLOCKER (место, суть), стоп участка.
Особенно: расхождение фактического кода с текстом «Было» (дерево
ушло вперёд).

## GIT DIFF REVIEW + ОТЧЁТ

Ревью диффа (переносимые — статус R). **НЕ коммить**; отчёт
`.ai/developer/task-report-S0-011.md` (шапка «Без коммита»;
golden-тест вывод; перенесённые инварианты поимённо; гейты;
команды/результаты; блокеры; риски).

## ФИНАЛЬНЫЙ СТАТУС

IMPLEMENTATION_COMPLETE / BLOCKED / FAILED.

## ФИНАЛЬНОЕ СООБЩЕНИЕ МНЕ

Кратко: статус; golden-тест зелёный (десять меток); инварианты
перенесены; гейты пусты; команды и результаты; блокеры; «Без коммита».
