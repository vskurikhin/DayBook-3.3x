package raft

import (
	"testing"
	"time"
)

// lookupTermStub возвращает фиксированный term для заданного индекса.
// Если index < 0, возвращает -1 (начальное состояние).
type lookupTermStub struct {
	terms map[int]int
}

func (l *lookupTermStub) lookup(index int) int {
	if index < 0 {
		return -1
	}
	if t, ok := l.terms[index]; ok {
		return t
	}
	return -1
}

// newTracker создаёт commitmentTracker с начальной конфигурацией голосующих.
// selfID и peerIDs формируют полный список голосующих узлов.
// term — начальный term трекера. startIndex по умолчанию 0.
func newTracker(t testing.TB, selfID int, peerIDs []int, term int) (*commitmentTracker, chan int) {
	t.Helper()
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(selfID, term, 0, commitCh)
	voters := make([]int, len(peerIDs)+1)
	voters[0] = selfID
	copy(voters[1:], peerIDs)
	lts := &lookupTermStub{terms: map[int]int{}}
	ct.setConfiguration(voters, lts.lookup)
	return ct, commitCh
}

// --- 1. Стандартное большинство (3 узла) ---

func TestCommitment_NoMatch(t *testing.T) {
	// Все matchIndex = -1 (начальное состояние), кворума нет.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_SelfOnly(t *testing.T) {
	// Только сам лидер продвинулся — кворума нет (нужно 2 из 3).
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_SelfAndOnePeer(t *testing.T) {
	// Лидер + один follower = 2 из 3 = кворум есть.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_AllNodes(t *testing.T) {
	// Все три узла подтвердили — кворум 3/3.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{10: 1}}
	ct.setMatch(0, 10, lts.lookup)
	ct.setMatch(1, 10, lts.lookup)
	ct.setMatch(2, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("getCommitIndex() = %d, want 10", got)
	}
}

func TestCommitment_MedianOrdering_Odd(t *testing.T) {
	// 5 узлов, разные значения matchIndex. Медиана = элемент [2] из 5.
	ct, _ := newTracker(t, 0, []int{1, 2, 3, 4}, 1)
	lts := &lookupTermStub{terms: map[int]int{-1: 1, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 1, lts.lookup)
	ct.setMatch(2, 2, lts.lookup)
	ct.setMatch(3, 3, lts.lookup)
	ct.setMatch(4, 4, lts.lookup)
	// sorted: [1, 2, 3, 4, 5], len=5, n/2=2 -> values[2] = 3
	if got := ct.getCommitIndex(); got != 3 {
		t.Fatalf("getCommitIndex() = %d, want 3", got)
	}
}

func TestCommitment_MedianOrdering_Even(t *testing.T) {
	// 4 узла (чётное количество голосующих). matchIndex:
	// {0:3, 1:1, 2:2, 3:-1} -> sorted [-1, 1, 2, 3], len=4.
	// Кворумный индекс n-quorumSize(n) = 4-3 = 1 -> values[1] = 1.
	ct, _ := newTracker(t, 0, []int{1, 2, 3}, 1)
	lts := &lookupTermStub{terms: map[int]int{-1: 1, 1: 1, 2: 1, 3: 1}}
	ct.setMatch(0, 3, lts.lookup)
	ct.setMatch(1, 1, lts.lookup)
	ct.setMatch(2, 2, lts.lookup)
	ct.setMatch(3, -1, lts.lookup)
	// sorted: [-1, 1, 2, 3], len=4, n-quorum = 1 -> values[1] = 1
	if got := ct.getCommitIndex(); got != 1 {
		t.Fatalf("getCommitIndex() = %d, want 1", got)
	}
}

// --- 2. Raft safety (проверка term и startIndex) ---

func TestCommitment_Safety_DifferentTerm(t *testing.T) {
	// term=2, но запись с индексом 5 принадлежит term=1 — фиксация невозможна.
	ct, _ := newTracker(t, 0, []int{1, 2}, 2)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_Safety_SameTerm(t *testing.T) {
	// term=2 и запись с индексом 5 принадлежит term=2 — фиксация разрешена.
	ct, _ := newTracker(t, 0, []int{1, 2}, 2)
	lts := &lookupTermStub{terms: map[int]int{5: 2}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_Safety_MedianShifts(t *testing.T) {
	// Медиана скачком переходит с отвергнутого индекса на принятый.
	// 4 voters (чётное n), term=2, terms = {3:1, 5:2}.
	ct, _ := newTracker(t, 0, []int{1, 2, 3}, 2)
	lts := &lookupTermStub{terms: map[int]int{3: 1, 5: 2}}

	// Шаг 1: matchIndexes = {0:5, 1:3, 2:3, 3:-1}
	// sorted = [-1, 3, 3, 5], кворумный индекс 4-3=1 -> 3.
	// lookupTerm(3)=1 != 2 -> фиксация заблокирована.
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 3, lts.lookup)
	ct.setMatch(2, 3, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("step 1: getCommitIndex() = %d, want -1 (median 3 has term 1)", got)
	}

	// Шаг 2: matchIndexes = {0:5, 1:3, 2:3, 3:5}
	// sorted = [3, 3, 5, 5], кворумный индекс 4-3=1 -> 3 — это 2/4,
	// кворума 3/4 нет: commitIndex остаётся -1.
	ct.setMatch(3, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("step 2: getCommitIndex() = %d, want -1 (2/4 is not a quorum)", got)
	}

	// Шаг 3: matchIndexes = {0:5, 1:5, 2:3, 3:5}
	// sorted = [3, 5, 5, 5], кворумный индекс 1 -> 5, lookupTerm(5)=2 == term
	// -> 3/4 = кворум, commitIndex 5.
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("step 3: getCommitIndex() = %d, want 5 (3/4 quorum)", got)
	}
}

func TestCommitment_StartIndex_BlocksCommit(t *testing.T) {
	// startIndex=10, median=10 >= 10 ✅, lookupTerm(10)=2 == term ✅
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 2, 10, commitCh)
	lts := &lookupTermStub{terms: map[int]int{10: 2}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 10, lts.lookup)
	ct.setMatch(1, 10, lts.lookup)
	ct.setMatch(2, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("getCommitIndex() = %d, want 10", got)
	}
}

func TestCommitment_StartIndex_PreventsOld(t *testing.T) {
	// startIndex=10, median=8 < 10 — фиксация заблокирована.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 2, 10, commitCh)
	lts := &lookupTermStub{terms: map[int]int{8: 2}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 8, lts.lookup)
	ct.setMatch(1, 8, lts.lookup)
	ct.setMatch(2, 8, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_StartIndex_Exact(t *testing.T) {
	// startIndex=5, median=5 >= 5 ✅
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 2, 5, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 2}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}
}

// --- 3. setConfiguration ---

func TestCommitment_SetConfiguration_Basic(t *testing.T) {
	// Установка конфигурации из трёх узлов после пустого конструктора.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)

	if len(ct.matchIndex) != 3 {
		t.Fatalf("matchIndex len = %d, want 3", len(ct.matchIndex))
	}
	for _, voter := range []int{0, 1, 2} {
		if ct.matchIndex[voter] != -1 {
			t.Fatalf("matchIndex[%d] = %d, want -1", voter, ct.matchIndex[voter])
		}
	}
}

func TestCommitment_SetConfiguration_PreservesMatchIndex(t *testing.T) {
	// Смена конфигурации: добавляем новый узел, удаляем старый,
	// matchIndex сохраняется для оставшихся.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 3, lts.lookup)

	// Меняем конфигурацию: убираем узел 2, добавляем узел 3
	ct.setConfiguration([]int{0, 1, 3}, lts.lookup)

	if ct.matchIndex[0] != 5 {
		t.Fatalf("matchIndex[0] = %d, want 5 (preserved)", ct.matchIndex[0])
	}
	if ct.matchIndex[1] != 3 {
		t.Fatalf("matchIndex[1] = %d, want 3 (preserved)", ct.matchIndex[1])
	}
	if _, ok := ct.matchIndex[2]; ok {
		t.Fatalf("matchIndex[2] should be removed")
	}
	if ct.matchIndex[3] != -1 {
		t.Fatalf("matchIndex[3] = %d, want -1 (new voter)", ct.matchIndex[3])
	}
}

func TestCommitment_SetConfiguration_CommitAfterChange(t *testing.T) {
	// После смены конфигурации commitIndex не сбрасывается.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("before conf change: getCommitIndex() = %d, want 5", got)
	}

	// Меняем конфигурацию с 3 узлов на 2 узла
	ct.setConfiguration([]int{0, 1}, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after conf change: getCommitIndex() = %d, want 5 (no reset)", got)
	}
}

// --- 4. Продвижение commitIndex (incremental) ---

func TestCommitment_Progressive(t *testing.T) {
	// Постепенное продвижение commitIndex по мере получения matchIndex.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{3: 1, 5: 1}}

	ct.setMatch(0, 3, lts.lookup)
	ct.setMatch(1, 3, lts.lookup)
	// matchIndexes = {0:3, 1:3, 2:-1} -> sorted [-1, 3, 3], median=3
	if got := ct.getCommitIndex(); got != 3 {
		t.Fatalf("after first two: getCommitIndex() = %d, want 3", got)
	}

	ct.setMatch(2, 5, lts.lookup)
	// matchIndexes = {0:3, 1:3, 2:5} -> sorted [3, 3, 5], median=3
	// 3 == ct.commitIndex, поэтому не продвигается
	if got := ct.getCommitIndex(); got != 3 {
		t.Fatalf("after third: getCommitIndex() = %d, want 3", got)
	}
}

func TestCommitment_Progressive_LargeSteps(t *testing.T) {
	// Продвижение с большими скачками matchIndex.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{100: 1, 150: 1, 200: 1}}

	ct.setMatch(1, 100, lts.lookup)
	// matchIndexes = {0:-1, 1:100, 2:-1} -> sorted [-1, -1, 100], median=-1
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("after peer1: getCommitIndex() = %d, want -1", got)
	}

	ct.setMatch(2, 200, lts.lookup)
	// matchIndexes = {0:-1, 1:100, 2:200} -> sorted [-1, 100, 200], median=100
	if got := ct.getCommitIndex(); got != 100 {
		t.Fatalf("after peer2: getCommitIndex() = %d, want 100", got)
	}

	ct.setMatch(0, 150, lts.lookup)
	// matchIndexes = {0:150, 1:100, 2:200} -> sorted [100, 150, 200], median=150
	if got := ct.getCommitIndex(); got != 150 {
		t.Fatalf("after self: getCommitIndex() = %d, want 150", got)
	}
}

