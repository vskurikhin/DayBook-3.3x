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

	// _latencyPrealloc — начальная ёмкость накопителей времени ответа.
	_latencyPrealloc = 4096

	// _traceLogLevelEnv — переменная окружения с уровнем трассировки узлов
	// кластера; уровень фиксируется в статистике прогона, поскольку
	// сравнивать прогоны на разных уровнях нельзя.
	_traceLogLevelEnv = "TRACE_LOG_LEVEL"

	// _fillerAlphabet — символы, которыми значение дополняется до заданного
	// размера.
	_fillerAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// errRequestRate — темп меньше одного тика в секунду не поддерживается.
var errRequestRate = errors.New("invalid -request-rate: must be >= 1")

// opKind — вид операции сценария нагрузки, выбираемый лестницей долей.
type opKind int

const (
	opDelete opKind = iota
	opWeakGet
	opGet
	opPut
)

var (
	_keys []string

	_getOK     atomic.Uint64
	_getFail   atomic.Uint64
	_putOK     atomic.Uint64
	_putFail   atomic.Uint64
	_verifyOK  atomic.Uint64
	_verifyBad atomic.Uint64

	_weakGetOK       atomic.Uint64
	_weakGetFail     atomic.Uint64
	_deleteOK        atomic.Uint64
	_deleteFail      atomic.Uint64
	_deleteVerifyOK  atomic.Uint64
	_deleteVerifyBad atomic.Uint64

	// _getDone, _putDone, _verifyDone, _weakGetDone, _deleteDone,
	// _deleteVerifyDone — завершённые операции. Каждая выполненная операция
	// увеличивает ровно один счётчик ровно один раз, включая пути ошибок.
	_getDone          atomic.Uint64
	_putDone          atomic.Uint64
	_verifyDone       atomic.Uint64
	_weakGetDone      atomic.Uint64
	_deleteDone       atomic.Uint64
	_deleteVerifyDone atomic.Uint64

	// _dropped — тики, пришедшие при исчерпанной одновременности: заданный
	// темп не достигнут.
	_dropped atomic.Uint64

	// _latencyDropped — измерения, которые не удалось сохранить. Ненулевое
	// значение делает прогон непригодным как базовая линия.
	_latencyDropped atomic.Uint64

	_getLatency     = newLatencyRecorder(_latencyPrealloc, 0)
	_putLatency     = newLatencyRecorder(_latencyPrealloc, 0)
	_weakGetLatency = newLatencyRecorder(_latencyPrealloc, 0)
	_deleteLatency  = newLatencyRecorder(_latencyPrealloc, 0)

	_values config.Values
)

//nolint:gochecknoinits
func init() {
	_values = config.ParseFlags()
	_keys = make([]string, _values.KeyCount)
	for i := range _keys {
		_keys[i] = fmt.Sprintf("key-%d", i)
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
	interval, err := tickInterval(_values.RequestRate)
	if err != nil {
		return err
	}
	peers := make([]string, 0, len(_values.Peers))
	for _, peer := range _values.Peers {
		peers = append(peers, peer.String())
	}
	client := kvclient.New(peers)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()
	if _values.Duration > 0 {
		durationCtx, durationCancel := context.WithTimeout(ctx, _values.Duration)
		defer durationCancel()
		ctx = durationCtx
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats(ctx)
	}()
	generate(ctx, client, interval, _values.Concurrency)
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
// счётчиком _dropped. Возврат происходит после завершения всех начатых
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
				_dropped.Add(1)
			}
		}
	}
}

// run выполняет одну операцию сценария нагрузки и учитывает её результат.
func run(ctx context.Context, client *kvclient.KVClient) {
	key := _keys[rand.Intn(len(_keys))]
	switch chooseOp(rand.Intn(100), _values.DeletePercent, _values.WeakGetPercent, _values.GetPercent) {
	case opDelete:
		del(ctx, client, key)
	case opWeakGet:
		weakGet(ctx, client, key)
	case opGet:
		get(ctx, client, key)
	default:
		put(ctx, client, key)
	}
}

// chooseOp — лестница распределения операций. Полосы:
// [0,d) DELETE; [d,d+w) WEAK-GET; [d+w,d+w+g) GET; остаток PUT.
// При d+w+g > 100 усекается последняя достигнутая полоса
// (при d>100 — DELETE, при d+w>100 — WEAK-GET, иначе GET),
// последующие полосы и PUT пусты. Значения долей вне [0,100]
// не поддерживаются: отрицательная доля сдвигает все
// последующие границы вниз.
func chooseOp(r, d, w, g int) opKind {
	switch {
	case r < d:
		return opDelete
	case r < d+w:
		return opWeakGet
	case r < d+w+g:
		return opGet
	default:
		return opPut
	}
}

