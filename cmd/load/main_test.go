package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vskurikhin/raft/internal/load/config"
	"github.com/vskurikhin/raft/pkg/kvclient"
)

// resetMetrics обнуляет счётчики и накопители времени ответа между тестами.
func resetMetrics() {
	counters := []*atomic.Uint64{
		&_getOK, &_getFail, &_putOK, &_putFail, &_verifyOK, &_verifyBad,
		&_weakGetOK, &_weakGetFail, &_deleteOK, &_deleteFail,
		&_deleteVerifyOK, &_deleteVerifyBad,
		&_getDone, &_putDone, &_verifyDone,
		&_weakGetDone, &_deleteDone, &_deleteVerifyDone,
		&_dropped, &_latencyDropped,
	}
	for _, counter := range counters {
		counter.Store(0)
	}
	_getLatency = newLatencyRecorder(_latencyPrealloc, 0)
	_putLatency = newLatencyRecorder(_latencyPrealloc, 0)
	_weakGetLatency = newLatencyRecorder(_latencyPrealloc, 0)
	_deleteLatency = newLatencyRecorder(_latencyPrealloc, 0)
	_rps = newRpsSeries()
}

// setValues подменяет параметры прогона и набор ключей на время теста.
func setValues(t *testing.T, v config.Values) {
	t.Helper()
	origValues, origKeys := _values, _keys
	t.Cleanup(func() {
		_values, _keys = origValues, origKeys
	})
	_values = v
	_keys = make([]string, v.KeyCount)
	for i := range _keys {
		_keys[i] = fmt.Sprintf("key-%d", i)
	}
}

// kvStub — подставной KV-сервис: хранит записанные значения в памяти и
// считает обработанные запросы.
type kvStub struct {
	mu       sync.Mutex
	data     map[string]string
	requests atomic.Int64
	delay    time.Duration
}

func newKVStub(delay time.Duration) *kvStub {
	return &kvStub{data: make(map[string]string), delay: delay}
}

func (s *kvStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/get/"):
		var req getRequest
		decode(w, r, &req)
		s.mu.Lock()
		value, found := s.data[req.Key]
		s.mu.Unlock()
		encode(w, getResponse{RespStatus: statusOK, KeyFound: found, Value: value})
	case strings.HasPrefix(r.URL.Path, "/weak-get/"):
		// Слабое чтение в стабе ведёт себя как обычное чтение: возвращает
		// значение ключа без изменения состояния хранилища. Ключ передаётся
		// path-сегментом GET-запроса (r.URL.Path уже декодирован).
		key := strings.TrimPrefix(r.URL.Path, "/weak-get/")
		s.mu.Lock()
		value, found := s.data[key]
		s.mu.Unlock()
		encode(w, getResponse{RespStatus: statusOK, KeyFound: found, Value: value})
	case strings.HasPrefix(r.URL.Path, "/delete/"):
		// Удаление извлекает прежнее значение ключа и убирает его из карты.
		var req getRequest
		decode(w, r, &req)
		s.mu.Lock()
		prev, found := s.data[req.Key]
		delete(s.data, req.Key)
		s.mu.Unlock()
		encode(w, deleteResponse{RespStatus: statusOK, KeyFound: found, PrevValue: prev})
	case strings.HasPrefix(r.URL.Path, "/put/"):
		var req putRequest
		decode(w, r, &req)
		s.mu.Lock()
		prev, found := s.data[req.Key]
		s.data[req.Key] = req.Value
		s.mu.Unlock()
		encode(w, putResponse{RespStatus: statusOK, KeyFound: found, PrevValue: prev})
	default:
		http.Error(w, "unknown route", http.StatusNotFound)
	}
}

// Проводные типы ответа продублированы, чтобы тест зависел только от формата
// JSON, а не от внутренних типов сервиса.
const statusOK = 1

type getRequest struct{ Key string }

type putRequest struct{ Key, Value string }

type getResponse struct {
	RespStatus int
	KeyFound   bool
	Value      string
}

type putResponse struct {
	RespStatus int
	KeyFound   bool
	PrevValue  string
}

// deleteResponse — ответ удаления, дублирующий DeleteResponse клиента.
type deleteResponse struct {
	RespStatus int
	KeyFound   bool
	PrevValue  string
}

