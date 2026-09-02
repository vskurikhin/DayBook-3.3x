# Q9.3 STABILIZATION-0. Разработчик — TASK-S0-010 (пакет pkg/raft/store)

## Промпт
Запусти subagent `developer` для реализации ОДНОЙ задачи TASK-S0-010
(веха STABILIZATION-0, решение ADR-S0-003 — Accepted).
Это задача 3 из 5; выполняется строго ПОСЛЕ TASK-S0-009; остальные
задачи этого запуска НЕ трогай.

## Роль
Ты — Senior Go Developer, реализуешь УТВЕРЖДЁННОЕ архитектурное
решение (SA-ревью: раунды 3–5, финал APPROVED).

- НЕ выполняй собственное архитектурное проектирование вместо Architect.
- НЕ меняй архитектурные решения самостоятельно.
- Производственный код изменяй ТОЛЬКО в объёме, предписанном задачей.

## КОНТЕКСТ

ADR-S0-003: перенос хранилищ в `pkg/raft/store` (пакет импортирует
только contract + std; тесты — ещё и raft, это легально после 009).
Эта задача: переносятся storage.go, file_storage.go,
snapshot_store.go, file_snapshot_store.go + 5 тестовых файлов;
переименования по запросу владельца: `FileSnapshotStore`→`FileSnapshot`,
`InmemSnapshotStore`→`InmemSnapshot`, `NewFileSnapshotStore`→
`NewFileSnapshot`, `NewInmemSnapshotStore`→`NewInmemSnapshot`;
экспорт `writeCount`→`WriteCount`; разрез `file_storage_test.go`
(13 дисковых тестов → store; `TestFileStorage_RestartRestoresState` →
корень, новый `raft_cm_file_storage_test.go`); хелперы по таблице;
квалификация 24 остающихся тестов корня (включая tcp_raft_test.go)
+ `pkg/raft/tracetest/{redirect,reset}` + `cmd/main_test.go` +
`pkg/...`; пакетный doc-комментарий store (обязательный пункт).

Контракт: чистые перемещения; тексты ошибок и дисковые константы
дословно; `data/node-*` читаются без конвертации.

## SA-ГЕЙТ И ПРЕДУСЛОВИЯ

1. review-round-5.md §5 = APPROVED; ADR-S0-003 Accepted. Иначе —
   ARCHITECTURE_NOT_APPROVED.
2. `.ai/developer/task-report-S0-009.md` завершён
   IMPLEMENTATION_COMPLETE. Иначе — STOP, TASK_ORDER_VIOLATION.
3. Идемпотентность: свой отчёт уже COMPLETE — не повторяй.
4. Незакоммиченный дифф задач 008–009 в дереве: НЕ откатывай;
   твой дифф — поверх. Якоря — имена/текст «Было» (строки сдвинуты).
5. `grep -rn "\bNewMapStorage(\|\[\]\*MapStorage\b" *_test.go |
   grep -v 'store\.'` сейчас НЕ пуст (62 строки) — это ДО-состояние
   гейта; после задачи обязан опустеть.

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ ПЕРЕД ИЗМЕНЕНИЕМ КОДА

- AGENTS.md, CLAUDE.md
- .ai/architect/decisions.md — ADR-S0-003: Decision п.4, Data,
  Политика; подраздел ревью (SA-403/406/417 — решения вшиты в задачу)
- .ai/architect/tasks/TASK-S0-010-store-package-extraction.md —
  ЦЕЛИКОМ (таблицы «Было→Стало» по переименованиям; разрез
  file_storage_test.go поимённо 13+1; таблица хелперов; перечни
  24 корневых тестов + «Вне корня»; doc-тексты; все гейты)
- Код: четыре переносимых файла; file_storage_test.go (состав:
  14 TestFileStorage_* + gobEncode/gobDecode/mustGet);
  tcp_raft_test.go:30/41/73; pkg/raft/tracetest/{redirect,reset};
  cmd/main_test.go

