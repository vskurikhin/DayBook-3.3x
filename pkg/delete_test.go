package pkg

import (
	"context"
	"reflect"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/vskurikhin/raft"
	"github.com/vskurikhin/raft/pkg/api"
)

// TestDeleteOnLeaderRemovesKey — удаление существующего ключа на лидере:
// сначала ключ кладётся через PUT, затем удаляется через DELETE, после чего
// чтение GET возвращает StatusOK с KeyFound=false и пустым значением.
// Ответ DELETE на существующий ключ несёт StatusOK, KeyFound=true и прежнее
// значение в PrevValue.
func TestDeleteOnLeaderRemovesKey(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	var putResp api.PutResponse
	postJSON(t, addr, "/put/", api.PutRequest{Key: "k", Value: "v"}, &putResp)
	if putResp.RespStatus != api.StatusOK || putResp.KeyFound || putResp.PrevValue != "" {
		t.Fatalf("put before delete = %+v, want StatusOK/not found/\"\"", putResp)
	}

	var delResp api.DeleteResponse
	postJSON(t, addr, "/delete/", api.DeleteRequest{Key: "k"}, &delResp)
	if delResp.RespStatus != api.StatusOK || !delResp.KeyFound || delResp.PrevValue != "v" {
		t.Fatalf("delete of existing key = %+v, want StatusOK/found/\"v\"", delResp)
	}

	var getResp api.GetResponse
	postJSON(t, addr, "/get/", api.GetRequest{Key: "k"}, &getResp)
	if getResp.RespStatus != api.StatusOK || getResp.KeyFound || getResp.Value != "" {
		t.Fatalf("get after delete = %+v, want StatusOK/not found/\"\"", getResp)
	}
}

// TestDeleteMissingKeyIdempotent — удаление несуществующего ключа дважды:
// оба ответа равны (side-by-side дисциплина) и несут StatusOK, KeyFound=false
// и пустое прежнее значение. Повторное удаление не является ошибкой.
func TestDeleteMissingKeyIdempotent(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	addr := h.ServiceAddr(lid)

	var first api.DeleteResponse
	postJSON(t, addr, "/delete/", api.DeleteRequest{Key: "ausente"}, &first)
	if first.RespStatus != api.StatusOK || first.KeyFound || first.PrevValue != "" {
		t.Fatalf("first delete of missing key = %+v, want StatusOK/not found/\"\"", first)
	}

	var second api.DeleteResponse
	postJSON(t, addr, "/delete/", api.DeleteRequest{Key: "ausente"}, &second)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second delete differs from first: first=%+v second=%+v", first, second)
	}
	if second.RespStatus != api.StatusOK || second.KeyFound || second.PrevValue != "" {
		t.Fatalf("second delete of missing key = %+v, want StatusOK/not found/\"\"", second)
	}
}

// TestDeleteToFollowerReportsNotLeader — удаление на узле, не являющемся
// лидером, отвергается со статусом StatusNotLeader. Статус формирует ветка
// ошибки future от Apply, а не предваряющая проверка лидерства.
func TestDeleteToFollowerReportsNotLeader(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	follower := (lid + 1) % 3
	addr := h.ServiceAddr(follower)

	var delResp api.DeleteResponse
	postJSON(t, addr, "/delete/", api.DeleteRequest{Key: "k"}, &delResp)
	if delResp.RespStatus != api.StatusNotLeader {
		t.Errorf("DELETE to follower status = %v, want %v", delResp.RespStatus, api.StatusNotLeader)
	}
}

// TestBasicPutDeleteSingleClient — базовый сценарий через клиента:
// put → delete → чтения WeakGet/Get возвращают found=false; ПОВТОРНЫЙ
// delete отсутствующего ключа завершается успехом без ошибки. Равенство
// первого и повторного ответов здесь не утверждается (первый вернул
// (prev, found=true), повторный — ("", false)); равенство — свойство
// двух удалений отсутствующего ключа (см. TestDeleteMissingKeyIdempotentViaClient).
func TestBasicPutDeleteSingleClient(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	client := h.NewClientSingleService(lid)

	// Ключ кладётся, затем удаляется тем же клиентом.
	h.CheckPut(client, "k", "v")
	h.CheckDelete(client, "k")

	// После удаления чтение ключа слабым и строгим путём не находит его.
	h.CheckWeakGetNotFound(client, "k")
	h.CheckGetNotFound(client, "k")

	// Повторное удаление отсутствующего ключа — успех без ошибки.
	h.CheckDelete(client, "k")
}