func decode(w http.ResponseWriter, r *http.Request, dst any) {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func encode(w http.ResponseWriter, resp any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// serverAddr возвращает адрес вида host:port тестового сервера.
func serverAddr(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	addr, ok := strings.CutPrefix(srv.URL, "http://")
	if !ok {
		t.Fatalf("unexpected test server URL: %s", srv.URL)
	}
	return addr
}

// TestTickInterval проверяет, что темп меньше одного тика в секунду
// отвергается до вычисления периода и не приводит к панике.
func TestTickInterval(t *testing.T) {
	for _, rate := range []int{0, -1, -100} {
		interval, err := tickInterval(rate)
		if err == nil {
			t.Errorf("tickInterval(%d) = %v, want error", rate, interval)
		}
	}
	interval, err := tickInterval(1)
	if err != nil {
		t.Fatalf("tickInterval(1): %v", err)
	}
	if interval != time.Second {
		t.Errorf("tickInterval(1) = %v, want 1s", interval)
	}
	if interval, err = tickInterval(100000); err != nil || interval != 10*time.Microsecond {
		t.Errorf("tickInterval(100000) = %v, %v; want 10µs, nil", interval, err)
	}
}

// TestTicksDue таблично проверяет чистую функцию абсолютного расписания:
// число тиков, подлежащих выпуску к моменту elapsed от старта. Покрыты
// отрицательное и нулевое elapsed, неположительный интервал, граница
// «на один наносекундный тик меньше интервала» (ровно ноль), точное
// равенство интервалу (ровно один тик), кратное число тиков и опоздание
// на два–пять тиков, а также хвост, не дотягивающий до следующего тика.
// Каждый случай описан комментарием; ожидание — целая часть elapsed/interval.
func TestTicksDue(t *testing.T) {
	const interval = 10 * time.Millisecond
	tests := []struct {
		name string
		// elapsed — время от старта расписания.
		elapsed time.Duration
		// interval — период тика; вынесен отдельным полем, чтобы покрыть
		// неположительные значения без отдельной таблицы.
		interval time.Duration
		// want — ожидаемое число подлежащих выпуску тиков.
		want int
	}{
		{
			// Отрицательное elapsed невозможно в рабочей системе, но функция
			// обязана вернуть ноль, а не паниковать.
			name:     "negative-elapsed",
			elapsed:  -time.Millisecond,
			interval: interval,
			want:     0,
		},
		{
			// Нулевой интервал возникает при темпе выше 10⁹; деление на ноль
			// запрещено — функция возвращает ноль.
			name:     "zero-interval",
			elapsed:  time.Second,
			interval: 0,
			want:     0,
		},
		{
			// Отрицательный интервал недопустим, но не должен приводить
			// к панике или отрицательному результату.
			name:     "negative-interval",
			elapsed:  time.Second,
			interval: -time.Millisecond,
			want:     0,
		},
		{
			// Ровно на 1 нс меньше интервала: первый тик ещё не наступил.
			name:     "below-first",
			elapsed:  interval - 1,
			interval: interval,
			want:     0,
		},
		{
			// Ровно интервал: наступил ровно один тик.
			name:     "exactly-first",
			elapsed:  interval,
			interval: interval,
			want:     1,
		},
		{
			// Кратное число интервалов без хвоста: пять полных периодов.
			name:     "multiple",
			elapsed:  5 * interval,
			interval: interval,
			want:     5,
		},
		{
			// Опоздание на два тика: второй тик уже подлежит выпуску.
			name:     "late-two",
			elapsed:  2 * interval,
			interval: interval,
			want:     2,
		},
		{
			// Опоздание на три тика.
			name:     "late-three",
			elapsed:  3 * interval,
			interval: interval,
			want:     3,
		},
		{
			// Опоздание на четыре тика.
			name:     "late-four",
			elapsed:  4 * interval,
			interval: interval,
			want:     4,
		},
		{
			// Опоздание на пять тиков.
			name:     "late-five",
			elapsed:  5 * interval,
			interval: interval,
			want:     5,
		},
		{
			// Хвост меньше интервала не порождает лишнего тика: 5,5 периода
			// дают пять тиков.
			name:     "tail-below-next",
			elapsed:  5*interval + interval/2,
			interval: interval,
			want:     5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ticksDue(test.elapsed, test.interval); got != test.want {
				t.Errorf("ticksDue(%v, %v) = %d, want %d",
					test.elapsed, test.interval, got, test.want)
			}
		})
	}
}

// TestGenerateLimitsConcurrency проверяет, что число одновременно
// выполняющихся запросов не превышает заданной конкурентности.
func TestGenerateLimitsConcurrency(t *testing.T) {
	for _, concurrency := range []int{1, 8} {
		t.Run(fmt.Sprintf("concurrency-%d", concurrency), func(t *testing.T) {
			resetMetrics()
			setValues(t, config.Values{
				Concurrency: concurrency,
				GetPercent:  100,
				KeyCount:    4,
				RequestRate: 500,
				ValueSize:   128,
			})

			var inFlight, maxInFlight atomic.Int64
			stub := newKVStub(20 * time.Millisecond)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := inFlight.Add(1)
				for {
					peak := maxInFlight.Load()
					if current <= peak || maxInFlight.CompareAndSwap(peak, current) {
						break
					}
				}
				defer inFlight.Add(-1)
				stub.ServeHTTP(w, r)
			}))
			defer srv.Close()

			client := kvclient.New([]string{serverAddr(t, srv)})
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			generate(ctx, client, 2*time.Millisecond, concurrency)

			if peak := maxInFlight.Load(); peak > int64(concurrency) {
				t.Errorf("max in flight = %d, want at most %d", peak, concurrency)
			}
			if _getDone.Load() == 0 {
				t.Error("no operations completed")
			}
			if inFlight.Load() != 0 {
				t.Errorf("in flight after return = %d, want 0", inFlight.Load())
			}
		})
	}
}

