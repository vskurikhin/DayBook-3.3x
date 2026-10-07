package tracelog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
)

// traceTestTimeout — предельное время всех ожиданий в тестах писателя.
// Ни одно ожидание не бесконечно: зависший сценарий падает, а не висит.
const traceTestTimeout = 2 * time.Second

// leaktestBudget — единый бюджет проверки утечек для тестов пакета;
// значение совпадает с бюджетом корневого пакета raft.
const leaktestBudget = 600 * time.Millisecond

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

// Set переводит часы на заданное время.
func (c *traceTestClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// traceTestSink — управляемый приёмник полных строк. Если задан hook, он
// выполняет запись (блокировка, ошибка, короткая запись); иначе строка
// добавляется в буфер под мьютексом.
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

// startTraceTestWriter создаёт отдельный писатель с внедрёнными
// приёмником, ёмкостью и часами. Порядок Cleanup: сначала leaktest, затем
// остановка писателя, поэтому LIFO-исполнение — остановка, затем
// проверка отсутствия утечек.
func startTraceTestWriter(t *testing.T, sink io.Writer, capacity int, clock func() time.Time) *Writer {
	t.Helper()
	return startTraceTestWriterAllowing(t, sink, capacity, clock, nil)
}

// startTraceTestWriterAllowing — вариант startTraceTestWriter для
// сценариев с намеренно внедрённой ошибкой записи: ожидаемая sticky-
// ошибка не считается нарушением Cleanup, любая иная — считается.
func startTraceTestWriterAllowing(
	t *testing.T, sink io.Writer, capacity int, clock func() time.Time, allowed error,
) *Writer {
	t.Helper()
	t.Cleanup(leaktest.CheckTimeout(t, leaktestBudget))
	w := New(sink, capacity, clock)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
		defer cancel()
		if err := w.Shutdown(ctx); err != nil && (allowed == nil || !errors.Is(err, allowed)) {
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

// waitTraceTestErr ожидает результат горутинной операции с предельным
// временем теста.
func waitTraceTestErr(t *testing.T, errCh <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(traceTestTimeout):
		t.Fatalf("ожидание %s: превышено предельное время %v", what, traceTestTimeout)
		return nil
	}
}

// flushTraceTest ставит Flush-маркер и возвращает записанные строки.
func flushTraceTest(t *testing.T, w *Writer, sink *traceTestSink) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancel()
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return sink.Lines()
}

// drainTraceTestWake опустошает coalescing-сигнал писателя. Вызывается,
// когда горутина писателя детерминированно заблокирована в приёмнике:
// состояние канала в этот момент стабильно.
func drainTraceTestWake(w *Writer) {
	for {
		select {
		case <-w.wake:
		default:
			return
		}
	}
}

// splitTraceTestLine разделяет строку трассировки на метку события и
// остаток (префикс и тело). Метка разбирается в UTC: тестовые часы
// используют UTC, поэтому обратное преобразование точно.
func splitTraceTestLine(t *testing.T, line string) (time.Time, string) {
	t.Helper()
	if len(line) <= len(traceTimeLayout)+1 {
		t.Fatalf("короткая строка трассировки: %q", line)
	}
	at, err := time.ParseInLocation(traceTimeLayout, line[:len(traceTimeLayout)], time.UTC)
	if err != nil {
		t.Fatalf("разбор метки строки %q: %v", line, err)
	}
	return at, line[len(traceTimeLayout)+1:]
}

// TestTraceWriterLabelAtEnqueue доказывает, что метка события снимается в
// момент приёма сообщения, а не в момент вывода: первая строка записана
// уже после перевода часов на час вперёд, но обязана несть прежнее время.
func TestTraceWriterLabelAtEnqueue(t *testing.T) {
	sink, started, unblock := newTraceBlockingSink(t)
	first := time.Date(2026, 9, 15, 11, 19, 37, 7000, time.FixedZone("test+03", 3*60*60))
	clock := newTraceTestClock(first, 0)
	w := startTraceTestWriter(t, sink, 8, clock.Now)

	w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 7}, "first")
	waitTraceTestChan(t, started, "блокировки писателя на первой строке")

	// Часы уходят далеко вперёд, пока writer заблокирован в приёмнике:
	// вторая строка принимается уже по новому времени.
	second := first.Add(time.Hour)
	clock.Set(second)
	w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 7}, "second")
	unblock()

	lines := flushTraceTest(t, w, sink)
	want := []string{
		fullTraceTestLine(first, 'F', 1, 7, "first"),
		fullTraceTestLine(second, 'F', 1, 7, "second"),
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("строки %q, ожидались %q", lines, want)
	}
}

