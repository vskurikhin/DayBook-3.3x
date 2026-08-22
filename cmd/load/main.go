package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vskurikhin/raft/internal/load/config"
	"github.com/vskurikhin/raft/pkg/kvclient"
)

const (
	// RequestTimeout — предельное время одной операции сценария нагрузки.
	RequestTimeout = 10 * time.Second

	// latencyPrealloc — начальная ёмкость накопителей времени ответа.
	latencyPrealloc = 4096

	// traceLogLevelEnv — переменная окружения с уровнем трассировки узлов
	// кластера; уровень фиксируется в статистике прогона, поскольку
	// сравнивать прогоны на разных уровнях нельзя.
	traceLogLevelEnv = "TRACE_LOG_LEVEL"

	// fillerAlphabet — символы, которыми значение дополняется до заданного
	// размера.
	fillerAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// errRequestRate — темп меньше одного тика в секунду не поддерживается.
var errRequestRate = errors.New("invalid -request-rate: must be >= 1")

var (
	keys []string

	getOK     atomic.Uint64
	getFail   atomic.Uint64
	putOK     atomic.Uint64
	putFail   atomic.Uint64
	verifyOK  atomic.Uint64
	verifyBad atomic.Uint64

	// getDone, putDone, verifyDone — завершённые операции. Каждая выполненная
	// операция увеличивает ровно один счётчик ровно один раз, включая пути
	// ошибок.
	getDone    atomic.Uint64
	putDone    atomic.Uint64
	verifyDone atomic.Uint64

	// dropped — тики, пришедшие при исчерпанной одновременности: заданный
	// темп не достигнут.
	dropped atomic.Uint64

	// latencyDropped — измерения, которые не удалось сохранить. Ненулевое
	// значение делает прогон непригодным как базовая линия.
	latencyDropped atomic.Uint64

	getLatency = newLatencyRecorder(latencyPrealloc, 0)
	putLatency = newLatencyRecorder(latencyPrealloc, 0)

	values config.Values
)

//nolint:gochecknoinits
func init() {
	values = config.ParseFlags()
	keys = make([]string, values.KeyCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
}

func main() {
	if err := runLoad(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runLoad проверяет параметры прогона, порождает запросы до отмены и печатает
// итоговую статистику.
func runLoad() error {
	interval, err := tickInterval(values.RequestRate)
	if err != nil {
		return err
	}
	peers := make([]string, 0, len(values.Peers))
	for _, peer := range values.Peers {
		peers = append(peers, peer.String())
	}
	client := kvclient.New(peers)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()
	if values.Duration > 0 {
		durationCtx, durationCancel := context.WithTimeout(ctx, values.Duration)
		defer durationCancel()
		ctx = durationCtx
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats(ctx)
	}()
	generate(ctx, client, interval, values.Concurrency)
	wg.Wait()
	summary()
	return nil
}

// tickInterval вычисляет период тикера по заданному темпу. Темп меньше одного
// тика в секунду недопустим и проверяется до вычисления периода.
func tickInterval(rate int) (time.Duration, error) {
	if rate < 1 {
		return 0, errRequestRate
	}
	return time.Second / time.Duration(rate), nil
}

// generate порождает операции по тикам тикера. Тик задаёт момент старта
// запроса, сам запрос выполняется в отдельной горутине; одновременность
// ограничена ёмкостью семафора. Тик при занятом семафоре учитывается
// счётчиком dropped. Возврат происходит после завершения всех начатых
// запросов.
func generate(ctx context.Context, client *kvclient.KVClient, interval time.Duration, concurrency int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case semaphore <- struct{}{}:
				wg.Add(1)
				// Запрос выполняется с собственным сроком: начатая операция
				// доводится до конца и после остановки генерации.
				//nolint:contextcheck
				go func() {
					defer wg.Done()
					defer func() { <-semaphore }()
					reqCtx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
					defer cancel()
					run(reqCtx, client)
				}()
			default:
				dropped.Add(1)
			}
		}
	}
}

// run выполняет одну операцию сценария нагрузки и учитывает её результат.
func run(ctx context.Context, client *kvclient.KVClient) {
	key := keys[rand.Intn(len(keys))]
	if rand.Intn(100) < values.GetPercent {
		get(ctx, client, key)
		return
	}
	value := makeValue(key, values.ValueSize)
	start := time.Now()
	_, _, err := client.Put(ctx, key, value)
	observe(putLatency, time.Since(start))
	putDone.Add(1)
	if err != nil {
		putFail.Add(1)
		fmt.Printf("PUT: %v\n", err)
		return
	}
	putOK.Add(1)
	if rand.Intn(100) < values.VerifyPercent {
		verify(ctx, client, key, value)
	}
}

// get выполняет чтение и учитывает его результат.
func get(ctx context.Context, client *kvclient.KVClient, key string) {
	start := time.Now()
	_, _, err := client.Get(ctx, key)
	observe(getLatency, time.Since(start))
	getDone.Add(1)
	if err != nil {
		getFail.Add(1)
		fmt.Printf("GET: %v\n", err)
		return
	}
	getOK.Add(1)
}

// verify перечитывает записанное значение; время чтения учитывается как GET,
// а сама операция — отдельным счётчиком. Расхождение значения не является
// признаком ошибки сервиса: повторы клиента не идемпотентны.
func verify(ctx context.Context, client *kvclient.KVClient, key, value string) {
	start := time.Now()
	got, found, err := client.Get(ctx, key)
	observe(getLatency, time.Since(start))
	verifyDone.Add(1)
	if err != nil || !found || got != value {
		verifyBad.Add(1)
		return
	}
	verifyOK.Add(1)
}

// makeValue формирует значение размером size байт: детерминированный префикс
// из ключа и момента записи, дополненный псевдослучайными байтами.
func makeValue(key string, size int) string {
	prefix := key + "-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-"
	if size <= 0 {
		return prefix
	}
	if len(prefix) >= size {
		return prefix[:size]
	}
	value := make([]byte, size)
	copy(value, prefix)
	for i := len(prefix); i < size; i++ {
		value[i] = fillerAlphabet[rand.Intn(len(fillerAlphabet))]
	}
	return string(value)
}

// observe сохраняет время выполнения операции. Путь записи не блокирует
// горутину запроса; отклонённое измерение учитывается счётчиком
// latencyDropped.
func observe(recorder *latencyRecorder, elapsed time.Duration) {
	if !recorder.record(elapsed) {
		latencyDropped.Add(1)
	}
}

// doneTotal — число завершённых операций всех видов.
func doneTotal() uint64 {
	return getDone.Load() + putDone.Load() + verifyDone.Load()
}

// stats раз в секунду печатает достигнутую скорость и время ответа операций,
// завершённых за прошедшую секунду.
func stats(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var prevDone uint64
	var getOffset, putOffset int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			done := doneTotal()
			rps := done - prevDone
			prevDone = done
			getSamples, getTotal := getLatency.from(getOffset)
			putSamples, putTotal := putLatency.from(putOffset)
			getOffset, putOffset = getTotal, putTotal
			recent := make([]time.Duration, 0, len(getSamples)+len(putSamples))
			recent = append(recent, getSamples...)
			recent = append(recent, putSamples...)
			slices.Sort(recent)
			slog.Info(fmt.Sprintf(
				"RPS=%5d  p50=%7.2fms p80=%7.2fms p95=%7.2fms p99=%7.2fms"+
					"  GET ok=%8d fail=%4d  PUT ok=%8d fail=%4d  VERIFY ok=%6d bad=%4d  dropped=%6d",
				rps,
				percentile(recent, 50), percentile(recent, 80),
				percentile(recent, 95), percentile(recent, 99),
				getOK.Load(), getFail.Load(),
				putOK.Load(), putFail.Load(),
				verifyOK.Load(), verifyBad.Load(),
				dropped.Load(),
			))
		}
	}
}

