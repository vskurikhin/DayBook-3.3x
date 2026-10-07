package raft

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/raft/internal/tracelog"
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

// format — строка отчёта со снимком агрегатов. Чистая функция: без доступа
// к глобальным переменным, полям CM и логирования.
// Колонки фиксированы (%5.2fms), имена сокращены (TakeSnap и т. п.).
// Префикс и метку времени добавляют снаружи (логгер, traceLogf).
// Аргумент — указатель (88 Б > порог gocritic:hugeParam).
func (r *latencyReport) format() string {
	return fmt.Sprintf(
		"AE=%5.2fms, BatchingFSM=%5.2fms, Election=%5.2fms, FSMSnapSh=%5.2fms,"+
			" InstSnapShot=%5.2fms, ProcessLog=%5.2fms, RqPVt=%5.2fms, RqVote=%5.2fms,"+
			" SendBatch=%5.2fms, TakeSnap=%5.2fms, TmOutNowRq=%5.2fms",
		r.appendEntries, r.fsmApply, r.election, r.handleFsmSnapshot,
		r.installSnapshot, r.processLogs, r.requestPreVote, r.requestVote,
		r.sendBatch, r.takeSnapshot, r.timeoutNowRequest,
	)
}

// statsPeer — пара nextIndex/matchIndex одного соседа в снимке отчёта.
// Значения скопированы из карт leaderState; срез пар принадлежит снимку.
type statsPeer struct {
	id         int
	nextIndex  int
	matchIndex int
}

// stepDownSnapshot — значения счётчиков шагов лидера вниз, снятые в снимок.
type stepDownSnapshot struct {
	higherTerm, checkQuorum, configExit int64
}

// report форматирует значения шагов вниз; чистая функция без доступа
// к состоянию CM.
func (s stepDownSnapshot) report() string {
	return fmt.Sprintf(
		"SDterm=%d SDquorum=%d SDconfig=%d",
		s.higherTerm, s.checkQuorum, s.configExit,
	)
}

// statsCountersSnapshot — согласованная копия счётчиков Raft, шагов вниз,
// пар соседей и размера незафиксированного хвоста. Формируется под cm.mu
// (countersSnapshotLocked); сортировка и форматирование выполняются после
// освобождения мьютекса и не читают живое состояние CM: срез пар — копия,
// суммы карт уже сведены в скаляры.
type statsCountersSnapshot struct {
	installSnapshotSent           int64
	installSnapshotReceived       int64
	installSnapshotSkippedStale   int64
	appendEntriesRejected         int64
	nextIndexRejectionIgnored     int64
	snapshotLogBoundaryViolation  int64
	snapshotIndexBehindDispatched int64
	sendBatchEntrySkipped         int64
	verifyCompleted               int64
	verifyWaitedHeartbeat         int64
	aeSentPerPeer                 int64
	verifyRedispatched            int64
	verifyRedispatchSuppressed    int64
	stepDowns                     stepDownSnapshot
	peers                         []statsPeer
	uncommitted                   int
}

// countersSnapshotLocked снимает значения счётчиков и копирует пары соседей
// в собственный срез снимка. Атомарные поля читаются через Load, суммы карт
// вычисляются здесь же. Формирование строки выполняется после освобождения
// мьютекса.
// Требует удержания cm.mu — мьютекса владельца карт.
func (cm *ConsensusModule) countersSnapshotLocked() statsCountersSnapshot {
	return statsCountersSnapshot{
		installSnapshotSent:           peerSumLocked(cm.counters.installSnapshotSent),
		installSnapshotReceived:       cm.counters.installSnapshotReceived.Load(),
		installSnapshotSkippedStale:   peerSumLocked(cm.counters.installSnapshotSkippedStale),
		appendEntriesRejected:         peerSumLocked(cm.counters.appendEntriesRejected),
		nextIndexRejectionIgnored:     peerSumLocked(cm.counters.nextIndexRejectionIgnored),
		snapshotLogBoundaryViolation:  cm.counters.snapshotLogBoundaryViolation.Load(),
		snapshotIndexBehindDispatched: cm.counters.snapshotIndexBehindDispatched.Load(),
		sendBatchEntrySkipped:         cm.counters.sendBatchEntrySkipped.Load(),
		verifyCompleted:               cm.counters.verifyCompleted,
		verifyWaitedHeartbeat:         cm.counters.verifyWaitedHeartbeat,
		aeSentPerPeer:                 peerSumLocked(cm.counters.aeSentPerPeer),
		verifyRedispatched:            peerSumLocked(cm.counters.verifyRedispatched),
		verifyRedispatchSuppressed:    peerSumLocked(cm.counters.verifyRedispatchSuppressed),
		stepDowns:                     cm.counters.stepDowns.snapshot(),
		peers:                         leaderPeersLocked(&cm.leaderState),
		uncommitted:                   cm.uncommittedLogLenLocked(),
	}
}

