package raft

import (
	"sort"
)

// commitmentTracker отслеживает прогресс репликации записей журнала для
// лидера Raft. Получает обновления matchIndex от каждого узла-избирателя
// (голосующий) и вычисляет commitIndex на основе кворумного порога
// (majority). При изменении commitIndex отправляет уведомление в commitCh.
//
// Алгоритм: при каждом обновлении matchIndex собираются все значения
// (только для голосующих текущей конфигурации), сортируются и commitIndex
// устанавливается равным значению matchVals[n-quorumSize(n)]
// (алгебраически то же, что matchVals[(n-1)/2]),
// но только если:
//   - запись с этим индексом принадлежит текущему term (Raft safety, §5.4.2),
//   - median >= startIndex (первый индекс текущего терма лидера).
//
// В отсортированном по возрастанию массиве значение matchVals[i]
// реплицировано не менее чем на n-i узлах; выбор индекса n-quorumSize(n)
// гарантирует, что фиксация возможна только при репликации записи не менее
// чем на quorumSize(n) = n/2 + 1 голосующих (Commit Safety, Raft Fig. 2).
// Для нечётных n индексы n-quorumSize(n) и n/2 совпадают; при чётных n
// старый индекс n/2 допускал фиксацию при n/2 репликах.
//
// Если хотя бы одно из условий не выполнено, commitIndex не продвигается.
// При изменении commitIndex в канал commitCh отправляется уведомление
// (non-blocking send), что позволяет leaderLoop асинхронно обработать
// новую фиксацию без ожидания. Потерянное уведомление (канал cap 1 занят)
// переотправляется при следующем вызове recalculateCommitIndex через
// флаг pendingNotify.
//
// Трекер НЕ является потокобезопасным. Реальная защита — внешний cm.mu
// ConsensusModule, удерживаемый на всех call sites: трекер напрямую
// вызывают горутины репликации на каждого соседа (setMatch) и leaderLoop
// (setConfiguration/commit/getCommitIndex) — но всегда под cm.mu.
type commitmentTracker struct {
	// matchIndex содержит пары voterID → последний известный совпадающий
	// индекс журнала. Только узлы-избиратели текущей конфигурации влияют
	// на commitIndex: setMatch/commit для ID вне map — no-op.
	matchIndex map[int]int

	commitIndex int // текущий commitIndex (монотонно возрастает)
	selfID      int // идентификатор лидера
	startIndex  int // первый индекс, который может быть зафиксирован в этом term
	commitCh    chan<- int
	term        int // текущий term лидера

	// pendingNotify — признак потерянного уведомления commitCh.
	// Устанавливается при каждом продвижении commitIndex; сбрасывается
	// при успешной non-blocking отправке. Пока флаг взведён, каждый вызов
	// recalculateCommitIndex (включая не продвигающие) повторяет попытку
	// отправки текущего ct.commitIndex.
	pendingNotify bool
}

// newCommitmentTracker создаёт трекер для лидера.
// - selfID — идентификатор лидера.
// - term — текущий term лидера, для обеспечения безопасности Raft (см. §5.4.2 спецификации).
// - startIndex — первый индекс, который может быть зафиксирован в этом term.
// - commitCh — канал для уведомления leaderLoop о новом commitIndex.
// Начальный набор голосующих пуст; вызывающий должен вызвать setConfiguration
// для инициализации списка голосующих узлов.
func newCommitmentTracker(selfID, term, startIndex int, commitCh chan<- int) *commitmentTracker {
	return &commitmentTracker{
		matchIndex:  make(map[int]int),
		commitIndex: -1,
		selfID:      selfID,
		startIndex:  startIndex,
		commitCh:    commitCh,
		term:        term,
	}
}