// summary печатает параметры прогона, счётчики операций и распределение
// времени ответа за весь прогон.
func summary() {
	getSamples, _ := getLatency.from(0)
	putSamples, _ := putLatency.from(0)
	traceLevel := os.Getenv(traceLogLevelEnv)
	if traceLevel == "" {
		traceLevel = "не задан"
	}
	slog.Info(fmt.Sprintf(
		"run: concurrency=%d request-rate=%d value-size=%d duration=%v %s=%s",
		values.Concurrency, values.RequestRate, values.ValueSize,
		values.Duration, traceLogLevelEnv, traceLevel,
	))
	slog.Info(fmt.Sprintf(
		"done: get=%d put=%d verify=%d  GET ok=%d fail=%d  PUT ok=%d fail=%d"+
			"  VERIFY ok=%d bad=%d  dropped=%d latencyDropped=%d",
		getDone.Load(), putDone.Load(), verifyDone.Load(),
		getOK.Load(), getFail.Load(),
		putOK.Load(), putFail.Load(),
		verifyOK.Load(), verifyBad.Load(),
		dropped.Load(), latencyDropped.Load(),
	))
	if bad := verifyBad.Load(); bad > 0 {
		// Перечитывание после записи не идемпотентно: повтор запроса клиентом
		// мог записать значение позже проверки, поэтому расхождение требует
		// разбора и само по себе не означает отказа сервиса.
		slog.Info(fmt.Sprintf(
			"verifyBad=%d: value mismatch on re-read, needs review (client retries are not idempotent)",
			bad,
		))
	}
	slog.Info(latencyLine("GET", getSamples))
	slog.Info(latencyLine("PUT", putSamples))
}