// TestDeleteMissingKeyIdempotentViaClient — удаление несуществующего
// ключа через клиента дважды: оба раза ("", false, nil) и ответы равны.
// Имя не префиксный близнец прямого HTTP-теста задачи 01
// (TestDeleteMissingKeyIdempotent): здесь предмет — клиентская половина.
func TestDeleteMissingKeyIdempotentViaClient(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	client := h.NewClientSingleService(lid)

	ctx, cancel := context.WithTimeout(h.ctx, _clientOpTimeout)
	defer cancel()

	firstPrev, firstFound, err := client.Delete(ctx, "ausente")
	if err != nil {
		t.Fatalf("first delete of missing key: %v", err)
	}
	if firstPrev != "" || firstFound {
		t.Errorf("first delete = (%q, %v); want (\"\", false)", firstPrev, firstFound)
	}

	secondPrev, secondFound, err := client.Delete(ctx, "ausente")
	if err != nil {
		t.Fatalf("second delete of missing key: %v", err)
	}
	if secondPrev != "" || secondFound {
		t.Errorf("second delete = (%q, %v); want (\"\", false)", secondPrev, secondFound)
	}
	if firstPrev != secondPrev || firstFound != secondFound {
		t.Errorf("second delete differs from first: first=(%q, %v) second=(%q, %v)",
			firstPrev, firstFound, secondPrev, secondFound)
	}
}

// TestDeleteToFollowerTimesOutThenLeaderSucceeds — клиент НЕ возвращает
// ошибку на StatusNotLeader: send ротирует и повторяет, а у клиента на
// один адрес nextLeader — no-op, поэтому повторы идут без задержки
// (горячий цикл) до исчерпания короткого бюджета контекста. После этого
// обычный клиент успешно удаляет ключ через лидера, и ответ несёт прежнее
// значение и признак существования. Проверка «ведомый отвечает
// StatusNotLeader» остаётся за прямым HTTP-тестом задачи 01
// (TestDeleteToFollowerReportsNotLeader), где она детерминирована.
func TestDeleteToFollowerTimesOutThenLeaderSucceeds(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()
	leaderClient := h.NewClientSingleService(lid)

	// Ключ кладётся обычным клиентом (до изоляции не требуется —
	// достаточно лидера).
	h.CheckPut(leaderClient, "k", "v")

	// Клиент, направленный только на ведомого, зацикливается на
	// StatusNotLeader и завершается по тайм-ауту контекста.
	follower := (lid + 1) % 3
	fc := h.NewClientSingleService(follower)
	h.CheckDeleteTimesOut(fc, "k")

	// Затем обычный клиент успешно удаляет ключ: ответ несёт прежнее
	// значение и признак его существования до удаления.
	ctx, cancel := context.WithTimeout(h.ctx, _clientOpTimeout)
	defer cancel()
	prev, found, err := leaderClient.Delete(ctx, "k")
	if err != nil {
		t.Fatalf("delete on leader: %v", err)
	}
	if prev != "v" || !found {
		t.Errorf("delete = (%q, %v); want (\"v\", true)", prev, found)
	}
}

// TestPutDeleteGetDifferentClients — операции над одним ключом разными
// клиентами: put, delete и weak-get (found=false) через отдельные
// экземпляры клиента.
func TestPutDeleteGetDifferentClients(t *testing.T) {
	defer leaktest.CheckTimeout(t, raft.LeaktestBudget)()

	h := NewHarness(t, 3)
	defer h.Shutdown()

	lid := h.CheckSingleLeader()

	// put одним клиентом.
	putClient := h.NewClientSingleService(lid)
	h.CheckPut(putClient, "k", "v")

	// delete другим клиентом.
	delClient := h.NewClientSingleService(lid)
	h.CheckDelete(delClient, "k")

	// weak-get третьим клиентом: ключ отсутствует.
	getClient := h.NewClientSingleService(lid)
	h.CheckWeakGetNotFound(getClient, "k")
}
