package raft

import (
	"testing"
	"time"
)

// Матричные (table-driven) версии тестов из commitment_test.go.
// Сценарии, шаги и ожидания перенесены без изменений; комментарии
// перенесены дословно из исходного файла.

// mInt возвращает указатель на значение для полей-ожиданий шага.
func mInt(v int) *int { return &v }

// commitmentStep — один шаг сценария над трекером.
type commitmentStep struct {
	op     string // setMatch | commit | setConfig | recalc | fillCh | drainCh
	id     int
	value  int
	voters []int
	want   *int // ожидаемый getCommitIndex() после шага
}

// commitmentCase — строка матрицы: конфигурация трекера и шаги.
type commitmentCase struct {
	name         string
	selfID       int
	term         int
	startIdx     int
	initVoters   []int
	terms        map[int]int
	emptyTracker bool
	steps        []commitmentStep
	// проверки внутреннего состояния после всех шагов
	wantMatch      map[int]int
	wantMatchLen   *int
	wantMatchGone  []int
	wantStartIndex *int
}

func (c commitmentCase) run(t *testing.T) {
	t.Helper()
	commitCh := make(chan int, 1)
	lts := &lookupTermStub{terms: c.terms}
	var ct *commitmentTracker
	if c.emptyTracker {
		ct = &commitmentTracker{
			matchIndex:  map[int]int{},
			commitIndex: -1,
			selfID:      c.selfID,
			startIndex:  c.startIdx,
			commitCh:    commitCh,
			term:        c.term,
		}
	} else {
		ct = newCommitmentTracker(c.selfID, c.term, c.startIdx, commitCh)
		if len(c.initVoters) > 0 {
			ct.setConfiguration(c.initVoters, lts.lookup)
		}
	}
	for i, s := range c.steps {
		switch s.op {
		case "setMatch":
			ct.setMatch(s.id, s.value, lts.lookup)
		case "commit":
			ct.commit(s.value, lts.lookup)
		case "setConfig":
			ct.setConfiguration(s.voters, lts.lookup)
		case "recalc":
			ct.recalculateCommitIndex(lts.lookup)
		case "fillCh":
			commitCh <- s.value
		case "drainCh":
			select {
			case got := <-commitCh:
				if got != s.value {
					t.Fatalf("step %d (%s): commitCh = %d, want %d", i, s.op, got, s.value)
				}
			case <-time.After(time.Second):
				t.Fatalf("step %d (%s): timeout waiting for commitCh", i, s.op)
			}
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
		if s.want != nil {
			if got := ct.getCommitIndex(); got != *s.want {
				t.Fatalf("step %d (%s): getCommitIndex() = %d, want %d", i, s.op, got, *s.want)
			}
		}
	}
	for voter, want := range c.wantMatch {
		if got := ct.matchIndex[voter]; got != want {
			t.Fatalf("matchIndex[%d] = %d, want %d", voter, got, want)
		}
	}
	if c.wantMatchLen != nil {
		if got := len(ct.matchIndex); got != *c.wantMatchLen {
			t.Fatalf("len(matchIndex) = %d, want %d", got, *c.wantMatchLen)
		}
	}
	for _, voter := range c.wantMatchGone {
		if _, ok := ct.matchIndex[voter]; ok {
			t.Fatalf("matchIndex[%d] should not exist", voter)
		}
	}
	if c.wantStartIndex != nil {
		if got := ct.startIndex; got != *c.wantStartIndex {
			t.Fatalf("startIndex = %d, want %d", got, *c.wantStartIndex)
		}
	}
}

// --- 1. Стандартное большинство (3 узла) ---

func TestCommitmentMatrix_Majority(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "NoMatch",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			// Все matchIndex = -1 (начальное состояние), кворума нет.
			steps: []commitmentStep{
				{op: "recalc", want: mInt(-1)},
			},
		},
		{
			name:   "SelfOnly",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// Только сам лидер продвинулся — кворума нет (нужно 2 из 3).
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5, want: mInt(-1)},
			},
		},
		{
			name:   "SelfAndOnePeer",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// Лидер + один follower = 2 из 3 = кворум есть.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "AllNodes",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{10: 1},
			// Все три узла подтвердили — кворум 3/3.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 10},
				{op: "setMatch", id: 1, value: 10},
				{op: "setMatch", id: 2, value: 10, want: mInt(10)},
			},
		},
		{
			name:   "MedianOrdering_Odd",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3, 4},
			terms: map[int]int{-1: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1},
			// 5 узлов, разные значения matchIndex. Медиана = элемент [2] из 5.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 1},
				{op: "setMatch", id: 2, value: 2},
				{op: "setMatch", id: 3, value: 3},
				{op: "setMatch", id: 4, value: 4, want: mInt(3)},
			},
		},
		{
			name:   "MedianOrdering_Even",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3},
			terms: map[int]int{-1: 1, 1: 1, 2: 1, 3: 1},
			// 4 узла (чётное количество голосующих). matchIndex:
			// {0:3, 1:1, 2:2, 3:-1} -> sorted [-1, 1, 2, 3], len=4.
			// Кворумный индекс n-quorumSize(n) = 4-3 = 1 -> values[1] = 1.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 3},
				{op: "setMatch", id: 1, value: 1},
				{op: "setMatch", id: 2, value: 2},
				// sorted: [-1, 1, 2, 3], len=4, n-quorum = 1 -> values[1] = 1
				{op: "setMatch", id: 3, value: -1, want: mInt(1)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 2. Raft safety (проверка term и startIndex) ---

func TestCommitmentMatrix_Safety(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "DifferentTerm",
			selfID: 0, term: 2, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// term=2, но запись с индексом 5 принадлежит term=1 — фиксация невозможна.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(-1)},
			},
		},
		{
			name:   "SameTerm",
			selfID: 0, term: 2, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 2},
			// term=2 и запись с индексом 5 принадлежит term=2 — фиксация разрешена.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "MedianShifts",
			selfID: 0, term: 2, initVoters: []int{0, 1, 2, 3},
			terms: map[int]int{3: 1, 5: 2},
			// Медиана скачком переходит с отвергнутого индекса на принятый.
			// 4 voters (чётное n), term=2, terms = {3:1, 5:2}.
			steps: []commitmentStep{
				// Шаг 1: matchIndexes = {0:5, 1:3, 2:3, 3:-1}
				// sorted = [-1, 3, 3, 5], кворумный индекс 4-3=1 -> 3.
				// lookupTerm(3)=1 != 2 -> фиксация заблокирована.
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 3},
				{op: "setMatch", id: 2, value: 3, want: mInt(-1)},
				// Шаг 2: matchIndexes = {0:5, 1:3, 2:3, 3:5}
				// sorted = [3, 3, 5, 5], кворумный индекс 4-3=1 -> 3 — это 2/4,
				// кворума 3/4 нет: commitIndex остаётся -1.
				{op: "setMatch", id: 3, value: 5, want: mInt(-1)},
				// Шаг 3: matchIndexes = {0:5, 1:5, 2:3, 3:5}
				// sorted = [3, 5, 5, 5], кворумный индекс 1 -> 5, lookupTerm(5)=2 == term
				// -> 3/4 = кворум, commitIndex 5.
				{op: "setMatch", id: 1, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "StartIndex_BlocksCommit",
			selfID: 0, term: 2, startIdx: 10, initVoters: []int{0, 1, 2},
			terms: map[int]int{10: 2},
			// startIndex=10, median=10 >= 10 ✅, lookupTerm(10)=2 == term ✅
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 10},
				{op: "setMatch", id: 1, value: 10},
				{op: "setMatch", id: 2, value: 10, want: mInt(10)},
			},
		},
		{
			name:   "StartIndex_PreventsOld",
			selfID: 0, term: 2, startIdx: 10, initVoters: []int{0, 1, 2},
			terms: map[int]int{8: 2},
			// startIndex=10, median=8 < 10 — фиксация заблокирована.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 8},
				{op: "setMatch", id: 1, value: 8},
				{op: "setMatch", id: 2, value: 8, want: mInt(-1)},
			},
		},
		{
			name:   "StartIndex_Exact",
			selfID: 0, term: 2, startIdx: 5, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 2},
			// startIndex=5, median=5 >= 5 ✅
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 3. setConfiguration ---

