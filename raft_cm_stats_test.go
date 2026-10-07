package raft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft/pkg/raft/store"
)

// -- Тесты единого периодического отчёта (RC-8): снимок, публикация,
// -- независимое отключение вывода и учёт ошибок. --
//
// Правило детерминизма: seq и липкая ошибка принадлежат горутине stats.
// Все проверки этих значений выполняются на литеральном CM без запущенной
// stats — прямой вызов публикации синхронно владеет состоянием. Проверки
// живого Server наблюдают только синхронизированно собранный вывод.

// statsTestCountersLine — эталон второй строки отчёта для newStatsTestCM:
// суммы карт, пары соседей (по возрастанию ID), шаги вниз и хвост журнала.
// Формат сверяется побайтово и не зависит от способа снятия снимка.
const statsTestCountersLine = "ISsent=2 ISrecv=0 ISstale=0 AErej=1 NIrejIgn=0 BndViol=0 " +
	"SnapLag=0 BatchSkip=0 VrfDone=0 VrfWtd=0 AESent=0 VrfRedisp=0 VrfRedispSupp=0 " +
	"p1:ni=25/mi=24 p2:ni=30/mi=29 SDterm=0 SDquorum=0 SDconfig=0 Uncommitted=2"

// statsTestLatencyLine — эталон первой строки для наблюдений 2 мс и 1 мс:
// колонки и точность прежнего формата сохранены, окно снимается публикацией.
const statsTestLatencyLine = "AE= 2.00ms, BatchingFSM= 0.00ms, Election= 0.00ms, FSMSnapSh= 0.00ms," +
	" InstSnapShot= 0.00ms, ProcessLog= 1.00ms, RqPVt= 0.00ms, RqVote= 0.00ms," +
	" SendBatch= 0.00ms, TakeSnap= 0.00ms, TmOutNowRq= 0.00ms"

// newStatsTestCM создаёт литеральный CM с детерминированным состоянием
// отчёта: лидер терма 7 с двумя соседями, счётчиками и возрастом 2 с.
// Горутины не запускаются, stats не работает — прямой вызов публикации
// безопасен и единолично владеет seq и липкой ошибкой.
func newStatsTestCM() *ConsensusModule {
	return &ConsensusModule{
		id: 2,
		cmState: cmState{
			state:        Leader,
			currentTerm:  7,
			lastLogIndex: 5,
			commitIndex:  3,
		},
		counters: raftCounters{
			installSnapshotSent:   map[int]int64{1: 2},
			appendEntriesRejected: map[int]int64{2: 1},
		},
		leaderState: leaderState{
			nextIndex:  map[int]int{2: 30, 1: 25},
			matchIndex: map[int]int{2: 29, 1: 24},
		},
		statsStartedAt: time.Now().Add(-2 * time.Second),
		statsInstance:  42,
	}
}

// statsLines разбирает вывод публикации на строки отчёта: три полные
// строки, каждая — с переводом строки.
func statsLines(t *testing.T, out string) []string {
	t.Helper()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("вывод публикации без завершающего перевода строки: %q", out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("строк отчёта %d, ожидалось 3:\n%s", len(lines), out)
	}
	return lines
}

// statsBodyAfterPrefix возвращает тело строки отчёта без обязательного
// префикса снимка. Тело — всегда корректный JSON, поэтому поиск идёт
// по завершающей скобке префикса.
func statsBodyAfterPrefix(t *testing.T, line string) string {
	t.Helper()
	idx := strings.Index(line, "] ")
	if idx < 0 {
		t.Fatalf("строка без обязательного префикса: %q", line)
	}
	return line[idx+2:]
}

// statsThirdLineBody возвращает тело третьей строки (PersistV1) без префикса.
func statsThirdLineBody(t *testing.T, out string) string {
	t.Helper()
	lines := statsLines(t, out)
	return statsBodyAfterPrefix(t, lines[2])
}

// statsDecodePersistV1 разбирает тело PersistV1 в документ схемы.
func statsDecodePersistV1(t *testing.T, body string) statsPersistV1 {
	t.Helper()
	var doc statsPersistV1
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("PersistV1 не является корректным JSON: %v\n%s", err, body)
	}
	return doc
}