// TestGenerateCountsDroppedTicks проверяет, что тик при занятом семафоре
// учитывается счётчиком, а не теряется молча.
func TestGenerateCountsDroppedTicks(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency: 1,
		GetPercent:  100,
		KeyCount:    4,
		RequestRate: 1000,
		ValueSize:   128,
	})

	stub := newKVStub(50 * time.Millisecond)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	generate(ctx, client, time.Millisecond, 1)

	if _dropped.Load() == 0 {
		t.Error("_dropped = 0, want > 0 for a saturated semaphore")
	}
}

// TestGenerateReleasesScheduledTicks проверяет на реальной нагрузке через
// подставной сервис инвариант цикла выдачи: каждый подлежащий выпуску тик
// либо начал операцию, либо учтён счётчиком _dropped, поэтому
// get + _dropped равны числу выпущенных тиков. Смесь фиксирована (только
// сильные чтения, без verify/delete/weak-get) — иначе дополнительные
// перечитывания исказили бы равенство. Темп 200 (интервал 5 мс),
// длительность ~2 с; допуск 99 % оставляет запас на тики, не обработанные
// к моменту отмены контекста, и не меняет порог валидности прогона 99,5 %.
func TestGenerateReleasesScheduledTicks(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency:   8,
		GetPercent:    100,
		KeyCount:      8,
		RequestRate:   200,
		ValueSize:     128,
		VerifyPercent: 0,
	})

	stub := newKVStub(20 * time.Millisecond)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	generate(ctx, client, 5*time.Millisecond, 8)

	// При фиксированной смеси из ненулевых видов операций остаются только
	// сильные чтения: put, weak-get и delete обязаны быть нулевыми.
	if got := _putDone.Load() + _weakGetDone.Load() + _deleteDone.Load(); got != 0 {
		t.Errorf("non-get done = %d, want 0", got)
	}
	const (
		rate     = 200
		seconds  = 2
		expected = rate * seconds
	)
	released := _getDone.Load() + _dropped.Load()
	if threshold := uint64(expected * 99 / 100); released < threshold {
		t.Errorf("released ticks = %d, want at least %d (99%% of %d)",
			released, threshold, expected)
	}
}

// TestOperationCountersCountEachOperationOnce проверяет, что сумма счётчиков
// завершённых операций равна числу выполненных HTTP-запросов.
func TestOperationCountersCountEachOperationOnce(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency:   4,
		GetPercent:    50,
		KeyCount:      8,
		RequestRate:   500,
		ValueSize:     128,
		VerifyPercent: 100,
	})

	stub := newKVStub(0)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	generate(ctx, client, 2*time.Millisecond, 4)

	done := doneTotal()
	if done == 0 {
		t.Fatal("no operations completed")
	}
	if requests := uint64(stub.requests.Load()); done != requests {
		t.Errorf("done total = %d, HTTP requests = %d, want equal", done, requests)
	}
	if _getFail.Load() != 0 || _putFail.Load() != 0 {
		t.Errorf("unexpected failures: get=%d put=%d", _getFail.Load(), _putFail.Load())
	}
	if _verifyBad.Load() != 0 {
		t.Errorf("_verifyBad = %d, want 0", _verifyBad.Load())
	}
	if _putDone.Load() != _verifyDone.Load() {
		t.Errorf("_putDone = %d, _verifyDone = %d, want equal", _putDone.Load(), _verifyDone.Load())
	}
	// Латентность чтения (GET) и перечитывания (verify) учитывается в разных
	// рекордерах: verify использует слабое чтение и пишет в _weakGetLatency.
	samples, _ := _getLatency.from(0)
	if uint64(len(samples)) != _getDone.Load() {
		t.Errorf("GET latency samples = %d, want %d", len(samples), _getDone.Load())
	}
	weakSamples, _ := _weakGetLatency.from(0)
	if uint64(len(weakSamples)) != _verifyDone.Load() {
		t.Errorf("WEAK GET latency samples = %d, want %d", len(weakSamples), _verifyDone.Load())
	}
}