// get выполняет чтение и учитывает его результат.
func get(ctx context.Context, client *kvclient.KVClient, key string) {
	start := time.Now()
	_, _, err := client.Get(ctx, key)
	observe(_getLatency, time.Since(start))
	_getDone.Add(1)
	if err != nil {
		_getFail.Add(1)
		fmt.Printf("GET: %v\n", err)
		return
	}
	_getOK.Add(1)
}

// weakGet выполняет слабое чтение (без записи в журнал) и учитывает его
// результат.
func weakGet(ctx context.Context, client *kvclient.KVClient, key string) {
	start := time.Now()
	_, _, err := client.WeakGet(ctx, key)
	observe(_weakGetLatency, time.Since(start))
	_weakGetDone.Add(1)
	if err != nil {
		_weakGetFail.Add(1)
		fmt.Printf("WEAK GET: %v\n", err)
		return
	}
	_weakGetOK.Add(1)
}

// put выполняет запись и учитывает её результат; после успешной записи с
// вероятностью VerifyPercent перечитывает записанное значение.
func put(ctx context.Context, client *kvclient.KVClient, key string) {
	value := makeValue(key, _values.ValueSize)
	start := time.Now()
	_, _, err := client.Put(ctx, key, value)
	observe(_putLatency, time.Since(start))
	_putDone.Add(1)
	if err != nil {
		_putFail.Add(1)
		fmt.Printf("PUT: %v\n", err)
		return
	}
	_putOK.Add(1)
	if rand.Intn(100) < _values.VerifyPercent {
		verify(ctx, client, key, value)
	}
}

// del выполняет удаление ключа и учитывает его результат; после успешного
// удаления с вероятностью VerifyPercent перечитывает ключ.
func del(ctx context.Context, client *kvclient.KVClient, key string) {
	start := time.Now()
	_, _, err := client.Delete(ctx, key)
	observe(_deleteLatency, time.Since(start))
	_deleteDone.Add(1)
	if err != nil {
		_deleteFail.Add(1)
		fmt.Printf("DELETE: %v\n", err)
		return
	}
	_deleteOK.Add(1)
	if rand.Intn(100) < _values.VerifyPercent {
		verifyDelete(ctx, client, key)
	}
}

// verifyDelete перечитывает удалённый ключ слабым чтением; время чтения
// учитывается как WEAK GET, а сама проверка — отдельным счётчиком.
// Успех — ключ не найден без ошибки; наличие ключа после удаления требует
// разбора и само по себе не означает отказа сервиса.
func verifyDelete(ctx context.Context, client *kvclient.KVClient, key string) {
	start := time.Now()
	_, found, err := client.WeakGet(ctx, key)
	observe(_weakGetLatency, time.Since(start))
	_deleteVerifyDone.Add(1)
	if !found && err == nil {
		_deleteVerifyOK.Add(1)
		return
	}
	_deleteVerifyBad.Add(1)
}

// verify перечитывает записанное значение слабым чтением; время
// чтения учитывается как WEAK GET, а сама операция — отдельным
// счётчиком. Расхождение значения не является признаком ошибки
// сервиса: повторы клиента не идемпотентны.
func verify(ctx context.Context, client *kvclient.KVClient, key, value string) {
	start := time.Now()
	got, found, err := client.WeakGet(ctx, key)
	observe(_weakGetLatency, time.Since(start))
	_verifyDone.Add(1)
	if err != nil || !found || got != value {
		_verifyBad.Add(1)
		return
	}
	_verifyOK.Add(1)
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
		value[i] = _fillerAlphabet[rand.Intn(len(_fillerAlphabet))]
	}
	return string(value)
}

// observe сохраняет время выполнения операции. Путь записи не блокирует
// горутину запроса; отклонённое измерение учитывается счётчиком
// _latencyDropped.
func observe(recorder *latencyRecorder, elapsed time.Duration) {
	if !recorder.record(elapsed) {
		_latencyDropped.Add(1)
	}
}

// doneTotal — число завершённых операций всех видов. Суммируются все шесть
// счётчиков *Done: по одному на каждую выполненную операцию.
func doneTotal() uint64 {
	return _getDone.Load() + _putDone.Load() + _verifyDone.Load() +
		_weakGetDone.Load() + _deleteDone.Load() + _deleteVerifyDone.Load()
}

