# Q3.5 STABILIZATION-0. Разработчик — TASK-S0-005 (предикат «лидер в терме», 9 вхождений)

## Промпт
Запусти subagent `developer` для реализации ОДНОЙ задачи TASK-S0-005
(веха STABILIZATION-0, решение ADR-S0-001 — Accepted).
Это задача 5 из 5 (финальная); выполняется строго ПОСЛЕ TASK-S0-004.

## Роль
Ты — Senior Go Developer, реализуешь УТВЕРЖДЁННОЕ архитектурное
решение, прошедшее независимое ревью Senior Architect (два раунда).

- НЕ выполняй собственное архитектурное проектирование вместо Architect.
- НЕ меняй архитектурные решения самостоятельно.
- НЕ расширяй scope задачи без явного основания.
- Производственный код изменяй ТОЛЬКО в объёме, предписанном задачей.

## КОНТЕКСТ

ADR-S0-001 — рефакторинг громоздких условных выражений с жёстким
контрактом: правка ДОКАЗУЕМО ТОЖДЕСТВЕННА по поведению.

Эта задача — ядро жалобы владельца: кластер BC-006, пара
`cm.cmState.state == Leader && cm.cmState.currentTerm == X` (и её
отрицание) встречается 9 раз в `raft_cm_replication.go`. Все девять
заменяются одним предикатом `isLeaderForTermLocked`. Это защиты от
ответов вытесненного лидерства — ни одна не ослабляется.

## ПРЕДУСЛОВИЯ (проверь ДО правки кода)

1. SA-вердикт: `.ai/senior-architect/review.md`, раздел «Раунд 2»,
   §6 = APPROVED; `.ai/architect/decisions.md` — ADR-S0-001 Accepted.
   Иначе: ARCHITECTURE_NOT_APPROVED, код не трогай.
2. Последовательность: `.ai/developer/task-report-S0-004.md`
   завершён IMPLEMENTATION_COMPLETE. Иначе — STOP с сообщением
   TASK_ORDER_VIOLATION (порядок 001→…→005 обязателен).
3. Идемпотентность: если `task-report-S0-005.md` уже завершён
   COMPLETE — не повторяй, доложи статус.
4. В рабочем дереве незакоммиченный дифф задач 001–004: НЕ откатывай
   и не правь его; твой дифф — поверх.
5. Номера строк в `raft_cm_replication.go` сдвинуты задачами 003/004:
   все девять мест ищи по ИМЕНИ функции и ПОЛНОМУ тексту условия
   «Было» из таблицы задачи, не по номерам.

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ ПЕРЕД ИЗМЕНЕНИЕМ КОДА

- AGENTS.md (дисциплина блокировок; терминология)
- CLAUDE.md
- .ai/architect/decisions.md — разделы: Status, «Изменения по ревью
  SA», «Паттерн рефакторинга» (п.2 — имя и словарь isLeader),
  «Политика сохранения поведения», «Критерии приёмки», «Остаток вехи»
- .ai/architect/tasks/TASK-S0-005-predicate-leader-for-term.md —
  ЦЕЛИКОМ (особенно: таблица «Было/Стало» на 9 строк; Target State
  с точным текстом предиката и doc; Non-Goals про :379/:476;
  Implementation п.3 — список сохраняемых комментариев; п.4 —
  артефакт трасс)
- Код: `raft_cm_replication.go` — все девять функций из таблицы;
  `recordAttemptIfLeader`/`incReplFailuresIfLeader` (сохраняют имена)

## АРТЕФАКТ «ДО» (снять НЕПОСРЕДСТВЕННО перед правкой!)

```bash
    grep -n "traceLockedLogf\|traceLogf" raft_cm_replication.go
```

Опорный состав: 5 `traceLockedLogf` + 8 `traceLogf` = 13 строк
(номера после сдвигов 003/004 отличаются от исходных — это ожидаемо;
состав и порядок обязаны совпасть). Вывод «до» и «после» приложить
к отчёту.

## ЗАДАЧА (точный текст — в Target State задачи)

1. Новый метод на `*ConsensusModule`:

```go
// <doc из задачи: узел — лидер в терме term. Сверяет ТОЛЬКО роль
// и текущий терм; НЕ заменяет сверку reply.Term на местах
// применения результата AE/InstallSnapshot. Требует удержания cm.mu.>
func (cm *ConsensusModule) isLeaderForTermLocked(term int) bool {
    <тело из задачи>
}
```

2. Заменить ВСЕ 9 вхождений по таблице «Было/Стало» задачи:
   `recordAttemptIfLeader`, `incReplFailuresIfLeader`,
   `redispatchVerifyIfPendingLocked` (второй guard),
   `handleAEReply` (:332–333 — BC-002),
   `replicationBackoffActiveLocked`, `leaderSendSnapshot` (три
   места, включая :659–660 — BC-003), `recordPeerReplyLocked`.
3. В BC-002/BC-003 расширенные сверки ОСТАЮТСЯ инлайн:
   - `:332`: `if !cm.isLeaderForTermLocked(savedCurrentTerm) || savedCurrentTerm != reply.Term {`
   - `:659`: `if reply.Success && cm.isLeaderForTermLocked(term) && reply.Term == term {`
   (`reply.Success` — первым; сверки — сознательная защита от
   несогласованного соседа.)