// TestOperationCountersOnErrorPaths проверяет, что операция учитывается ровно
// один раз и при ответе сервера с ошибкой, и при таймауте.
func TestOperationCountersOnErrorPaths(t *testing.T) {
	// Обработчик получает канал, закрытие которого освобождает запросы,
	// оставшиеся без ответа к концу теста.
	tests := []struct {
		name    string
		handler func(released <-chan struct{}) http.Handler
		timeout time.Duration
	}{
		{
			name: "server-error",
			handler: func(_ <-chan struct{}) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "internal", http.StatusInternalServerError)
				})
			},
			timeout: 100 * time.Millisecond,
		},
		{
			name: "timeout",
			handler: func(released <-chan struct{}) http.Handler {
				return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					select {
					case <-r.Context().Done():
					case <-released:
					}
				})
			},
			timeout: 200 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetMetrics()
			setValues(t, config.Values{
				Concurrency: 1,
				GetPercent:  100,
				KeyCount:    1,
				RequestRate: 100,
				ValueSize:   128,
			})

			released := make(chan struct{})
			srv := httptest.NewServer(test.handler(released))
			defer srv.Close()
			defer close(released)
			client := kvclient.New([]string{serverAddr(t, srv)})

			const operations = 3
			for range operations {
				ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
				run(ctx, client)
				cancel()
			}

			if _getDone.Load() != operations {
				t.Errorf("_getDone = %d, want %d", _getDone.Load(), operations)
			}
			if _getFail.Load() != operations {
				t.Errorf("_getFail = %d, want %d", _getFail.Load(), operations)
			}
			if _getOK.Load() != 0 {
				t.Errorf("_getOK = %d, want 0", _getOK.Load())
			}
			if samples, _ := _getLatency.from(0); len(samples) != operations {
				t.Errorf("latency samples = %d, want %d", len(samples), operations)
			}
		})
	}
}