// TestTraceWriterSequentialFIFO проверяет 10 000 последовательных
// постановок: число, уникальность и порядок номеров, а также строго
// возрастающие метки при монотонных часах.
func TestTraceWriterSequentialFIFO(t *testing.T) {
	const total = 10000
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &traceTestSink{}
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, traceQueueCapacity, clock.Now)

	for i := 0; i < total; i++ {
		w.Enqueue(Prefix{Letter: 'L', ID: 7, Term: 99}, "seq %05d", i)
	}
	lines := flushTraceTest(t, w, sink)
	if len(lines) != total {
		t.Fatalf("записано %d строк, ожидалось %d", len(lines), total)
	}
	for i, line := range lines {
		want := fullTraceTestLine(start.Add(time.Duration(i)*time.Microsecond), 'L', 7, 99,
			fmt.Sprintf("seq %05d", i))
		if line != want {
			t.Fatalf("строка %d: %q, ожидалась %q", i, line, want)
		}
	}
}

// TestTraceWriterConcurrentFIFO проверяет 8×1000 конкурентных сообщений
// под общей блокировкой-моделью cm.mu: полноту (все номера ровно один
// раз), отсутствие обгона и дублей, возрастание меток. Часть сообщений
// проходит резервный путь переполнения, при этом порядок сохраняется.
func TestTraceWriterConcurrentFIFO(t *testing.T) {
	const (
		senders      = 8
		perSender    = 1000
		total        = senders * perSender
		prefixSuffix = "] seq "
	)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink := &traceTestSink{}
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, traceQueueCapacity, clock.Now)

	var modelMu sync.Mutex
	next := 0
	done := make(chan struct{}, senders)
	for sender := 0; sender < senders; sender++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perSender; i++ {
				modelMu.Lock()
				n := next
				next++
				w.Enqueue(Prefix{Letter: 'F', ID: id, Term: 3}, "seq %d", n)
				modelMu.Unlock()
			}
		}(sender)
	}
	for sender := 0; sender < senders; sender++ {
		waitTraceTestChan(t, done, "завершения отправителя")
	}

	lines := flushTraceTest(t, w, sink)
	if len(lines) != total {
		t.Fatalf("записано %d строк, ожидалось %d", len(lines), total)
	}
	seqPattern := regexp.MustCompile(` \[[FLC?],N:\d+,T:003\] seq (\d+)\n$`)
	var previous time.Time
	for i, line := range lines {
		match := seqPattern.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("строка %d не соответствует формату: %q", i, line)
		}
		var seq int
		if _, err := fmt.Sscanf(match[1], "%d", &seq); err != nil {
			t.Fatalf("разбор номера строки %d: %v", i, err)
		}
		if seq != i {
			t.Fatalf("строка %d содержит номер %d: порядок нарушен (обгон или дубль)", i, seq)
		}
		at, _ := splitTraceTestLine(t, line)
		if i > 0 && !at.After(previous) {
			t.Fatalf("метка строки %d (%v) не больше метки предыдущей (%v)", i, at, previous)
		}
		previous = at
	}
	if !strings.Contains(lines[0], prefixSuffix) {
		t.Fatalf("префикс первой строки не содержит %q: %q", prefixSuffix, lines[0])
	}
}

