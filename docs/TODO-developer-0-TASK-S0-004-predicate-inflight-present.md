# Q3.4 STABILIZATION-0. Разработчик — TASK-S0-004 (предикат наличия inflight)

## Промпт
Запусти subagent `developer` для реализации ОДНОЙ задачи TASK-S0-004
(веха STABILIZATION-0, решение ADR-S0-001 — Accepted).
Это задача 4 из 5; выполняется строго ПОСЛЕ TASK-S0-003; остальные
задачи этого запуска НЕ трогай.

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

Эта задача — `raft_cm_replication.go`, находка BC-001 (пример
владельца :176): в defer горутины репликации условие
`ownsInflight && inflightAE != nil && inflightAE[peerID] != nil`
смешивает владение флагом и структурные nil-проверки. Пара nil-
проверок уходит в предикат `inflightPresentLocked`; всё, что связано
с протоколом владения, ОСТАЁТСЯ видимым инлайн. Второе применение
той же пары — первый guard `redispatchVerifyIfPendingLocked`
(исходно :249).

Здесь живёт инвариант «на одного соседа не более одной живой горутины
репликации» (комментарии исходно :158–168): сброс флага — только
горутиной-владельцем, захват — только через CompareAndSwap.

## ПРЕДУСЛОВИЯ (проверь ДО правки кода)

1. SA-вердикт: `.ai/senior-architect/review.md`, раздел «Раунд 2»,
   §6 = APPROVED; `.ai/architect/decisions.md` — ADR-S0-001 Accepted.
   Иначе: ARCHITECTURE_NOT_APPROVED, код не трогай.
2. Последовательность: `.ai/developer/task-report-S0-003.md`
   завершён IMPLEMENTATION_COMPLETE. Иначе — STOP с сообщением
   TASK_ORDER_VIOLATION (порядок 001→…→005 обязателен).
3. Идемпотентность: если `task-report-S0-004.md` уже завершён
   COMPLETE — не повторяй, доложи статус.
4. В рабочем дереве незакоммиченный дифф задач 001–003: НЕ откатывай
   и не правь его; твой дифф — поверх.
5. Номера строк в `raft_cm_replication.go` УЖЕ сдвинуты задачей 003:
   ищи места по ИМЕНИ функции и тексту условия «Было» из задачи
   (defer в `leaderSendAEsToPeer`; первый guard в
   `redispatchVerifyIfPendingLocked`), а не по номерам.

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ ПЕРЕД ИЗМЕНЕНИЕМ КОДА

- AGENTS.md (дисциплина блокировок: суффикс Locked + «Требует
  удержания cm.mu»; терминология)
- CLAUDE.md
- .ai/architect/decisions.md — разделы: Status, «Изменения по ревью
  SA», «Паттерн рефакторинга» (п.1), «Политика сохранения
  поведения», «Критерии приёмки»
- .ai/architect/tasks/TASK-S0-004-predicate-inflight-present.md —
  ЦЕЛИКОМ (особенно Target State: точный текст предиката и doc;
  Risks)
- Код: `raft_cm_replication.go` — `leaderSendAEsToPeer` (defer),
  `redispatchVerifyIfPendingLocked`, комментарии протокола владения

## ЗАДАЧА (точный текст — в Target State задачи)

1. Новый метод на `*ConsensusModule`:

```go
// <doc из задачи: наличие записи флага отправки для соседа;
// существование флага НЕ даёт права его сбрасывать — флаг
// захватывается только через CompareAndSwap(false, true), сброс
// выполняет только горутина-владелец. Требует удержания cm.mu.>
func (cm *ConsensusModule) inflightPresentLocked(peerID int) bool {
    <пара nil-проверек из задачи>
}
```

2. Defer `leaderSendAEsToPeer`: `ownsInflight` остаётся ПЕРВЫМ
   операндом инлайн; хвост `cm.leaderState.inflightAE != nil &&
   cm.leaderState.inflightAE[peerID] != nil` заменяется на
   `cm.inflightPresentLocked(peerID)`.
3. `redispatchVerifyIfPendingLocked`: первый guard (исходно :249)
   — та же замена пары.
4. НЕизменно: границы `cm.mu.Lock()/Unlock()` в defer, порядок
   операторов, пара «`Store(false)` →
   `redispatchVerifyIfPendingLocked`» в одной критической секции,
   условие `plan != _replicationSkip`.