// TestObserveCountsDroppedLatency проверяет, что отклонённое измерение
// учитывается счётчиком и не блокирует горутину запроса.
func TestObserveCountsDroppedLatency(t *testing.T) {
	resetMetrics()
	recorder := newLatencyRecorder(1, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		observe(recorder, time.Millisecond)
		observe(recorder, 2*time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("запись времени ответа заблокировала горутину запроса")
	}

	if _latencyDropped.Load() != 1 {
		t.Errorf("_latencyDropped = %d, want 1", _latencyDropped.Load())
	}
	if samples, total := recorder.from(0); total != 1 || len(samples) != 1 {
		t.Errorf("recorded = %d samples, want 1", total)
	}
}

// TestMakeValueSize проверяет, что значение имеет заданный размер.
func TestMakeValueSize(t *testing.T) {
	for _, size := range []int{16, 128, 1024} {
		value := makeValue("key-1", size)
		if len(value) != size {
			t.Errorf("len(makeValue(key, %d)) = %d, want %d", size, len(value), size)
		}
		if !strings.HasPrefix(value, "key-1-") {
			t.Errorf("makeValue(key-1, %d) = %q, want prefix key-1-", size, value)
		}
	}
}

// TestPercentileAndMean проверяет расчёт перцентилей и среднего.
func TestPercentileAndMean(t *testing.T) {
	samples := make([]time.Duration, 0, 100)
	for i := 1; i <= 100; i++ {
		samples = append(samples, time.Duration(i)*time.Millisecond)
	}
	if got := percentile(samples, 50); got != 51 {
		t.Errorf("p50 = %v, want 51", got)
	}
	if got := percentile(samples, 99); got != 100 {
		t.Errorf("p99 = %v, want 100", got)
	}
	if got := mean(samples); got != 50.5 {
		t.Errorf("mean = %v, want 50.5", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("p50 of empty = %v, want 0", got)
	}
}

// TestChooseOp исчерпывающе проверяет лестницу распределения операций для
// всех r ∈ [0,100) на наборах долей, покрывающих все режимы: нулевые новые
// доли (инвариант текущего поведения), насыщение одной полосой, полная
// лестница без усечения и усечения последней достигнутой полосы при сумме
// долей больше 100. Тест полностью детерминирован и не использует посев
// генератора случайных чисел.
func TestChooseOp(t *testing.T) {
	tests := []struct {
		name string
		d, w int
		g    int
		// want — эталон результата для каждого r из [0,100).
		want func(r int) opKind
	}{
		{
			// Нулевые новые доли: поведение и поток обращений к rand точно
			// текущие — при r<G чтение, иначе запись.
			name: "zero-new-bands",
			d:    0, w: 0, g: 66,
			want: func(r int) opKind {
				if r < 66 {
					return opGet
				}
				return opPut
			},
		},
		{
			// Только удаление: полоса DELETE занимает весь диапазон.
			name: "delete-100",
			d:    100, w: 0, g: 0,
			want: func(r int) opKind { return opDelete },
		},
		{
			// Только слабое чтение: полоса WEAK-GET занимает весь диапазон.
			name: "weak-get-100",
			d:    0, w: 100, g: 0,
			want: func(r int) opKind { return opWeakGet },
		},
		{
			// Полная лестница без усечения: DELETE+WEAK-GET+GET == 100,
			// остаток PUT пуст.
			name: "full-ladder-100",
			d:    30, w: 20, g: 50,
			want: func(r int) opKind {
				switch {
				case r < 30:
					return opDelete
				case r < 50:
					return opWeakGet
				default:
					return opGet
				}
			},
		},
		{
			// Сумма долей больше 100: последняя достигнутая полоса (GET)
			// усекается до 100, PUT пуст.
			name: "sum-over-100",
			d:    40, w: 40, g: 40,
			want: func(r int) opKind {
				switch {
				case r < 40:
					return opDelete
				case r < 80:
					return opWeakGet
				default:
					return opGet
				}
			},
		},
		{
			// DELETE+WEAK-GET > 100: WEAK-GET усекается, GET и PUT пусты.
			name: "dw-over-100",
			d:    60, w: 60, g: 0,
			want: func(r int) opKind {
				switch {
				case r < 60:
					return opDelete
				default:
					return opWeakGet
				}
			},
		},
		{
			// DELETE > 100: DELETE усекается, все последующие полосы пусты.
			name: "d-over-100",
			d:    150, w: 0, g: 0,
			want: func(r int) opKind { return opDelete },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Исчерпывающая проверка по всем возможным значениям r ∈ [0,100).
			for r := 0; r < 100; r++ {
				got := chooseOp(r, test.d, test.w, test.g)
				if want := test.want(r); got != want {
					t.Errorf("chooseOp(%d, %d, %d, %d) = %v, want %v",
						r, test.d, test.w, test.g, got, want)
				}
			}
		})
	}
}

// TestDeletePercentSaturating проверяет интеграционное поведение при
// DeletePercent=100 и VerifyPercent=100: каждая операция — удаление, после
// каждого успешного удаления выполняется verify-after-delete слабым чтением,
// а сумма всех шести *Done равна числу выполненных HTTP-запросов.
func TestDeletePercentSaturating(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency:   4,
		DeletePercent: 100,
		KeyCount:      8,
		RequestRate:   500,
		ValueSize:     128,
		VerifyPercent: 100,
	})

	stub := newKVStub(0)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	generate(ctx, client, 2*time.Millisecond, 4)

	requests := uint64(stub.requests.Load())
	if requests == 0 {
		t.Fatal("no operations completed")
	}
	// Все операции — удаления: никаких чтений и записей других видов.
	if got := _getDone.Load() + _putDone.Load() + _weakGetDone.Load() + _verifyDone.Load(); got != 0 {
		t.Errorf("non-delete done = %d, want 0", got)
	}
	if got := _deleteDone.Load(); got == 0 {
		t.Error("_deleteDone = 0, want > 0")
	}
	if got := _deleteOK.Load(); got != _deleteDone.Load() {
		t.Errorf("_deleteOK = %d, want %d", got, _deleteDone.Load())
	}
	if got := _deleteFail.Load(); got != 0 {
		t.Errorf("_deleteFail = %d, want 0", got)
	}
	// При VerifyPercent=100 каждое успешное удаление перечитывается слабым
	// чтением: _deleteVerifyDone учитывается ровно по числу успешных удалений.
	if got := _deleteVerifyDone.Load(); got != _deleteOK.Load() {
		t.Errorf("_deleteVerifyDone = %d, want %d", got, _deleteOK.Load())
	}
	// Каждый HTTP-запрос — это либо delete, либо verify-after-delete.
	if got := _deleteDone.Load() + _deleteVerifyDone.Load(); got != requests {
		t.Errorf("delete+verify requests = %d, want %d", got, requests)
	}
	// Ключ после удаления в стабе не найден — все проверки успешны.
	if got := _deleteVerifyOK.Load(); got != _deleteVerifyDone.Load() {
		t.Errorf("_deleteVerifyOK = %d, want %d", got, _deleteVerifyDone.Load())
	}
	if got := _deleteVerifyBad.Load(); got != 0 {
		t.Errorf("_deleteVerifyBad = %d, want 0", got)
	}
	// Сумма всех шести *Done равна числу выполненных HTTP-запросов: каждая
	// операция (удаление или перечитывание) даёт ровно один запрос и один
	// счётчик *Done.
	if done := doneTotal(); done != requests {
		t.Errorf("done total = %d, HTTP requests = %d, want equal", done, requests)
	}
}

// TestWeakGetPercentSaturating проверяет интеграционное поведение при
// WeakGetPercent=100: каждая операция — слабое чтение, _weakGetDone равен
// числу HTTP-запросов стаба.
func TestWeakGetPercentSaturating(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency:    4,
		KeyCount:       8,
		RequestRate:    500,
		ValueSize:      128,
		WeakGetPercent: 100,
	})

	stub := newKVStub(0)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	generate(ctx, client, 2*time.Millisecond, 4)

	requests := uint64(stub.requests.Load())
	if requests == 0 {
		t.Fatal("no operations completed")
	}
	// При WeakGetPercent=100 все операции — слабые чтения.
	if got := _weakGetDone.Load(); got != requests {
		t.Errorf("_weakGetDone = %d, want %d (all operations are weak-get)", got, requests)
	}
	if got := _weakGetOK.Load(); got != _weakGetDone.Load() {
		t.Errorf("_weakGetOK = %d, want %d", got, _weakGetDone.Load())
	}
	if got := _weakGetFail.Load(); got != 0 {
		t.Errorf("_weakGetFail = %d, want 0", got)
	}
}