func TestCommitment_SelfCommit(t *testing.T) {
	// self.commit(10) — обновили selfID, но соседи не подтвердили.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{10: 1}}

	ct.commit(10, lts.lookup)
	// sorted [-1, -1, 10], median=-1 — кворума нет
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1 (no majority)", got)
	}
}

func TestCommitment_SelfCommit_SingleNode(t *testing.T) {
	// Одиночный узел: self.commit(10) → median=10 → кворум 1/1.
	ct, _ := newTracker(t, 0, []int{}, 1)
	lts := &lookupTermStub{terms: map[int]int{10: 1}}

	ct.commit(10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("getCommitIndex() = %d, want 10", got)
	}
}

// --- 5. Граничные случаи setMatch ---

func TestCommitment_SetMatchDecreasingIgnored(t *testing.T) {
	// Уменьшающийся matchIndex игнорируется (инвариант монотонности).
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{10: 1, 5: 1}}

	ct.setMatch(1, 10, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.matchIndex[1]; got != 10 {
		t.Fatalf("matchIndex[1] = %d, want 10", got)
	}
}

func TestCommitment_SetMatchUnknownPeer(t *testing.T) {
	// setMatch для узла, не являющегося голосующим текущей конфигурации —
	// no-op (membership guard): ghost-ключ НЕ добавляется в map
	// и не участвует в кворуме. До фикса setMatch создавал ключ
	// matchIndex[99] и искажал состав кворума.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}

	ct.setMatch(99, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
	if got := len(ct.matchIndex); got != 3 {
		t.Fatalf("len(matchIndex) = %d, want 3 (ghost key must not be added)", got)
	}
	if _, ok := ct.matchIndex[99]; ok {
		t.Fatal("matchIndex[99] must not exist (membership guard)")
	}
}

func TestCommitment_NegativeMatchIndex(t *testing.T) {
	// После конструктора и setConfiguration все matchIndex = -1.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("initial getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_EmptyVoterList(t *testing.T) {
	// Одиночный узел: setConfiguration([]int{0}) — один голосующий.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 1, -1: 0}}
	ct.setConfiguration([]int{0}, lts.lookup)

	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("initial getCommitIndex() = %d, want -1", got)
	}

	// После commit(5) -> self matchIndex = 5, median = 5
	ct.commit(5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after commit: getCommitIndex() = %d, want 5", got)
	}
}

