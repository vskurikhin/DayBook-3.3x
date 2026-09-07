package pkg

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

// checkQuorumBudget — запас времени на обнаружение потери кворума лидером:
// два срока проверки кворума плюс период пульса и накладные HTTP.
const checkQuorumBudget = 2*2*raft.TCPRPCTimeout + 4*raft.HeartbeatTimeoutMs*time.Millisecond

// postJSON отправляет запрос напрямую сервису id и разбирает ответ.
// Прямой HTTP нужен там, где предмет проверки — статус ответа конкретного
// узла: клиент библиотеки обходит адреса и статус отдельного узла скрывает.
func postJSON(t *testing.T, addr, path string, req, resp any) {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpResp, err := http.Post("http://"+addr+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s%s: %v", addr, path, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// TestWriteToFollowerReportsNotLeader — AC-2 TASK-3-001: запись на узле,
// не являющемся лидером, отвергается с прежним статусом StatusNotLeader.
// Статус формирует ветка ошибки future от Apply, а не предваряющая
// проверка лидерства.
func TestWriteToFollowerReportsNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	follower := (lid + 1) % 3
	addr := h.ServiceAddr(follower)

	var putResp api.PutResponse
	postJSON(t, addr, "/put/", api.PutRequest{Key: "k", Value: "v"}, &putResp)
	if putResp.RespStatus != api.StatusNotLeader {
		t.Errorf("PUT to follower status = %v, want %v", putResp.RespStatus, api.StatusNotLeader)
	}

	var casResp api.CASResponse
	postJSON(t, addr, "/cas/", api.CASRequest{Key: "k", CompareValue: "v", Value: "w"}, &casResp)
	if casResp.RespStatus != api.StatusNotLeader {
		t.Errorf("CAS to follower status = %v, want %v", casResp.RespStatus, api.StatusNotLeader)
	}
}

// TestWriteToIsolatedLeaderReportsNotLeader — AC-6 TASK-3-001 (совместно
// с TASK-3-003): лидер, изолированный от кворума, шагает вниз и отвечает
// на запись прежним статусом StatusNotLeader. Ограничение размера
// незафиксированного хвоста журнала проверяется тестом корневого пакета
// (TestCheckQuorum_UncommittedLimitBoundsLog): поле не экспортируется.
func TestWriteToIsolatedLeaderReportsNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	c := h.NewClient()
	h.CheckPut(c, "key", "value")

	h.DisconnectServiceFromPeers(lid)

	start := time.Now()
	var putResp api.PutResponse
	postJSON(t, h.ServiceAddr(lid), "/put/", api.PutRequest{Key: "key", Value: "other"}, &putResp)
	elapsed := time.Since(start)

	if putResp.RespStatus != api.StatusNotLeader {
		t.Errorf("PUT to isolated leader status = %v, want %v", putResp.RespStatus, api.StatusNotLeader)
	}
	if elapsed > checkQuorumBudget {
		t.Errorf("PUT to isolated leader answered in %v, want at most %v", elapsed, checkQuorumBudget)
	}
}