// TestTraceWriterOverflowKeepsOrder проверяет резервный путь
// переполнения на малых ёмкостях 2 и 4: заполненная очередь и уже
// изъятая писателем строка не обгоняются строкой переполнения, порядок
// постановки сохраняется полностью.
func TestTraceWriterOverflowKeepsOrder(t *testing.T) {
	for _, capacity := range []int{2, 4} {
		t.Run(fmt.Sprintf("capacity=%d", capacity), func(t *testing.T) {
			sink, started, unblock := newTraceBlockingSink(t)
			start := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
			var calls atomic.Int64
			attempts := make(chan int, capacity+2)
			clock := func() time.Time {
				n := int(calls.Add(1))
				attempts <- n
				return start.Add(time.Duration(n) * time.Microsecond)
			}
			w := startTraceTestWriter(t, sink, capacity, clock)

			w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", 0)
			waitTraceTestChan(t, started, "блокировки писателя на первой строке")

			// Строка 0 изъята писателем и ждёт приёмник; очередь
			// заполняется ровно до ёмкости.
			for i := 1; i <= capacity; i++ {
				w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", i)
			}

			// Следующая постановка обязана уйти на резервный путь:
			// очередь полна, писатель держит writeMu в приёмнике.
			overflowDone := make(chan struct{})
			go func() {
				w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", capacity+1)
				close(overflowDone)
			}()
			for want := 1; want <= capacity+2; want++ {
				select {
				case got := <-attempts:
					if got != want {
						t.Fatalf("вызов часов %d, ожидался %d", got, want)
					}
				case <-time.After(traceTestTimeout):
					t.Fatalf("часы вызваны %d раз(а), ожидался вызов %d", want-1, want)
				}
			}
			select {
			case <-overflowDone:
				t.Fatal("постановка переполнения завершилась при заблокированном писателе: строка обогнала приём")
			default:
			}

			unblock()
			waitTraceTestChan(t, overflowDone, "завершения постановки переполнения")
			lines := flushTraceTest(t, w, sink)
			if len(lines) != capacity+2 {
				t.Fatalf("записано %d строк, ожидалось %d", len(lines), capacity+2)
			}
			for i, line := range lines {
				want := fullTraceTestLine(
					start.Add(time.Duration(i+1)*time.Microsecond), 'F', 1, 1,
					fmt.Sprintf("msg %d", i))
				if line != want {
					t.Fatalf("строка %d: %q, ожидалась %q (переполнение нарушило порядок)", i, line, want)
				}
			}
		})
	}
}

// TestTraceWriterCapacityWithoutSaturation прогоняет число сообщений
// меньшее ёмкости очереди: резервный путь не запускается, порядок и
// полнота сохраняются.
func TestTraceWriterCapacityWithoutSaturation(t *testing.T) {
	const total = traceQueueCapacity - 96
	start := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	sink := &traceTestSink{}
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, traceQueueCapacity, clock.Now)

	for i := 0; i < total; i++ {
		w.Enqueue(Prefix{Letter: 'C', ID: 2, Term: 1000}, "item %d", i)
	}
	lines := flushTraceTest(t, w, sink)
	if len(lines) != total {
		t.Fatalf("записано %d строк, ожидалось %d", len(lines), total)
	}
	for i, line := range lines {
		want := fullTraceTestLine(start.Add(time.Duration(i)*time.Microsecond), 'C', 2, 1000,
			fmt.Sprintf("item %d", i))
		if line != want {
			t.Fatalf("строка %d: %q, ожидалась %q", i, line, want)
		}
	}
}

