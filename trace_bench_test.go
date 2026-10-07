package raft

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vskurikhin/raft/internal/tracelog"
)

// Параметры работы обоих бенчмарков включённого пути трассировки. Работа
// до и после изменения пути обязана совпадать: одинаковые формат
// сообщения, значение длительности, ступень и размер пачки. Меняются
// только границы измерительного интервала и место ожидания записи.
const (
	// traceBenchBatch — предельное число вызовов трассировочного
	// помощника под одной блокировкой cm.mu. Пачка заведомо меньше
	// ёмкости очереди фонового писателя (4096), поэтому резервный
	// путь при заполнении не запускается.
	traceBenchBatch = 64

	// traceBenchFormat — формат сообщения, совпадающий с сообщением
	// о длительности сохранения в постоянном хранилище.
	traceBenchFormat = "persistToStorage elapsed %s"

	// traceBenchElapsed — то же значение длительности, что и в рабочем
	// вызове; в очередь сообщений оно попадает по значению.
	traceBenchElapsed = time.Duration(1234567)
)

// traceBenchLineSuffix — ожидаемое окончание каждой записанной строки:
// префикс ведомого (F, N:0, T:001) и сообщение о длительности 1234567 нс.
// Начало строки (отметка времени) добавляет стандартный логгер.
const traceBenchLineSuffix = "[F,N:0,T:001] persistToStorage elapsed 1.234567ms"

// traceBenchAuditSuffix — суффикс файла-свидетеля сверки, который ведётся
// рядом с файлом-приёмником трассировки.
const traceBenchAuditSuffix = ".audit"

// traceBenchRequireMode пропускает бенчмарк, если процесс запущен без
// пары переменных окружения измерительного режима: обычный прогон тестов
// не должен писать трассировку на диск. Если пара задана, трассировка
// обязана быть включена, поэтому проверка порога — отказ, а не пропуск:
// измерительный запуск не имеет права пропустить бенчмарк.
func traceBenchRequireMode(b *testing.B) {
	b.Helper()
	_, fileSet := os.LookupEnv(traceBenchEnvFile)
	_, levelSet := os.LookupEnv(traceBenchEnvLevel)
	if !fileSet && !levelSet {
		b.Skip("measurement mode is off: set " + traceBenchEnvFile + " and " + traceBenchEnvLevel)
	}
	if !traceEnabled(_traceLevelProgress) {
		b.Fatalf("measurement mode is on, but progress-level tracing is disabled")
	}
}

// traceBenchWriteBatch выполняет пачку из n вызовов трассировочного
// помощника под одной блокировкой cm.mu. Блокировка берётся и снимается
// в этой же функции; n не превосходит traceBenchBatch.
func traceBenchWriteBatch(cm *ConsensusModule, n int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := 0; i < n; i++ {
		cm.traceLogfLocked(traceBenchFormat, traceBenchElapsed)
	}
}

// traceBenchDrain ожидает, пока ранее поставленные строки будут записаны
// в приёмник: Flush-маркер отделяет уже принятый префикс очереди от
// последующих постановок. Предельное время ожидания ограничивает
// измерительный прогон при зависшем вводе-выводе.
func traceBenchDrain(b *testing.B) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := FlushTrace(ctx); err != nil {
		b.Fatalf("FlushTrace: %v", err)
	}
}

// traceBenchFileSize возвращает текущий размер файла-приёмника. Вызов
// выполняется вне измерительного интервала: размер фиксирует границу
// диапазона строк, записанных конкретным запуском бенчмарка (при -count=6
// файл накапливает строки нескольких запусков).
func traceBenchFileSize(b *testing.B, path string) int64 {
	b.Helper()
	info, err := os.Stat(path)
	if err != nil {
		b.Fatalf("trace file %q: %v", path, err)
	}
	return info.Size()
}

