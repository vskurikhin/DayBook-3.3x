package pkg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

// getJSON выполняет GET-запрос напрямую сервису и разбирает JSON-ответ.
// Прямой HTTP — по тем же причинам, что postJSON: предмет проверки —
// ответ конкретного узла, который клиент библиотеки скрывает ротацией.
func getJSON(t *testing.T, addr, path string, resp any) {
	t.Helper()

	httpResp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s%s: %v", addr, path, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s%s: status %d", addr, path, httpResp.StatusCode)
	}
	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// TestWeakGetToFollowerReportsNotLeader — слабое чтение на узле, не
// являющемся лидером, отвергается с прежним статусом StatusNotLeader.
func TestWeakGetToFollowerReportsNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	follower := (lid + 1) % 3
	addr := h.ServiceAddr(follower)

	req := api.GetRequest{Key: "k"}
	var weakResp api.GetResponse
	getJSON(t, addr, "/weak-get/"+url.PathEscape("k"), &weakResp)
	if weakResp.RespStatus != api.StatusNotLeader {
		t.Errorf("weak-get to follower status = %v, want %v", weakResp.RespStatus, api.StatusNotLeader)
	}

	var getResp api.GetResponse
	postJSON(t, addr, "/get/", req, &getResp)
	if getResp.RespStatus != api.StatusNotLeader {
		t.Errorf("get to follower status = %v, want %v", getResp.RespStatus, api.StatusNotLeader)
	}
	if !reflect.DeepEqual(weakResp, getResp) {
		t.Errorf("weak-get and get answers differ on follower: weak=%+v get=%+v", weakResp, getResp)
	}
}

// TestWeakGetOnLeaderValueAndMissingKey — слабое чтение на лидере:
// существующий ключ возвращает значение, отсутствующий ключ — StatusOK
// с KeyFound=false и пустым значением.
func TestWeakGetOnLeaderValueAndMissingKey(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	c := h.NewClient()
	h.CheckPut(c, "llave", "cosa")

	var present api.GetResponse
	getJSON(t, addr, "/weak-get/"+url.PathEscape("llave"), &present)
	if present.RespStatus != api.StatusOK || !present.KeyFound || present.Value != "cosa" {
		t.Errorf("weak-get of existing key = %+v, want StatusOK/found/\"cosa\"", present)
	}

	var weakMissing api.GetResponse
	getJSON(t, addr, "/weak-get/"+url.PathEscape("ausente"), &weakMissing)
	if weakMissing.RespStatus != api.StatusOK || weakMissing.KeyFound || weakMissing.Value != "" {
		t.Errorf("weak-get of missing key = %+v, want StatusOK/not found/\"\"", weakMissing)
	}

	var getMissing api.GetResponse
	postJSON(t, addr, "/get/", api.GetRequest{Key: "ausente"}, &getMissing)
	if getMissing.RespStatus != api.StatusOK || getMissing.KeyFound || getMissing.Value != "" {
		t.Errorf("get of missing key = %+v, want StatusOK/not found/\"\"", getMissing)
	}
	if !reflect.DeepEqual(weakMissing, getMissing) {
		t.Errorf("weak-get and get answers differ on missing key: weak=%+v get=%+v", weakMissing, getMissing)
	}
}

// TestBasicPutWeakGetSingleClient — паритет TestBasicPutGetSingleClient:
// put → слабое чтение одним клиентом; /get с Э4 консенсусный — оба
// дают то же значение.
func TestBasicPutWeakGetSingleClient(t *testing.T) {
	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "llave", "cosa")

	h.CheckWeakGet(c1, "llave", "cosa")
	h.CheckGet(c1, "llave", "cosa")
}

// TestBasicPutWeakGetDifferentClients — паритет
// TestBasicPutGetDifferentClients: put клиентом c1, слабое чтение
// другим клиентом c2.
func TestBasicPutWeakGetDifferentClients(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckPut(c1, "k", "v")

	c2 := h.NewClient()
	h.CheckWeakGet(c2, "k", "v")
}

// TestWeakGetMissingKeyStatusOK — слабое чтение отсутствующего ключа:
// без ошибки, found=false (/get с Э4 консенсусный).
func TestWeakGetMissingKeyStatusOK(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c1 := h.NewClient()
	h.CheckWeakGetNotFound(c1, "missing")
}

// TestDisconnectLeaderWeakGetTimesOut — паритет фрагмента
// TestDisconnectLeaderAfterPuts: таймаут слабого чтения на изолированном
// лидере. put-клиент и таймаут-клиент — РАЗНЫЕ: PUT выполняется до
// изоляции обычным многоадресным клиентом, таймаут проверяется клиентом,
// привязанным только к изолированному лидеру.
func TestDisconnectLeaderWeakGetTimesOut(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()

	// PUT до изоляции — обычным клиентом; в образце put-клиент
	// отличается от таймаут-клиента.
	pc := h.NewClient()
	h.CheckPut(pc, "k", "v")

	h.DisconnectServiceFromPeers(lid)
	// Оставшийся кворум (2 из 3) избирает нового лидера;
	// изолированный сервис исключён из опроса.
	newlid := h.CheckSingleLeader()
	if newlid == lid {
		t.Errorf("got the same leader")
	}

	// Таймаут-клиент привязан только к изолированному лидеру:
	// единственный адрес, VerifyLeader не получает кворума —
	// WeakGet завершается по дедлайну контекста.
	tc := h.NewClientSingleService(lid)
	h.CheckWeakGetTimesOut(tc, "k")

	h.ReconnectServiceToPeers(lid)
	h.WaitForSingleLeader(3 * time.Second)
}

