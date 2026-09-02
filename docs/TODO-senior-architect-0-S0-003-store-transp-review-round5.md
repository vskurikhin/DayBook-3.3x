# Q8 STABILIZATION-0. Сеньёр Архитектор — раунд 5 ADR-S0-003 (подтверждающая сверка)

## Промпт
Запусти subagent `senior-architect` для подтверждающего review
устранения замечаний раунда 4 по ADR-S0-003 (Architect отработал
C1–C3) вехи STABILIZATION-0, поднаправление «вынос store/transp».

Промпты предыдущих раундов:
раунд 3 (полный): [TODO-senior-architect-0-S0-003-store-transp-review.md](./TODO-senior-architect-0-S0-003-store-transp-review.md)
раунд 4 (верификация R1–R14): […-review-round4.md](./TODO-senior-architect-0-S0-003-store-transp-review-round4.md)

## Роль: SENIOR ARCHITECT, раунд 5 — ПОДТВЕРЖДАЮЩИЙ

Ты — тот же независимый Senior Architect. Предыстория:

- раунд 3 — CHANGES_REQUIRED (R1–R14);
- раунд 4 — CHANGES_REQUIRED узкий: R1–R14 закрыты все
  (SA-401…416 CLOSED/SUPERSEDED), остались SA-417 (HIGH:
  TASK-S0-010 потерял три файла) и SA-418 (MEDIUM: судьба двух
  тестов в 009) + LOW SA-419…421;
- твой же §7 раунда 4: «повторное полное ревью не требуется —
  достаточно сверки двух перечней (TASK-S0-009 «Тесты»,
  TASK-S0-010 «Current State»/«Files»); раунд 5 может быть
  подтверждающим»;
- Architect внёс C1–C3; координатор применил SA-421(а)
  к артефакту Analyst (§10 вопрос 8).

Твоя задача — сверить именно эти правки, убедиться, что они не
внесли новых проблем, и вынести финальный вердикт. Архитектура,
направление, R1–R14 и решения владельца — вне пересмотра.

Production code НЕ изменять.
Architecture НЕ исправлять самостоятельно.
TASK НЕ переписывать.

## ФОРМАТ ВЫВОДА

Создай новый файл:

    .ai/senior-architect/review-round-5.md

Заголовок: «ADR-S0-003 — раунд 5 (2026-09-XX), подтверждающий».
Не изменяй другие файлы, кроме созданного review-round-5.md.
Новые находки — SA-422+ (SA-417…421 заняты раундом 4).

## ОБЯЗАТЕЛЬНО ПРОЧИТАТЬ

Твой раунд 4 (норма для сверки):

    .ai/senior-architect/review-round-4.md  (§6 находки, §7 вердикт)

Правленные артефакты (дельта раунда 5 архитектора):

    .ai/architect/tasks/TASK-S0-009-server-transport-injection.md
    .ai/architect/tasks/TASK-S0-010-store-package-extraction.md
    .ai/architect/tasks/TASK-S0-011-transp-package-extraction.md
    .ai/architect/tasks/TASK-S0-012-docs-sync.md
    .ai/architect/decisions.md   (подраздел «Раунд 4» внутри
                                  «Изменения по ревью SA (раунд 3)»)
    .ai/architect/architecture.md (~строка 219)

Координаторская правка артефакта Analyst:

    .ai/analyst/stabilization-0-2026-09-02-store-transp-extraction.md
    (§10, вопрос 8 — правило полного пути метки)

Код — по местам новых утверждений:

    tcp_raft_test.go (:30, :41, :73), pkg/raft/tracetest/redirect/
    redirect_test.go:65, pkg/raft/tracetest/reset/reset_test.go:59,
    server_test.go:24–43 (TestNewServer), cmd/main_test.go:68–69
    (TestRunWithPeerConnect), server.go:142/157/167/176/208
    (страховки), transport.go (состав интерфейса Transport — для
    сверки контура заглушки).

## СВЕРКА (по твоим же Required action раунда 4)

### C1 (SA-417) — три файла в TASK-S0-010
- `tcp_raft_test.go` — в перечне квалификации `store.` (символы
  по коду: `[]*MapStorage` :30/:41, `NewMapStorage()` :73) и в Files
  (не только в «синхронизации комментариев»);
- оба `pkg/raft/tracetest/{redirect,reset}/*_test.go` — в «Вне
  корня» и Files, `raft.NewMapStorage` → `store.NewMapStorage`;
- счёт остающихся потребителей store = **24** (твой вывод:
  29 − 5 уезжающих) — во всех местах;
- гейт неквалифицированных вхождений:
  `grep -rn "\bNewMapStorage(\|\[\]\*MapStorage\b" *_test.go |
  grep -v 'store\.'` — пусто ПОСЛЕ правки; прогони ДО правки
  на текущем дереве и убедись, что гейт сейчас НЕ пуст (ловит
 (tcp_raft_test.go) — т.е. осмыслен);