// TestStatsPublishThreeLines — базовый контракт разрешённого вывода:
// одна публикация даёт ровно три строки в фиксированном порядке
// (латентность, счётчики, PersistV1), все три несут префикс одного снимка,
// старые форматы совпадают побайтово с эталонами, PersistV1 содержит
// обязательные поля, а окно латентности сброшено ровно один раз.
func TestStatsPublishThreeLines(t *testing.T) {
	cm := newStatsTestCM()
	cm.latency.appendEntries.observe(2 * time.Millisecond)
	cm.latency.processLogs.observe(time.Millisecond)

	var out, diag bytes.Buffer
	cm.publishStats(&out, &diag)

	if diag.Len() != 0 {
		t.Fatalf("диагностический вывод при успехе: %q", diag.String())
	}
	if cm.statsSeq != 1 {
		t.Fatalf("statsSeq = %d, want 1", cm.statsSeq)
	}
	if cm.statsOutputErr != nil {
		t.Fatalf("statsOutputErr = %v, want nil", cm.statsOutputErr)
	}

	lines := statsLines(t, out.String())
	const prefix = "[L,N:2,T:007] "
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("строка %d без префикса снимка: %q", i+1, line)
		}
	}
	if got := strings.TrimPrefix(lines[0], prefix); got != statsTestLatencyLine {
		t.Fatalf("строка латентности:\n got = %q\nwant = %q", got, statsTestLatencyLine)
	}
	if got := strings.TrimPrefix(lines[1], prefix); got != statsTestCountersLine {
		t.Fatalf("строка счётчиков:\n got = %q\nwant = %q", got, statsTestCountersLine)
	}

	doc := statsDecodePersistV1(t, statsThirdLineBody(t, out.String()))
	if doc.Schema != 1 {
		t.Errorf("Schema = %d, want 1", doc.Schema)
	}
	if doc.Instance != 42 {
		t.Errorf("Instance = %d, want 42", doc.Instance)
	}
	if doc.Seq != 1 {
		t.Errorf("Seq = %d, want 1", doc.Seq)
	}
	if doc.Role != "Leader" {
		t.Errorf("Role = %q, want Leader", doc.Role)
	}
	if doc.Term != 7 {
		t.Errorf("Term = %d, want 7", doc.Term)
	}
	if doc.Persistence != _statsGroupUnavailable || doc.Dirty != _statsGroupUnavailable {
		t.Errorf("группы = (%q, %q), want обе %q", doc.Persistence, doc.Dirty, _statsGroupUnavailable)
	}
	if doc.StorageSnapshotNs != nil {
		t.Errorf("StorageSnapshotNs = %v, want null (возможность ещё не реализована)", *doc.StorageSnapshotNs)
	}
	if doc.OutputError != "" {
		t.Errorf("OutputError = %q, want пустую строку", doc.OutputError)
	}
	if doc.AgeNs < int64(2*time.Second) {
		t.Errorf("AgeNs = %d, want >= %d", doc.AgeNs, int64(2*time.Second))
	}
	if doc.CmSnapshotNs <= 0 {
		t.Errorf("CmSnapshotNs = %d, want > 0", doc.CmSnapshotNs)
	}
	if _, err := time.Parse(time.RFC3339Nano, doc.Utc); err != nil {
		t.Errorf("UTC %q не разбирается: %v", doc.Utc, err)
	}

	// Ровно один сброс окна на публикацию: оба наблюдения ушли в снимок,
	// повторного накопления нет.
	if got := cm.latency.appendEntries.count.Load(); got != 0 {
		t.Errorf("appendEntries.count после публикации = %d, want 0", got)
	}
	if got := cm.latency.appendEntries.sumUs.Load(); got != 0 {
		t.Errorf("appendEntries.sumUs после публикации = %d, want 0", got)
	}
	if got := cm.latency.processLogs.count.Load(); got != 0 {
		t.Errorf("processLogs.count после публикации = %d, want 0", got)
	}
}