func TestCommitmentMatrix_SetConfiguration(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "Basic",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{},
			// Установка конфигурации из трёх узлов после пустого конструктора.
			wantMatch:    map[int]int{0: -1, 1: -1, 2: -1},
			wantMatchLen: mInt(3),
		},
		{
			name:   "PreservesMatchIndex",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// Смена конфигурации: добавляем новый узел, удаляем старый,
			// matchIndex сохраняется для оставшихся.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 3},
				// Меняем конфигурацию: убираем узел 2, добавляем узел 3
				{op: "setConfig", voters: []int{0, 1, 3}},
			},
			wantMatch:     map[int]int{0: 5, 1: 3, 3: -1},
			wantMatchGone: []int{2},
		},
		{
			name:   "CommitAfterChange",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// После смены конфигурации commitIndex не сбрасывается.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
				// Меняем конфигурацию с 3 узлов на 2 узла
				{op: "setConfig", voters: []int{0, 1}, want: mInt(5)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 4. Продвижение commitIndex (incremental) ---

func TestCommitmentMatrix_Progressive(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "Basic",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{3: 1, 5: 1},
			// Постепенное продвижение commitIndex по мере получения matchIndex.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 3},
				// matchIndexes = {0:3, 1:3, 2:-1} -> sorted [-1, 3, 3], median=3
				{op: "setMatch", id: 1, value: 3, want: mInt(3)},
				{op: "setMatch", id: 2, value: 5},
				// matchIndexes = {0:3, 1:3, 2:5} -> sorted [3, 3, 5], median=3
				// 3 == ct.commitIndex, поэтому не продвигается
				{op: "recalc", want: mInt(3)},
			},
		},
		{
			name:   "LargeSteps",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{100: 1, 150: 1, 200: 1},
			// Продвижение с большими скачками matchIndex.
			steps: []commitmentStep{
				// matchIndexes = {0:-1, 1:100, 2:-1} -> sorted [-1, -1, 100], median=-1
				{op: "setMatch", id: 1, value: 100, want: mInt(-1)},
				// matchIndexes = {0:-1, 1:100, 2:200} -> sorted [-1, 100, 200], median=100
				{op: "setMatch", id: 2, value: 200, want: mInt(100)},
				// matchIndexes = {0:150, 1:100, 2:200} -> sorted [100, 150, 200], median=150
				{op: "setMatch", id: 0, value: 150, want: mInt(150)},
			},
		},
		{
			name:   "SelfCommit",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{10: 1},
			// self.commit(10) — обновили selfID, но соседи не подтвердили.
			steps: []commitmentStep{
				// sorted [-1, -1, 10], median=-1 — кворума нет
				{op: "commit", value: 10, want: mInt(-1)},
			},
		},
		{
			name:   "SelfCommit_SingleNode",
			selfID: 0, term: 1, initVoters: []int{0},
			terms: map[int]int{10: 1},
			// Одиночный узел: self.commit(10) → median=10 → кворум 1/1.
			steps: []commitmentStep{
				{op: "commit", value: 10, want: mInt(10)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 5. Граничные случаи setMatch ---

func TestCommitmentMatrix_SetMatchEdge(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "SetMatchDecreasingIgnored",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{10: 1, 5: 1},
			// Уменьшающийся matchIndex игнорируется (инвариант монотонности).
			steps: []commitmentStep{
				{op: "setMatch", id: 1, value: 10},
				{op: "setMatch", id: 1, value: 5},
			},
			wantMatch: map[int]int{1: 10},
		},
		{
			name:   "SetMatchUnknownPeer",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// setMatch для узла, не являющегося голосующим текущей конфигурации —
			// no-op (membership guard): ghost-ключ НЕ добавляется в map
			// и не участвует в кворуме.
			steps: []commitmentStep{
				{op: "setMatch", id: 99, value: 5, want: mInt(-1)},
			},
			wantMatchLen:  mInt(3),
			wantMatchGone: []int{99},
		},
		{
			name:   "NegativeMatchIndex",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			// После конструктора и setConfiguration все matchIndex = -1.
			steps: []commitmentStep{
				{op: "recalc", want: mInt(-1)},
			},
		},
		{
			name:   "EmptyVoterList",
			selfID: 0, term: 1, initVoters: []int{0},
			terms: map[int]int{5: 1, -1: 0},
			// Одиночный узел: setConfiguration([]int{0}) — один голосующий.
			steps: []commitmentStep{
				{op: "recalc", want: mInt(-1)},
				// После commit(5) -> self matchIndex = 5, median = 5
				{op: "commit", value: 5, want: mInt(5)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 6. commitCh non-blocking send ---

func TestCommitmentMatrix_Channel(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "ChannelNonBlocking",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// Не читаем из commitCh. setMatch не должен блокироваться.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "ChannelDelivery",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// Проверка, что commitCh действительно получает новое значение.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "drainCh", value: 5},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 7. setTerm с startIndex ---
//
// Секция удалена вместе с методом setTerm (мёртвый код):
// production-вызовов у setTerm не было, а term/startIndex трекера
// устанавливаются только в newCommitmentTracker и не меняются
// в течение лидерства.

// --- 8. Множественные setMatch ---

func TestCommitmentMatrix_MultipleSetMatch(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "MultipleSetMatchNoCommit",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1, 6: 1, 7: 1},
			// Несколько setMatch без достижения кворума.
			steps: []commitmentStep{
				{op: "setMatch", id: 1, value: 5},
				// matchIndexes = {0:-1, 1:5, 2:6} -> sorted [-1, 5, 6], median=5
				{op: "setMatch", id: 2, value: 6, want: mInt(5)},
				// matchIndexes = {0:7, 1:5, 2:6} -> sorted [5, 6, 7], median=6
				{op: "setMatch", id: 0, value: 7, want: mInt(6)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 9. Дополнительные тесты ---

func TestCommitmentMatrix_Additional(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "CommitThenSetMatch",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1, 8: 1},
			// commit для selfID, затем setMatch для peer-ов.
			steps: []commitmentStep{
				// matchIndexes = {0:5, 1:-1, 2:-1} -> sorted [-1, -1, 5], median=-1
				{op: "commit", value: 5, want: mInt(-1)},
				// matchIndexes = {0:5, 1:8, 2:-1} -> sorted [-1, 5, 8], median=5
				{op: "setMatch", id: 1, value: 8, want: mInt(5)},
				// matchIndexes = {0:5, 1:8, 2:8} -> sorted [5, 8, 8], median=8
				{op: "setMatch", id: 2, value: 8, want: mInt(8)},
			},
		},
		{
			name:   "FiveNodes",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3, 4},
			terms: map[int]int{10: 1, -1: 0},
			// Кворум 3/5.
			steps: []commitmentStep{
				// self + 1 peer = 2/5 — кворума нет
				{op: "setMatch", id: 0, value: 10},
				{op: "setMatch", id: 1, value: 10, want: mInt(-1)},
				// self + 2 peers = 3/5 — кворум есть
				{op: "setMatch", id: 2, value: 10, want: mInt(10)},
			},
		},
		{
			name:   "commitChBufferSize1PreventsLoss",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1, 10: 1},
			// Проверка, что commitCh с буфером 1 не теряет уведомления.
			steps: []commitmentStep{
				// Первый commit
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5},
				{op: "drainCh", value: 5},
				// Второй commit
				{op: "setMatch", id: 0, value: 10},
				{op: "setMatch", id: 1, value: 10},
				{op: "setMatch", id: 2, value: 10},
				{op: "drainCh", value: 10},
			},
		},
		{
			name:   "CommitIndexNeverDecreases",
			selfID: 0, term: 2, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 2, 8: 1},
			// commitIndex не уменьшается при попытке фиксации записи чужого term.
			steps: []commitmentStep{
				// term=2, median=5, lookupTerm(5)=2 ✅ → commitIndex=5
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
				// median=8, но lookupTerm(8)=1 != 2 ❌ → commitIndex не меняется
				{op: "setMatch", id: 0, value: 8},
				{op: "setMatch", id: 1, value: 8},
				{op: "setMatch", id: 2, value: 8, want: mInt(5)},
			},
		},
		{
			name:   "RecalculateWithNegativeMatch",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{-1: 0, 3: 1},
			// Медиана с отрицательным matchIndex (начальное состояние).
			steps: []commitmentStep{
				// matchIndexes = {0:3, 1:-1, 2:-1} -> sorted [-1, -1, 3], median=-1
				{op: "setMatch", id: 0, value: 3, want: mInt(-1)},
				// matchIndexes = {0:3, 1:3, 2:-1} -> sorted [-1, 3, 3], median=3
				{op: "setMatch", id: 1, value: 3, want: mInt(3)},
			},
		},
		{
			name:   "EmptyMatchMap",
			selfID: 0, term: 1, emptyTracker: true,
			terms: map[int]int{},
			// Пустой matchIndex — защита recalculateCommitIndex от паники.
			steps: []commitmentStep{
				{op: "recalc", want: mInt(-1)},
			},
		},
		{
			name:   "SetConfiguration_RecalculateOnConfigChange",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1, -1: 0},
			// При смене конфигурации recalculateCommitIndex вызывается автоматически.
			steps: []commitmentStep{
				// Достигаем кворума с 3 узлами на индексе 5
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
				// Меняем конфигурацию на 2 узла — кворум 2/2 сохраняется
				{op: "setConfig", voters: []int{0, 1}, want: mInt(5)},
			},
		},
		{
			name:   "SingleNode_AfterConfiguration",
			selfID: 0, term: 1, initVoters: []int{0},
			terms: map[int]int{10: 1},
			// Одиночный узел после setConfiguration.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 10, want: mInt(10)},
			},
		},
		{
			name:   "ConfigurationChange_LostQuorum",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3, 4},
			terms: map[int]int{5: 1, -1: 0},
			// Смена конфигурации с 5 узлов на 3: кворум был 2/5 (нет), стал 2/3 (есть).
			steps: []commitmentStep{
				// Только 2 узла из 5 — кворума нет
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5, want: mInt(-1)},
				// Меняем конфигурацию на 3 узла: 0 и 1 подтвердили → кворум 2/3 есть
				{op: "setConfig", voters: []int{0, 1, 2}, want: mInt(5)},
			},
		},
		{
			name:   "StartIndex_NotResetByConfigChange",
			selfID: 0, term: 1, startIdx: 10, initVoters: []int{0, 1, 2},
			terms: map[int]int{10: 1},
			// Смена конфигурации не сбрасывает startIndex.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 10},
				{op: "setMatch", id: 1, value: 10},
				{op: "setMatch", id: 2, value: 10, want: mInt(10)},
				// Смена конфигурации, startIndex не должен измениться
				{op: "setConfig", voters: []int{0, 1}},
			},
			wantStartIndex: mInt(10),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 10. Кворумная граница для чётных n ---

func TestCommitmentMatrix_EvenQuorumBoundary(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "TwoNodes_SelfOnlyNoCommit",
			selfID: 0, term: 1, initVoters: []int{0, 1},
			terms: map[int]int{5: 1},
			// n=2: только self продвинулся — 1/2 не является кворумом (нужно 2/2).
			// Старый код с медианой n/2 фиксировал в одиночку (sorted[-1,5] → 5).
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5, want: mInt(-1)},
			},
		},
		{
			name:   "TwoNodes_BothCommit",
			selfID: 0, term: 1, initVoters: []int{0, 1},
			terms: map[int]int{5: 1},
			// n=2: оба узла подтвердили 5 — кворум 2/2, commit 5.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "FourNodes_QuorumBoundary",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3},
			terms: map[int]int{3: 1, 5: 1},
			// n=4: кворум 3/4. {5,5,3,3} → commit 3 (2/4 на индексе 5 — не кворум;
			// старый код давал 5). {5,5,5,3} → commit 5 (граница 3/4 достижима).
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 3},
				// sorted [3, 3, 5, 5], индекс 4-3=1 → 3
				{op: "setMatch", id: 3, value: 3, want: mInt(3)},
				// sorted [3, 5, 5, 5], индекс 1 → 5
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
			},
		},
		{
			name:   "SixNodes_QuorumBoundary",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2, 3, 4, 5},
			terms: map[int]int{2: 1, 5: 1},
			// n=6: кворум 4/6. {5,5,5,2,2,2} → commit 2 (старый код: 5);
			// {5,5,5,5,2,2} → commit 5.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5},
				{op: "setMatch", id: 3, value: 2},
				{op: "setMatch", id: 4, value: 2},
				// sorted [2, 2, 2, 5, 5, 5], индекс 6-4=2 → 2
				{op: "setMatch", id: 5, value: 2, want: mInt(2)},
				// sorted [2, 2, 5, 5, 5, 5], индекс 2 → 5
				{op: "setMatch", id: 3, value: 5, want: mInt(5)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 11. Membership guard (ghost-ключи) ---

func TestCommitmentMatrix_MembershipGuard(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "GhostCannotCompleteQuorum",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1},
			// 3 voters + ack неголосующего (ghost-голос) + ack одного голосующего.
			// Без guard: ghost-ключ делает n=4 и «кворум 3 из 4» собирается из
			// двух реальных голосующих (старый код: commit 5). С guard: ack
			// неголосующего — no-op, 2/3 реальных голосующих не образуют кворум.
			steps: []commitmentStep{
				{op: "setMatch", id: 99, value: 5},                // ghost: no-op по guard
				{op: "setMatch", id: 1, value: 5, want: mInt(-1)}, // один реальный голосующий
			},
			wantMatchLen: mInt(3),
		},
		{
			name:   "ConfigTransition_3to2_NoSelfCommit",
			selfID: 0, term: 2, initVoters: []int{0, 1, 2},
			terms: map[int]int{6: 2},
			// Регрессия FINDING C: при переходе 3→2 синхронный premature commit
			// config entry в appendConfigurationEntry (commit(M+1) без единого
			// подтверждения) должен быть невозможен. voters {0,1,2}, match
			// {5,3,3}, term=2, terms={6:2}; setConfiguration([0,1]); commit(6).
			// Старый код: sorted[3,6] → медиана 6 → commit 6 без ack'ов.
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 3},
				{op: "setMatch", id: 2, value: 3, want: mInt(-1)},
				{op: "setConfig", voters: []int{0, 1}},
				// sorted [3, 6], кворумный индекс 2-2=0 → 3; lookupTerm(3)=-1 != 2
				{op: "commit", value: 6, want: mInt(-1)},
			},
		},
		{
			name:   "ConfigTransition_3to4_NewVoterLag",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{5: 1, 6: 1},
			// 3 voters {5,5,5} → commit 5. setConfiguration([0,1,2,3]): новый
			// голосующий стартует с -1 — пересчёт не занижает доказанную фиксацию.
			// Новые записи фиксируются только при 3/4 (новый голосующий не образует
			// кворум в одиночку/вдвоём с лидером).
			steps: []commitmentStep{
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5},
				{op: "setMatch", id: 2, value: 5, want: mInt(5)},
				{op: "setConfig", voters: []int{0, 1, 2, 3}, want: mInt(5)},
				// 2/4 (self + один старый голосующий) на индексе 6 — не кворум.
				{op: "commit", value: 6},
				{op: "setMatch", id: 1, value: 6, want: mInt(5)},
				// 3/4 — кворум: commit 6.
				{op: "setMatch", id: 2, value: 6, want: mInt(6)},
			},
		},
		{
			name:   "SelfRemoved_CommitNoOp",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{6: 1},
			// После self-removal (setConfiguration без selfID) commit() — no-op:
			// ghost-self не появляется в map, commitIndex не растёт.
			steps: []commitmentStep{
				{op: "setConfig", voters: []int{1, 2}},
				{op: "commit", value: 6, want: mInt(-1)},
			},
			wantMatchGone: []int{0},
			wantMatchLen:  mInt(2),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- 12. Переотправка уведомления commitCh ---

func TestCommitmentMatrix_NotifyRetry(t *testing.T) {
	cases := []commitmentCase{
		{
			name:   "NotifyRetriedAfterLostSend",
			selfID: 0, term: 1, initVoters: []int{0, 1, 2},
			terms: map[int]int{3: 1, 5: 1},

			// Канал commitCh (ёмкость 1) уже занят в момент попытки продвижения.
			// Из‑за этого отправка нового commitIndex теряется (неблокирующая отправка не проходит).
			// Флаг pendingNotify остаётся установленным, сигнализируя, что уведомление нужно повторить.
			// После того как потребитель заберёт старое значение из канала, следующий пересчёт
			// (даже если он не продвигает commitIndex) обязан заново отправить текущее значение.
			steps: []commitmentStep{
				// Занять канал «чужим» значением — имитация недренированного
				// уведомления от предыдущей фиксации.
				{op: "fillCh", value: 999},
				// Попытка продвинуть commitIndex до 5. Неблокирующая отправка в commitCh
				// не удаётся (канал полон), поэтому новое значение не уходит.
				// При этом флаг pendingNotify остаётся активным — это означает, что
				// позже нужно будет повторить отправку.
				{op: "setMatch", id: 0, value: 5},
				{op: "setMatch", id: 1, value: 5, want: mInt(5)},
				// Потребитель забирает старое значение из канала (освобождает место).
				{op: "drainCh", value: 999},
				// Следующий пересчёт commitIndex не увеличивает его (медиана 3 ≤ текущего 5),
				// но всё равно обязан отправить текущее значение commitIndex (5) в канал,
				// потому что ранее отправка была потеряна.
				{op: "setMatch", id: 2, value: 3},
				{op: "drainCh", value: 5},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}
