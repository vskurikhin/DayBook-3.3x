package raft

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"

	"github.com/vskurikhin/raft/internal/tracelog"
)

// TestTracePrefix проверяет табличный контракт границы raft → tracelog:
// сопоставление состояния консенсус-модуля букве префикса (F/L/C и
// отметка «?» для PreCandidate/Dead) и побайтовый формат префикса через
// единственный источник tracelog.FormatPrefix. Идентификатор узла
// 0/1/99, терм типа int, включая значение больше 999.
func TestTracePrefix(t *testing.T) {
	tests := []struct {
		name  string
		state CMState
		id    int
		term  int
		want  string
	}{
		{"follower, нули", Follower, 0, 0, "[F,N:0,T:000] "},
		{"follower, терм 7", Follower, 1, 7, "[F,N:1,T:007] "},
		{"candidate, id и терм 99", Candidate, 99, 99, "[C,N:99,T:099] "},
		{"leader, терм больше 999", Leader, 1, 1000, "[L,N:1,T:1000] "},
		{"pre-candidate без буквы", PreCandidate, 1, 7, "[?,N:1,T:007] "},
		{"dead без буквы", Dead, 1, 7, "[?,N:1,T:007] "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tracelog.FormatPrefix(tracelog.Prefix{
				Letter: stateLetter(tc.state),
				ID:     tc.id,
				Term:   tc.term,
			})
			if got != tc.want {
				t.Fatalf("FormatPrefix(stateLetter(%v), %d, %d) = %q, ожидалось %q",
					tc.state, tc.id, tc.term, got, tc.want)
			}
		})
	}
}

// traceTestTimeLayout — формат метки события для построения ожидаемых
// полных строк тестов границы; побайтовый контракт самой метки проверяется
// тестами пакета tracelog.
const traceTestTimeLayout = "2006/01/02 15:04:05.000000"

// traceTestTimeout — предельное время всех ожиданий в тестах границы
// трассировки. Ни одно ожидание не бесконечно: зависший сценарий падает,
// а не висит.
const traceTestTimeout = 2 * time.Second

// traceTestClock — управляемые часы писателя. Каждый вызов Now
// возвращает текущее значение и сдвигает его на заданный шаг; нулевой
// шаг фиксирует часы. Доступ синхронизирован, поэтому часы пригодны для
// конкурентных постановок.
type traceTestClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

// newTraceTestClock создаёт часы со стартовым временем и шагом сдвига.
func newTraceTestClock(start time.Time, step time.Duration) *traceTestClock {
	return &traceTestClock{now: start, step: step}
}

// Now возвращает текущее значение и сдвигает часы на шаг.
func (c *traceTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := c.now
	c.now = c.now.Add(c.step)
	return at
}

// newTraceTestFollower собирает ведомого с двумя записями журнала и
// заполненной картой термов: сочетание достижимо после обработки
// AppendEntries. Горутин нет, лидерских карт нет — они появляются
// только у лидера. Приспособление используется как источник ссылочных
// аргументов общего пути постановки.
func newTraceTestFollower() *ConsensusModule {
	cm := &ConsensusModule{}
	cm.id = 7
	cm.cmState.state = Follower
	cm.cmState.currentTerm = 3
	cm.cmState.votedFor = -1
	cm.cmState.lastSnapshotIndex = -1
	cm.cmState.lastSnapshotTerm = -1
	cm.cmState.log = []LogEntry{
		{Index: 1, Term: 1, Type: LogCommand},
		{Index: 2, Term: 3, Type: LogCommand},
	}
	cm.cmState.lastLogIndex = 2
	cm.cmState.lastLogTerm = 3
	cm.cmState.commitIndex = 2
	cm.cmState.termIndexMap = map[int]int{1: 1, 3: 2}
	return cm
}

// traceTestSink — управляемый приёмник полных строк. Если задан hook, он
// выполняет запись (блокировка); иначе строка добавляется в буфер под
// мьютексом.
type traceTestSink struct {
	mu    sync.Mutex
	lines []string
	hook  func(p []byte) (int, error)
}

// Write принимает полную строку одним вызовом.
func (s *traceTestSink) Write(p []byte) (int, error) {
	if s.hook != nil {
		return s.hook(p)
	}
	s.appendLine(p)
	return len(p), nil
}

// appendLine добавляет строку в буфер; используется hook-сценариями.
func (s *traceTestSink) appendLine(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, string(p))
}