// TestStatsPublishDisabledCollectsNoOutput — stats-output=false:
// публикация не пишет ни одного байта и не трогает seq/липкую ошибку,
// но секундный сбор продолжается — окна латентности снимаются и сбрасываются
// на каждой публикации, старые окна не накапливаются.
func TestStatsPublishDisabledCollectsNoOutput(t *testing.T) {
	cm := newStatsTestCM()
	cm.disableStatsOutput = true

	sink := &countingWriter{}
	// Накопительные счётчики продолжают жить при выключенном выводе:
	// публикация их не обнуляет и не подменяет.
	cm.counters.installSnapshotReceived.Add(5)
	cm.counters.stepDowns.higherTerm.Add(1)
	cm.latency.election.observe(time.Millisecond)
	cm.publishStats(sink, sink)
	cm.latency.election.observe(time.Millisecond)
	cm.publishStats(sink, sink)

	if sink.calls != 0 {
		t.Fatalf("выключенный вывод выполнил %d записей, want 0", sink.calls)
	}
	if cm.statsSeq != 0 {
		t.Fatalf("statsSeq = %d, want 0 (при выключенном выводе попыток нет)", cm.statsSeq)
	}
	if cm.statsOutputErr != nil {
		t.Fatalf("statsOutputErr = %v, want nil (ложной ошибки вывода быть не должно)", cm.statsOutputErr)
	}
	if got := cm.counters.installSnapshotReceived.Load(); got != 5 {
		t.Fatalf("installSnapshotReceived = %d, want 5 (счётчик не сброшен)", got)
	}
	if got := cm.counters.stepDowns.higherTerm.Load(); got != 1 {
		t.Fatalf("stepDowns.higherTerm = %d, want 1 (счётчик не сброшен)", got)
	}
	// Каждое окно снято и обнулено: повторное наблюдение не смешивается
	// с предыдущим, накопления старых окон нет.
	if got := cm.latency.election.count.Load(); got != 0 {
		t.Fatalf("election.count = %d, want 0", got)
	}
	if got := cm.latency.election.sumUs.Load(); got != 0 {
		t.Fatalf("election.sumUs = %d, want 0", got)
	}
}

// TestStatsPublishStopsAtFailingLine — матрица ошибок и коротких записей
// каждой из трёх строк: выпуск прекращается на первой неуспешной строке,
// ошибка становится липкой, а следующая успешная PersistV1 её показывает.
// Проверка выполняется при уровне трассировки 0 (CM без трассировки)
// и без traceWriter: учёт ошибок от него не зависит.
func TestStatsPublishStopsAtFailingLine(t *testing.T) {
	cases := []struct {
		name   string
		failAt int
		short  bool
	}{
		{name: "latency-error", failAt: 1},
		{name: "counters-error", failAt: 2},
		{name: "persist-error", failAt: 3},
		{name: "latency-short", failAt: 1, short: true},
		{name: "counters-short", failAt: 2, short: true},
		{name: "persist-short", failAt: 3, short: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := newStatsTestCM()
			sink := &failingWriter{failAt: tc.failAt, short: tc.short, failErr: errors.New("sink down")}
			var diag bytes.Buffer
			cm.publishStats(sink, &diag)

			if sink.calls != tc.failAt {
				t.Fatalf("записей в приёмник %d, want %d (выпуск обязан прекратиться)", sink.calls, tc.failAt)
			}
			if cm.statsSeq != 1 {
				t.Fatalf("statsSeq = %d, want 1 (номер попытки выпуска)", cm.statsSeq)
			}
			if cm.statsOutputErr == nil {
				t.Fatal("statsOutputErr = nil, ошибка вывода обязана быть учтена")
			}
			if tc.short && !errors.Is(cm.statsOutputErr, io.ErrShortWrite) {
				t.Fatalf("statsOutputErr = %v, want io.ErrShortWrite", cm.statsOutputErr)
			}
			if tc.short && !strings.Contains(diag.String(), "short write") {
				t.Fatalf("диагностика %q не содержит признака короткой записи", diag.String())
			}
			if !strings.Contains(diag.String(), "stats output error") {
				t.Fatalf("диагностика %q не описывает ошибку вывода", diag.String())
			}
			sticky := cm.statsOutputErr

			// Следующая успешная публикация: три строки, липкая ошибка
			// в PersistV1, сам счётчик ошибки не очищается.
			var out bytes.Buffer
			cm.publishStats(&out, io.Discard)
			if cm.statsSeq != 2 {
				t.Fatalf("statsSeq = %d, want 2", cm.statsSeq)
			}
			if cm.statsOutputErr != sticky {
				t.Fatalf("липкая ошибка заменена: %v -> %v", sticky, cm.statsOutputErr)
			}
			doc := statsDecodePersistV1(t, statsThirdLineBody(t, out.String()))
			if doc.OutputError == "" {
				t.Fatal("PersistV1 успешной публикации не содержит липкую ошибку")
			}
			if doc.Seq != 2 {
				t.Fatalf("Seq успешной публикации = %d, want 2", doc.Seq)
			}
		})
	}
}