// TestTraceWriterFlushUnderContinuousSenders проверяет границу Flush при
// непрерывных отправителях: успешный возврат маркера гарантирует запись
// всех сообщений, принятых до вызова. После остановки отправителей
// итоговый Flush покрывает весь журнал без потерь и дублей.
func TestTraceWriterFlushUnderContinuousSenders(t *testing.T) {
	const senders = 4
	start := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	sink := &traceTestSink{}
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, traceQueueCapacity, clock.Now)

	var modelMu sync.Mutex
	sent := 0
	stop := make(chan struct{})
	var sendersWG sync.WaitGroup
	for sender := 0; sender < senders; sender++ {
		sendersWG.Add(1)
		go func() {
			defer sendersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				modelMu.Lock()
				n := sent
				sent++
				w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 5}, "seq %d", n)
				modelMu.Unlock()
			}
		}()
	}

	// Непрерывные отправители живы: каждый Flush обязан покрыть как
	// минимум число сообщений, принятых до его вызова.
	for round := 0; round < 3; round++ {
		modelMu.Lock()
		accepted := sent
		modelMu.Unlock()
		lines := flushTraceTest(t, w, sink)
		if len(lines) < accepted {
			t.Fatalf("Flush вернулся раньше записи %d принятых строк (записано %d)", accepted, len(lines))
		}
	}

	close(stop)
	sendersWG.Wait()
	lines := flushTraceTest(t, w, sink)
	modelMu.Lock()
	total := sent
	modelMu.Unlock()
	if len(lines) != total {
		t.Fatalf("записано %d строк, ожидалось %d", len(lines), total)
	}
	seqPattern := regexp.MustCompile(`\] seq (\d+)\n$`)
	for i, line := range lines {
		match := seqPattern.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("строка %d не соответствует формату: %q", i, line)
		}
		if match[1] != fmt.Sprintf("%d", i) {
			t.Fatalf("строка %d содержит номер %s: порядок нарушен", i, match[1])
		}
	}
}

// TestTraceWriterFlushFullQueue проверяет Flush при полной очереди:
// отменённый до освобождения писателя маркер возвращает ошибку
// контекста, последующий успешный маркер дожидается места и всех
// принятых строк; сам маркер в приёмник не пишется.
func TestTraceWriterFlushFullQueue(t *testing.T) {
	const capacity = 4
	start := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	sink, started, unblock := newTraceBlockingSink(t)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, capacity, clock.Now)

	w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", 0)
	waitTraceTestChan(t, started, "блокировки писателя на первой строке")
	for i := 1; i <= capacity; i++ {
		w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", i)
	}

	// Место под маркер ждать негде: очередь полна, писатель заблокирован.
	cancelledCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Flush(cancelledCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Flush при полной очереди вернул %v, ожидалась ошибка предельного времени", err)
	}

	successCtx, cancelSuccess := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancelSuccess()
	flushErr := make(chan error, 1)
	go func() { flushErr <- w.Flush(successCtx) }()
	unblock()
	if err := waitTraceTestErr(t, flushErr, "успешного Flush после освобождения писателя"); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	lines := sink.Lines()
	if len(lines) != capacity+1 {
		t.Fatalf("записано %d строк, ожидалось %d (маркер не является строкой)", len(lines), capacity+1)
	}
	for i, line := range lines {
		want := fullTraceTestLine(start.Add(time.Duration(i)*time.Microsecond), 'F', 1, 1,
			fmt.Sprintf("msg %d", i))
		if line != want {
			t.Fatalf("строка %d: %q, ожидалась %q", i, line, want)
		}
	}
}

// TestTraceWriterFlushCancelBeforeMarker удерживает токен admit и
// отменяет контекст Flush: маркер поставить невозможно, очередь
// остаётся пустой, писатель не получает работы. Аргументы ранее
// поставленных сообщений это не освобождает — их обработку подтверждает
// только последующий успешный Flush.
func TestTraceWriterFlushCancelBeforeMarker(t *testing.T) {
	sink := &traceTestSink{}
	start := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 8, clock.Now)

	w.admit <- struct{}{} // токен удерживается тестом: Flush ждёт вход
	ctx, cancel := context.WithCancel(context.Background())
	flushErr := make(chan error, 1)
	go func() { flushErr <- w.Flush(ctx) }()
	cancel()
	if err := waitTraceTestErr(t, flushErr, "отмены Flush до маркера"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush вернул %v, ожидалась отмена контекста", err)
	}
	<-w.admit // токен возвращается: маркер не был поставлен

	lines := flushTraceTest(t, w, sink)
	if len(lines) != 0 {
		t.Fatalf("после отменённого до маркера Flush записаны строки: %q", lines)
	}
}