// Эталонные строки run: и done: взяты дословно из исторического артефакта
// .doc/TODO/2026-09-06.P-PERF-2/2026-09-06-P-PERF-2-base-consensus/trace/P-PERF-2-base-consensus_loadkv.out
// (строки 60–61). Артефакт снят текущим форматом вывода генератора до правок
// TASK-001, поэтому строки обязаны совпадать побайтово, включая двойные
// пробелы (межверсионный гейт RISK-013). Литералы не собираются через
// fmt.Sprintf с форматами main.go: источник — ссылка на артефакт.
const (
	goldenRunLine  = "run: concurrency=128 request-rate=500 value-size=128 duration=1m0s TRACE_LOG_LEVEL=не задан  mix: delete=0 weak-get=0 get=75 put=25 (эффективные)"
	goldenDoneLine = "done: get=22485 put=7418 verify=0 weak-get=0 delete=0 delete-verify=0  GET ok=22485 fail=0  PUT ok=7418 fail=0  VERIFY ok=0 bad=0  WEAK GET ok=0 fail=0  DELETE ok=0 fail=0  DELETE-VERIFY ok=0 bad=0  _dropped=0 _latencyDropped=0"
)

// messageHandler — обработчик slog, сохраняющий только текст сообщения
// в буфер; используется тестами сводки для побайтовой сверки строк.
type messageHandler struct {
	buf *bytes.Buffer
}

func (h *messageHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *messageHandler) Handle(_ context.Context, r slog.Record) error {
	if _, err := h.buf.WriteString(r.Message); err != nil {
		return err
	}
	return h.buf.WriteByte('\n')
}

func (h *messageHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *messageHandler) WithGroup(string) slog.Handler      { return h }

// slogCapture — буфер сообщений стандартного логгера, перехваченных тестом.
type slogCapture struct {
	buf bytes.Buffer
}

// lines возвращает сообщения, накопленные с момента последней очистки.
func (c *slogCapture) lines() []string {
	raw := strings.TrimSuffix(c.buf.String(), "\n")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

// reset очищает буфер накопленных сообщений.
func (c *slogCapture) reset() {
	c.buf.Reset()
}

// captureSlog перенаправляет стандартный логгер slog в буфер и возвращает
// объект для чтения сообщений. slog.SetDefault перенаправляет и пакет log,
// поэтому вместе с slog.Default() сохраняются и восстанавливаются
// log.Writer() и log.Flags() — возврат одного slog.Default() вывод
// log.Printf не возвращает (утечка глобального состояния в следующие
// тесты). Восстановление выполняется через t.Cleanup.
func captureSlog(t *testing.T) *slogCapture {
	t.Helper()
	origDefault := slog.Default()
	origWriter := log.Writer()
	origFlags := log.Flags()
	capture := &slogCapture{}
	slog.SetDefault(slog.New(&messageHandler{buf: &capture.buf}))
	t.Cleanup(func() {
		slog.SetDefault(origDefault)
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
	})
	return capture
}

// checkLine находит в выводе строку с заданным префиксом и сверяет её
// с эталоном дословно.
func checkLine(t *testing.T, lines []string, prefix, want string) {
	t.Helper()
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			if line != want {
				t.Errorf("%s line = %q, want %q", prefix, line, want)
			}
			return
		}
	}
	t.Errorf("line with prefix %q not found in output: %q", prefix, lines)
}