// leaderPeersLocked копирует пары ID/nextIndex/matchIndex лидера в срез
// снимка. Порядок обхода карты не определён; сортировка по ID выполняется
// форматтером после освобождения мьютекса.
// Требует удержания cm.mu.
func leaderPeersLocked(ls *leaderState) []statsPeer {
	peers := make([]statsPeer, 0, len(ls.nextIndex))
	for id, next := range ls.nextIndex {
		peers = append(peers, statsPeer{id: id, nextIndex: next, matchIndex: ls.matchIndex[id]})
	}
	return peers
}

// report форматирует строку счётчиков из снимка: суммы, показатели
// по каждому соседу (в порядке возрастания ID), шаги лидера вниз и размер
// незафиксированного хвоста. Чистая функция: сортирует собственный срез пар
// снимка, не обращается к живым картам и полям CM.
func (s *statsCountersSnapshot) report() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b,
		"ISsent=%d ISrecv=%d ISstale=%d AErej=%d NIrejIgn=%d BndViol=%d SnapLag=%d "+
			"BatchSkip=%d VrfDone=%d VrfWtd=%d AESent=%d "+
			"VrfRedisp=%d VrfRedispSupp=%d",
		s.installSnapshotSent, s.installSnapshotReceived, s.installSnapshotSkippedStale,
		s.appendEntriesRejected, s.nextIndexRejectionIgnored, s.snapshotLogBoundaryViolation,
		s.snapshotIndexBehindDispatched, s.sendBatchEntrySkipped,
		s.verifyCompleted, s.verifyWaitedHeartbeat, s.aeSentPerPeer,
		s.verifyRedispatched, s.verifyRedispatchSuppressed)
	sort.Slice(s.peers, func(i, j int) bool { return s.peers[i].id < s.peers[j].id })
	for _, p := range s.peers {
		_, _ = fmt.Fprintf(&b, " p%d:ni=%d/mi=%d", p.id, p.nextIndex, p.matchIndex)
	}
	_, _ = fmt.Fprintf(&b, " %s Uncommitted=%d", s.stepDowns.report(), s.uncommitted)
	return b.String()
}

// uncommittedLogLenLocked возвращает размер незафиксированного хвоста
// журнала — число записей, добавленных в журнал, но ещё не зафиксированных.
// Величина считается по индексам (lastLogIndex - commitIndex), а не по длине
// среза журнала: после сжатия журнала позиция в срезе не равна индексу записи.
// Требует удержания cm.mu.
func (cm *ConsensusModule) uncommittedLogLenLocked() int {
	return cm.cmState.lastLogIndex - cm.cmState.commitIndex
}

// statsSnapshot — согласованный снимок состояния CM для одного выпуска
// периодического отчёта. Все значения скопированы; ссылок на карты, журнал
// и другие изменяемые коллекции CM нет: даже пары соседей лежат в counters.peers
// собственной копией среза.
type statsSnapshot struct {
	at       time.Time     // момент снятия; несёт монотонные часы
	age      time.Duration // монотонный возраст CM от создания
	instance uint64        // идентификатор экземпляра CM для PersistV1
	role     CMState
	id       int
	term     int
	latency  latencyReport
	counters statsCountersSnapshot
}