// --- 6. commitCh non-blocking send ---

func TestCommitment_ChannelNonBlocking(t *testing.T) {
	// Не читаем из commitCh. setMatch не должен блокироваться.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}

	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_ChannelDelivery(t *testing.T) {
	// Проверка, что commitCh действительно получает новое значение.
	ct, commitCh := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}

	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)

	select {
	case got := <-commitCh:
		if got != 5 {
			t.Fatalf("commitCh = %d, want 5", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for commitCh")
	}
}

// --- 7. setTerm с startIndex ---
//
// Секция удалена вместе с методом setTerm (мёртвый код):
// production-вызовов у setTerm не было, а term/startIndex трекера
// устанавливаются только в newCommitmentTracker и не меняются
// в течение лидерства.

// --- 8. Множественные setMatch ---

func TestCommitment_MultipleSetMatchNoCommit(t *testing.T) {
	// Несколько setMatch без достижения кворума.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1, 6: 1, 7: 1}}

	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 6, lts.lookup)
	// matchIndexes = {0:-1, 1:5, 2:6} -> sorted [-1, 5, 6], median=5
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}

	ct.setMatch(0, 7, lts.lookup)
	// matchIndexes = {0:7, 1:5, 2:6} -> sorted [5, 6, 7], median=6
	if got := ct.getCommitIndex(); got != 6 {
		t.Fatalf("getCommitIndex() = %d, want 6", got)
	}
}

