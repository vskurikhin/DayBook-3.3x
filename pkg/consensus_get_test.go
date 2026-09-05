package pkg

import (
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

// TestGetOnLeaderReturnsValue — консенсусный /get на лидере: команда
// CommandGet проходит через журнал (как put). Существующий ключ возвращает
// StatusOK с найденным значением, отсутствующий — StatusOK с KeyFound=false
// и пустым значением. Проверяет новый консенсусный путь /get (с Э4),
// зеркальный контуру /put/.
func TestGetOnLeaderReturnsValue(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	var putResp api.PutResponse
	postJSON(t, addr, "/put/", api.PutRequest{Key: "llave", Value: "cosa"}, &putResp)
	if putResp.RespStatus != api.StatusOK || putResp.KeyFound || putResp.PrevValue != "" {
		t.Fatalf("put before get = %+v, want StatusOK/not found/\"\"", putResp)
	}

	var present api.GetResponse
	postJSON(t, addr, "/get/", api.GetRequest{Key: "llave"}, &present)
	if present.RespStatus != api.StatusOK || !present.KeyFound || present.Value != "cosa" {
		t.Fatalf("consensus get of existing key = %+v, want StatusOK/found/\"cosa\"", present)
	}

	var missing api.GetResponse
	postJSON(t, addr, "/get/", api.GetRequest{Key: "ausente"}, &missing)
	if missing.RespStatus != api.StatusOK || missing.KeyFound || missing.Value != "" {
		t.Fatalf("consensus get of missing key = %+v, want StatusOK/not found/\"\"", missing)
	}
}

// TestGetToFollowerReportsNotLeader — консенсусный /get на ведомом узле
// отвергается со статусом StatusNotLeader (как put): команда, отправленная
// на узел, не являющийся лидером, не фиксируется, и сервер отвечает
// NotLeader.
func TestGetToFollowerReportsNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	follower := (lid + 1) % 3
	addr := h.ServiceAddr(follower)

	var getResp api.GetResponse
	postJSON(t, addr, "/get/", api.GetRequest{Key: "k"}, &getResp)
	if getResp.RespStatus != api.StatusNotLeader {
		t.Fatalf("consensus get to follower status = %v, want %v", getResp.RespStatus, api.StatusNotLeader)
	}
}