// setConfiguration устанавливает новый набор голосующих узлов.
// Вызывается при старте лидера и при каждом изменении конфигурации кластера
// (Feature 2 — Membership). Сохраняет существующие matchIndex для узлов,
// которые остаются голосующими. Узлы, вышедшие из состава голосующих, удаляются
// из matchIndex.
func (ct *commitmentTracker) setConfiguration(voters []int, lookupTerm func(int) int) {
	newMatchIndex := make(map[int]int, len(voters))
	for _, voter := range voters {
		if val, ok := ct.matchIndex[voter]; ok {
			newMatchIndex[voter] = val
		} else {
			newMatchIndex[voter] = -1
		}
	}
	ct.matchIndex = newMatchIndex
	ct.recalculateCommitIndex(lookupTerm)
}

// setMatch обновляет значение matchIndex для указанного узла.
// Если peerID не является голосующим текущей конфигурации трекера —
// вызов игнорируется (membership guard: ack'и неголосующих и
// удалённых серверов не создают ghost-ключей, искажающих кворум).
// Если новый matchIndex меньше текущего — игнорируется (инвариант:
// matchIndex никогда не уменьшается). После обновления пересчитывает
// commitIndex через кворумную медиану.
//
// lookupTerm — функция для получения term записи журнала по индексу,
// необходима для проверки Raft safety.
func (ct *commitmentTracker) setMatch(peerID, index int, lookupTerm func(int) int) {
	if _, ok := ct.matchIndex[peerID]; !ok {
		return
	}
	if index < ct.matchIndex[peerID] {
		return
	}
	ct.matchIndex[peerID] = index
	ct.recalculateCommitIndex(lookupTerm)
}

// commit принудительно продвигает commitIndex через обновление matchIndex
// для selfID. Используется в dispatchLogs для single-node акселератора:
// лидер немедленно отмечает свою запись как совпадающую.
//
// Если selfID отсутствует в наборе голосующих трекера (лидер прекратил свою работу)
// — вызов игнорируется (membership guard): «фиктивный собственный узел» не появляется
// в map и не участвует в кворуме.
func (ct *commitmentTracker) commit(index int, lookupTerm func(int) int) {
	if _, ok := ct.matchIndex[ct.selfID]; !ok {
		return
	}
	ct.matchIndex[ct.selfID] = index
	ct.recalculateCommitIndex(lookupTerm)
}

// getCommitIndex возвращает текущее значение commitIndex.
func (ct *commitmentTracker) getCommitIndex() int {
	return ct.commitIndex
}

// recalculateCommitIndex вычисляет новый commitIndex на основе кворумной
// медианы всех значений matchIndex. Для проверки Raft safety используются
// два условия: term записи равен текущему term лидера, и индекс >= startIndex.
//
// Кворумный подход: сортируем все matchIndex, берём элемент с индексом
// n-quorumSize(n) — значение, реплицированное не менее чем на
// quorumSize(n) голосующих. Если запись по этому индексу удовлетворяет
// условиям безопасности, устанавливаем commitIndex = median и отправляем
// уведомление в commitCh (non-blocking).
//
// Переотправка уведомления (pendingNotify) выполняется на всех выходах
// функции, кроме guard'а пустого набора голосующих: потерянное уведомление
// повторно отправляется при последующих вызовах, в том числе проходящих
// через ветку без продвижения commitIndex.
func (ct *commitmentTracker) recalculateCommitIndex(lookupTerm func(int) int) {
	if len(ct.matchIndex) == 0 {
		return
	}
	matchVals := make([]int, 0, len(ct.matchIndex))
	for _, idx := range ct.matchIndex {
		matchVals = append(matchVals, idx)
	}
	sort.Ints(matchVals)

	quorum := quorumSize(len(matchVals))
	median := matchVals[len(matchVals)-quorum]
	if median > ct.commitIndex &&
		lookupTerm(median) == ct.term && median >= ct.startIndex {
		ct.commitIndex = median
		ct.pendingNotify = true
	}
	if ct.pendingNotify {
		select {
		case ct.commitCh <- ct.commitIndex:
			ct.pendingNotify = false
		default:
		}
	}
}