- целевой прогон `go test -race ./pkg/raft/tracetest/...` —
  в критериях.

### C2 (SA-418) — судьба двух тестов в TASK-S0-009
- `TestNewServer`: заглушка `stubTransportManager` — контур в
  задаче полон и ТОЧНО соответствует интерфейсу `Transport` +
  расширениям `TransportManager` (сверь с transport.go и Target
  State задачи: все методы, включая Consumer/IsShutdown и
  Connect/Disconnect/DisconnectAll/Close); заглушка без слушателя
  и горутин; литерал `Transport: stubTransportManager{}` проходит
  `isNilInterface` (значение-структура);
- `TestRunWithPeerConnect`: полный литерал соседа — `raft.New` +
  `raft.NewTCPTransport(":0", 0, 0)` + `Storage:
  raft.NewMapStorage()` + `Serve()`; закрытие — существующий
  `t.Cleanup(peer.Shutdown)`; эквивалентность дефолтов (191 мс /
  maxPool) оговорена;
- последовательность 009→010 для `Storage:` соседа
  (`raft.NewMapStorage` → `store.NewMapStorage`) отражена в ОБОИХ
  задачах; `cmd/main_test.go` — в перечнях правимых 010.

### C3 (LOW, тем же проходом)
- SA-419: форма пятой страховки уточнена (`== nil` в
  `GetListenAddr`, server.go:176); гейт
  `grep -cn "s.transport [=!]= nil" server.go` — 0;
- SA-420: `--exclude-dir='part*'` (в 010/011 — и `.doc`) в
  критериях 009/010/011; старых `grep -v part`/`grep -v '/part'`
  не осталось — прочисти grep;
- SA-421(б): architecture.md ~:219 — «бывш. `InmemSnapshotStore`»;
- SA-421(в): конвенция квалификаторов («в тестах store/transp
  корневые символы — через `raft.`; листовые — через `contract.`»)
  — в ADR Decision п.6 и TASK-S0-012;
- SA-421(а): §10 вопрос 8 артефакта Analyst — правило полного
  пути метки (координаторская правка; сверь соответствие твоему
  Required action и согласованность с §8.1/RISK-005).

## НОВЫЕ ПРОБЛЕМЫ ПРАВОК (минимум)

1. Заглушка `stubTransportManager`: не привлечёт ли линтер
   (unused-методы реализованного интерфейса допустимы; unused
   тип — если тест использует литерал, используется); не
   расползается ли её контур за 14 методов;
2. Гейт C1 `grep -v 'store\.'` — не маскирует ли вхождения вида
   `store.NewMapStorage` (должен) и не пропускает ли
   `x.NewMapStorage` без квалификатора `store.`? — прогони;
3. Дельта не задела нетронутые разделы задач (избирательность
   правок — сравни структуру);
4. `git status --porcelain` — производственный код не изменён.

## СТАТУСЫ

SA-417, SA-418, SA-419, SA-420, SA-421 — CLOSED / PARTIALLY /
NOT CLOSED, одной строкой обоснование каждый.

## ФОРМАТ review-round-5.md (коротко)

    # ADR-S0-003 — раунд 5 (2026-09-XX), подтверждающий
    ## 1. Executive Summary
    ## 2. Сверка C1/C2/C3 (таблица: пункт → статус → evidence)
    ## 3. Статусы SA-417…SA-421
    ## 4. Новые находки (SA-422+; если нет — явно «новых нет»)
    ## 5. Final Verdict
    ## 6. Final Self-Review (5–7 вопросов)

## FINAL VERDICT

    APPROVED / CHANGES_REQUIRED

APPROVED — если C1 и C2 закрыты (сверены по коду), C3 внесён,
новых проблем нет, код не тронут.

### После APPROVED укажи явно

- ADR-S0-003 переводится Architect'ом из Proposed в Accepted;
- TASK-S0-008…012 передаются Developer'у строго последовательно
  008 → 009 → 010 → 011 → 012 (Dependencies на TASK-S0-007
  удовлетворены: закоммичено до 870f9d9), полный набор критериев
  после каждой задачи, БЕЗ коммита (дифф в рабочем дереве,
  коммитит владелец);
- golden-тест меток провода
  (`pkg/raft/transp/transport_wire_golden_test.go` +
  `testdata/wire_labels.txt`) — постоянный барьер контракта
  провода, обязан пережить веху как обязательный тест;
- смена номера раунда: это твоё пятое ревью (review.md раунды
  1–2, review-round-3/4/5) — файл нумерации не менять.

## ПРАВИЛА РАУНДА 5

- Максимальная краткость: это подтверждающая сверка, не полное
  ревью; всё, что вне C1–C3 и новых проблем правок, — вне scope.
- Проверяй по коду и grep (в т.ч. исполняй гейты на дереве),
  не по заявлениям документов.
- Лучше CHANGES_REQUIRED, чем преждевременный APPROVED — но
  объем замечаний раунда 5 должен соответствовать его механике:
  текстовые правки перечней не тянут на полное ревью.