// TestWeakGetPostMethodNotAllowed — после перевода /weak-get/ на GET
// POST отвергается стандартным ответом маршрутизатора: 405 с
// заголовком Allow (содержит GET) — при wildcard-паттерне ЛЮБОЙ
// POST (с остатком и без) даёт 405 (проверено прогонами). Кейс
// HEAD: неявное обслуживание GET-паттерном (Q6) — StatusOK,
// тело подавлено, Cache-Control: no-store (SA-759).
func TestWeakGetPostMethodNotAllowed(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	// POST с остатком пути: стандартный 405 маршрутизатора с Allow.
	resp, err := http.Post("http://"+addr+"/weak-get/k", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /weak-get/k: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /weak-get/k status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("POST /weak-get/k Allow = %q, want it to contain GET", allow)
	}

	// POST без остатка (пустой ключ) — тоже 405: wildcard-паттерн
	// отвергает любой POST на /weak-get/…
	resp, err = http.Post("http://"+addr+"/weak-get/", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /weak-get/: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /weak-get/ status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}

	// HEAD: неявное обслуживание GET-паттерном — StatusOK, тело
	// подавлено, Cache-Control: no-store (свидетель Q6).
	headResp, err := http.Head("http://" + addr + "/weak-get/k")
	if err != nil {
		t.Fatalf("HEAD /weak-get/k: %v", err)
	}
	headBody, _ := io.ReadAll(headResp.Body)
	_ = headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Errorf("HEAD /weak-get/k status = %d, want %d", headResp.StatusCode, http.StatusOK)
	}
	if cc := headResp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("HEAD /weak-get/k Cache-Control = %q, want %q", cc, "no-store")
	}
	if len(headBody) != 0 {
		t.Errorf("HEAD /weak-get/k body = %d bytes, want 0", len(headBody))
	}
}

// TestWeakGetKeyEncoding — контракт URL-кодирования path-остатка:
// ключи с пробелом, слэшем, процентом, юникодом И ПУСТОЙ/DOT-ключи
// ("", ".", "..") проходят сквозь pathKey (клиент) и PathValue
// (сервер) без потерь; прямой GET с кодированным остатком даёт тот
// же ответ, что и WeakGet. Пустой ключ законно возвращает
// ("", false, nil) — как до Этапа 5 (SA-753: регресс недопустим).
// Временной ассерт: каждый WeakGet укладывается в 2 c — шторм
// повторов (5628 запросов за 1,2 с в прогоне SA) исключён.
func TestWeakGetKeyEncoding(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	c := h.NewClient()
	keys := []string{"", "space key", "a/b", "100%", "ключ é", ".", ".."}
	for _, k := range keys {
		if k != "" {
			h.CheckPut(c, k, "v:"+k)
		}
	}

	for _, k := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		value, found, err := c.WeakGet(ctx, k)
		cancel()
		if err != nil {
			t.Fatalf("WeakGet(%q): %v", k, err)
		}
		if k == "" {
			if value != "" || found {
				t.Errorf("WeakGet(%q) = (%q, %v); want (\"\", false)", k, value, found)
			}
		} else if value != "v:"+k || !found {
			t.Errorf("WeakGet(%q) = (%q, %v); want (%q, true)", k, value, found, "v:"+k)
		}
		if elapsed := time.Since(start); elapsed >= 2*time.Second {
			t.Errorf("WeakGet(%q) took %v; want < 2s (no retry storm)", k, elapsed)
		}

		// Прямой GET — независимый оракул контракта кодирования: ожидаемое
		// кодирование задано таблицей, а не клиентским хелпером pathKey.
		encoded := map[string]string{"": "", ".": "%2E", "..": "%2E%2E"}
		esc, ok := encoded[k]
		if !ok {
			esc = url.PathEscape(k)
		}
		var resp api.GetResponse
		getJSON(t, addr, "/weak-get/"+esc, &resp)
		if resp.RespStatus != api.StatusOK {
			t.Errorf("direct GET /weak-get/%s status = %v, want %v", esc, resp.RespStatus, api.StatusOK)
		}
		if k == "" {
			if resp.KeyFound || resp.Value != "" {
				t.Errorf("direct GET empty key = %+v, want not found/\"\"", resp)
			}
		} else if !resp.KeyFound || resp.Value != "v:"+k {
			t.Errorf("direct GET %q = %+v, want found/%q", k, resp, "v:"+k)
		}
	}
}

// TestWeakGetResponseNotCacheable — ответ GET /weak-get/ несёт
// Cache-Control: no-store: слабое чтение актуально только на момент
// подтверждения лидерства и не должно сохраняться кешами.
func TestWeakGetResponseNotCacheable(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	resp, err := http.Get("http://" + addr + "/weak-get/k")
	if err != nil {
		t.Fatalf("GET /weak-get/k: %v", err)
	}
	_ = resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
}