// --- 9. Дополнительные тесты ---

func TestCommitment_CommitThenSetMatch(t *testing.T) {
	// commit для selfID, затем setMatch для peer-ов.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1, 8: 1}}

	ct.commit(5, lts.lookup)
	// matchIndexes = {0:5, 1:-1, 2:-1} -> sorted [-1, -1, 5], median=-1
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("after commit only: getCommitIndex() = %d, want -1", got)
	}

	ct.setMatch(1, 8, lts.lookup)
	// matchIndexes = {0:5, 1:8, 2:-1} -> sorted [-1, 5, 8], median=5
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after peer1: getCommitIndex() = %d, want 5", got)
	}

	ct.setMatch(2, 8, lts.lookup)
	// matchIndexes = {0:5, 1:8, 2:8} -> sorted [5, 8, 8], median=8
	if got := ct.getCommitIndex(); got != 8 {
		t.Fatalf("after peer2: getCommitIndex() = %d, want 8", got)
	}
}

func TestCommitment_FiveNodes(t *testing.T) {
	// Кворум 3/5.
	ct, _ := newTracker(t, 0, []int{1, 2, 3, 4}, 1)
	lts := &lookupTermStub{terms: map[int]int{10: 1, -1: 0}}

	// self + 1 peer = 2/5 — кворума нет
	ct.setMatch(0, 10, lts.lookup)
	ct.setMatch(1, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("2/5: getCommitIndex() = %d, want -1", got)
	}

	// self + 2 peers = 3/5 — кворум есть
	ct.setMatch(2, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("3/5: getCommitIndex() = %d, want 10", got)
	}
}

