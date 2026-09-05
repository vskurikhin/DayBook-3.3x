package kvclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vskurikhin/raft/pkg/api"
)

// okHandler отвечает статусом OK на любой запрос KV.
func okHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, r, api.StatusOK)
	})
}

// notLeaderHandler отвечает «не лидер», заставляя клиента перейти
// на следующий адрес.
func notLeaderHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, r, api.StatusNotLeader)
	})
}

// hangingHandler не отвечает до отмены запроса клиентом либо до закрытия
// канала done, освобождающего обработчик при завершении теста.
func hangingHandler(done <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	})
}

// slowHandler отвечает статусом OK с задержкой delay.
func slowHandler(t *testing.T, delay time.Duration) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		writeResponse(t, w, r, api.StatusOK)
	})
}

func writeResponse(t *testing.T, w http.ResponseWriter, r *http.Request, status api.ResponseStatus) {
	t.Helper()
	var resp any
	switch {
	case strings.HasPrefix(r.URL.Path, "/get/"):
		resp = api.GetResponse{RespStatus: status, KeyFound: true, Value: "value"}
	case strings.HasPrefix(r.URL.Path, "/weak-get/"):
		resp = api.GetResponse{RespStatus: status, KeyFound: true, Value: "value"}
	case strings.HasPrefix(r.URL.Path, "/put/"):
		resp = api.PutResponse{RespStatus: status}
	case strings.HasPrefix(r.URL.Path, "/delete/"):
		resp = api.DeleteResponse{RespStatus: status, KeyFound: true, PrevValue: "prev"}
	default:
		resp = api.StatusResponse{RespStatus: status}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encoding response: %v", err)
	}
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

// TestClientConcurrentUse проверяет, что один экземпляр клиента безопасно
// используется несколькими горутинами, в том числе при ротации адреса.
func TestClientConcurrentUse(t *testing.T) {
	okSrv := httptest.NewServer(okHandler(t))
	defer okSrv.Close()
	notLeaderSrv := httptest.NewServer(notLeaderHandler(t))
	defer notLeaderSrv.Close()

	client := New([]string{serverAddr(t, notLeaderSrv), serverAddr(t, okSrv)})

	const goroutines = 8
	const perGoroutine = 20
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for range perGoroutine {
				if _, _, err := client.ConsensusGet(ctx, "key"); err != nil {
					t.Errorf("goroutine %d: ConsensusGet: %v", id, err)
					return
				}
				if _, _, err := client.WeakGet(ctx, "key"); err != nil {
					t.Errorf("goroutine %d: WeakGet: %v", id, err)
					return
				}
				if _, _, err := client.Put(ctx, "key", "value"); err != nil {
					t.Errorf("goroutine %d: Put: %v", id, err)
					return
				}
				if _, _, err := client.Delete(ctx, "key"); err != nil {
					t.Errorf("goroutine %d: Delete: %v", id, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestWeakGetReturnsValueAndFound проверяет, что WeakGet возвращает
// из ответа сервера ожидаемые value и found (семантика ветки
// /weak-get/ тестового сервера).
func TestWeakGetReturnsValueAndFound(t *testing.T) {
	okSrv := httptest.NewServer(okHandler(t))
	defer okSrv.Close()

	client := New([]string{serverAddr(t, okSrv)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, found, err := client.WeakGet(ctx, "key")
	if err != nil {
		t.Fatalf("WeakGet: %v", err)
	}
	if value != "value" || !found {
		t.Errorf("WeakGet = (%q, %v); want (%q, %v)", value, found, "value", true)
	}
}

// TestDeleteReturnsPrevAndFound проверяет, что Delete возвращает из
// ответа сервера ожидаемые prev и found (семантика ветки /delete/
// тестового сервера), а не только отсутствие ошибки: значение и признак
// существования берутся из тела ответа DeleteResponse.
func TestDeleteReturnsPrevAndFound(t *testing.T) {
	okSrv := httptest.NewServer(okHandler(t))
	defer okSrv.Close()

	client := New([]string{serverAddr(t, okSrv)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prev, found, err := client.Delete(ctx, "key")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if prev != "prev" || !found {
		t.Errorf("Delete = (%q, %v); want (%q, %v)", prev, found, "prev", true)
	}
}

// TestNewUsesDefaultTimeout проверяет, что New даёт таймаут запроса
// _defaultRequestTimeout: запрос к «висящему» адресу прерывается примерно
// через это время, и клиент переключается на следующий адрес.
func TestNewUsesDefaultTimeout(t *testing.T) {
	done := make(chan struct{})
	hangSrv := httptest.NewServer(hangingHandler(done))
	defer hangSrv.Close()
	defer close(done)
	okSrv := httptest.NewServer(okHandler(t))
	defer okSrv.Close()

	client := New([]string{serverAddr(t, hangSrv), serverAddr(t, okSrv)})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if _, _, err := client.ConsensusGet(ctx, "key"); err != nil {
		t.Fatalf("ConsensusGet: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < _defaultRequestTimeout/2 {
		t.Errorf("elapsed = %v, want at least %v", elapsed, _defaultRequestTimeout/2)
	}
	if elapsed > 2*_defaultRequestTimeout {
		t.Errorf("elapsed = %v, want at most %v", elapsed, 2*_defaultRequestTimeout)
	}
	if got := client.leader(); got != 1 {
		t.Errorf("leader = %d, want 1", got)
	}
}

// TestNewWithTimeout проверяет, что заданный таймаут действительно
// используется: ответ, приходящий позже таймаута по умолчанию, но раньше
// заданного, успешно принимается.
func TestNewWithTimeout(t *testing.T) {
	const responseDelay = 1200 * time.Millisecond

	slowSrv := httptest.NewServer(slowHandler(t, responseDelay))
	defer slowSrv.Close()
	addrs := []string{serverAddr(t, slowSrv)}

	client := NewWithTimeout(addrs, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	if _, _, err := client.ConsensusGet(ctx, "key"); err != nil {
		t.Fatalf("ConsensusGet with 5s timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed < responseDelay {
		t.Errorf("elapsed = %v, want at least %v", elapsed, responseDelay)
	}

	// Тот же сервер и таймаут по умолчанию: каждый запрос обрывается
	// раньше ответа, и операция не завершается до отмены контекста.
	defaultClient := New(addrs)
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shortCancel()
	if _, _, err := defaultClient.ConsensusGet(shortCtx, "key"); err == nil {
		t.Error("ConsensusGet with default timeout: want error, got nil")
	}
}