// takeStatsSnapshot снимает состояние отчёта за один захват cm.mu: роль,
// терм и ID, защищённые счётчики с суммами карт, пары соседей, размер
// незафиксированного хвоста и момент снятия. Здесь же ровно один раз
// выполняется сброс агрегатов латентности — как при включённом, так и при
// выключенном выводе. Атомарные поля читаются через Load.
// Блокировка берётся и снимается в этой функции.
func (cm *ConsensusModule) takeStatsSnapshot() statsSnapshot {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return statsSnapshot{
		at:       time.Now(),
		age:      time.Since(cm.statsStartedAt),
		instance: cm.statsInstance,
		role:     cm.cmState.state,
		id:       cm.id,
		term:     cm.cmState.currentTerm,
		latency:  cm.latency.snapshotAndReset(),
		counters: cm.countersSnapshotLocked(),
	}
}

// publishStats выполняет один выпуск периодического отчёта: снимает
// согласованный снимок и, если вывод разрешён, пишет три строки в out —
// латентность, счётчики Raft, PersistV1 — уже без cm.mu. Порядок строк
// фиксирован; префикс всех трёх строк взят из одного снимка.
//
// Первая неуспешная строка прекращает текущий выпуск; ошибка становится
// липкой и публикуется в следующей успешной PersistV1. Одна диагностическая
// попытка в diag выполняется на каждый неуспешный выпуск; ошибка самой
// диагностики входит в липкую ошибку, пока та ещё не установлена. Raft
// из-за ошибок стандартного вывода не останавливается — следующий тик
// повторяет попытку.
//
// Владелец seq и липкой ошибки — этот путь; в производстве его вызывает
// единственная горутина stats. Прямой вызов в тесте допустим только на CM
// без запущенной stats.
func (cm *ConsensusModule) publishStats(out, diag io.Writer) {
	snap := cm.takeStatsSnapshot()
	if cm.disableStatsOutput {
		return
	}
	cm.statsSeq++
	prefix := tracelog.FormatPrefix(tracelog.Prefix{
		Letter: stateLetter(snap.role),
		ID:     snap.id,
		Term:   snap.term,
	})
	lines := []string{
		prefix + snap.latency.format(),
		prefix + snap.counters.report(),
		prefix + snap.persistReport(cm.statsSeq, cm.statsOutputErr, _statsPersistLineLimit-len(prefix)),
	}
	for _, line := range lines {
		if err := writeStatsLine(out, line); err != nil {
			cm.recordStatsOutputError(err, diag)
			return
		}
	}
}

// writeStatsLine пишет полную строку отчёта одним вызовом Write, добавляя
// перевод строки. Короткая запись трактуется как ошибка (io.ErrShortWrite).
// Нулевой приёмник возвращает ошибку вызывающему, а не паникует.
func writeStatsLine(w io.Writer, line string) error {
	if w == nil {
		return errors.New("raft: periodic stats writer is nil")
	}
	line += "\n"
	n, err := io.WriteString(w, line)
	if err == nil && n < len(line) {
		err = io.ErrShortWrite
	}
	return err
}

// recordStatsOutputError фиксирует неуспех выпуска: первая ошибка остается
// липкой до конца жизни CM. Диагностическая запись выполняется один раз на
// выпуск; её ошибка присоединяется к липкой, только если липкая ещё
// не установлена — первая ошибка не заменяется.
func (cm *ConsensusModule) recordStatsOutputError(err error, diag io.Writer) {
	first := cm.statsOutputErr == nil
	if first {
		cm.statsOutputErr = err
	}
	if diag == nil {
		return
	}
	diagErr := writeStatsLine(diag, "raft: stats output error: "+err.Error())
	if first && diagErr != nil {
		cm.statsOutputErr = errors.Join(err, diagErr)
	}
}

