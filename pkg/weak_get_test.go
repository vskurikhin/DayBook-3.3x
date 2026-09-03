package pkg

import (
	"reflect"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

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
	postJSON(t, addr, "/weak-get/", req, &weakResp)
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
	postJSON(t, addr, "/weak-get/", api.GetRequest{Key: "llave"}, &present)
	if present.RespStatus != api.StatusOK || !present.KeyFound || present.Value != "cosa" {
		t.Errorf("weak-get of existing key = %+v, want StatusOK/found/\"cosa\"", present)
	}

	var weakMissing api.GetResponse
	postJSON(t, addr, "/weak-get/", api.GetRequest{Key: "ausente"}, &weakMissing)
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
// put → слабое чтение одним клиентом; для явной фиксации паритета
// дополнительно выполняется обычное чтение Get, оба дают то же значение.
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
// без ошибки, found=false (семантика 1:1 с /get/).
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