// TestComputeRpsStats проверяет чистую функцию статистики посекундного ряда
// по вручную посчитанным эталонам: краевые случаи n = 0, 1, 2, нечётное
// и чётное n с дробной медианой, выборочное стандартное отклонение
// (делитель n−1) и тождество «среднее × секунд = сумма».
func TestComputeRpsStats(t *testing.T) {
	tests := []struct {
		name   string
		values []uint64
		want   rpsStats
	}{
		{
			// Пустой ряд: все величины и секунды равны нулю.
			name:   "empty",
			values: nil,
			want:   rpsStats{},
		},
		{
			// Одна секунда: среднее/медиана/мин/макс/сумма равны значению,
			// стандартное отклонение равно нулю (делитель n−1 неприменим).
			name:   "single",
			values: []uint64{150},
			want: rpsStats{
				seconds: 1, mean: 150, median: 150, stddev: 0,
				min: 150, max: 150, total: 150,
			},
		},
		{
			// Две секунды: медиана — полусумма, stddev = √5000 ≈ 70,7.
			name:   "two",
			values: []uint64{100, 200},
			want: rpsStats{
				seconds: 2, mean: 150, median: 150, stddev: math.Sqrt(5000),
				min: 100, max: 200, total: 300,
			},
		},
		{
			// Нечётное n: медиана — центральный элемент 30,
			// stddev = √1250 ≈ 35,4.
			name:   "odd",
			values: []uint64{10, 20, 30, 40, 100},
			want: rpsStats{
				seconds: 5, mean: 40, median: 30, stddev: math.Sqrt(1250),
				min: 10, max: 100, total: 200,
			},
		},
		{
			// Чётное n с нечётной суммой центральных элементов: медиана 2,5
			// вычисляется в float64 — ловит целочисленное усечение полусуммы.
			name:   "even-fractional-median",
			values: []uint64{1, 2, 3, 4},
			want: rpsStats{
				seconds: 4, mean: 2.5, median: 2.5, stddev: math.Sqrt(5.0 / 3.0),
				min: 1, max: 4, total: 10,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := computeRpsStats(test.values)
			if got.seconds != test.want.seconds {
				t.Errorf("seconds = %d, want %d", got.seconds, test.want.seconds)
			}
			if math.Abs(got.mean-test.want.mean) > 1e-9 {
				t.Errorf("mean = %v, want %v", got.mean, test.want.mean)
			}
			if math.Abs(got.median-test.want.median) > 1e-9 {
				t.Errorf("median = %v, want %v", got.median, test.want.median)
			}
			if math.Abs(got.stddev-test.want.stddev) > 1e-9 {
				t.Errorf("stddev = %v, want %v", got.stddev, test.want.stddev)
			}
			if got.min != test.want.min {
				t.Errorf("min = %d, want %d", got.min, test.want.min)
			}
			if got.max != test.want.max {
				t.Errorf("max = %d, want %d", got.max, test.want.max)
			}
			if got.total != test.want.total {
				t.Errorf("total = %d, want %d", got.total, test.want.total)
			}
			// Тождество «среднее × секунд = сумма» в пределах погрешности
			// float: связка величин ADR-PPERF2-010 выполняется точно.
			if product := got.mean * float64(got.seconds); math.Abs(product-float64(got.total)) > 1e-9 {
				t.Errorf("mean × seconds = %v, total = %d, want equal", product, got.total)
			}
		})
	}
}

// TestSummaryOutputGolden сверяет строки сводки с жёсткими литералами:
// run: и done: — дословно с историческим артефактом, rps: и ops: — точные
// значения для заданных входов. Дополнительно проверяется изоляция _rps:
// два последовательных вызова summary() с разными рядами не влияют друг
// на друга (resetMetrics пересоздаёт накопитель).
func TestSummaryOutputGolden(t *testing.T) {
	// Параметры прогона из артефакта: темп 500, одновременность 128,
	// размер 128, длительность 1m0s, доли 0/0/75/25.
	setValues(t, config.Values{
		Concurrency:    128,
		DeletePercent:  0,
		Duration:       time.Minute,
		GetPercent:     75,
		KeyCount:       2000,
		RequestRate:    500,
		ValueSize:      128,
		VerifyPercent:  0,
		WeakGetPercent: 0,
	})
	// Эталон run: содержит TRACE_LOG_LEVEL=не задан: принудительно снимаем
	// переменную окружения, чтобы тест не зависел от окружения разработчика.
	origTraceLevel, hadTraceLevel := os.LookupEnv(_traceLogLevelEnv)
	if hadTraceLevel {
		t.Cleanup(func() { os.Setenv(_traceLogLevelEnv, origTraceLevel) })
		os.Unsetenv(_traceLogLevelEnv)
	}

	t.Run("artifact-lines", func(t *testing.T) {
		resetMetrics()
		capture := captureSlog(t)
		// Счётчики из артефакта: get=22485, put=7418, остальные нулевые.
		_getDone.Store(22485)
		_putDone.Store(7418)
		_getOK.Store(22485)
		_putOK.Store(7418)
		// Ряд [100, 200, 210, 190]: после исключения первой секунды
		// статистика считается по [200, 210, 190] — seconds=3, mean=200.0,
		// median=200.0, stddev=10.0 (√(((0)²+(10)²+(−10)²)/2) = √100),
		// min=190, max=210, total=600.
		for _, v := range []uint64{100, 200, 210, 190} {
			_rps.record(v)
		}
		summary()
		got := capture.lines()
		checkLine(t, got, "run:", goldenRunLine)
		checkLine(t, got, "done:", goldenDoneLine)
		checkLine(t, got, "rps:", "rps: seconds=3 mean=200.0 median=200.0 stddev=10.0 min=190 max=210 total=600")
		// Доли по счётчикам полного прогона: 22485/29903 = 75,2 %,
		// 7418/29903 = 24,8 %.
		checkLine(t, got, "ops:", "ops: done=29903 (get=75.2% put=24.8% verify=0.0% weak-get=0.0% delete=0.0% delete-verify=0.0%)")

		// Изоляция _rps: после resetMetrics новый ряд не смешивается со старым.
		capture.reset()
		resetMetrics()
		for _, v := range []uint64{5, 15} {
			_rps.record(v)
		}
		summary()
		checkLine(t, capture.lines(), "rps:", "rps: seconds=1 mean=15.0 median=15.0 stddev=0.0 min=15 max=15 total=15")
	})

	t.Run("ops-shares", func(t *testing.T) {
		resetMetrics()
		capture := captureSlog(t)
		// done=100 из get=60/put=20/verify=6/weak-get=10/delete=2/
		// delete-verify=2 → 60.0%/20.0%/6.0%/10.0%/2.0%/2.0%.
		_getDone.Store(60)
		_putDone.Store(20)
		_verifyDone.Store(6)
		_weakGetDone.Store(10)
		_deleteDone.Store(2)
		_deleteVerifyDone.Store(2)
		summary()
		checkLine(t, capture.lines(), "ops:",
			"ops: done=100 (get=60.0% put=20.0% verify=6.0% weak-get=10.0% delete=2.0% delete-verify=2.0%)")
	})

	t.Run("ops-zero-done", func(t *testing.T) {
		resetMetrics()
		capture := captureSlog(t)
		// Все счётчики нулевые: доли обязаны быть 0.0 без деления на ноль.
		summary()
		checkLine(t, capture.lines(), "ops:",
			"ops: done=0 (get=0.0% put=0.0% verify=0.0% weak-get=0.0% delete=0.0% delete-verify=0.0%)")
	})
}