// TestStatsPublishDiagnosticFailureJoined — результат диагностической
// записи в stderr учитывается: при отказе и основного, и диагностического
// приёмников сохраняются обе ошибки, первая остаётся первой.
func TestStatsPublishDiagnosticFailureJoined(t *testing.T) {
	cm := newStatsTestCM()
	sink := &failingWriter{failAt: 1, failErr: errors.New("primary down")}
	diagFail := &failingWriter{failAt: 1, failErr: errors.New("diag down")}
	cm.publishStats(sink, diagFail)

	if cm.statsOutputErr == nil {
		t.Fatal("statsOutputErr = nil, обе ошибки обязаны быть учтены")
	}
	text := cm.statsOutputErr.Error()
	if !strings.Contains(text, "primary down") {
		t.Fatalf("липкая ошибка %q потеряла основную ошибку", text)
	}
	if !strings.Contains(text, "diag down") {
		t.Fatalf("липкая ошибка %q потеряла ошибку диагностики", text)
	}

	// Успешная диагностика: липкая ошибка остаётся ровно основной,
	// лишние ошибки не появляются.
	cm2 := newStatsTestCM()
	var diag bytes.Buffer
	cm2.publishStats(&failingWriter{failAt: 2, failErr: errors.New("primary down")}, &diag)
	if !strings.Contains(diag.String(), "primary down") {
		t.Fatalf("диагностика %q не описывает основную ошибку", diag.String())
	}
	if strings.Contains(cm2.statsOutputErr.Error(), "diag down") {
		t.Fatalf("липкая ошибка %q содержит ошибку успешной диагностики", cm2.statsOutputErr)
	}
}

// TestStatsPublishWritesWithoutMutex — запись каждой из трёх строк
// выполняется без cm.mu: пока приёмник заблокирован в Write, другая
// горутина обязана взять cm.mu (TryLock). Тест детерминирован: точка
// блокировки задаётся каналом приёмника, без временных пауз.
func TestStatsPublishWritesWithoutMutex(t *testing.T) {
	for blockAt := 1; blockAt <= 3; blockAt++ {
		t.Run(fmt.Sprintf("line-%d", blockAt), func(t *testing.T) {
			cm := newStatsTestCM()
			sink := newGateWriter(blockAt)
			done := make(chan struct{})
			go func() {
				defer close(done)
				cm.publishStats(sink, io.Discard)
			}()

			<-sink.entered
			if !cm.mu.TryLock() {
				t.Fatalf("cm.mu удержан во время записи строки %d", blockAt)
			}
			cm.mu.Unlock()
			close(sink.release)
			<-done

			if got := strings.Count(sink.String(), "\n"); got != 3 {
				t.Fatalf("записей строк %d, want 3", got)
			}
		})
	}
}