// TestTraceWriterFlushCancelAfterMarker отменяет Flush после постановки
// маркера: отмена не удаляет маркер, он обрабатывается, а последующий
// успешный Flush завершается и подтверждает обработку сообщений,
// поставленных до маркера.
func TestTraceWriterFlushCancelAfterMarker(t *testing.T) {
	sink, started, unblock := newTraceBlockingSink(t)
	start := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 8, clock.Now)

	w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", 0)
	waitTraceTestChan(t, started, "блокировки писателя на первой строке")
	drainTraceTestWake(w)

	ctx, cancel := context.WithCancel(context.Background())
	flushErr := make(chan error, 1)
	go func() { flushErr <- w.Flush(ctx) }()
	waitTraceTestChan(t, w.wake, "постановки Flush-маркера")
	cancel()
	if err := waitTraceTestErr(t, flushErr, "отмены Flush после маркера"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush вернул %v, ожидалась отмена контекста", err)
	}

	unblock()
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancelSecond()
	if err := w.Flush(secondCtx); err != nil {
		t.Fatalf("повторный Flush: %v", err)
	}
	lines := sink.Lines()
	want := []string{fullTraceTestLine(start, 'F', 1, 1, "msg 0")}
	if !slices.Equal(lines, want) {
		t.Fatalf("строки %q, ожидались %q (маркер не должен писаться)", lines, want)
	}
}

// TestTraceWriterShutdownCancelAfterMarker проверяет идемпотентность
// Shutdown по состоянию режима: отменённый после постановки stop-маркера
// вызов возвращает ошибку контекста, повторный Shutdown ждёт ту же
// остановку и завершается успешно; синхронная постановка после
// остановки записывается.
func TestTraceWriterShutdownCancelAfterMarker(t *testing.T) {
	sink, started, unblock := newTraceBlockingSink(t)
	start := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 16, clock.Now)

	for i := 0; i <= 2; i++ {
		w.Enqueue(Prefix{Letter: 'F', ID: 1, Term: 1}, "msg %d", i)
	}
	waitTraceTestChan(t, started, "блокировки писателя на первой строке")
	drainTraceTestWake(w)

	ctx, cancel := context.WithCancel(context.Background())
	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- w.Shutdown(ctx) }()
	waitTraceTestChan(t, w.wake, "постановки stop-маркера")
	cancel()
	if err := waitTraceTestErr(t, shutdownErr, "отмены Shutdown после маркера"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown вернул %v, ожидалась отмена контекста", err)
	}
	if !w.syncMode.Load() {
		t.Fatal("после принятого stop-маркера режим не переведён в синхронный")
	}

	unblock()
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancelSecond()
	if err := w.Shutdown(secondCtx); err != nil {
		t.Fatalf("повторный Shutdown: %v", err)
	}
	w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "sync suffix")
	lines := sink.Lines()
	want := []string{
		fullTraceTestLine(start, 'F', 1, 1, "msg 0"),
		fullTraceTestLine(start.Add(time.Microsecond), 'F', 1, 1, "msg 1"),
		fullTraceTestLine(start.Add(2*time.Microsecond), 'F', 1, 1, "msg 2"),
		fullTraceTestLine(start.Add(3*time.Microsecond), 'F', 1, 1, "sync suffix"),
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("строки %q, ожидались %q", lines, want)
	}
}