func TestCommitment_commitChBufferSize1PreventsLoss(t *testing.T) {
	// Проверка, что commitCh с буфером 1 не теряет уведомления.
	ct, commitCh := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1, 10: 1}}

	// Первый commit
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)

	select {
	case <-commitCh:
	case <-time.After(time.Second):
		t.Fatal("timeout reading first commitCh message")
	}

	// Второй commit
	ct.setMatch(0, 10, lts.lookup)
	ct.setMatch(1, 10, lts.lookup)
	ct.setMatch(2, 10, lts.lookup)

	select {
	case got := <-commitCh:
		if got != 10 {
			t.Fatalf("commitCh = %d, want 10", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout reading second commitCh message")
	}
}

func TestCommitment_CommitIndexNeverDecreases(t *testing.T) {
	// commitIndex не уменьшается при попытке фиксации записи чужого term.
	ct, _ := newTracker(t, 0, []int{1, 2}, 2)
	lts := &lookupTermStub{terms: map[int]int{5: 2, 8: 1}}

	// term=2, median=5, lookupTerm(5)=2 ✅ → commitIndex=5
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("first: getCommitIndex() = %d, want 5", got)
	}

	// median=8, но lookupTerm(8)=1 != 2 ❌ → commitIndex не меняется
	ct.setMatch(0, 8, lts.lookup)
	ct.setMatch(1, 8, lts.lookup)
	ct.setMatch(2, 8, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("second: getCommitIndex() = %d, want 5 (no regression)", got)
	}
}

func TestCommitment_RecalculateWithNegativeMatch(t *testing.T) {
	// Медиана с отрицательным matchIndex (начальное состояние).
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{-1: 0, 3: 1}}

	ct.setMatch(0, 3, lts.lookup)
	// matchIndexes = {0:3, 1:-1, 2:-1} -> sorted [-1, -1, 3], median=-1
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}

	ct.setMatch(1, 3, lts.lookup)
	// matchIndexes = {0:3, 1:3, 2:-1} -> sorted [-1, 3, 3], median=3
	if got := ct.getCommitIndex(); got != 3 {
		t.Fatalf("getCommitIndex() = %d, want 3", got)
	}
}

func TestCommitment_EmptyMatchMap(t *testing.T) {
	// Пустой matchIndex — защита recalculateCommitIndex от паники.
	ct := &commitmentTracker{
		matchIndex:  map[int]int{},
		commitIndex: -1,
		selfID:      0,
		startIndex:  0,
		commitCh:    make(chan int, 1),
		term:        1,
	}
	lts := &lookupTermStub{terms: map[int]int{}}
	ct.recalculateCommitIndex(lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1", got)
	}
}

func TestCommitment_SetConfiguration_RecalculateOnConfigChange(t *testing.T) {
	// При смене конфигурации recalculateCommitIndex вызывается автоматически.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 1, -1: 0}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)

	// Достигаем кворума с 3 узлами на индексе 5
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("before config change: getCommitIndex() = %d, want 5", got)
	}

	// Меняем конфигурацию на 2 узла — кворум 2/2 сохраняется
	ct.setConfiguration([]int{0, 1}, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after config change: getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_SingleNode_AfterConfiguration(t *testing.T) {
	// Одиночный узел после setConfiguration.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{10: 1}}
	ct.setConfiguration([]int{0}, lts.lookup)

	ct.setMatch(0, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("getCommitIndex() = %d, want 10", got)
	}
}

func TestCommitment_ConfigurationChange_LostQuorum(t *testing.T) {
	// Смена конфигурации с 5 узлов на 3: кворум был 2/5 (нет), стал 2/3 (есть).
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 1, -1: 0}}
	ct.setConfiguration([]int{0, 1, 2, 3, 4}, lts.lookup)

	// Только 2 узла из 5 — кворума нет
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("2/5: getCommitIndex() = %d, want -1", got)
	}

	// Меняем конфигурацию на 3 узла: 0 и 1 подтвердили → кворум 2/3 есть
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after config change to 3 nodes: getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_StartIndex_NotResetByConfigChange(t *testing.T) {
	// Смена конфигурации не сбрасывает startIndex.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 10, commitCh)
	lts := &lookupTermStub{terms: map[int]int{10: 1}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)

	ct.setMatch(0, 10, lts.lookup)
	ct.setMatch(1, 10, lts.lookup)
	ct.setMatch(2, 10, lts.lookup)
	if got := ct.getCommitIndex(); got != 10 {
		t.Fatalf("after match: getCommitIndex() = %d, want 10", got)
	}

	// Смена конфигурации, startIndex не должен измениться
	ct.setConfiguration([]int{0, 1}, lts.lookup)
	if ct.startIndex != 10 {
		t.Fatalf("startIndex = %d, want 10 (unchanged)", ct.startIndex)
	}
}

// --- 10. Кворумная граница для чётных n ---