// TestStatsSnapshotIndependentOfCM — снимок владеет своими данными:
// после освобождения cm.mu мутации карт, пар соседей и смена роли
// не изменяют ни форматирование снимка, ни его скаляры. Форматтер
// не читает живой CM.
func TestStatsSnapshotIndependentOfCM(t *testing.T) {
	cm := newStatsTestCM()
	cm.counters.verifyCompleted = 3
	cm.counters.aeSentPerPeer = map[int]int64{1: 4, 2: 5}

	snap := cm.takeStatsSnapshot()
	before := snap.counters.report()
	roleBefore, termBefore := snap.role, snap.term

	cm.mu.Lock()
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 99
	cm.counters.verifyCompleted = 0
	cm.counters.aeSentPerPeer[1] = 100
	cm.leaderState.nextIndex[1] = 1000
	cm.leaderState.matchIndex[1] = 999
	delete(cm.leaderState.nextIndex, 2)
	cm.mu.Unlock()

	if after := snap.counters.report(); after != before {
		t.Fatalf("снимок изменился вместе с CM:\nbefore = %q\nafter  = %q", before, after)
	}
	if snap.role != roleBefore || snap.term != termBefore {
		t.Fatalf("скаляры снимка изменились: роль %v->%v, терм %d->%d",
			roleBefore, snap.role, termBefore, snap.term)
	}
	if !strings.Contains(before, "VrfDone=3") || !strings.Contains(before, "AESent=9") {
		t.Fatalf("снимок потерял исходные значения: %q", before)
	}
	if !strings.Contains(before, "p1:ni=25/mi=24 p2:ni=30/mi=29") {
		t.Fatalf("снимок потерял исходные пары соседей: %q", before)
	}
}

// TestStatsSeqAndInstance — ряд PersistV1: номер выпуска растёт на каждую
// попытку, экземпляр не меняется в пределах CM и различается у разных
// экземпляров, включая создаваемые подряд.
func TestStatsSeqAndInstance(t *testing.T) {
	cm := newStatsTestCM()
	var out bytes.Buffer
	cm.publishStats(&out, io.Discard)
	cm.publishStats(&out, io.Discard)

	all := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(all) != 6 {
		t.Fatalf("строк за две публикации %d, ожидалось 6", len(all))
	}
	first := statsDecodePersistV1(t, statsBodyAfterPrefix(t, all[2]))
	second := statsDecodePersistV1(t, statsBodyAfterPrefix(t, all[5]))
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("Seq = (%d, %d), want (1, 2)", first.Seq, second.Seq)
	}
	if first.Instance != second.Instance {
		t.Fatalf("Instance изменился внутри CM: %d -> %d", first.Instance, second.Instance)
	}

	a, b := nextStatsInstance(), nextStatsInstance()
	if a == b {
		t.Fatalf("nextStatsInstance повторил значение %d", a)
	}
}

// TestStatsPersistReportLimit — предел 16 KiB относится только к PersistV1:
// длинный текст липкой ошибки не превращает строку в обрезанный JSON,
// документ остаётся корректным и не превышает предел, а признак превышения
// виден в поле ошибки. Старая строка с соседями лимитом не ограничена.
func TestStatsPersistReportLimit(t *testing.T) {
	cm := newStatsTestCM()
	cm.statsOutputErr = errors.New(strings.Repeat("x", _statsPersistLineLimit+100))

	var out bytes.Buffer
	cm.publishStats(&out, io.Discard)

	body := statsThirdLineBody(t, out.String())
	if len(body) > _statsPersistLineLimit {
		t.Fatalf("тело PersistV1 %d байт, предел %d", len(body), _statsPersistLineLimit)
	}
	thirdLine := statsLines(t, out.String())[2]
	if len(thirdLine) > _statsPersistLineLimit {
		t.Fatalf("полная строка PersistV1 %d байт, предел %d", len(thirdLine), _statsPersistLineLimit)
	}
	doc := statsDecodePersistV1(t, body)
	if doc.OutputError != _statsPersistOverflow {
		t.Fatalf("OutputError = %q, want %q", doc.OutputError, _statsPersistOverflow)
	}
	if doc.Schema != 1 || doc.Seq != 1 || doc.Instance != 42 {
		t.Fatalf("аварийный документ потерял обязательные поля: %+v", doc)
	}
}