5. Комментарии протокола владения (исходно :158–168) и guard'а
   (исходно :247–253) сохраняются (или обоснование переносится
   в doc предиката — по тексту задачи).

## КЛЮЧЕВЫЕ КОНТРОЛИ

- Предикат cm.mu НЕ берёт: defer вызывает его под УЖЕ взятым
  Lock — самозахват немедленно дедлокнит все отправки
  (непереходность `sync.Mutex`). Реальные барьеры контракта:
  неизменность мест вызова внутри существующих критических секций
  (ревью диффа) + `go test -race .`.
- Индексирование nil-карты в Go безопасно; замена пары тождественна
  по де Моргану без знаков сравнения.
- Прочие nil-проверки inflight (`leaderSendAEs`,
  `leaderSendAEsToPeerIfIdle` — исходно :499/:529) НЕ трогать.

## КРИТЕРИИ ПРИЁМКИ

- [ ] `ownsInflight` — первый операнд инлайн; тело defer не
      перестроено; Lock/Unlock — на местах.
- [ ] Doc предиката — с оговоркой о владении флагом и «Требует
      удержания cm.mu».
- [ ] `go build -gcflags=-m . 2>&1 | grep inflightPresentLocked`
      — «can inline» (вывод приложить).
- [ ] `go test -run TestLockOwnershipDiscipline .` — зелёный.
- [ ] Целевой прогон (поимённо, `-race`, отдельно от полного):

      go test -run 'TestInflightAE_DeferReset|TestInflightAE_DeferResetOnSnapshotPath|TestInflightAE_NilSafe|TestInflightAE_NonOwnerDoesNotClearFlag|TestInflightAE_SingleReplicationGoroutineUnderConcurrentPaths|TestBecomeFollower_InflightAE_NilEntry' -race .

- [ ] gofmt/goimports по `raft_cm_replication.go` — без замечаний.
- [ ] `golangci-lint run --verbose` — без новых замечаний.
- [ ] `go test .` и `go test ./pkg/...` — зелёные (НЕ `./...`;
      до ~15 минут, таймауты с запасом; -count ≤ 3).
- [ ] **Без коммита**: дифф остаётся в рабочем дереве.

## ЗАПРЕТЫ

- НЕ перестраивать протокол владения флагом; НЕ объединять условие
  `plan != _replicationSkip` с предикатом.
- НЕ трогать одиночные nil-проверки в `leaderSendAEs` /
  `leaderSendAEsToPeerIfIdle` и циклические проверки
  (`raft_cm_election.go`, `raft_cm_leader.go`).
- Измеримого бенчмарк-гейта на изменяемый код НЕТ — НЕ добавлять
  бенчмарки.
- Новые тесты не добавлять; существующие не ослаблять.

## ПОСЛЕ ПРАВКИ — ВАЖНО

Номера строк снова сдвинутся: задача 005 сверяется по имени функции
и тексту «Было» — сообщи в отчёте величину суммарного сдвига.

## ОБРАБОТКА АРХИТЕКТУРНЫХ ПРОБЛЕМ

При противоречии между кодом, задачей, ADR или ревью — НЕ решай сам:
создай ARCHITECTURE_BLOCKER (место, суть), останови этот участок.

## GIT DIFF REVIEW

```bash
    git diff --stat
    git diff
```

Нет случайных изменений, debug-кода, изменений вне scope (дифф задач
001–003 не повреждён). **НЕ коммить** — коммитит владелец; строка
«Без коммита» обязательна в шапке отчёта.

## ФИНАЛЬНЫЙ ОТЧЁТ

`.ai/developer/task-report-S0-004.md` (шапка: задача, статус,
«Без коммита»): что изменено (включая сдвиг строк); «can inline»;
команды и результаты (lint, целевые тесты -race, полный прогон,
TestLockOwnershipDiscipline); ревью собственного git diff; блокеры;
остаточные риски.

## ФИНАЛЬНЫЙ СТАТУС

Один из: IMPLEMENTATION_COMPLETE / IMPLEMENTATION_BLOCKED /
IMPLEMENTATION_FAILED. COMPLETE — только при всех зелёных критериях.

## ФИНАЛЬНОЕ СООБЩЕНИЕ МНЕ

Кратко: статус; изменённые файлы; команды и результаты; «can inline»;
подтверждение «Без коммита».