func TestCommitment_TwoNodes_SelfOnlyNoCommit(t *testing.T) {
	// n=2: только self продвинулся — 1/2 не является кворумом (нужно 2/2).
	// Старый код с медианой n/2 фиксировал в одиночку (sorted[-1,5] → 5).
	ct, _ := newTracker(t, 0, []int{1}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1 (self-only is not a quorum for n=2)", got)
	}
}

func TestCommitment_TwoNodes_BothCommit(t *testing.T) {
	// n=2: оба узла подтвердили 5 — кворум 2/2, commit 5.
	ct, _ := newTracker(t, 0, []int{1}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_FourNodes_QuorumBoundary(t *testing.T) {
	// n=4: кворум 3/4. {5,5,3,3} → commit 3 (2/4 на индексе 5 — не кворум;
	// старый код давал 5). {5,5,5,3} → commit 5 (граница 3/4 достижима).
	ct, _ := newTracker(t, 0, []int{1, 2, 3}, 1)
	lts := &lookupTermStub{terms: map[int]int{3: 1, 5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 3, lts.lookup)
	ct.setMatch(3, 3, lts.lookup)
	// sorted [3, 3, 5, 5], индекс 4-3=1 → 3
	if got := ct.getCommitIndex(); got != 3 {
		t.Fatalf("boundary low: getCommitIndex() = %d, want 3", got)
	}

	ct.setMatch(2, 5, lts.lookup)
	// sorted [3, 5, 5, 5], индекс 1 → 5
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("boundary high: getCommitIndex() = %d, want 5", got)
	}
}

func TestCommitment_SixNodes_QuorumBoundary(t *testing.T) {
	// n=6: кворум 4/6. {5,5,5,2,2,2} → commit 2 (старый код: 5);
	// {5,5,5,5,2,2} → commit 5.
	ct, _ := newTracker(t, 0, []int{1, 2, 3, 4, 5}, 1)
	lts := &lookupTermStub{terms: map[int]int{2: 1, 5: 1}}
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	ct.setMatch(3, 2, lts.lookup)
	ct.setMatch(4, 2, lts.lookup)
	ct.setMatch(5, 2, lts.lookup)
	// sorted [2, 2, 2, 5, 5, 5], индекс 6-4=2 → 2
	if got := ct.getCommitIndex(); got != 2 {
		t.Fatalf("boundary low: getCommitIndex() = %d, want 2", got)
	}

	ct.setMatch(3, 5, lts.lookup)
	// sorted [2, 2, 5, 5, 5, 5], индекс 2 → 5
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("boundary high: getCommitIndex() = %d, want 5", got)
	}
}

// --- 11. Membership guard (ghost-ключи) ---

func TestCommitment_GhostCannotCompleteQuorum(t *testing.T) {
	// 3 voters + ack неголосующего (ghost-голос) + ack одного голосующего.
	// Без guard: ghost-ключ делает n=4 и «кворум 3 из 4» собирается из
	// двух реальных голосующих (старый код: commit 5). С guard: ack
	// неголосующего — no-op, 2/3 реальных голосующих не образуют кворум.
	ct, _ := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{5: 1}}

	ct.setMatch(99, 5, lts.lookup) // ghost: no-op по guard
	ct.setMatch(1, 5, lts.lookup)  // один реальный голосующий
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1 (ghost ack must not complete quorum)", got)
	}
	if got := len(ct.matchIndex); got != 3 {
		t.Fatalf("len(matchIndex) = %d, want 3", got)
	}
}

func TestCommitment_ConfigTransition_3to2_NoSelfCommit(t *testing.T) {
	// Регрессия FINDING C: при переходе 3→2 синхронный premature commit
	// config entry в appendConfigurationEntry (commit(M+1) без единого
	// подтверждения) должен быть невозможен. voters {0,1,2}, match
	// {5,3,3}, term=2, terms={6:2}; setConfiguration([0,1]); commit(6).
	// Старый код: sorted[3,6] → медиана 6 → commit 6 без ack'ов.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 2, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{6: 2}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 3, lts.lookup)
	ct.setMatch(2, 3, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("before transition: getCommitIndex() = %d, want -1", got)
	}

	ct.setConfiguration([]int{0, 1}, lts.lookup)
	ct.commit(6, lts.lookup)
	// sorted [3, 6], кворумный индекс 2-2=0 → 3; lookupTerm(3)=-1 != 2
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("after 3→2 transition: getCommitIndex() = %d, want -1 (no premature commit)", got)
	}
}

