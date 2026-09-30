package pkg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

// verifyLeaderRequest выполняет HTTP-запрос к эндпоинту /verifyleader/.
// Запрос строится через http.NewRequestWithContext с контекстным
// тайм-аутом (зеркало checkServiceReachable): handleVerifyLeader ждёт
// future блокирующим .Error() и не видит отмены запроса, поэтому голый
// http.Get/http.Post при потере кворума дал бы зависание теста вместо
// внятного падения.
func verifyLeaderRequest(t *testing.T, method, addr string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+"/verifyleader/", nil)
	if err != nil {
		t.Fatalf("building %s /verifyleader/ request: %v", method, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s /verifyleader/: %v", method, err)
	}
	return resp
}

// TestVerifyLeaderGetOnLeader — контракт перевода: GET /verifyleader/
// на лидере отвечает 200, тело декодируется в StatusResponse со
// статусом StatusOK.
func TestVerifyLeaderGetOnLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	resp := verifyLeaderRequest(t, http.MethodGet, addr)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /verifyleader/ status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var sr api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if sr.RespStatus != api.StatusOK {
		t.Errorf("GET /verifyleader/ on leader status = %v, want %v", sr.RespStatus, api.StatusOK)
	}
}

// TestVerifyLeaderGetOnFollower — симметрия R3: готовность на любой
// роли — GET на адрес ведомого → 200, тело декодируется, статус
// StatusOK либо StatusNotLeader (оба допустимы). ОБЯЗАТЕЛЬНЫЙ ассерт
// Cache-Control=="no-store": ветка StatusNotLeader — единственное место,
// где следствие «оба ответа несут no-store» имеет действующего
// свидетеля (на ведомом rs.VerifyLeader() немедленно возвращает
// ErrNotLeader — тест гарантированно проходит по этой ветке; ответ
// ведомого — самый частый ответ эндпоинта в кластере).
func TestVerifyLeaderGetOnFollower(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr((lid + 1) % 3)

	resp := verifyLeaderRequest(t, http.MethodGet, addr)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /verifyleader/ status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var sr api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if sr.RespStatus != api.StatusOK && sr.RespStatus != api.StatusNotLeader {
		t.Errorf("GET /verifyleader/ on follower status = %v, want StatusOK or StatusNotLeader", sr.RespStatus)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET /verifyleader/ on follower Cache-Control = %q, want %q", cc, "no-store")
	}
}

// TestVerifyLeaderPostMethodNotAllowed — после перевода POST отвергается
// стандартным ответом маршрутизатора: 405 с заголовком Allow (содержит
// GET). Кейс HEAD: неявное обслуживание GET-паттерном — 200, тело
// подавлено, Cache-Control: no-store.
func TestVerifyLeaderPostMethodNotAllowed(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	// POST: стандартный 405 маршрутизатора с Allow, содержащим GET.
	resp := verifyLeaderRequest(t, http.MethodPost, addr)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /verifyleader/ status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("POST /verifyleader/ Allow = %q, want it to contain GET", allow)
	}

	// HEAD: неявное обслуживание GET-паттерном — StatusOK, тело
	// подавлено, Cache-Control: no-store.
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+addr+"/verifyleader/", http.NoBody)
	if err != nil {
		t.Fatalf("building HEAD /verifyleader/ request: %v", err)
	}
	headResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD /verifyleader/: %v", err)
	}
	headBody, _ := io.ReadAll(headResp.Body)
	_ = headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Errorf("HEAD /verifyleader/ status = %d, want %d", headResp.StatusCode, http.StatusOK)
	}
	if cc := headResp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("HEAD /verifyleader/ Cache-Control = %q, want %q", cc, "no-store")
	}
	if len(headBody) != 0 {
		t.Errorf("HEAD /verifyleader/ body = %d bytes, want 0", len(headBody))
	}
}

// TestVerifyLeaderResponseNotCacheable — вердикт о лидерстве актуален
// только на момент кворумного подтверждения ReadIndex: ответ GET
// /verifyleader/ несёт Cache-Control: no-store.
func TestVerifyLeaderResponseNotCacheable(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	resp := verifyLeaderRequest(t, http.MethodGet, addr)
	_ = resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET /verifyleader/ Cache-Control = %q, want %q", cc, "no-store")
	}
}

// TestVerifyLeaderClientRoundTrip — сквозной round-trip через клиент:
// многоадресный клиент Harness возвращает индекс действующего лидера
// без ошибки. Единственная живая защита корректности декодирования
// (до перевода метод декодировал ответ в интерфейс и всегда ошибался).
func TestVerifyLeaderClientRoundTrip(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()
	h.CheckSingleLeader()

	c := h.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := c.VerifyLeader(ctx)
	// Несущий ассерт: именно err == nil доказывает корректность
	// декодирования (ADR-API-1V-002) и не зависит от перевыборов.
	if err != nil {
		t.Fatalf("VerifyLeader: %v", err)
	}

	// Совпадение индекса — против СВЕЖЕГО съёма лидера ПОСЛЕ вызова:
	// это снимает зависимость от перевыборов между исходным съёмом
	// и вызовом (при перевыборах клиент честно вернёт нового лидера,
	// и сравнение со старым лидером ложно падало бы). Остаточное окно
	// «вызов → повторный съём» исчезающе мало; редкий флак лечится
	// повторным прогоном.
	lidAfter := h.CheckSingleLeader()
	if h.ServiceAddr(got) != h.ServiceAddr(lidAfter) {
		t.Errorf("VerifyLeader returned index %d (addr %q), want current leader %d (addr %q)",
			got, h.ServiceAddr(got), lidAfter, h.ServiceAddr(lidAfter))
	}
}