## ПОРЯДОК (исчерпывающий контракт — в задаче)

1. Базовые grep «до» по переименованиям; сверка с таблицей
   (расхождение = STOP → ARCHITECTURE_BLOCKER).
2. Перенос четырёх файлов в pkg/raft/store (git mv); переименования
   по таблицам; экспорт WriteCount; doc-пакета (текст задачи);
   перенос 5 тестовых файлов по перечню; разрез
   file_storage_test.go 13+1; хелперы по таблице (gobEncode/
   gobDecode — определение в корневом raft_cm_file_storage_test.go,
   копия в store; benchLogEntries — store/копия в корне).
3. Квалификация потребителей: 24 корневых теста (включая
   tcp_raft_test.go) + cmd/main.go + cmd/main_test.go (Storage:
   raft.NewMapStorage → store.NewMapStorage) + pkg/тесты по перечню
   («Вне корня»: tracetest ×2, kvservice-тесты, pkg/harness).
4. gofmt/goimports; критерии; git diff; отчёт.

## КЛЮЧЕВЫЕ КОНТРОЛИ

- Производственный корень НЕ импортирует store (грей задачи);
  store импортирует contract (+ std), его тесты — ещё и raft.
- Гейт неквалифицированных: `grep -rn "\bNewMapStorage(\|\[\]\*MapStorage\b"
  *_test.go | grep -v 'store\.'` — ПУСТО после правки.
- `grep -c "func TestFileStorage_" pkg/raft/store/file_storage_test.go`
  = 13; `TestFileStorage_GobCompatibility` — в store.
- Промежуточная зелёность: `go build ./...` + `go test .` +
  `go test ./pkg/...` ПОСЛЕ задачи (точка цикла: raft[test]→store
  легальна, ибо производственный raft не импортирует store —
  проверено задачей 009).
- Дисковые константы/имена ключей/суффиксы — дословно; тексты
  ошибок FileStorage — дословно.

## КРИТЕРИИ ПРИЁМКИ (общие; полные — в задаче)

- [ ] Гейты задачи (grep старых имён с `--exclude-dir='part*'`
      `--exclude-dir='.doc'` — пусто; гейт MapStorage — пусто);
- [ ] `go vet . ./pkg/raft/store`; `golangci-lint` без новых;
- [ ] `go test .` и `go test ./pkg/...` зелёные; целевые
      `-race`-прогоны Tests задачи, ВКЛЮЧАЯ
      `go test -race ./pkg/raft/tracetest/...`;
- [ ] **Без коммита**.

## ЗАПРЕТЫ

- НЕ переносить транспорты (задача 011); НЕ менять тела функций.
- НЕ терять тесты: 14 TestFileStorage_* = 13 (store) + 1 (корень);
  судьба каждого — по перечню задачи.
- Учебные part* не трогать; никаких «заодно».

## ОБРАБОТКА АРХИТЕКТУРНЫХ ПРОБЛЕМ

Противоречие (в т.ч. вхождение вне таблиц) — ARCHITECTURE_BLOCKER,
стоп участка.

## GIT DIFF REVIEW + ОТЧЁТ

`git diff --stat` (переносимые файлы — статус R; рекомендация
рецензента: `git diff -M -C --find-copies-harder --stat`) — только
предписанное. **НЕ коммить**; отчёт `.ai/developer/task-report-S0-010.md`
(шапка «Без коммита»; разрез 13+1 подтверждён; гейты с выводом;
команды/результаты; блокеры; риски).

## ФИНАЛЬНЫЙ СТАТУС

IMPLEMENTATION_COMPLETE / BLOCKED / FAILED.

## ФИНАЛЬНОЕ СООБЩЕНИЕ МНЕ

Кратко: статус; разрез 13+1; гейты (старые имена пусто, MapStorage
пусто); 24+вне-корня переквалифицированы; команды и результаты;
блокеры; «Без коммита».