func TestCommitment_ConfigTransition_3to4_NewVoterLag(t *testing.T) {
	// 3 voters {5,5,5} → commit 5. setConfiguration([0,1,2,3]): новый
	// голосующий стартует с -1 — пересчёт не занижает доказанную фиксацию.
	// Новые записи фиксируются только при 3/4 (новый голосующий не образует
	// кворум в одиночку/вдвоём с лидером).
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{5: 1, 6: 1}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	ct.setMatch(2, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("before transition: getCommitIndex() = %d, want 5", got)
	}

	ct.setConfiguration([]int{0, 1, 2, 3}, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("after transition: getCommitIndex() = %d, want 5 (not lowered by new voter)", got)
	}

	// 2/4 (self + один старый голосующий) на индексе 6 — не кворум.
	ct.commit(6, lts.lookup)
	ct.setMatch(1, 6, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("2/4 on index 6: getCommitIndex() = %d, want 5", got)
	}

	// 3/4 — кворум: commit 6.
	ct.setMatch(2, 6, lts.lookup)
	if got := ct.getCommitIndex(); got != 6 {
		t.Fatalf("3/4 on index 6: getCommitIndex() = %d, want 6", got)
	}
}

func TestCommitment_SelfRemoved_CommitNoOp(t *testing.T) {
	// После self-removal (setConfiguration без selfID) commit() — no-op:
	// ghost-self не появляется в map, commitIndex не растёт.
	commitCh := make(chan int, 1)
	ct := newCommitmentTracker(0, 1, 0, commitCh)
	lts := &lookupTermStub{terms: map[int]int{6: 1}}
	ct.setConfiguration([]int{0, 1, 2}, lts.lookup)

	ct.setConfiguration([]int{1, 2}, lts.lookup)
	ct.commit(6, lts.lookup)
	if got := ct.getCommitIndex(); got != -1 {
		t.Fatalf("getCommitIndex() = %d, want -1 (self removed)", got)
	}
	if _, ok := ct.matchIndex[0]; ok {
		t.Fatal("matchIndex[0] must not exist after self-removal")
	}
	if got := len(ct.matchIndex); got != 2 {
		t.Fatalf("len(matchIndex) = %d, want 2", got)
	}
}

// --- 12. Переотправка уведомления commitCh ---

func TestCommitment_NotifyRetriedAfterLostSend(t *testing.T) {
	// Канал commitCh занят (cap 1) в момент продвижения — отправка
	// потеряна. После дренажа следующий вызов recalculateCommitIndex
	// (не продвигающий: median <= commitIndex) обязан переотправить
	// ТЕКУЩИЙ commitIndex. Без фикса потребитель застревал бы навсегда
	// (P2 lost notification).
	ct, commitCh := newTracker(t, 0, []int{1, 2}, 1)
	lts := &lookupTermStub{terms: map[int]int{3: 1, 5: 1}}

	// Занять канал «чужим» значением — имитация недренированного
	// уведомления от предыдущей фиксации.
	commitCh <- 999

	// Продвижение до 5: non-blocking send проваливается (канал полон),
	// pendingNotify остаётся взведённым.
	ct.setMatch(0, 5, lts.lookup)
	ct.setMatch(1, 5, lts.lookup)
	if got := ct.getCommitIndex(); got != 5 {
		t.Fatalf("getCommitIndex() = %d, want 5", got)
	}

	// Дренаж: потребитель читает «старое» значение.
	select {
	case got := <-commitCh:
		if got != 999 {
			t.Fatalf("commitCh = %d, want 999", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout draining commitCh")
	}

	// Следующий recalc не продвигает (median 3 <= 5), но обязан
	// переотправить текущий commitIndex 5.
	ct.setMatch(2, 3, lts.lookup)

	select {
	case got := <-commitCh:
		if got != 5 {
			t.Fatalf("commitCh = %d, want 5 (retried current commitIndex)", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout: lost notification was not retried")
	}
}
