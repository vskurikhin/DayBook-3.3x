package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		// значение ключа без изменения состояния хранилища.
		var req getRequest
		decode(w, r, &req)
		s.mu.Lock()
		value, found := s.data[req.Key]
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
// а сумма всех шести *Done равна числу выполненных HTTP-запросов (SA-602).
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
	// счётчик *Done (SA-602).
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