// stats — секундный цикл периодического отчёта. Сбор выполняется на каждом
// тике независимо от настройки вывода; seq и липкая ошибка принадлежат
// этой горутине.
func (cm *ConsensusModule) stats(shutdownCh chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-shutdownCh:
			return
		case <-ticker.C:
			cm.publishStats(os.Stdout, os.Stderr)
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

	// stepDowns — шаги лидера вниз, в ведомые, с разбивкой по причине.
	stepDowns stepDownCounters

	// sendBatchEntrySkipped — записи, пропущенные при сборке батча для
	// машины состояний (запись выше конца журнала, позиция вне журнала,
	// несовпадение индекса). Штатная причина — сжатие журнала: запись уже
	// покрыта снимком.
	sendBatchEntrySkipped atomic.Int64

	// verifyCompleted — успешно завершённые verify-запросы (ReadIndex),
	// получившие кворум голосов через очередь pendingVerify. Знаменатель доли
	// «дождавшихся пульса». Защищён cm.mu: инкремент — под уже удерживаемым
	// cm.mu в countVerifyVotesLocked, чтение — из горутины stats.
	verifyCompleted int64

	// verifyWaitedHeartbeat — успешно завершённые verify-запросы, между
	// постановкой в pendingVerify и завершением которых успел сработать тик
	// пульса (leaderState.heartbeatTicks вырос относительно значения при
	// постановке). Числитель доли. Защищён cm.mu: инкремент — под уже
	// удерживаемым cm.mu, чтение — из горутины stats.
	verifyWaitedHeartbeat int64

	// aeSentPerPeer — исходящие AppendEntries по соседу: фактическая отправка
	// через транспорт. Контроль частоты исходящих AE (немедленная
	// перерассылка не должна превращаться в пульсацию). Защищён cm.mu:
	// инкремент — под уже удерживаемым cm.mu, чтение — из горутины stats.
	aeSentPerPeer map[int]int64

	// verifyRedispatched — фактические немедленные перерассылки AppendEntries
	// по соседу при неудовлетворённом verify-запросе (после успешного захвата
	// флага и запуска горутины). Числитель границы частоты перерассылок.
	// Защищён cm.mu: инкремент — под уже удерживаемым cm.mu, чтение — из
	// горутины stats.
	verifyRedispatched map[int]int64

	// verifyRedispatchSuppressed — перерассылки AppendEntries, подавленные
	// окном троттлинга (окно ещё не истекло) по соседу. Наблюдаемость того,
	// что троттлинг срабатывает при плотном потоке verify. Защищён cm.mu:
	// инкремент — под уже удерживаемым cm.mu, чтение — из горутины stats.
	verifyRedispatchSuppressed map[int]int64
}

// stepDownCounters — шаги лидера вниз, в ведомые, по причинам:
// higherTerm — обнаружен больший терм; checkQuorum — кворум голосующих
// не отвечал дольше checkQuorumTimeout; configExit — лидер не входит
// в зафиксированную конфигурацию. Разбивка отвечает на вопрос
// «шаги вниз вызваны потерей контакта или чем-то иным?», на который
// суммарный счётчик ответить не способен. Поля — atomic.Int64,
// инкремент без захвата cm.mu.
type stepDownCounters struct {
	higherTerm, checkQuorum, configExit atomic.Int64
}

// snapshot копирует значения счётчиков шагов вниз в снимок отчёта.
func (c *stepDownCounters) snapshot() stepDownSnapshot {
	return stepDownSnapshot{
		higherTerm:  c.higherTerm.Load(),
		checkQuorum: c.checkQuorum.Load(),
		configExit:  c.configExit.Load(),
	}
}

// incPeerCountLocked инкрементирует счётчик по каждому соседу, лениво
// инициализируя карту (нулевое значение raftCounters должно быть готово
// к использованию), и возвращает (возможно, переаллоцированную) карту.
// Требует удержания cm.mu — мьютекса владельца карты.
func incPeerCountLocked(m map[int]int64, peerID int) map[int]int64 {
	if m == nil {
		m = make(map[int]int64)
	}
	m[peerID]++
	return m
}

// peerSumLocked суммирует значения карты по каждому соседу. Вызывается
// горутиной stats для отчёта, не для горячего пути.
// Требует удержания cm.mu — мьютекса владельца карты.
func peerSumLocked(m map[int]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}
