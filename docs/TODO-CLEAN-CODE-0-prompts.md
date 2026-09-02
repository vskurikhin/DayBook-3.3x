# Q0 CLEAN-CODE-0. Концепция и цели milestone: сопровождаемость, ужесточение линтера
___


# Q3
Подготовь промпт для архитектора по иcправлению замечаний ревью .ai/senior-architect/review.md.
Шаблон промпта [TODO-architect-template-prompt.md](./docs/TODO-architect-template-prompt.md) . 
Запиши в файл docs/TODO-architect-0-Linter-fix-by-review.md
___


# Q4
@senior-architect архитектор отработал рауд 2.
## ОСНОВНАЯ ЦЕЛЬ
Твоя задача — проверить работу Architect по предыдущим замечаниям .ai/senior-architect/review.md.
Твоя задача — быть последним архитектурным барьером перед передачей
TASK Developer.
Нужно определить:
1. действительно ли исправлены все замечания;
2. не маскируют ли решения production defects;
3. не создают ли новые проблемы concurrency / durability / Raft correctness;
4. может ли Developer реализовать решение без принятия новых
   архитектурных решений;
5. достаточно ли доказательств для перехода к implementation.
   Production code НЕ изменять.
   Architecture НЕ исправлять самостоятельно.
   TASK НЕ переписывать.
   Все найденные проблемы оформить в:
   .ai/senior-architect/review.md

## Должно быть в результате
Обнови:
.ai/senior-architect/review.md
В конце ОБЯЗАТЕЛЬНО:
APPROVED
CHANGES_REQUIRED

После вердикта (если APPROVED):
укажи явно, что ADR могут быть переведены Architect'ом из PROPOSED в принятый статус
и что TASK-* передаются Developer'у (порядок обязателен).
___


# Q5
Запусти subagent `architect`.
Разработчик для задачи TASK-CC-003 поставил ARCHITECTURE_BLOCKER.
Нужно локально решить проблему с "мутация 2 FAIL (блокер)".
Исправь постановку, выбери решение.
___


# Q6
@developer
архитектор исправил задачу
.ai/architect/tasks/TASK-CC-003-start-election-decomposition.md
заверши её реализацию.

## ФИНАЛЬНЫЙ ОТЧЁТ

Обнови `.ai/developer/task-report-CC-003.md` на хорошем понятном русском
языке: таблица метрик AC-1 по обеим функциям (lines/statements/gocognit/
nestif, измерено, с использованными командами), выводы четырёх
осевых прогонов AC-2 и полного AC-3, результат серии AC-5 (10 из 10),
**исходы мутаций AC-7**, чек-лист AC-8 по каждому пункту «Что менять
нельзя». В финальном сообщении кратко: выполненные пункты, изменённые
файлы, запущенные команды и результаты, `golangci-lint run`, `go vet`,
были ли `ARCHITECTURE_BLOCKER`, какие риски остались.

## ФИНАЛЬНЫЙ СТАТУС

В конце укажи один статус: `IMPLEMENTATION_COMPLETE` /
`IMPLEMENTATION_BLOCKED` / `IMPLEMENTATION_FAILED`.
`IMPLEMENTATION_COMPLETE` — только если метод выделен дословно,
AC-1…AC-10 выполнены и перечисленные прогоны зелёные.

## ПРОВЕРЬ, КАКИЕ ЗАДАЧИ УЖЕ ВЫПОЛНЕНЫ

Перед началом проверь `.ai/developer/task-report-CC-*.md` и `git log`:
этот запуск — только TASK-CC-003 (004+ не трогай; если 003 уже
реализована — не повторяй, доложи статус). Отчёты пиши на хорошем
понятном русском языке.
___


# Q7
Run subagent `analyst`.
Read @docs/TODO-CLEAN-CODE-0-concept-goals.md.
Read the prompt from the file @docs/TODO-analyst-CG-4-uber-style-guide-audit.md and get to work.
___


# Q8
Подготовь промпт для разработчика TASK-CC-016.
Шаблон в

@TODO-developer-template-prompt.md
___


9. **Коммит 2**: только `pkg/api/api.go`; сообщение в стиле серии,
   например `JSON field tags`.
6. **Коммиты**: **пять — по одному на файл** (ADR-P08-050,
   решение 5); только кодовые файлы — `.ai/` в `.gitignore`
   (строка 209), отчёт не версионируется; сообщения в стиле серии
   (см. `git log`), например `Function order: kvservice`,
   `Function order: future`, `Function order: snapshot store`,
   `Function order: kvclient`, `Function order: tcp transport`.
7. **Без коммита**: коммитов не делай (ни в текущую ветку, ни
   в main) — дифф остаётся в рабочем дереве; владелец коммитит
   по отчёту (по ADR-P08-049, решение 5 — один коммит или два
   по логическим блокам: корневой пакет; `internal/config`);
   `.ai/` в `.gitignore` (строка 209), отчёт не версионируется.