// Lines возвращает копию записанных строк.
func (s *traceTestSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

// newTraceBlockingSink создаёт приёмник, который блокирует первую запись
// до вызова unblock. started закрывается при входе в первую запись, что
// позволяет тесту детерминированно узнать: писатель взял writeMu и
// выполняет ввод-вывод. Разблокирование дополнительно регистрируется в
// Cleanup, поэтому даже после Fatal теста управляемый приёмник будет
// освобождён и проверка утечек не зависнет.
func newTraceBlockingSink(t *testing.T) (sink *traceTestSink, started <-chan struct{}, unblock func()) {
	t.Helper()
	sink = &traceTestSink{}
	startedCh := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	sink.hook = func(p []byte) (int, error) {
		startedOnce.Do(func() { close(startedCh) })
		<-release
		sink.appendLine(p)
		return len(p), nil
	}
	var releaseOnce sync.Once
	unblock = func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return sink, startedCh, unblock
}

// startTraceTestWriter создаёт отдельный писатель tracelog с внедрёнными
// приёмником, ёмкостью и часами. Глобальный уровень трассировки не
// используется и не меняется. Порядок Cleanup: сначала leaktest, затем
// остановка писателя, поэтому LIFO-исполнение — остановка, затем
// проверка отсутствия утечек.
func startTraceTestWriter(t *testing.T, sink io.Writer, capacity int, clock func() time.Time) *tracelog.Writer {
	t.Helper()
	t.Cleanup(leaktest.CheckTimeout(t, LeaktestBudget))
	w := tracelog.New(sink, capacity, clock)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
		defer cancel()
		if err := w.Shutdown(ctx); err != nil {
			t.Errorf("shutdown trace writer: %v", err)
		}
	})
	return w
}

// waitTraceTestChan ожидает закрытия или события канала с предельным
// временем теста.
func waitTraceTestChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(traceTestTimeout):
		t.Fatalf("ожидание %s: превышено предельное время %v", what, traceTestTimeout)
	}
}

// flushTraceTest ставит Flush-маркер и возвращает записанные строки.
func flushTraceTest(t *testing.T, w *tracelog.Writer, sink *traceTestSink) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancel()
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return sink.Lines()
}

// fullTraceTestLine собирает ожидаемую полную строку по тем же правилам
// контракта: метка события, пробел, префикс, тело и перевод строки
// только при его отсутствии.
func fullTraceTestLine(at time.Time, letter rune, id, term int, body string) string {
	line := at.Format(traceTestTimeLayout) + " " +
		tracelog.FormatPrefix(tracelog.Prefix{Letter: letter, ID: id, Term: term}) + body
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	return line
}

// TestTraceWriterLockedEnqueueReleasesCMLock проверяет ненасыщенный путь
// под cm.mu: пока приёмник заблокирован и удерживает writeMu, общий
// enqueueTraceLocked завершается и другой участник получает cm.mu
// до разрешения ввода-вывода.
func TestTraceWriterLockedEnqueueReleasesCMLock(t *testing.T) {
	cm := newTraceTestFollower()
	sink, started, unblock := newTraceBlockingSink(t)
	start := time.Date(2026, 12, 12, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, traceQueueCapacity, clock.Now)

	w.EnqueueText(tracelog.Prefix{Letter: 'F', ID: 7, Term: 3}, "warmup")
	waitTraceTestChan(t, started, "блокировки писателя")

	enqueueDone := make(chan struct{})
	go func() {
		cm.mu.Lock()
		cm.enqueueTraceLocked(w, "unsaturated %d", 42)
		cm.mu.Unlock()
		close(enqueueDone)
	}()
	waitTraceTestChan(t, enqueueDone, "постановки через cm.mu при заблокированном писателе")

	acquired := make(chan struct{})
	go func() {
		cm.mu.Lock()
		close(acquired)
		cm.mu.Unlock()
	}()
	waitTraceTestChan(t, acquired, "получения cm.mu до разрешения Write")

	unblock()
	lines := flushTraceTest(t, w, sink)
	want := []string{
		fullTraceTestLine(start, 'F', 7, 3, "warmup"),
		fullTraceTestLine(start.Add(time.Microsecond), 'F', 7, 3, "unsaturated 42"),
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("строки %q, ожидались %q", lines, want)
	}
}

// TestTraceWriterSprintfLockedSnapshot доказывает, что общий
// enqueueTraceSprintfLocked снимает карту, срез и указатель ответа в
// строку синхронно под cm.mu: мутация после снятия блокировки не
// изменяет записанный текст, гонки нет.
func TestTraceWriterSprintfLockedSnapshot(t *testing.T) {
	cm := newTraceTestFollower()
	reply := &RequestVoteReply{Term: 5}
	sink, started, unblock := newTraceBlockingSink(t)
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 16, clock.Now)

	// Прогревочная строка задерживает писателя: текст ссылочного
	// сообщения обязан быть снят до мутации.
	w.EnqueueText(tracelog.Prefix{Letter: 'F', ID: 7, Term: 3}, "blocker")
	waitTraceTestChan(t, started, "блокировки писателя")

	const format = "log=%v map=%v reply=%+v"
	cm.mu.Lock()
	snapshot := fmt.Sprintf(format, cm.cmState.log, cm.cmState.termIndexMap, reply)
	cm.enqueueTraceSprintfLocked(w, format, cm.cmState.log, cm.cmState.termIndexMap, reply)
	cm.mu.Unlock()

	// Мутации после снятия cm.mu не должны попасть в записанный текст.
	cm.cmState.log[0].Term = 99
	cm.cmState.termIndexMap[1] = 99
	reply.Term = 99
	unblock()

	lines := flushTraceTest(t, w, sink)
	if len(lines) != 2 {
		t.Fatalf("записано %d строк, ожидалось 2", len(lines))
	}
	if !strings.Contains(lines[1], snapshot) {
		t.Fatalf("строка %q не содержит снимок %q", lines[1], snapshot)
	}
	if strings.Contains(lines[1], "99") {
		t.Fatalf("в строку попала мутация после снятия cm.mu: %q", lines[1])
	}
}