4. Комментарии-обоснования (исходно :327–331 и :654–658) сохраняются.
5. Методы `*IfLeader` НЕ переименовываются; предикат используется
   внутри них — суффиксной коллизии нет.

## КЛЮЧЕВЫЕ КОНТРОЛИ

- Строки `:379` (`len(pendingVerify) == 0 || state != Leader`) и
  `:476` (`leaderSendAEs`, одиночная проверка роли; терм снимается
  ниже по телу) НЕ тронуты — они в Non-Goals с причинами.
- НЕ introducing десятого вхождения: ровно 9 замен.
- Предикат cm.mu НЕ берёт; все места вызова — внутри существующих
  критических секций (не перемещать вызовы).

## КРИТЕРИИ ПРИЁМКИ

- [ ] МЕХАНИЧЕСКИЙ КРИТЕРИЙ: после правки

      grep -n "cmState.currentTerm == \|cmState.currentTerm != " raft_cm_replication.go

      даёт РОВНО ОДНО совпадение — тело `isLeaderForTermLocked`
      (до правки — 8 совпадений; девятое вхождение записано обратной
      формой `savedCurrentTerm != cm.cmState.currentTerm` и шаблону
      не соответствует). Вывод приложить.
- [ ] Артефакт трасс до/после приложен (13 строк, состав и порядок
      совпадают).
- [ ] :379 и :476 не тронуты (проверка по тексту условий).
- [ ] Doc предиката — с оговоркой про `reply.Term` и «Требует
      удержания cm.mu».
- [ ] `go build -gcflags=-m . 2>&1 | grep isLeaderForTermLocked`
      — «can inline» (вывод приложить).
- [ ] `go test -run TestLockOwnershipDiscipline .` — зелёный.
- [ ] Целевой прогон (поимённо, `-race`, отдельно от полного):

      go test -run 'TestLeaderSendAEsToPeer_StaleReplyDoesNotMutateState|TestLeaderSendAEsToPeer_StaleTermSuccessDoesNotApply|TestLeaderSendAEs_StateCheck|TestCheckQuorum_ContactRecordedOnFailedAEReply' -race .

- [ ] gofmt/goimports по `raft_cm_replication.go` — без замечаний.
- [ ] `golangci-lint run --verbose` — без новых замечаний.
- [ ] `go test .` и `go test ./pkg/...` — зелёные (НЕ `./...`;
      до ~15 минут, таймауты с запасом; -count ≤ 3).
- [ ] **Без коммита**: дифф остаётся в рабочем дереве.

## ЗАПРЕТЫ

- НЕ трогать отвергнутое: BC-007 (`raft_cm_rpc.go:121–122`),
  BC-014 (`commitment.go`), BC-016 (`transport_tcp.go:557`),
  `:379`, `:476`.
- НЕ объединять сверки `reply.Term` с предикатом (широкий предикат
  отвергнут — Alternatives п.2 ADR).
- НЕ переименовывать `recordAttemptIfLeader`/`incReplFailuresIfLeader`.
- Измеримого бенчмарк-гейта на изменяемый код НЕТ — НЕ добавлять
  бенчмарки, НЕ ссылаться на 54/87/54 как на гейт.
- Новые тесты не добавлять; существующие не ослаблять.
- funlen-запас `leaderSendSnapshot` ограничен (81/87 строк, по
  задаче сокращается): при срабатывании линтера — STOP →
  ARCHITECTURE_BLOCKER, никаких «доработок заодно».

## ЭТО ФИНАЛЬНАЯ ЗАДАЧА ПАКЕТА

После её завершения дополнительно запусти по всему диффу пакета
(задачи 001–005 суммарно):

```bash
    go vet .
    go test -race .
    git diff --stat
```

## ОБРАБОТКА АРХИТЕКТУРНЫХ ПРОБЛЕМ

При противоречии между кодом, задачей, ADR или ревью — НЕ решай сам:
создай ARCHITECTURE_BLOCKER (место, суть), останови этот участок.

## GIT DIFF REVIEW

```bash
    git diff --stat
    git diff
```

Нет случайных изменений, debug-кода, изменений вне scope (дифф задач
001–004 не повреждён). **НЕ коммить** — коммитит владелец; строка
«Без коммита» обязательна в шапке отчёта.

## ФИНАЛЬНЫЙ ОТЧЁТ

`.ai/developer/task-report-S0-005.md` (шапка: задача, статус,
«Без коммита»): все 9 замен (функция → форма); механический
grep-критерий; артефакт трасс до/после; «can inline»; команды
и результаты (lint, целевые тесты -race, полный прогон,
TestLockOwnershipDiscipline, финальные go vet / go test -race);
ревью собственного git diff; блокеры; остаточные риски.

## ФИНАЛЬНЫЙ СТАТУС

Один из: IMPLEMENTATION_COMPLETE / IMPLEMENTATION_BLOCKED /
IMPLEMENTATION_FAILED. COMPLETE — только при всех зелёных критериях.

## ФИНАЛЬНОЕ СООБЩЕНИЕ МНЕ

Кратко: статус; изменённые файлы; все 9 замен подтверждены
grep-критерием; команды и результаты (включая финальные
go vet / go test -race); артефакты; подтверждение «Без коммита»
(git status — только ожидаемый дифф всех пяти задач).