// latencyLine форматирует распределение времени ответа одного вида операций.
func latencyLine(name string, samples []time.Duration) string {
	if len(samples) == 0 {
		return name + ": count=0"
	}
	slices.Sort(samples)
	return fmt.Sprintf(
		"%s: count=%d mean=%.2fms p50=%.2fms p90=%.2fms p95=%.2fms p99=%.2fms max=%.2fms",
		name, len(samples), mean(samples),
		percentile(samples, 50), percentile(samples, 90),
		percentile(samples, 95), percentile(samples, 99),
		milliseconds(samples[len(samples)-1]),
	)
}

// percentile возвращает перцентиль в миллисекундах по упорядоченным
// измерениям.
func percentile(sorted []time.Duration, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := len(sorted) * p / 100
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return milliseconds(sorted[index])
}

// mean возвращает среднее время ответа в миллисекундах.
func mean(samples []time.Duration) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	return milliseconds(total / time.Duration(len(samples)))
}

// milliseconds переводит длительность в миллисекунды с точностью до микросекунд.
func milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// latencyRecorder накапливает время ответа за весь прогон. Срез защищён
// мьютексом и растёт по мере необходимости, поэтому на рабочем пути
// (limit = 0) отказов записи не бывает.
type latencyRecorder struct {
	mu      sync.Mutex
	samples []time.Duration
	limit   int
}

// newLatencyRecorder создаёт накопитель с преаллокацией capacity измерений.
// Ненулевой limit ограничивает число сохраняемых измерений и применяется
// только в тестах, воспроизводящих отказ записи.
func newLatencyRecorder(capacity, limit int) *latencyRecorder {
	return &latencyRecorder{
		samples: make([]time.Duration, 0, capacity),
		limit:   limit,
	}
}

// record сохраняет измерение и возвращает false, если запись отклонена.
func (r *latencyRecorder) record(elapsed time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.limit > 0 && len(r.samples) >= r.limit {
		return false
	}
	r.samples = append(r.samples, elapsed)
	return true
}

// from возвращает копию измерений, начиная с позиции offset, и их общее число.
func (r *latencyRecorder) from(offset int) ([]time.Duration, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := len(r.samples)
	if offset > total {
		offset = total
	}
	tail := make([]time.Duration, total-offset)
	copy(tail, r.samples[offset:])
	return tail, total
}