// TestSummaryExcludesFirstSecond проверяет правило неполных секунд
// (ADR-PPERF2-010 п. 4): первая секунда ряда исключается из всех шести
// величин сводки. Проверка потока RPS= здесь недоступна — его печатает
// только горутина stats; инвариант «ряд = печатаемые строки» проверяется
// тестом TestStatsSeriesMatchesPrintedLines.
func TestSummaryExcludesFirstSecond(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency: 8,
		GetPercent:  100,
		KeyCount:    8,
		RequestRate: 200,
		ValueSize:   128,
	})
	capture := captureSlog(t)
	// Ряд с отличным первым элементом: r_1 = 10 исключается, статистика
	// считается по [200, 210, 190] — seconds=3, total=600.
	for _, v := range []uint64{10, 200, 210, 190} {
		_rps.record(v)
	}
	summary()
	checkLine(t, capture.lines(), "rps:",
		"rps: seconds=3 mean=200.0 median=200.0 stddev=10.0 min=190 max=210 total=600")
}

// TestStatsSeriesMatchesPrintedLines проверяет на реальной нагрузке через
// подставной сервис инвариант «ряд = печатаемые строки RPS=» и завершение
// по контексту (F-LOAD-003). Параметры заданы явно: заглушка ~20 мс,
// интервал тика 5 мс (темп 200), одновременность 8; контекст отменяется
// через ~2,5 с. Длительность теста ~3 с — приемлемо для пакета.
func TestStatsSeriesMatchesPrintedLines(t *testing.T) {
	resetMetrics()
	setValues(t, config.Values{
		Concurrency:   8,
		GetPercent:    100,
		KeyCount:      8,
		RequestRate:   200,
		ValueSize:     128,
		VerifyPercent: 0,
	})
	capture := captureSlog(t)

	stub := newKVStub(20 * time.Millisecond)
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := kvclient.New([]string{serverAddr(t, srv)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Обе горутины, как в runLoad: stats печатает строки RPS= и накапливает
	// ряд, generate порождает запросы до отмены контекста.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stats(ctx)
	}()
	go func() {
		defer wg.Done()
		generate(ctx, client, 5*time.Millisecond, 8)
	}()

	// Нагрузка идёт ~2,5 с, затем контекст отменяется; generate доводит
	// начатые запросы, stats выходит по ctx.Done().
	time.Sleep(2500 * time.Millisecond)
	cancel()
	wg.Wait()

	series := _rps.snapshot()
	if len(series) < 2 {
		t.Fatalf("rps series length = %d, want at least 2", len(series))
	}
	// Инвариант ADR-PPERF2-010: длина ряда равна числу строк RPS=.
	rpsLines := 0
	for _, line := range capture.lines() {
		if strings.HasPrefix(line, "RPS=") {
			rpsLines++
		}
	}
	if len(series) != rpsLines {
		t.Errorf("series length = %d, RPS= lines = %d, want equal", len(series), rpsLines)
	}
	// После завершения stats ряд не растёт: повторный снимок.
	time.Sleep(200 * time.Millisecond)
	if after := _rps.snapshot(); len(after) != len(series) {
		t.Errorf("series grew after stats exit: %d -> %d", len(series), len(after))
	}
	// Долёт запросов после последнего тика учтён счётчиками, но не рядом:
	// doneTotal строго больше суммы ряда (F-LOAD-003).
	var sum uint64
	for _, v := range series {
		sum += v
	}
	if done := doneTotal(); done <= sum {
		t.Errorf("doneTotal = %d, series sum = %d, want strictly greater", done, sum)
	}
}