// TestStatsPublishNoStorageWrites — выпуск отчёта не создаёт записей
// хранилища: диагностический путь работает только с памятью CM.
func TestStatsPublishNoStorageWrites(t *testing.T) {
	cm := newStatsTestCM()
	storage := store.NewFileStorage(t.TempDir())
	cm.storage = storage

	var out bytes.Buffer
	cm.publishStats(&out, io.Discard)

	if got := storage.WriteCount(); got != 0 {
		t.Fatalf("WriteCount = %d, want 0 (публикация не пишет в Storage)", got)
	}
}

// TestServerDisableStatsOutputNoLines — путь Config.DisableStatsOutput →
// Server → CM: за время жизни Server с выключенным выводом в стандартный
// поток не попадает ни одна периодическая строка, при этом секундный сбор
// работает — сброс агрегата латентности доказывает обработку тика.
func TestServerDisableStatsOutputNoLines(t *testing.T) {
	t.Cleanup(leaktest.CheckTimeout(t, LeaktestBudget))

	// Приёмник периодического отчёта — глобальный os.Stdout: подмена
	// выполняется временным файлом только на время теста и снимается
	// до leaktest.
	captureFile, err := os.CreateTemp("", "raft-stats-disabled-*.log")
	if err != nil {
		t.Fatal(err)
	}
	capturePath := captureFile.Name()
	original := os.Stdout
	os.Stdout = captureFile
	t.Cleanup(func() {
		os.Stdout = original
		_ = captureFile.Close()
		_ = os.Remove(capturePath)
	})

	commitCh := make(chan CommitEntry)
	readerDone := make(chan any)
	go func() {
		defer close(readerDone)
		for range commitCh {
		}
	}()

	ready := make(chan any)
	srv := New(&Config{
		DisableStatsOutput: true,
		Fsm:                NewCommitChannelFSM(commitCh),
		PeerIds:            []int{},
		ServerID:           0,
		Storage:            store.NewMapStorage(),
		Transport:          stubTransportManager{},
	}, ready)
	srv.Serve()
	t.Cleanup(func() {
		srv.Shutdown()
		close(commitCh)
		<-readerDone
	})
	close(ready)

	if !srv.cm.disableStatsOutput {
		t.Fatal("CM не получил DisableStatsOutput=true из Config")
	}

	// Доказательство обработки тика без чтения полей stats: наблюдаемое
	// снаружи обнуление агрегата латентности происходит только в снимке
	// секундного цикла. Значения полей CM здесь не читаются.
	srv.cm.latency.election.observe(time.Millisecond)
	err = waitCond("stats tick with disabled output", 3*time.Second,
		func() bool { return srv.cm.latency.election.count.Load() == 0 },
		func() string {
			return fmt.Sprintf("election.count=%d", srv.cm.latency.election.count.Load())
		})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("выключенный вывод написал в стандартный поток: %q", data)
	}
}

// countingWriter — приёмник, считающий вызовы Write: содержимое не важно,
// важно отсутствие записей при выключенном выводе.
type countingWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	return len(p), nil
}

// failingWriter отказывает на заданном по счёту вызове Write: ошибкой либо
// короткой записью. Остальные записи принимаются.
type failingWriter struct {
	mu      sync.Mutex
	failAt  int
	short   bool
	failErr error
	calls   int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls != w.failAt {
		return len(p), nil
	}
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.failErr
}

// gateWriter блокирует Write на заданном по счёту вызове. Тест получает
// детерминированную точку «строка пишется» через entered и освобождает
// приёмник сигналом release — без временных пауз.
type gateWriter struct {
	blockAt int
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
	buf   bytes.Buffer
}

func newGateWriter(blockAt int) *gateWriter {
	return &gateWriter{
		blockAt: blockAt,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *gateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.calls++
	blocked := w.calls == w.blockAt
	if blocked {
		close(w.entered)
	}
	w.mu.Unlock()

	if blocked {
		<-w.release
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// String возвращает накопленный вывод.
func (w *gateWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