// traceBenchVerifyRange проверяет, что диапазон [start, end) файла
// содержит ровно want строк ожидаемого вида, и возвращает их число.
// Незавершённая последняя строка (короткая запись) — отказ. Проверка
// идёт по смещениям конкретного запуска: весь файл целиком к одному
// запуску не относится из-за -count=6.
func traceBenchVerifyRange(b *testing.B, path string, start, end int64, want int) int {
	b.Helper()
	if end <= start {
		b.Fatalf("trace file %q: no bytes written in range [%d, %d)", path, start, end)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatalf("trace file %q: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	last := make([]byte, 1)
	if _, err := f.ReadAt(last, end-1); err != nil {
		b.Fatalf("trace file %q: read last byte: %v", path, err)
	}
	if last[0] != '\n' {
		b.Fatalf("trace file %q: range [%d, %d) does not end with a complete line", path, start, end)
	}

	suffix := []byte(traceBenchLineSuffix)
	lines := 0
	scanner := bufio.NewScanner(io.NewSectionReader(f, start, end-start))
	for scanner.Scan() {
		if !bytes.HasSuffix(scanner.Bytes(), suffix) {
			b.Fatalf("trace file %q: line %d does not end with %q", path, lines+1, traceBenchLineSuffix)
		}
		lines++
	}
	if err := scanner.Err(); err != nil {
		b.Fatalf("trace file %q: scan range [%d, %d): %v", path, start, end, err)
	}
	if lines != want {
		b.Fatalf("trace file %q: range [%d, %d) has %d lines, want %d", path, start, end, lines, want)
	}
	return lines
}

// traceBenchReport дописывает в файл-свидетель строку о сверке: имя
// бенчмарка, смещения диапазона, число строк и ожидаемое b.N. Свидетель
// попадает в артефакт прогона и подтверждает, что каждая запись прошла
// проверку по смещениям конкретного запуска (при -count=6 файл накапливает
// строки нескольких запусков).
func traceBenchReport(b *testing.B, path string, start, end int64, lines int) {
	b.Helper()
	audit := path + traceBenchAuditSuffix
	f, err := os.OpenFile(audit, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		b.Fatalf("trace audit file %q: %v", audit, err)
	}
	_, writeErr := fmt.Fprintf(
		f,
		"%s start=%d end=%d lines=%d want=%d\n",
		b.Name(), start, end, lines, b.N,
	)
	closeErr := f.Close()
	if writeErr != nil {
		b.Fatalf("trace audit file %q: %v", audit, writeErr)
	}
	if closeErr != nil {
		b.Fatalf("trace audit file %q: %v", audit, closeErr)
	}
}

// BenchmarkTraceEnabledCriticalSection измеряет только критическую секцию:
// захват cm.mu, пачку вызовов трассировочного помощника и снятие блокировки.
// Подготовка и ожидание записи (пустое на синхронном пути) — вне
// измерительного интервала, поэтому ns/op относится к критической секции,
// а не к пропускной способности приёмника. Один измеряемый вызов — одна
// строка трассировки, всего ровно b.N строк.
func BenchmarkTraceEnabledCriticalSection(b *testing.B) {
	traceBenchRequireMode(b)
	cm := newAppendEntriesBenchCM(1)
	path := os.Getenv(traceBenchEnvFile)
	start := traceBenchFileSize(b, path)
	b.ResetTimer()

	written := 0
	for written < b.N {
		n := min(traceBenchBatch, b.N-written)
		b.StopTimer()
		b.StartTimer()
		traceBenchWriteBatch(cm, n)
		b.StopTimer()
		traceBenchDrain(b)
		written += n
	}

	b.StopTimer()
	end := traceBenchFileSize(b, path)
	lines := traceBenchVerifyRange(b, path, start, end, b.N)
	traceBenchReport(b, path, start, end, lines)
}

// BenchmarkTraceEnabledFullCycle измеряет полный цикл: те же пачки и
// блокировки, что и в CriticalSection, но ожидание записи выполняется
// внутри измерительного интервала после снятия cm.mu, включая последнюю
// пачку. Таймер останавливается только после полного опустошения, поэтому
// ns/op, B/op и allocs/op включают работу фонового писателя.
func BenchmarkTraceEnabledFullCycle(b *testing.B) {
	traceBenchRequireMode(b)
	cm := newAppendEntriesBenchCM(1)
	path := os.Getenv(traceBenchEnvFile)
	start := traceBenchFileSize(b, path)
	b.ResetTimer()

	written := 0
	for written < b.N {
		n := min(traceBenchBatch, b.N-written)
		traceBenchWriteBatch(cm, n)
		traceBenchDrain(b)
		written += n
	}

	b.StopTimer()
	end := traceBenchFileSize(b, path)
	lines := traceBenchVerifyRange(b, path, start, end, b.N)
	traceBenchReport(b, path, start, end, lines)
}

// Параметры диагностического ряда насыщения очереди. Ряд не является
// базой доказательства: он показывает стоимость постановки, которая
// застала очередь заполненной и потому пошла резервным путём, вместе
// с координацией насыщения, а не только стоимость отправки.
const (
	// traceSatCapacity — ёмкость очереди локального писателя: две
	// ячейки позволяют детерминированно заполнить очередь целиком.
	traceSatCapacity = 2

	// traceSatTotal — полное число сообщений итерации: одно изъято
	// писателем, очередь заполнена до ёмкости и одна постановка
	// проходит резервным путём.
	traceSatTotal = traceSatCapacity + 2

	// traceSatTimeout — предельное время каждого ожидания ряда:
	// зависший сценарий падает, а не висит.
	traceSatTimeout = 2 * time.Second
)

// traceSatLineSuffix — ожидаемое окончание каждой строки диагностического
// ряда: префикс локального писателя и тело с длительностью.
const traceSatLineSuffix = " [F,N:1,T:001] persistToStorage elapsed 1.234567ms\n"

// traceSatSink — приёмник диагностического ряда с управляемой первой
// записью. Первый Write сигналит о входе в приёмник и ждёт разрешения:
// писатель остаётся в нём и не опустошает очередь, что позволяет
// заполнить её до ёмкости. Последующие записи выполняются сразу.
type traceSatSink struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	lines       []string
}

// newTraceSatSink создаёт приёмник с каналами координации.
func newTraceSatSink() *traceSatSink {
	return &traceSatSink{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// Write принимает полную строку. Первая запись сигналит о начале ввода-
// вывода и ждёт разрешения; остальные записи выполняются сразу.
func (s *traceSatSink) Write(p []byte) (int, error) {
	s.startedOnce.Do(func() {
		close(s.started)
		<-s.release
	})
	s.mu.Lock()
	s.lines = append(s.lines, string(p))
	s.mu.Unlock()
	return len(p), nil
}

// releaseAll разрешает первую запись; повторные вызовы безопасны.
func (s *traceSatSink) releaseAll() {
	s.releaseOnce.Do(func() { close(s.release) })
}

// written возвращает копию принятых строк.
func (s *traceSatSink) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

// traceSatClock — часы локального писателя с сигналом о вызове.
// Постановка вызывает часы после входа в механизм очереди и до попытки
// отправки, поэтому получение сигнала гарантирует: измеряемая
// постановка уже в этом механизме и при заполненной очереди уйдёт
// резервным путём. Сигнал неблокирующий: при полном канале он теряется.
func traceSatClock(called chan<- struct{}) func() time.Time {
	return func() time.Time {
		select {
		case called <- struct{}{}:
		default:
		}
		return time.Now()
	}
}

// drainTraceSatClock опустошает накопленные сигналы часов подготовки,
// чтобы измеряемая постановка отличалась по сигналу от подготовительных.
func drainTraceSatClock(called <-chan struct{}) {
	for {
		select {
		case <-called:
		default:
			return
		}
	}
}

// BenchmarkTraceEnqueueSaturated — диагностический ряд насыщения: одна
// постановка сообщения, заставшая очередь заполненной и ушедшая
// резервным путём. Ряд не является базой доказательства и не
// сравнивается с базами: измеряемая величина включает координацию
// насыщения (ожидание разрешения приёмника и полное ожидание
// результата резервной постановки), а не только отправку. Локальный
// писатель создаётся tracelog.New, задержка задаётся каналами, не sleep.
func BenchmarkTraceEnqueueSaturated(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		runTraceSatIteration(b)
	}
}

// runTraceSatIteration выполняет одну итерацию диагностического ряда:
// подготовку вне таймера (писатель заблокирован в приёмнике, очередь
// заполнена), измеряемую постановку с координацией и остановку
// экземпляра после таймера.
func runTraceSatIteration(b *testing.B) {
	b.Helper()
	sink := newTraceSatSink()
	called := make(chan struct{}, 1)
	w := tracelog.New(sink, traceSatCapacity, traceSatClock(called))
	defer func() {
		// Разблокирование обязательно на любом пути: иначе остановка
		// писателя и горутина постановки переполнения останутся ждать.
		sink.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), traceSatTimeout)
		defer cancel()
		if err := w.Shutdown(ctx); err != nil {
			b.Fatalf("diagnostic writer shutdown: %v", err)
		}
	}()

	prefix := tracelog.Prefix{Letter: 'F', ID: 1, Term: 1}
	// Подготовка вне таймера: писатель изымает первое сообщение и
	// блокируется в приёмнике; очередь заполняется до ёмкости.
	w.Enqueue(prefix, traceBenchFormat, traceBenchElapsed)
	select {
	case <-sink.started:
	case <-time.After(traceSatTimeout):
		b.Fatalf("diagnostic writer did not reach the controlled receiver")
	}
	for i := 0; i < traceSatCapacity; i++ {
		w.Enqueue(prefix, traceBenchFormat, traceBenchElapsed)
	}
	drainTraceSatClock(called)

	// Измеряемый интервал: постановка переполнения, разрешение
	// приёмника и полное ожидание результата.
	done := make(chan struct{})
	b.StartTimer()
	go func() {
		w.Enqueue(prefix, traceBenchFormat, traceBenchElapsed)
		close(done)
	}()
	select {
	case <-called:
	case <-time.After(traceSatTimeout):
		b.Fatalf("diagnostic enqueue did not enter the writer")
	}
	sink.releaseAll()
	select {
	case <-done:
	case <-time.After(traceSatTimeout):
		b.Fatalf("diagnostic enqueue did not complete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), traceSatTimeout)
	err := w.Flush(ctx)
	cancel()
	if err != nil {
		b.Fatalf("diagnostic flush: %v", err)
	}
	b.StopTimer()

	// Проверка вне таймера: полное число строк и ожидаемое тело.
	lines := sink.written()
	if len(lines) != traceSatTotal {
		b.Fatalf("diagnostic receiver got %d lines, want %d", len(lines), traceSatTotal)
	}
	for i, line := range lines {
		if !strings.HasSuffix(line, traceSatLineSuffix) {
			b.Fatalf("diagnostic line %d does not end with %q: %q", i, traceSatLineSuffix, line)
		}
	}
}
