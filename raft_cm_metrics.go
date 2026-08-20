package raft

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// latencyMetric — один агрегат латентности: сумма замеров в микросекундах и
// их количество. Нулевое значение готово к использованию (важно для CM,
// собираемых литералом в тестах).
type latencyMetric struct {
	sumUs atomic.Int64 // сумма замеров в микросекундах за текущее окно
	count atomic.Int64 // число замеров за текущее окно
}

// observe регистрирует один замер. Никогда не блокируется, не захватывает
// мьютексов, корректна из любого контекста, включая удержание cm.mu.
// Порядок двух Add — нормативный: на него опирается доказательство
// корректности снятия агрегата в meanMsAndReset.
func (m *latencyMetric) observe(d time.Duration) {
	m.sumUs.Add(d.Microseconds())
	m.count.Add(1)
}

// meanMsAndReset возвращает среднее в миллисекундах за прошедшее окно и
// обнуляет агрегат. Вызывается только из stats (единственный читатель).
//
// НОРМАТИВНО:
//  1. счётчик снимается ПЕРВЫМ, сумма — ВТОРОЙ (обратный порядок к observe);
//  2. если счётчик оказался нулевым, а сумма — нет, сумма возвращается
//     в следующее окно (carry-back), где её встретит «отставший» счётчик.
func (m *latencyMetric) meanMsAndReset() float64 {
	c := m.count.Swap(0) // (1) сначала счётчик
	s := m.sumUs.Swap(0) // (2) затем сумма
	if c == 0 {
		if s != 0 {
			m.sumUs.Add(s) // carry-back: замер не теряется
		}
		return 0
	}
	return float64(s) / float64(c) / 1000.0
}

// cmLatency — набор агрегатов латентности одного ConsensusModule.
// Собственного мьютекса нет: поля — atomic.Int64.
type cmLatency struct {
	appendEntries, fsmApply, election, handleFsmSnapshot, installSnapshot,
	processLogs, requestPreVote, requestVote, sendBatch, takeSnapshot,
	timeoutNowRequest latencyMetric
}

// latencyReport — снимок агрегатов одного окна в миллисекундах.
// Возвращается по значению, принимается только по указателю
// (11 × float64 = 88 B > порог).
type latencyReport struct {
	appendEntries, fsmApply, election, handleFsmSnapshot, installSnapshot,
	processLogs, requestPreVote, requestVote, sendBatch, takeSnapshot,
	timeoutNowRequest float64
}

// snapshotAndReset снимает средние всех 11 агрегатов за прошедшее окно
// и обнуляет их. Вызывается только из stats (раз в секунду).
func (l *cmLatency) snapshotAndReset() latencyReport {
	return latencyReport{
		appendEntries:     l.appendEntries.meanMsAndReset(),
		fsmApply:          l.fsmApply.meanMsAndReset(),
		election:          l.election.meanMsAndReset(),
		handleFsmSnapshot: l.handleFsmSnapshot.meanMsAndReset(),
		installSnapshot:   l.installSnapshot.meanMsAndReset(),
		processLogs:       l.processLogs.meanMsAndReset(),
		requestPreVote:    l.requestPreVote.meanMsAndReset(),
		requestVote:       l.requestVote.meanMsAndReset(),
		sendBatch:         l.sendBatch.meanMsAndReset(),
		takeSnapshot:      l.takeSnapshot.meanMsAndReset(),
		timeoutNowRequest: l.timeoutNowRequest.meanMsAndReset(),
	}
}

// format форматирует снимок агрегатов в одну строку отчёта. Чистая функция:
// не читает глобалей и полей CM, не логирует. Набор, порядок
// и имена колонок, а также формат %5.2fms сохраняются прежними; собственной
// метки времени и префикса [state,N:id,T:term] нет — их добавляют логгер
// трассировки и traceLogf. Приём только по указателю —
// (88 B > порог gocritic:hugeParam).
func (r *latencyReport) format() string {
	return fmt.Sprintf(
		"AE=%5.2fms, BatchingFSM=%5.2fms, Election=%5.2fms, FSMSnapSh=%5.2fms,"+
			" InstSnapShot=%5.2fms, ProcessLog=%5.2fms, RqPVt=%5.2fms, RqVote=%5.2fms,"+
			" SendBatch=%5.2fms, TakeSnapshot=%5.2fms, TmOutNowRq=%5.2fms",
		r.appendEntries, r.fsmApply, r.election, r.handleFsmSnapshot,
		r.installSnapshot, r.processLogs, r.requestPreVote, r.requestVote,
		r.sendBatch, r.takeSnapshot, r.timeoutNowRequest,
	)
}