// TestTraceWriterShutdownCancelBeforeMarker доказывает семантику
// отмены до постановки stop-маркера: состояние не меняется, повторный
// Shutdown после освобождения токена успешно останавливает писателя,
// синхронные постановки записываются. Отмена ожидания аргументы ранее
// принятых сообщений не освобождает: их обработку подтверждает только
// повторный успешный Shutdown.
func TestTraceWriterShutdownCancelBeforeMarker(t *testing.T) {
	sink := &traceTestSink{}
	start := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 8, clock.Now)

	w.admit <- struct{}{} // токен удерживается тестом: Shutdown ждёт вход
	ctx, cancel := context.WithCancel(context.Background())
	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- w.Shutdown(ctx) }()
	cancel()
	if err := waitTraceTestErr(t, shutdownErr, "отмены Shutdown до маркера"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown вернул %v, ожидалась отмена контекста", err)
	}
	if w.syncMode.Load() {
		t.Fatal("отменённый до маркера Shutdown изменил режим")
	}
	<-w.admit

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancelSecond()
	if err := w.Shutdown(secondCtx); err != nil {
		t.Fatalf("повторный Shutdown: %v", err)
	}
	w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "sync suffix")
	lines := sink.Lines()
	want := []string{fullTraceTestLine(start, 'F', 1, 1, "sync suffix")}
	if !slices.Equal(lines, want) {
		t.Fatalf("строки %q, ожидались %q", lines, want)
	}
}

// TestTraceWriterTwoShutdownsWithLiveSenders запускает два конкурентных
// Shutdown при живых отправителях: оба завершаются успешно, потерь и
// дублей нет, синхронная постановка после остановки записывается.
func TestTraceWriterTwoShutdownsWithLiveSenders(t *testing.T) {
	const (
		senders    = 4
		startAfter = 100
	)
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	sink := &traceTestSink{}
	clock := newTraceTestClock(start, time.Microsecond)
	w := startTraceTestWriter(t, sink, 64, clock.Now)

	var modelMu sync.Mutex
	sent := 0
	stop := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once
	var sendersWG sync.WaitGroup
	for sender := 0; sender < senders; sender++ {
		sendersWG.Add(1)
		go func(id int) {
			defer sendersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				modelMu.Lock()
				n := sent
				sent++
				w.Enqueue(Prefix{Letter: 'F', ID: id, Term: 1}, "seq %d", n)
				modelMu.Unlock()
				if n >= startAfter {
					startedOnce.Do(func() { close(started) })
				}
			}
		}(sender)
	}
	waitTraceTestChan(t, started, "живых отправителей")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), traceTestTimeout)
	defer cancelShutdown()
	firstErr := make(chan error, 1)
	secondErr := make(chan error, 1)
	go func() { firstErr <- w.Shutdown(shutdownCtx) }()
	go func() { secondErr <- w.Shutdown(shutdownCtx) }()
	if err := waitTraceTestErr(t, firstErr, "первого Shutdown"); err != nil {
		t.Fatalf("первый Shutdown: %v", err)
	}
	if err := waitTraceTestErr(t, secondErr, "второго Shutdown"); err != nil {
		t.Fatalf("второй Shutdown: %v", err)
	}

	close(stop)
	sendersWG.Wait()

	// Синхронный суффикс: после done постановка пишется сразу.
	w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "sync suffix")
	lines := sink.Lines()
	modelMu.Lock()
	total := sent
	modelMu.Unlock()
	if len(lines) != total+1 {
		t.Fatalf("записано %d строк, ожидалось %d (все seq + суффикс)", len(lines), total+1)
	}
	seqPattern := regexp.MustCompile(`\] seq (\d+)\n$`)
	seen := make([]bool, total)
	for i, line := range lines[:len(lines)-1] {
		match := seqPattern.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("строка %d не соответствует формату: %q", i, line)
		}
		var seq int
		if _, err := fmt.Sscanf(match[1], "%d", &seq); err != nil {
			t.Fatalf("разбор номера строки %d: %v", i, err)
		}
		if seq < 0 || seq >= total {
			t.Fatalf("строка %d содержит номер %d вне диапазона [0, %d)", i, seq, total)
		}
		if seen[seq] {
			t.Fatalf("номер %d встретился дважды", seq)
		}
		seen[seq] = true
	}
	for seq, ok := range seen {
		if !ok {
			t.Fatalf("номер %d не записан", seq)
		}
	}
	if !strings.HasSuffix(lines[len(lines)-1], "sync suffix\n") {
		t.Fatalf("строка синхронного суффикса не последняя: %q", lines[len(lines)-1])
	}
}

