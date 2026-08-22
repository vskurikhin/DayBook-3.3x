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
		&getOK, &getFail, &putOK, &putFail, &verifyOK, &verifyBad,
		&getDone, &putDone, &verifyDone, &dropped, &latencyDropped,
	}
	for _, counter := range counters {
		counter.Store(0)
	}
	getLatency = newLatencyRecorder(latencyPrealloc, 0)
	putLatency = newLatencyRecorder(latencyPrealloc, 0)
}

// setValues подменяет параметры прогона и набор ключей на время теста.
func setValues(t *testing.T, v config.Values) {
	t.Helper()
	origValues, origKeys := values, keys
	t.Cleanup(func() {
		values, keys = origValues, origKeys
	})
	values = v
	keys = make([]string, v.KeyCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
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
			if getDone.Load() == 0 {
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

	if dropped.Load() == 0 {
		t.Error("dropped = 0, want > 0 for a saturated semaphore")
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
	if getFail.Load() != 0 || putFail.Load() != 0 {
		t.Errorf("unexpected failures: get=%d put=%d", getFail.Load(), putFail.Load())
	}
	if verifyBad.Load() != 0 {
		t.Errorf("verifyBad = %d, want 0", verifyBad.Load())
	}
	if putDone.Load() != verifyDone.Load() {
		t.Errorf("putDone = %d, verifyDone = %d, want equal", putDone.Load(), verifyDone.Load())
	}
	samples, _ := getLatency.from(0)
	if uint64(len(samples)) != getDone.Load()+verifyDone.Load() {
		t.Errorf("GET latency samples = %d, want %d", len(samples), getDone.Load()+verifyDone.Load())
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

			if getDone.Load() != operations {
				t.Errorf("getDone = %d, want %d", getDone.Load(), operations)
			}
			if getFail.Load() != operations {
				t.Errorf("getFail = %d, want %d", getFail.Load(), operations)
			}
			if getOK.Load() != 0 {
				t.Errorf("getOK = %d, want 0", getOK.Load())
			}
			if samples, _ := getLatency.from(0); len(samples) != operations {
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

	if latencyDropped.Load() != 1 {
		t.Errorf("latencyDropped = %d, want 1", latencyDropped.Load())
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