func (cm *ConsensusModule) stats(shutdownCh chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-shutdownCh:
			return
		case <-ticker.C:
			// Снятие агрегатов — безусловно, чтобы окно агрегации оставалось
			// «за секунду» и при выключенном выводе.
			rep := cm.latency.snapshotAndReset()
			// Формирование строк и вывод — только при включённой трассировке
			// в os.Stdout.
			// Префикс [state,N:id,T:term] добавляет stdoutTracePrintln.
			if traceEnabled(0) {
				cm.stdoutTracePrintln(rep.format())
			}
		}
	}
}

// raftCounters — счётчики ключевых событий репликации и снимков.
// Отвечают на диагностический вопрос «это цикл или
// единичное событие?», на который средняя латентность не способна
// ответить в принципе. Нулевое значение готово к использованию
// (CM, собираемые литералом в тестах).
//
// Глобальные поля — atomic.Int64, инкремент без захвата cm.mu.
// Per-peer карты защищены cm.mu: инкремент — под уже удерживаемым
// cm.mu, чтение — только из горутины stats (раз в секунду, O(пиров)).
type raftCounters struct {
	// installSnapshotSent — отправки InstallSnapshot, по узлу-получателю.
	installSnapshotSent map[int]int64

	// installSnapshotReceived — установки снимка, применённые данным
	// узлом (без учёта повторных установок устаревшего снимка).
	installSnapshotReceived atomic.Int64

	// installSnapshotSkippedStale — пропуски установки устаревшего снимка
	// по пиру-отправителю (leaderID).
	installSnapshotSkippedStale map[int]int64

	// appendEntriesRejected — отказы AppendEntries, по пиру.
	appendEntriesRejected map[int]int64

	// nextIndexRejectionIgnored — срабатывания предохранителя
	// (отбракованный невозможный отказ), по пиру.
	nextIndexRejectionIgnored map[int]int64

	// snapshotLogBoundaryViolation — нарушения в runtime
	// (пост-условие).
	snapshotLogBoundaryViolation atomic.Int64

	// snapshotIndexBehindDispatched — снимки, созданные в момент, когда
	// машина состояний отставала от диспетчеризации
	// (fsmAppliedIndex < lastApplied). Ранее такой снимок фиксировался с
	// завышенным индексом и терял подтверждённые записи; теперь счётчик
	// показывает, как часто это окно проходится.
	snapshotIndexBehindDispatched atomic.Int64

	// sendBatchEntrySkipped — записи, пропущенные при сборке батча для
	// машины состояний (запись выше конца журнала, позиция вне журнала,
	// несовпадение индекса). Штатная причина — сжатие журнала: запись уже
	// покрыта снимком.
	sendBatchEntrySkipped atomic.Int64
}

// incPeerCount инкрементирует счётчик по каждому соседу, лениво инициализируя
// карту (нулевое значение raftCounters должно быть готово к использованию),
// и возвращает (возможно, переаллоцированную) карту. Требует удержания cm.mu.
func incPeerCount(m map[int]int64, peerID int) map[int]int64 {
	if m == nil {
		m = make(map[int]int64)
	}
	m[peerID]++
	return m
}

// peerSum суммирует значения карты по каждому соседу. Чтение — только под cm.mu
// (горутина stats); суммирование для отчёта, не для hot path.
func peerSum(m map[int]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}

// report форматирует периодическую строку счётчиков и показатели по каждому соседу
// nextIndex/matchIndex лидера. Формат: суммарные
// счётчики, затем для каждого пира (в порядке возрастания ID) пара
// ni/mi. Вызывается только из stats под cm.mu.
func (c *raftCounters) report(ls *leaderState) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "ISsent=%d ISrecv=%d ISstale=%d AErej=%d NIrejIgn=%d BndViol=%d SnapLag=%d BatchSkip=%d",
		peerSum(c.installSnapshotSent), c.installSnapshotReceived.Load(),
		peerSum(c.installSnapshotSkippedStale), peerSum(c.appendEntriesRejected),
		peerSum(c.nextIndexRejectionIgnored), c.snapshotLogBoundaryViolation.Load(),
		c.snapshotIndexBehindDispatched.Load(), c.sendBatchEntrySkipped.Load())
	peers := make([]int, 0, len(ls.nextIndex))
	for p := range ls.nextIndex {
		peers = append(peers, p)
	}
	sort.Ints(peers)
	for _, p := range peers {
		_, _ = fmt.Fprintf(&b, " p%d:ni=%d/mi=%d", p, ls.nextIndex[p], ls.matchIndex[p])
	}
	return b.String()
}