// stats раз в секунду печатает достигнутую скорость и время ответа операций,
// завершённых за прошедшую секунду.
func stats(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var prevDone uint64
	var getOffset, putOffset, weakGetOffset, deleteOffset int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			done := doneTotal()
			rps := done - prevDone
			prevDone = done
			getSamples, getTotal := _getLatency.from(getOffset)
			putSamples, putTotal := _putLatency.from(putOffset)
			weakGetSamples, weakGetTotal := _weakGetLatency.from(weakGetOffset)
			deleteSamples, deleteTotal := _deleteLatency.from(deleteOffset)
			getOffset, putOffset = getTotal, putTotal
			weakGetOffset, deleteOffset = weakGetTotal, deleteTotal
			recent := make([]time.Duration, 0,
				len(getSamples)+len(putSamples)+len(weakGetSamples)+len(deleteSamples))
			recent = append(recent, getSamples...)
			recent = append(recent, putSamples...)
			recent = append(recent, weakGetSamples...)
			recent = append(recent, deleteSamples...)
			slices.Sort(recent)
			slog.Info(fmt.Sprintf(
				"RPS=%5d  p50=%7.2fms p80=%7.2fms p95=%7.2fms p99=%7.2fms"+
					"  GET ok=%8d fail=%4d  PUT ok=%8d fail=%4d  VERIFY ok=%6d bad=%4d"+
					"  WEAK GET ok=%8d fail=%4d  DELETE ok=%8d fail=%4d  _dropped=%6d",
				rps,
				percentile(recent, 50), percentile(recent, 80),
				percentile(recent, 95), percentile(recent, 99),
				_getOK.Load(), _getFail.Load(),
				_putOK.Load(), _putFail.Load(),
				_verifyOK.Load(), _verifyBad.Load(),
				_weakGetOK.Load(), _weakGetFail.Load(),
				_deleteOK.Load(), _deleteFail.Load(),
				_dropped.Load(),
			))
		}
	}
}

// summary печатает параметры прогона, счётчики операций и распределение
// времени ответа за весь прогон.
func summary() {
	getSamples, _ := _getLatency.from(0)
	putSamples, _ := _putLatency.from(0)
	weakGetSamples, _ := _weakGetLatency.from(0)
	deleteSamples, _ := _deleteLatency.from(0)
	traceLevel := os.Getenv(_traceLogLevelEnv)
	if traceLevel == "" {
		traceLevel = "не задан"
	}
	d := _values.DeletePercent
	w := _values.WeakGetPercent
	g := _values.GetPercent
	// Эффективные доли по формулам: каждая полоса не выходит за границы
	// [0,100] с учётом уже занятых предыдущими полосами.
	effectiveD := min(d, 100)
	effectiveW := min(w, 100-effectiveD)
	effectiveG := min(g, 100-effectiveD-effectiveW)
	effectiveP := 100 - effectiveD - effectiveW - effectiveG
	slog.Info(fmt.Sprintf(
		"run: concurrency=%d request-rate=%d value-size=%d duration=%v %s=%s"+
			"  mix: delete=%d weak-get=%d get=%d put=%d (эффективные)",
		_values.Concurrency, _values.RequestRate, _values.ValueSize,
		_values.Duration, _traceLogLevelEnv, traceLevel,
		effectiveD, effectiveW, effectiveG, effectiveP,
	))
	if d+w+g > 100 {
		slog.Info("WARNING: сумма долей > 100 — последняя полоса усечена, PUT пуст")
	}
	slog.Info(fmt.Sprintf(
		"done: get=%d put=%d verify=%d weak-get=%d delete=%d delete-verify=%d"+
			"  GET ok=%d fail=%d  PUT ok=%d fail=%d  VERIFY ok=%d bad=%d"+
			"  WEAK GET ok=%d fail=%d  DELETE ok=%d fail=%d  DELETE-VERIFY ok=%d bad=%d"+
			"  _dropped=%d _latencyDropped=%d",
		_getDone.Load(), _putDone.Load(), _verifyDone.Load(),
		_weakGetDone.Load(), _deleteDone.Load(), _deleteVerifyDone.Load(),
		_getOK.Load(), _getFail.Load(),
		_putOK.Load(), _putFail.Load(),
		_verifyOK.Load(), _verifyBad.Load(),
		_weakGetOK.Load(), _weakGetFail.Load(),
		_deleteOK.Load(), _deleteFail.Load(),
		_deleteVerifyOK.Load(), _deleteVerifyBad.Load(),
		_dropped.Load(), _latencyDropped.Load(),
	))
	if bad := _verifyBad.Load(); bad > 0 {
		// Перечитывание после записи не идемпотентно: повтор запроса клиентом
		// мог записать значение позже проверки, поэтому расхождение требует
		// разбора и само по себе не означает отказа сервиса.
		slog.Info(fmt.Sprintf(
			"_verifyBad=%d: value mismatch on re-read, needs review (client retries are not idempotent)",
			bad,
		))
	}
	if bad := _deleteVerifyBad.Load(); bad > 0 {
		// Хеджированная формулировка: ключ прочитан после удаления — требует
		// разбора; при DELETE_PERCENT>0 возможна гонка с параллельной записью
		// того же ключа, поэтому расхождение не однозначно означает отказ.
		slog.Info(fmt.Sprintf(
			"_deleteVerifyBad=%d: key read after delete, needs review (parallel write may race at DELETE_PERCENT>0)",
			bad,
		))
	}
	slog.Info(latencyLine("GET", getSamples))
	slog.Info(latencyLine("PUT", putSamples))
	slog.Info(latencyLine("WEAK GET", weakGetSamples))
	slog.Info(latencyLine("DELETE", deleteSamples))
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