// TestTraceWriterWriteErrorAndShortWrite проверяет sticky-ошибку и
// короткую запись: первая ошибка запоминается и возвращается Flush,
// последующие сообщения обрабатываются, частичная строка не
// переписывается.
func TestTraceWriterWriteErrorAndShortWrite(t *testing.T) {
	errSink := errors.New("тестовый сбой приёмника")
	start := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

	t.Run("error", func(t *testing.T) {
		sink := &traceTestSink{}
		var calls atomic.Int32
		sink.hook = func(p []byte) (int, error) {
			if calls.Add(1) == 1 {
				return 0, errSink
			}
			sink.appendLine(p)
			return len(p), nil
		}
		clock := newTraceTestClock(start, time.Microsecond)
		w := startTraceTestWriterAllowing(t, sink, 4, clock.Now, errSink)

		w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "first")
		w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "second")
		ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
		defer cancel()
		if err := w.Flush(ctx); !errors.Is(err, errSink) {
			t.Fatalf("Flush вернул %v, ожидалась sticky-ошибка %v", err, errSink)
		}
		lines := sink.Lines()
		want := []string{fullTraceTestLine(start.Add(time.Microsecond), 'F', 1, 1, "second")}
		if !slices.Equal(lines, want) {
			t.Fatalf("строки %q, ожидались %q (сообщение после ошибки обработано)", lines, want)
		}
	})

	t.Run("short write", func(t *testing.T) {
		sink := &traceTestSink{}
		var calls atomic.Int32
		sink.hook = func(p []byte) (int, error) {
			if calls.Add(1) == 1 {
				sink.appendLine(p[:len(p)-1])
				return len(p) - 1, nil
			}
			sink.appendLine(p)
			return len(p), nil
		}
		clock := newTraceTestClock(start, time.Microsecond)
		w := startTraceTestWriterAllowing(t, sink, 4, clock.Now, io.ErrShortWrite)

		w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "first")
		w.EnqueueText(Prefix{Letter: 'F', ID: 1, Term: 1}, "second")
		ctx, cancel := context.WithTimeout(context.Background(), traceTestTimeout)
		defer cancel()
		if err := w.Flush(ctx); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("Flush вернул %v, ожидалась %v", err, io.ErrShortWrite)
		}
		lines := sink.Lines()
		if len(lines) != 2 {
			t.Fatalf("записано %d строк, ожидалось 2", len(lines))
		}
		if lines[0] != strings.TrimSuffix(fullTraceTestLine(start, 'F', 1, 1, "first"), "\n") {
			t.Fatalf("частичная строка %q не совпадает с усечённой полной", lines[0])
		}
		wantSecond := fullTraceTestLine(start.Add(time.Microsecond), 'F', 1, 1, "second")
		if lines[1] != wantSecond {
			t.Fatalf("строка %q, ожидалась %q", lines[1], wantSecond)
		}
		if !strings.Contains(lines[0], "first") || strings.Contains(lines[0], "second") {
			t.Fatalf("короткая запись переписана или склеена: %q", lines[0])
		}
	})
}

// fullTraceTestLine собирает ожидаемую полную строку по тем же правилам
// контракта: метка события, пробел, префикс, тело и перевод строки
// только при его отсутствии.
func fullTraceTestLine(at time.Time, letter rune, id, term int, body string) string {
	line := at.Format(traceTimeLayout) + " " + FormatPrefix(Prefix{Letter: letter, ID: id, Term: term}) + body
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	return line
}
